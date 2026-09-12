// internal/crawldial/build.go
//
// One place that turns crawlrun.Params into a configured crawler.
//
// It exists because there are now two front ends. If cmd/crawl assembled its
// own crawler and cmd/crawlui assembled another, the two would drift — a flag
// default changed in one, a binding store opened at a different path in the
// other — and the difference would show up as a crawl that behaves one way
// from the terminal and another way from the window. Same intent has to
// produce the same crawl.
//
// Nothing here knows about a toolkit. That is deliberate: the setup is the
// part worth testing, and it should not need a display to run.
package crawldial

import (
	"fmt"
	"os"

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

// StaticCreds is the no-vault path: one credential for every device.
type StaticCreds struct {
	Username string
	Password string
	KeyPath  string
}

// Built is a crawler and the things a caller still needs afterwards.
type Built struct {
	Crawler  *crawler.Crawler
	Bindings *credres.FileBindings
	Resolver *credres.Resolver

	// SNMPBindings and SNMPResolver are set when SNMP credentials come from
	// the vault. Separate from the SSH pair; see SNMPBindingsPath. Fold
	// both after a crawl.
	SNMPBindings *credres.FileBindings
	SNMPResolver *credres.Resolver

	// CredNames maps credential ID to display name, for reporting. Never
	// secret material.
	CredNames map[string]string

	// Close releases the vault. Always non-nil.
	Close func()
}

// Options are the things Params deliberately does not carry: secrets, a
// bastion, and where output goes.
type Options struct {
	// Static is used when Params.VaultPath is empty.
	Static StaticCreds

	// Vault is an already-unlocked vault to use instead of opening
	// Params.VaultPath.
	//
	// A GUI unlocks once and has nowhere to prompt for a master password
	// afterwards. Without this, Build calls vaultcli.Open on the run's own
	// goroutine, that falls through to a terminal read nobody is watching,
	// and the run blocks forever before dialing anything — which looks
	// exactly like a device that will not answer.
	//
	// Params.VaultPath is still read: it names where the bindings live and
	// it is recorded with the run. It is simply not opened.
	Vault *vault.Vault

	// SNMP credentials, tried in this order on every device when the
	// methods include snmp. Secret material, which is why they are here and
	// not in Params: a saved profile must never hold a community string.
	SNMP []snmpprobe.Credential

	// Jump is an optional bastion.
	Jump *sshcore.JumpConfig

	// BindingsPath overrides the default, which sits beside the vault.
	BindingsPath string

	// Log receives crawl progress lines. Nil discards them.
	Log crawler.Logf

	// CredLog receives credential-resolution lines. Nil discards them.
	CredLog func(string, ...any)

	// Emit receives structured events. Nil is fine and costs nothing — it is
	// what makes this usable from a CLI that only wants the log.
	Emit crawlrun.Emit
}

// Build validates the parameters and assembles a crawler from them.
//
// Validation happens here rather than at the caller so both front ends refuse
// the same things. The returned error joins every validation problem, since a
// form wants to mark all the bad fields at once and a CLI wants to print them
// all rather than one per run.
func Build(p crawlrun.Params, opts Options) (*Built, error) {
	if errs := p.Validate(); len(errs) > 0 {
		msg := "invalid crawl parameters:"
		for _, e := range errs {
			msg += "\n  " + e.Error()
		}
		return nil, fmt.Errorf("%s", msg)
	}

	policy := sshcore.HostKeyStrict
	if p.HostKeys == crawlrun.HostKeyTOFU {
		policy = sshcore.HostKeyTOFU
	}

	// Refuse a run with nothing to authenticate with.
	//
	// capturedial has always had this guard; crawl did not, and the
	// difference showed: with a locked vault and empty static credentials
	// the crawler dials every device offering nothing, and every one comes
	// back "no handler for keyboard-interactive question: Password:" —
	// hundreds of identical failures that look like a network problem
	// rather than a missing unlock.
	// The test is "nothing at all", deliberately not "no password and no
	// key": a username alone is a complete credential when an SSH agent is
	// loaded, and the auth chain tries the agent first. capturedial's
	// version of this guard requires a password or a key and would reject
	// an agent-only run — that is a bug there, not a pattern to copy.
	// Per method: SSH credentials are only required of a run that will use
	// SSH, so an SNMP-only run is not refused for lacking a login it never
	// attempts.
	useSSH, useSNMP := p.Uses(crawlrun.MethodSSH), p.Uses(crawlrun.MethodSNMP)
	if useSSH && p.VaultPath == "" && opts.Vault == nil && opts.Static.Username == "" &&
		opts.Static.Password == "" && opts.Static.KeyPath == "" {
		msg := "no credentials: unlock a vault, or supply a username with a key, " +
			"a password, or a loaded SSH agent " +
			"(every device would fail the handshake otherwise)"
		// Naming a vault that exists beats silently adopting it: a run
		// that quietly picks one up is a run that quietly picks up
		// whichever credentials happen to be in it.
		if def := vaultcli.DefaultPath(); def != "" {
			if _, err := os.Stat(def); err == nil {
				msg += fmt.Sprintf("\n  a vault exists at %s", def)
			}
		}
		return nil, fmt.Errorf("%s", msg)
	}
	// SNMP credentials come from Options when supplied there, and otherwise
	// from the vault, resolved per device the way SSH credentials are.
	vaultMode := p.VaultPath != ""
	snmpFromVault := useSNMP && len(opts.SNMP) == 0
	if snmpFromVault && !vaultMode {
		return nil, fmt.Errorf("no SNMP credentials: the methods include snmp, none were supplied, " +
			"and no vault was named to resolve them from " +
			"(every device would time out on its first request)")
	}

	base := BaseConfig{
		OnNewHostKey:   HostKeyEmitter(opts.Emit),
		Announce:       dial.Stderr("crawl"),
		Legacy:         p.Legacy,
		HostKeys:       policy,
		Jump:           opts.Jump,
		KnownHostsPath: p.KnownHostsPath,
	}

	credLog := opts.CredLog
	if credLog == nil {
		credLog = func(string, ...any) {}
	}

	out := &Built{Close: func() {}}

	// The vault is opened only for a method that will read it. An SNMP-only
	// run handed its credentials directly, with a vault path left in its
	// profile, would otherwise stop to ask for a master password it has no
	// use for.
	var v *vault.Vault
	if vaultMode && (useSSH || snmpFromVault) {
		v = opts.Vault
		if v == nil {
			opened, err := vaultcli.Open(p.VaultPath)
			if err != nil {
				return nil, err
			}
			// Lock only what we opened. A caller-supplied vault
			// outlives this run, and locking it here would leave
			// the next one without credentials.
			out.Close = func() { opened.Lock() }
			v = opened
		}
		out.CredNames = map[string]string{}
		if metas, err := v.List(); err == nil {
			for _, m := range metas {
				out.CredNames[m.ID] = m.Name
			}
		}
	}
	bp := opts.BindingsPath
	if bp == "" {
		bp = vaultcli.BindingsPath(p.VaultPath)
	}

	var dialFn crawler.DialFunc
	switch {
	case !useSSH:
	case !vaultMode:
		dialFn = StaticDialer(base, opts.Static.Username, opts.Static.Password, opts.Static.KeyPath)
	default:
		bindings, err := credres.OpenFileBindings(bp)
		if err != nil {
			out.Close()
			return nil, err
		}
		out.Bindings = bindings
		out.Resolver = credres.New(v, bindings, credres.Config{
			BreakerThreshold: p.CredBreaker,
			MaxPerHost:       p.MaxCreds,
			Log:              credLog,
			Emit:             opts.Emit,
		})
		dialFn = dial.NewVault(out.Resolver, base, p.CredTags, credLog).Dial
	}

	var snmpFn crawler.SNMPFunc
	sopt := snmpprobe.Options{Timeout: p.SNMPTimeout, Log: opts.Log}
	switch {
	case !useSNMP:
	case !snmpFromVault:
		snmpFn = NewSNMPFunc(opts.SNMP, sopt)
	default:
		if !VaultHasSNMP(v) {
			out.Close()
			return nil, fmt.Errorf("no SNMP credentials: the methods include snmp and the vault at %s "+
				"holds no enabled SNMP credential (add one with: omvault add -auth snmp-v2c ...)", p.VaultPath)
		}
		sb, err := credres.OpenFileBindings(SNMPBindingsPath(bp))
		if err != nil {
			out.Close()
			return nil, err
		}
		out.SNMPBindings = sb
		// The breaker stays on: it counts only OutcomeAuthRejected, which
		// for SNMP is a v3 refusal -- real evidence. A v2c miss arrives as
		// OutcomeNoAnswer and counts for nothing.
		out.SNMPResolver = credres.New(v, sb, credres.Config{
			Method:           crawlrun.MethodSNMP,
			Classify:         SNMPOutcome,
			BreakerThreshold: p.CredBreaker,
			MaxPerHost:       p.MaxCreds,
			Log:              credLog,
			Emit:             opts.Emit,
		})
		snmpFn = NewVaultSNMPFunc(out.SNMPResolver, p.CredTags, sopt)
	}

	out.Crawler = crawler.New(crawler.Config{
		Dial:            dialFn,
		SNMP:            snmpFn,
		Methods:         p.CollectionMethods(),
		MaxDepth:        p.Depth,
		Concurrency:     p.Concurrency,
		ExcludePatterns: p.Exclude,
		AllowDomains:    p.AllowDomains,
		Domains:         p.Domains,
		SessionOpts:     netexec.Options{CommandTimeout: p.Timeout},
		Log:             opts.Log,
		Emit:            opts.Emit,
	})
	return out, nil
}

// Fold writes each device's own names back into the binding store after a
// crawl.
//
// This is the second of the two writes the store needs, and it exists because
// of an ordering fact: credres reports a successful authentication from inside
// Walk, which returns the moment the credential is accepted — before the
// session is open and before the prompt has been read. SysName cannot exist at
// that point. Without this pass the store only ever learns the string the
// crawler dialed, and a run seeded the other way never benefits.
//
// Connected devices only. For a device that answered, these names are
// first-hand: the prompt said so, and the address is the one that replied. For
// a device that failed, the same fields hold a neighbor's claim, which may be
// a truncated LLDP name or a chassis MAC — and a wrong alias hands one
// device's credential pin to another.
func Fold(bindings *credres.FileBindings, devices []*topo.Device, suffixes []string, logf crawler.Logf) {
	if bindings == nil {
		return
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	before := bindings.Len()
	folded := 0
	for _, d := range devices {
		if d == nil || d.Failed {
			continue
		}
		if err := bindings.Bind(d.Canonical(), d.Hostname, d.SysName, d.IPAddress); err != nil {
			logf("crawl: could not fold identity for %s: %v", d.Canonical(), err)
			continue
		}
		folded++
	}
	if after := bindings.Len(); after != before {
		// A drop means two shapes of one device just became one record. Worth
		// a line: it is the only visible sign the store stopped splitting.
		logf("crawl: folded %d device identities; binding records %d -> %d",
			folded, before, after)
	}

	// Identity does not depend on the suffix context any more — aliases span
	// the stripped and unstripped forms either way — so this is a check, not
	// a fix. Still worth one line, because a context the store has not seen
	// before is the condition under which records used to split quietly.
	if prior, isNew := bindings.NoteContext(suffixes); isNew && len(prior) > 0 {
		logf("crawl: binding store was written under %v; this run adds %v", prior, suffixes)
	}
}

// NewVaultDialer builds a per-device dialer backed by the credential
// resolver. Exported for cmd/crawl, which assembles its own pieces from flags
// rather than going through Build.
func NewVaultDialer(res *credres.Resolver, base BaseConfig, tags []string, log func(string, ...any)) crawler.DialFunc {
	if log == nil {
		log = func(string, ...any) {}
	}
	return dial.NewVault(res, base, tags, log).Dial
}

// MapOptions derives the topology options from the crawl parameters.
//
// It exists for the same reason Build does, and for a reason that already bit
// once: cmd/crawlui was wired up with StripDomains and quietly without
// TrustUnidirectional, so the window produced a different map from the same
// run than the terminal did — and the missing option is invisible in the
// result. You do not see the links that were dropped.
//
// Every front end goes through here, and build_test.go fails if a field is
// ever added to topo.Options without being mapped.
func MapOptions(p crawlrun.Params) topo.Options {
	return topo.Options{
		StripDomains:        p.Domains,
		TrustUnidirectional: p.TrustUnidirectional,
	}
}
