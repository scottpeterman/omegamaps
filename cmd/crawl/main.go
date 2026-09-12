// cmd/crawl/main.go
// crawl — topology crawler over SSH, SNMP, or both. BFS from one or more
// seeds, collect each device's CDP/LLDP neighbors, and emit a topology map
// JSON compatible with the existing viewer/seed-artifact toolchain.
//
// Example (lab):
//
//	crawl -seed lab-r1.lab.example -user admin -depth 3 \
//	      -exclude "linux,idrac,poweredge" -o map.json
//
//	crawl -seed 10.20.0.5 -user admin -jump admin@lab-jump1.lab.example \
//	      -trust-unidirectional -o map.json
//
// With a vault, credentials are resolved per device instead of being fixed on
// the command line. The master password comes from OMEGAMAPS_VAULT_PASSWORD
// or a terminal prompt:
//
//	crawl -seed lab-r1.lab.example -vault ~/.omegamaps/vault.json \
//	      -cred-tag lab -depth 3 -o map.json
//
// -methods sets how each device is collected, and in what order: "ssh" (the
// default), "snmp", or both. A device moves to the next method when one fails
// or reaches it but finds no neighbors. SNMP credentials come from the vault
// (-vault; add them with omvault) and are resolved per device with bindings,
// exactly like SSH credentials. Without a vault, or to override it, they come
// from the environment -- never from flags, so they stay out of ps and shell
// history. A v3 user is tried first, then each community in order:
//
//	OMEGAMAPS_SNMP_COMMUNITY     one community, or several comma-separated
//	OMEGAMAPS_SNMP_V3_USER       v3 user name
//	OMEGAMAPS_SNMP_V3_AUTH       v3 auth key         (with _V3_AUTHPROTO, default SHA)
//	OMEGAMAPS_SNMP_V3_PRIV       v3 privacy key      (with _V3_PRIVPROTO, default AES)
//
//	OMEGAMAPS_SNMP_COMMUNITY=... crawl -seed lab-r1.lab.example -methods snmp,ssh \
//	      -user admin -depth 3 -o map.json
package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/scottpeterman/omegamaps/internal/buildinfo"
	"github.com/scottpeterman/omegamaps/internal/crawldial"
	"github.com/scottpeterman/omegamaps/internal/crawler"
	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/credres"
	"github.com/scottpeterman/omegamaps/internal/dial"
	"github.com/scottpeterman/omegamaps/internal/netexec"
	"github.com/scottpeterman/omegamaps/internal/snmpprobe"
	"github.com/scottpeterman/omegamaps/internal/sshcore"
	"github.com/scottpeterman/omegamaps/internal/topo"
	"github.com/scottpeterman/omegamaps/internal/vault"
	"github.com/scottpeterman/omegamaps/internal/vaultcli"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// checkArgs refuses the two symptoms of a flag given no value. That flag
// swallows the next one -- "-events -exclude x -v" sets -events to "-exclude"
// -- and Go's flag parsing then stops at "x", so -exclude and -v are never
// seen. Nothing complains: the run starts, without its exclusions and without
// its log, and looks hung. Checked before anything prompts or dials.
func checkArgs(fs *flag.FlagSet) error {
	var swallowed []string
	fs.Visit(func(f *flag.Flag) {
		if looksLikeFlag(f.Value.String()) {
			swallowed = append(swallowed, fmt.Sprintf("-%s %q", f.Name, f.Value.String()))
		}
	})
	if len(swallowed) > 0 {
		return fmt.Errorf("%s: that value looks like another flag -- was a value left out?",
			strings.Join(swallowed, ", "))
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unexpected argument %q: crawl takes only flags, and parsing stops at the "+
			"first non-flag, so everything from here on was ignored -- was a flag's value left out, "+
			"or a list not comma-joined?", fs.Arg(0))
	}
	return nil
}

// looksLikeFlag reports whether a flag's value is shaped like a flag: a dash
// and then a letter. Negative numbers (-cred-breaker -1) are values.
func looksLikeFlag(v string) bool {
	t := strings.TrimLeft(v, "-")
	return len(t) < len(v) && len(t) > 0 && unicode.IsLetter(rune(t[0]))
}

// snmpCredsFromEnv reads SNMP credentials from the environment: a v3 user
// first when one is set, then each v2c community in order.
func snmpCredsFromEnv() []snmpprobe.Credential {
	var creds []snmpprobe.Credential
	if user := strings.TrimSpace(os.Getenv("OMEGAMAPS_SNMP_V3_USER")); user != "" {
		creds = append(creds, snmpprobe.Credential{
			User:         user,
			AuthKey:      os.Getenv("OMEGAMAPS_SNMP_V3_AUTH"),
			AuthProtocol: os.Getenv("OMEGAMAPS_SNMP_V3_AUTHPROTO"),
			PrivKey:      os.Getenv("OMEGAMAPS_SNMP_V3_PRIV"),
			PrivProtocol: os.Getenv("OMEGAMAPS_SNMP_V3_PRIVPROTO"),
		})
	}
	for _, c := range strings.Split(os.Getenv("OMEGAMAPS_SNMP_COMMUNITY"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			creds = append(creds, snmpprobe.Credential{Community: c})
		}
	}
	return creds
}

func parseJump(spec, fallbackUser string) (*sshcore.JumpConfig, error) {
	j := &sshcore.JumpConfig{Username: fallbackUser}
	rest := spec
	if i := strings.IndexByte(rest, '@'); i >= 0 {
		j.Username = rest[:i]
		rest = rest[i+1:]
	}
	if host, portStr, err := net.SplitHostPort(rest); err == nil {
		p, perr := strconv.Atoi(portStr)
		if perr != nil {
			return nil, fmt.Errorf("jump port %q: %v", portStr, perr)
		}
		j.Host, j.Port = host, p
	} else {
		j.Host = rest
	}
	if j.Host == "" {
		return nil, fmt.Errorf("jump spec %q has no host", spec)
	}
	return j, nil
}

// expandCSV splits comma-separable repeatable flag values.
func expandCSV(in []string) []string {
	var out []string
	for _, s := range in {
		for _, p := range strings.Split(s, ",") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

func main() {
	var seeds, exclude, allowDomains, domains, credTags stringList
	flag.Var(&seeds, "seed", "seed device (repeatable or comma-separated)")
	flag.Var(&exclude, "exclude", "exclusion substring(s), comma-separable, matched vs platform/hostname/sysname and, for neighbors, the remote port description")
	flag.Var(&allowDomains, "allow-domain", "only dial neighbors under these domain suffixes (repeatable); others map as leaves")
	flag.Var(&domains, "domain", "domain suffix (repeatable): stripped from node names in the map and appended when resolving bare neighbor names")
	user := flag.String("user", os.Getenv("USER"), "ssh username")
	keyPath := flag.String("key", "", "private key path")
	askPass := flag.Bool("password", false, "prompt for a password")
	jumpSpec := flag.String("jump", "", "jump host: [user@]host[:port]")
	jumpKey := flag.String("jump-key", "", "jump host private key path")
	legacy := flag.Bool("legacy", false, "enable legacy KEX/ciphers")
	insecure := flag.Bool("insecure-hostkey", false, "skip host key verification (lab only; off by default)")
	depth := flag.Int("depth", 3, "max crawl depth (0 = seeds only)")
	conc := flag.Int("concurrency", 5, "concurrent devices per depth")
	timeout := flag.Duration("timeout", 30*time.Second, "per-command timeout")
	trustUni := flag.Bool("trust-unidirectional", false, "accept one-sided link claims between discovered devices (legacy-parity)")
	vaultPath := flag.String("vault", "", "credential vault path; enables multi-credential resolution (overrides -user/-key/-password)")
	bindingsPath := flag.String("bindings", "", "credential binding store path (default: alongside the vault)")
	flag.Var(&credTags, "cred-tag", "only offer credentials carrying all of these tags (repeatable or comma-separated)")
	maxCreds := flag.Int("max-creds", 0, "cap credentials tried per device (0 = default, negative = unlimited)")
	credBreaker := flag.Int("cred-breaker", 0, "park a credential after this many distinct devices reject it (0 = default, negative = off)")
	knownHosts := flag.String("known-hosts", "", "known_hosts path for discovered keys (default ~/.ssh/known_hosts; use a dedicated file to keep discovery keys separate)")
	eventsPath := flag.String("events", "", "record every crawl event to this file as JSON Lines: the stream a front end shows, for replay or other tools")
	snmpTimeout := flag.Duration("snmp-timeout", 5*time.Second, "per-request SNMP timeout; also what each v2c credential that does not answer costs")
	methodsFlag := flag.String("methods", "ssh", "collection methods in the order to try them: ssh, snmp, or both (e.g. snmp,ssh); SNMP credentials come from -vault, or from OMEGAMAPS_SNMP_* environment variables, which override it")
	outPath := flag.String("o", "map.json", "output topology file")
	verbose := flag.Bool("v", false, "verbose progress")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Line("crawl"))
		return
	}
	if err := checkArgs(flag.CommandLine); err != nil {
		fmt.Fprintf(os.Stderr, "crawl: %v\n", err)
		os.Exit(2)
	}

	var expanded []string
	for _, s := range seeds {
		for _, part := range strings.Split(s, ",") {
			if part = strings.TrimSpace(part); part != "" {
				expanded = append(expanded, part)
			}
		}
	}
	if len(expanded) == 0 {
		fmt.Fprintln(os.Stderr, "crawl: at least one -seed is required")
		os.Exit(2)
	}

	methods, err := crawlrun.ParseMethods(*methodsFlag)
	if err == nil && len(methods) == 0 {
		err = fmt.Errorf("no method given; use ssh, snmp, or both")
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "crawl: -methods: %v\n", err)
		os.Exit(2)
	}
	useSSH := slices.Contains(methods, crawlrun.MethodSSH)
	useSNMP := slices.Contains(methods, crawlrun.MethodSNMP)

	// The run is the one record of what happened: every event from the
	// crawler and both credential resolvers goes through it and is stamped
	// there, and -events records exactly that stream, in that order.
	run := crawlrun.New()
	var events *crawlrun.EventWriter
	var eventsFile *os.File
	if *eventsPath != "" {
		f, err := os.Create(*eventsPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "crawl: -events: %v\n", err)
			os.Exit(1)
		}
		eventsFile = f
		events = crawlrun.NewEventWriter(f)
		run.Tap(events.Write)
	}
	// SNMP credentials from the environment win; without them, the vault
	// resolves them per device, with bindings, as it does for SSH.
	var snmpCreds []snmpprobe.Credential
	snmpFromVault := false
	if useSNMP {
		if snmpCreds = snmpCredsFromEnv(); len(snmpCreds) == 0 {
			if *vaultPath == "" {
				fmt.Fprintln(os.Stderr, "crawl: -methods includes snmp but no credentials were found: "+
					"set OMEGAMAPS_SNMP_COMMUNITY and/or OMEGAMAPS_SNMP_V3_USER, "+
					"or name a -vault holding SNMP credentials")
				os.Exit(2)
			}
			snmpFromVault = true
		}
	}

	var password string
	if *askPass && useSSH {
		// The same prompt as the vault's: written to the terminal, so it
		// is seen when stderr is redirected to a log.
		p, err := vaultcli.Prompt("ssh password")
		if err != nil {
			fmt.Fprintf(os.Stderr, "crawl: reading password: %v\n", err)
			os.Exit(1)
		}
		password = p
	}

	policy := sshcore.HostKeyTOFU
	if *insecure {
		policy = sshcore.HostKeyInsecure
	}

	var jump *sshcore.JumpConfig
	if *jumpSpec != "" {
		j, err := parseJump(*jumpSpec, *user)
		if err != nil {
			fmt.Fprintf(os.Stderr, "crawl: %v\n", err)
			os.Exit(2)
		}
		j.PrivateKeyPath = *jumpKey
		jump = j
	}

	base := crawldial.BaseConfig{
		Announce:       dial.Stderr("crawl"),
		Legacy:         *legacy,
		HostKeys:       policy,
		Jump:           jump,
		KnownHostsPath: *knownHosts,
	}

	credLog := func(string, ...any) {}
	if *verbose {
		credLog = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}

	// Two dial modes. Without a vault the flags supply one credential for the
	// whole crawl, which is unchanged behavior. With a vault, credres decides
	// per device and learns across the crawl.
	var (
		dial      crawler.DialFunc
		resolver  *credres.Resolver
		bindings  *credres.FileBindings
		credNames map[string]string
		v         *vault.Vault
	)
	// The vault is opened only for a method that reads it -- SSH in vault
	// mode, or SNMP with nothing in the environment -- so an SNMP run with
	// its credentials in the environment never asks for a master password.
	if *vaultPath != "" && (useSSH || snmpFromVault) {
		var err error
		if v, err = vaultcli.Open(*vaultPath); err != nil {
			fmt.Fprintf(os.Stderr, "crawl: %v\n", err)
			os.Exit(1)
		}
		defer v.Lock()
		// Names for reporting only; never secret material.
		credNames = map[string]string{}
		if metas, err := v.List(); err == nil {
			for _, m := range metas {
				credNames[m.ID] = m.Name
			}
		}
	}
	bp := *bindingsPath
	if bp == "" {
		bp = vaultcli.BindingsPath(*vaultPath)
	}

	switch {
	case !useSSH:
		if (*vaultPath != "" && !snmpFromVault) || flagWasSet("password") || flagWasSet("key") {
			fmt.Fprintln(os.Stderr, "crawl: -methods has no ssh; -key and -password are ignored, "+
				"and so is -vault while SNMP credentials come from the environment")
		}
	case *vaultPath == "":
		dial = crawldial.StaticDialer(base, *user, password, *keyPath)
	default:
		var err error
		if bindings, err = credres.OpenFileBindings(bp); err != nil {
			fmt.Fprintf(os.Stderr, "crawl: %v\n", err)
			os.Exit(1)
		}
		resolver = credres.New(v, bindings, credres.Config{
			BreakerThreshold: *credBreaker,
			MaxPerHost:       *maxCreds,
			Log:              credLog,
			Emit:             run.Emit(),
		})
		dial = crawldial.NewVaultDialer(resolver, base, expandCSV(credTags), credLog)
		if flagWasSet("user") || flagWasSet("key") || flagWasSet("password") {
			fmt.Fprintln(os.Stderr, "crawl: -vault is set; -user/-key/-password are ignored")
		}
	}

	logf := crawler.Logf(nil)
	if *verbose {
		logf = func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		}
	}

	var (
		snmpFn       crawler.SNMPFunc
		snmpBindings *credres.FileBindings
	)
	sopt := snmpprobe.Options{Timeout: *snmpTimeout, Log: logf}
	switch {
	case !useSNMP:
	case !snmpFromVault:
		snmpFn = crawldial.NewSNMPFunc(snmpCreds, sopt)
	default:
		if !crawldial.VaultHasSNMP(v) {
			fmt.Fprintln(os.Stderr, "crawl: -methods includes snmp, no OMEGAMAPS_SNMP_* credentials are set, "+
				"and the vault holds no enabled SNMP credential (add one with: omvault add -auth snmp-v2c ...)")
			os.Exit(2)
		}
		var err error
		if snmpBindings, err = credres.OpenFileBindings(crawldial.SNMPBindingsPath(bp)); err != nil {
			fmt.Fprintf(os.Stderr, "crawl: %v\n", err)
			os.Exit(1)
		}
		snmpRes := credres.New(v, snmpBindings, credres.Config{
			Method:           crawlrun.MethodSNMP,
			Classify:         crawldial.SNMPOutcome,
			BreakerThreshold: *credBreaker,
			MaxPerHost:       *maxCreds,
			Log:              credLog,
			Emit:             run.Emit(),
		})
		snmpFn = crawldial.NewVaultSNMPFunc(snmpRes, expandCSV(credTags), sopt)
	}

	c := crawler.New(crawler.Config{
		Dial:            dial,
		SNMP:            snmpFn,
		Methods:         methods,
		Emit:            run.Emit(),
		MaxDepth:        *depth,
		Concurrency:     *conc,
		ExcludePatterns: exclude,
		AllowDomains:    expandCSV(allowDomains),
		Domains:         expandCSV(domains),
		SessionOpts:     netexec.Options{CommandTimeout: *timeout},
		Log:             logf,
	})

	devices := c.Crawl(expanded)
	run.Finish()
	if events != nil {
		err := events.Flush()
		if cerr := eventsFile.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "crawl: -events: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "crawl: %d events recorded in %s\n", run.Seq(), *eventsPath)
		}
	}

	crawldial.Fold(bindings, devices, expandCSV(domains), logf)
	crawldial.Fold(snmpBindings, devices, expandCSV(domains), logf)

	if resolver != nil {
		reportCredentialStats(resolver, credNames)
	}

	// Through the shared helper, so the CLI and the window cannot generate
	// different maps from the same run.
	topoMap := topo.Generate(devices, crawldial.MapOptions(crawlrun.Params{
		Domains:             expandCSV(domains),
		TrustUnidirectional: *trustUni,
	}))
	data, err := topo.MarshalMap(topoMap)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crawl: marshal: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*outPath, data, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "crawl: write %s: %v\n", *outPath, err)
		os.Exit(1)
	}

	ok, failed := 0, 0
	for _, d := range devices {
		if d.Failed {
			failed++
		} else {
			ok++
		}
	}
	fmt.Fprintf(os.Stderr, "crawl: %d discovered, %d failed, %d nodes in %s\n",
		ok, failed, len(topoMap), *outPath)
	if ok == 0 {
		os.Exit(1)
	}
}
