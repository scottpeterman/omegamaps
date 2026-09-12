// cmd/omvault/main.go
//
// omvault — create and manage the credential vault that `crawl -vault` reads.
//
// Secrets are never taken from the command line. A password on argv lands in
// shell history, in the process table, and in any shell-integration log the
// terminal happens to keep, so every secret here is prompted or comes from the
// environment. This is the same reason the vault stores nothing in plaintext:
// the file is not the only place a credential can leak from.
//
// Example — the two credentials a crawl usually needs:
//
//	omvault init
//	omvault add -name lab-key -user admin -key ~/.ssh/id_ed25519 -tag lab -priority 10
//	omvault add -name lab-pw  -user admin -tag lab -priority 20
//	omvault list
//
// Priority orders the ladder, lower first — so the key is tried before the
// password above. Both carry the "lab" tag, which is what
// `crawl -cred-tag lab` selects on.
//
// SNMP credentials live in the same vault and are resolved by a crawl the same
// way, with their own bindings. The community or keys are prompted like any
// other secret:
//
//	omvault add -name lab-ro -auth snmp-v2c -tag lab
//	omvault add -name lab-v3 -auth snmp-v3 -user monitor -snmp-auth-proto SHA256 -snmp-priv-proto AES -tag lab
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/scottpeterman/omegamaps/internal/buildinfo"
	"github.com/scottpeterman/omegamaps/internal/snmpprobe"
	"github.com/scottpeterman/omegamaps/internal/vault"
	"github.com/scottpeterman/omegamaps/internal/vaultcli"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

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

const usage = `omvault — manage the omegamaps credential vault

usage: omvault [-vault PATH] <command> [flags]

commands:
  init              create a new vault
  add               add a credential (secrets are prompted, never on argv)
  list              list credentials (no secret material)
  rm NAME|ID        remove a credential
  disable NAME|ID   take a credential out of automatic selection
  enable NAME|ID    put it back
  default           report which credential a session naming none would use
  default NAME|ID   make it the default
  default -clear    leave no default
  keyring set       store this vault's master password in the OS keyring
  keyring clear     remove it
  keyring status    report what the unlock path would find

run "omvault <command> -h" for command flags
`

func main() {
	vaultPath := flag.String("vault", vaultcli.DefaultPath(), "vault file path")
	flag.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(buildinfo.Line("omvault"))
		return
	}

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}

	var err error
	switch args[0] {
	case "init":
		err = cmdInit(*vaultPath)
	case "add":
		err = cmdAdd(*vaultPath, args[1:])
	case "list", "ls":
		err = cmdList(*vaultPath)
	case "rm", "remove", "delete":
		err = cmdRemove(*vaultPath, args[1:])
	case "disable":
		err = cmdSetDisabled(*vaultPath, args[1:], true)
	case "enable":
		err = cmdSetDisabled(*vaultPath, args[1:], false)
	case "default":
		err = cmdDefault(*vaultPath, args[1:])
	case "keyring":
		err = cmdKeyring(*vaultPath, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "omvault: unknown command %q\n\n", args[0])
		flag.Usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "omvault: %v\n", err)
		os.Exit(1)
	}
}

func cmdInit(path string) error {
	v := vault.New(path)
	if v.Exists() {
		return fmt.Errorf("a vault already exists at %s", path)
	}
	if dir := parentDir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}
	master, err := vaultcli.MasterNew()
	if err != nil {
		return err
	}
	if err := v.Create(master); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "created %s\n", path)
	return nil
}

// cmdKeyring manages the OS-keyring entry that lets a crawl unlock the vault
// with no human present. Storing an unverified password is how a keyring
// entry becomes a lockout, so `set` opens the vault with the password before
// filing it.
func cmdKeyring(path string, argv []string) error {
	sub := ""
	if len(argv) > 0 {
		sub = argv[0]
	}
	switch sub {
	case "set":
		v := vault.New(path)
		if !v.Exists() {
			return fmt.Errorf("no vault at %s", path)
		}
		master, err := vaultcli.Prompt("vault master password")
		if err != nil {
			return err
		}
		if err := v.Unlock(master); err != nil {
			return fmt.Errorf("not storing an unverified password: %w", err)
		}
		v.Lock()
		if err := vaultcli.KeyringSet(path, master); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "stored master password for %s in the OS keyring\n", path)
		return nil

	case "clear":
		if err := vaultcli.KeyringClear(path); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "cleared any keyring entry for %s\n", path)
		return nil

	case "status":
		st := vaultcli.Keyring(path)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "vault\t%s\n", path)
		fmt.Fprintf(w, "keyring account\t%s\n", st.Account)
		switch {
		case st.Disabled:
			fmt.Fprintf(w, "state\tdisabled (%s is set)\n", vaultcli.NoKeyringEnvVar)
		case st.Err != nil:
			fmt.Fprintf(w, "state\tunavailable: %v\n", st.Err)
		case st.HasEntry:
			fmt.Fprintf(w, "state\tentry present\n")
		default:
			fmt.Fprintf(w, "state\tavailable, no entry\n")
		}
		if _, ok := os.LookupEnv(vaultcli.MasterEnvVar); ok {
			fmt.Fprintf(w, "note\t%s is also set (used only if the keyring has no entry)\n",
				vaultcli.MasterEnvVar)
		}
		return w.Flush()

	default:
		return fmt.Errorf("keyring: expected set, clear, or status")
	}
}

func cmdAdd(path string, argv []string) error {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	name := fs.String("name", "", "credential name (required, unique)")
	user := fs.String("user", "", "username: the SSH login, or the SNMPv3 user (required except for snmp-v2c)")
	keyPath := fs.String("key", "", "private key path; implies -auth publickey")
	authType := fs.String("auth", "", "auth type: password, publickey, agent, snmp-v2c, snmp-v3 (default: publickey if -key is set, else password)")
	snmpAuthProto := fs.String("snmp-auth-proto", "", "SNMPv3 auth protocol: MD5, SHA, SHA224, SHA256, SHA384, SHA512 (default SHA)")
	snmpPrivProto := fs.String("snmp-priv-proto", "", "SNMPv3 privacy protocol: DES, AES, AES192, AES256, AES192C, AES256C (default AES)")
	askPassphrase := fs.Bool("passphrase", false, "prompt for the key passphrase")
	desc := fs.String("desc", "", "free-text description")
	priority := fs.Int("priority", 0, "ladder order within the same scope; lower runs first")
	makeDefault := fs.Bool("default", false, "also make this the default credential for sessions naming none")
	var tags, cidrs, platforms stringList
	fs.Var(&tags, "tag", "tag (repeatable or comma-separated)")
	fs.Var(&cidrs, "scope-cidr", "restrict to targets inside this prefix (repeatable)")
	fs.Var(&platforms, "scope-platform", "restrict to these fingerprinted platforms (repeatable)")
	domain := fs.String("scope-domain", "", "restrict to identities under this domain suffix")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	at, err := canonicalAuthType(inferAuthType(*authType, *keyPath))
	if err != nil {
		return err
	}
	snmpV2c := vault.StringToAuthType(at) == vault.AuthSNMPv2c
	if *name == "" || (*user == "" && !snmpV2c) {
		return fmt.Errorf("-name and -user are required (-user is not needed for snmp-v2c)")
	}

	c := vault.Credential{
		Name:        *name,
		Username:    *user,
		AuthType:    at,
		Description: *desc,
		Priority:    *priority,
		Tags:        expandCSV(tags),
		Scope: vault.Scope{
			DomainSuffix: *domain,
			CIDRs:        expandCSV(cidrs),
			Platforms:    expandCSV(platforms),
		},
	}

	// Only the material the declared auth type will actually use is
	// collected, matching how the dialer applies it. Storing a password on a
	// publickey credential would mean carrying a secret nothing ever reads.
	if c.IsSNMP() {
		if *keyPath != "" || *askPassphrase {
			return fmt.Errorf("-key and -passphrase do not apply to SNMP credentials")
		}
		if *makeDefault {
			return vault.ErrSNMPDefault
		}
	} else if *snmpAuthProto != "" || *snmpPrivProto != "" {
		return fmt.Errorf("-snmp-auth-proto and -snmp-priv-proto apply only to -auth snmp-v3")
	}

	switch c.Method() {
	case vault.AuthSNMPv2c:
		c.Username = ""
		community, err := vaultcli.Prompt(fmt.Sprintf("SNMP community for %s", *name))
		if err != nil {
			return err
		}
		if community == "" {
			return fmt.Errorf("empty community")
		}
		c.Password = community
	case vault.AuthSNMPv3:
		authKey, err := vaultcli.Prompt(fmt.Sprintf("SNMPv3 auth key for %s", *user))
		if err != nil {
			return err
		}
		if authKey == "" {
			return fmt.Errorf("empty auth key (a v3 credential here is authNoPriv or authPriv)")
		}
		c.Password = authKey
		c.SNMPAuthProtocol = strings.ToUpper(*snmpAuthProto)
		privKey, err := vaultcli.Prompt("SNMPv3 privacy key (empty for authNoPriv)")
		if err != nil {
			return err
		}
		if privKey != "" {
			c.KeyPassphrase = privKey
			c.SNMPPrivProtocol = strings.ToUpper(*snmpPrivProto)
		} else if *snmpPrivProto != "" {
			return fmt.Errorf("-snmp-priv-proto given but no privacy key entered")
		}
	case vault.AuthPublicKey:
		if *keyPath == "" {
			return fmt.Errorf("-key is required for publickey credentials")
		}
		c.KeyPath = *keyPath
		if *askPassphrase {
			pp, err := vaultcli.Prompt("key passphrase")
			if err != nil {
				return err
			}
			c.KeyPassphrase = pp
		}
	case vault.AuthAgent:
		// Nothing to store; the agent holds the material.
	default:
		pw, err := vaultcli.Prompt(fmt.Sprintf("password for %s@%s", *user, *name))
		if err != nil {
			return err
		}
		if pw == "" {
			return fmt.Errorf("empty password")
		}
		c.Password = pw
	}

	// Refused now rather than on every device of the next crawl.
	if c.IsSNMP() {
		if _, err := snmpprobe.FromVault(c); err != nil {
			return err
		}
	}

	v, err := vaultcli.Open(path)
	if err != nil {
		return err
	}
	defer v.Lock()

	added, err := v.Add(c)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "added %q (%s, %s)\n", added.Name, added.ID, added.AuthType)

	if *makeDefault {
		if err := v.SetDefault(added.ID); err != nil {
			return fmt.Errorf("credential added, but making it the default failed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "%q is now the default credential\n", added.Name)
	}
	return nil
}

// cmdDefault reports or changes the credential a session naming none uses.
//
// The BARE form REPORTS. A command whose no-argument spelling silently changes
// something is one typo away from a bad afternoon, and "which one is it" is the
// question asked far more often than "make it that one".
func cmdDefault(path string, argv []string) error {
	fs := flag.NewFlagSet("default", flag.ExitOnError)
	clear := fs.Bool("clear", false, "leave no default credential")
	if err := fs.Parse(argv); err != nil {
		return err
	}
	rest := fs.Args()
	if *clear && len(rest) > 0 {
		return fmt.Errorf("usage: omvault default [NAME|ID] | omvault default -clear")
	}
	if len(rest) > 1 {
		return fmt.Errorf("usage: omvault default [NAME|ID] | omvault default -clear")
	}

	v, err := vaultcli.Open(path)
	if err != nil {
		return err
	}
	defer v.Lock()

	switch {
	case *clear:
		if err := v.ClearDefault(); err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "no default credential")
		return nil

	case len(rest) == 1:
		c, err := v.Get(rest[0])
		if err != nil {
			return err
		}
		// Refused rather than silently set: Default() skips a disabled
		// credential, so this would look like it worked and then do
		// nothing on every connection.
		if c.Disabled {
			return fmt.Errorf("%q is disabled; enable it first", c.Name)
		}
		if err := v.SetDefault(c.ID); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "%q is now the default credential\n", c.Name)
		return nil

	default:
		name := v.DefaultName()
		if name == "" {
			fmt.Fprintln(os.Stderr, "no default credential; sessions naming none authenticate with what they carry")
			return nil
		}
		fmt.Fprintf(os.Stderr, "default credential: %s\n", name)
		return nil
	}
}

func cmdList(path string) error {
	v, err := vaultcli.Open(path)
	if err != nil {
		return err
	}
	defer v.Lock()

	metas, err := v.List()
	if err != nil {
		return err
	}
	if len(metas) == 0 {
		fmt.Fprintln(os.Stderr, "vault is empty")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tUSER\tAUTH\tPRIO\tTAGS\tSCOPE\tSTATE")
	for _, m := range metas {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\n",
			m.Name, joinOr(nonEmpty(m.Username), "-"), m.AuthLabel, m.Priority,
			joinOr(m.Tags, "-"), scopeSummary(m.Scope), state(m))
	}
	return w.Flush()
}

func cmdRemove(path string, argv []string) error {
	if len(argv) != 1 {
		return fmt.Errorf("usage: omvault rm NAME|ID")
	}
	v, err := vaultcli.Open(path)
	if err != nil {
		return err
	}
	defer v.Lock()

	c, err := v.Get(argv[0])
	if err != nil {
		return err
	}
	if err := v.Delete(c.ID); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "removed %q\n", c.Name)
	return nil
}

func cmdSetDisabled(path string, argv []string, disabled bool) error {
	if len(argv) != 1 {
		return fmt.Errorf("usage: omvault disable|enable NAME|ID")
	}
	v, err := vaultcli.Open(path)
	if err != nil {
		return err
	}
	defer v.Lock()

	c, err := v.Get(argv[0])
	if err != nil {
		return err
	}
	if err := v.SetDisabled(c.ID, disabled); err != nil {
		return err
	}
	verb := "enabled"
	if disabled {
		verb = "disabled"
	}
	fmt.Fprintf(os.Stderr, "%s %q\n", verb, c.Name)
	return nil
}

// authTypeAliases maps what someone might type for -auth onto the canonical
// stored string.
var authTypeAliases = map[string]string{
	"password":             "password",
	"publickey":            "publickey",
	"public_key":           "publickey",
	"key":                  "publickey",
	"keyboard-interactive": "keyboard-interactive",
	"keyboard_interactive": "keyboard-interactive",
	"mfa":                  "keyboard-interactive",
	"agent":                "agent",
	"snmp-v2c":             vault.AuthTypeSNMPv2c,
	"snmpv2c":              vault.AuthTypeSNMPv2c,
	"snmp_v2c":             vault.AuthTypeSNMPv2c,
	"snmp-v2":              vault.AuthTypeSNMPv2c, // v2 without the c was never deployed
	"snmpv2":               vault.AuthTypeSNMPv2c,
	"v2c":                  vault.AuthTypeSNMPv2c,
	"snmp-v3":              vault.AuthTypeSNMPv3,
	"snmpv3":               vault.AuthTypeSNMPv3,
	"snmp_v3":              vault.AuthTypeSNMPv3,
	"v3":                   vault.AuthTypeSNMPv3,
}

// canonicalAuthType refuses an -auth value it does not know, rather than
// storing it as typed. The vault reads any unrecognized auth_type as a
// password, so "snmp-v2" stored verbatim would have been an SSH password
// credential holding a community string -- offered to every SSH device.
func canonicalAuthType(s string) (string, error) {
	if c, ok := authTypeAliases[strings.ToLower(strings.TrimSpace(s))]; ok {
		return c, nil
	}
	return "", fmt.Errorf("unknown -auth %q: use password, publickey, keyboard-interactive, agent, snmp-v2c, or snmp-v3", s)
}

// inferAuthType fills in -auth when it was not given. Supplying -key without
// -auth is the common case and means publickey; anything else defaults to
// password, which is what a bare -name/-user pair is asking for.
// nonEmpty is s as a one-element list, or nil when it is empty, for joinOr.
func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

func inferAuthType(authFlag, keyPath string) string {
	if authFlag != "" {
		return authFlag
	}
	if keyPath != "" {
		return "publickey"
	}
	return "password"
}

func state(m vault.Meta) string {
	var parts []string
	if m.Disabled {
		parts = append(parts, "disabled")
	}
	if m.IsDefault {
		parts = append(parts, "default")
	}
	if !m.HasSecret && m.AuthLabel != "agent" {
		parts = append(parts, "no-secret")
	}
	return joinOr(parts, "ok")
}

func scopeSummary(s vault.Scope) string {
	var parts []string
	if s.DomainSuffix != "" {
		parts = append(parts, "*."+s.DomainSuffix)
	}
	parts = append(parts, s.CIDRs...)
	parts = append(parts, s.Platforms...)
	return joinOr(parts, "any")
}

func joinOr(parts []string, empty string) string {
	if len(parts) == 0 {
		return empty
	}
	return strings.Join(parts, ",")
}

// parentDir is filepath.Dir without importing path/filepath for one call on a
// path that is already slash-separated by construction.
func parentDir(path string) string {
	i := strings.LastIndexAny(path, `/\`)
	if i <= 0 {
		return ""
	}
	return path[:i]
}
