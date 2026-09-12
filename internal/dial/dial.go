// internal/dial/dial.go
//
// How a connection to a device gets made, independent of why.
//
// This started life inside the crawler and then inside crawldial, which was
// fine while crawl was the only thing that dialed. Capture is the second
// consumer and wants exactly the same behavior — one credential from flags, or
// a vault ladder ordered per device — so the choice was between capture
// importing "crawler" for a type that has nothing to do with crawling, or the
// two of them growing their own dial layers. The second is how a flag goes
// missing from one front end and nobody notices; the first is a lie in the
// import list.
//
// So the primitives live here and know nothing about crawls, maps, or
// captures. What stays in crawldial is the crawl-specific assembly — building
// a Crawler from crawl parameters, mapping topo options, folding identities —
// none of which capture has any use for.
//
// # Why the host-key callback is a plain function
//
// BaseConfig used to carry a crawlrun.Emit so a first-contact key landed in
// the crawl's decisions pane. A package named "dial" importing a package named
// "crawlrun" would put the same lie back in the import list, so the field is
// now an untyped callback and each front end adapts it to whatever its own run
// model calls an event.
package dial

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/scottpeterman/omegamaps/internal/credres"
	"github.com/scottpeterman/omegamaps/internal/sshcore"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

// Target describes one device something wants a connection to.
//
// The caller does not know how the connection gets made — which credential,
// which bastion, which host-key policy. It knows which device it means, and
// this carries enough for the dial layer to decide the rest without the caller
// learning anything about either.
type Target struct {
	// Target is the string to actually dial: post-CGNAT resolution and
	// post-domain completion. A transport detail, not an identity — the
	// same device can be dialed by address on one hop and by name on
	// another.
	Target string

	// Reported is the claim as received, before any resolution.
	// Diagnostics only: it is what makes "dialed X, claimed as Y"
	// legible in a log.
	Reported string

	// Credential names a vault entry — by name or id — that this device is
	// known to use, and it is tried first.
	//
	// It is how a caller that already knows the answer says so: a session
	// in the inventory names its credential, and a run driven from that
	// inventory should not rediscover it by walking a ladder. Empty means
	// the caller has nothing to say, which is every crawl (a crawl meets
	// devices nobody has written anything down about yet).
	//
	// Ignored by Static, which has exactly one credential and no choice to
	// make.
	Credential string

	// Identity is the key this device was claimed under. Anything caching
	// per device — credential bindings, jump bindings — keys on this and
	// never on Target, or one device warms two entries and neither one
	// helps.
	Identity string

	// Addr is the literal address when one is already known, for CIDR
	// scope matching. Set only when Target is itself an address; a device
	// known by name has no address here until after the connection is up.
	Addr string

	// Depth is the BFS depth this device was reached at, for callers that
	// discovered it by walking. Seeds are 0, and a caller working from a
	// device list leaves it 0 throughout — it is a property of how the
	// target was found, not of the target.
	Depth int
}

// Func opens an SSH connection to a target.
//
// The context is the caller's cancellation signal. A dial layer that honors it
// lets a stopped run abandon a connection already in progress; one that
// ignores it still works, but a stop waits out that device's timeout.
type Func func(ctx context.Context, t Target) (*sshcore.Client, error)

// NewHostKeyFunc is called when a key is trusted on first contact. host is
// the device's identity when the dialer was given one (see ApplyFor), and the
// dialed host otherwise.
//
// A plain function rather than an event type: this package must not know what
// a run model is. Both front ends adapt it — crawl to a crawlrun event,
// capture to a capturerun one.
type NewHostKeyFunc func(host, keyType, fingerprint string)

// BaseConfig is the part of an sshcore.Config that does not vary per device or
// per credential: algorithm policy, host-key policy, the bastion, and where
// discovered host keys are written.
type BaseConfig struct {
	// OnNewHostKey reports a key trusted on first contact. Discovery meets
	// devices it has never seen, so an unknown key is the normal case and
	// TOFU accepts it — recording what was seen is what makes that
	// auditable rather than blind. A key that CHANGED never reaches here;
	// sshcore fails it closed.
	OnNewHostKey NewHostKeyFunc

	// Announce writes a human-readable line about the same event. nil
	// discards it; the CLIs point this at stderr.
	Announce func(format string, args ...any)

	Legacy         bool
	HostKeys       sshcore.HostKeyPolicy
	Jump           *sshcore.JumpConfig
	KnownHostsPath string
}

// SplitTarget separates a target into a host and a port, where 0 means the
// transport's default.
//
// A target carries its port because the alternative is a second field to
// thread through every list, file and form that names a device, and the one
// place a port is genuinely per-device is the place a device list is least
// structured. Console servers and lab topologies both put many devices behind
// one address on different ports, and until this existed neither could be
// captured at all.
//
// Only a target that splits cleanly is split. A bare name has no colon, a bare
// IPv6 address has too many, and both are returned whole with port 0 — which
// is what makes this safe to apply to every target rather than only the ones
// somebody flagged.
func SplitTarget(target string) (host string, port int) {
	target = strings.TrimSpace(target)
	h, p, err := net.SplitHostPort(target)
	if err != nil {
		// A bracketed IPv6 with no port still needs its brackets off:
		// sshcore joins host and port itself and would bracket it twice.
		if strings.HasPrefix(target, "[") && strings.HasSuffix(target, "]") {
			return strings.TrimSuffix(strings.TrimPrefix(target, "["), "]"), 0
		}
		return target, 0
	}
	n, err := strconv.Atoi(p)
	if err != nil || n <= 0 || n > 65535 {
		// Splittable but not a port number. Dialing the whole string and
		// failing says more than silently dropping the part that is wrong.
		return target, 0
	}
	return h, n
}

// Apply builds the dial config for one target. Credentials are layered on top
// by the caller, because in vault mode there is more than one to try.
//
// The target may carry a port. Every dialer in this package goes through here,
// so teaching this one function is what gives crawl and capture the same
// answer — a port understood by one front end and not the other is the shape
// of bug this package exists to prevent.
func (b BaseConfig) Apply(target string) sshcore.Config { return b.ApplyFor(target, "") }

// ApplyFor is Apply for a device the caller knows the identity of, and it is
// what every dialer here uses. The difference is who a first-contact host key
// is reported against: the device, not the string that happened to be dialed.
//
// Reporting it against the dialed string was a bug with a visible cost. A
// device unreachable by name is retried at the address its neighbor reported,
// so the key arrives while dialing an address -- and an event keyed on that
// address is, to the run model, a second device. It sat queued behind the real
// one until the run ended and was then counted as failed: every device reached
// by address showed up twice, once as a failure, on any network without DNS
// for its device names.
func (b BaseConfig) ApplyFor(target, identity string) sshcore.Config {
	host, port := SplitTarget(target)
	who := identity
	if who == "" {
		who = host
	}
	return sshcore.Config{
		Host:             host,
		Port:             port,
		LegacyAlgorithms: b.Legacy,
		HostKeys:         b.HostKeys,
		Jump:             b.Jump,
		KnownHostsPath:   b.KnownHostsPath,
		// Auto-accept unknown keys on first contact and persist to
		// known_hosts. Key MISMATCH still fails closed in sshcore
		// regardless of this callback.
		HostKeyPrompt: func(hostname string, remote net.Addr, key ssh.PublicKey) (bool, error) {
			fp := ssh.FingerprintSHA256(key)
			if b.OnNewHostKey != nil {
				b.OnNewHostKey(who, key.Type(), fp)
			}
			if b.Announce != nil {
				b.Announce("accepting new host key for %s (%s %s)", hostname, key.Type(), fp)
			}
			return true, nil
		},
	}
}

// Stderr is the Announce most CLIs want.
func Stderr(prefix string) func(string, ...any) {
	return func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, prefix+": "+format+"\n", args...)
	}
}

// Static uses one credential for every device.
func Static(base BaseConfig, user, password, keyPath string) Func {
	return func(ctx context.Context, t Target) (*sshcore.Client, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cfg := base.ApplyFor(t.Target, t.Identity)
		cfg.Username = user
		cfg.Password = password
		cfg.PrivateKeyPath = keyPath
		return sshcore.Dial(cfg)
	}
}

// Vault resolves credentials per device out of the vault.
type Vault struct {
	res  *credres.Resolver
	base BaseConfig
	tags []string
	log  func(string, ...any)

	// dial is indirected so the credential ladder can be tested without a
	// server. nil means sshcore.Dial. This is the only seam in the file
	// that exists for tests, and it earns its place: the ladder is the
	// code that decides how many authentication attempts a device sees.
	dial func(sshcore.Config) (*sshcore.Client, error)
}

// NewVault builds a vault-backed dialer.
func NewVault(res *credres.Resolver, base BaseConfig, tags []string, log func(string, ...any)) *Vault {
	return &Vault{res: res, base: base, tags: tags, log: log}
}

func (d *Vault) dialFn() func(sshcore.Config) (*sshcore.Client, error) {
	if d.dial != nil {
		return d.dial
	}
	return sshcore.Dial
}

// applyCredential layers one credential onto a dial config. The credential's
// declared auth type decides which fields are set — deliberately not "set
// everything that is non-empty", because offering a password alongside a key
// turns one ladder rung into two authentication attempts against the device
// and doubles the lockout exposure for the same credential.
func applyCredential(cfg *sshcore.Config, c vault.Credential) {
	cfg.Username = c.Username
	switch c.Method() {
	case vault.AuthPublicKey:
		cfg.PrivateKeyPath = c.KeyPath
		cfg.KeyPassphrase = c.KeyPassphrase
	case vault.AuthAgent:
		cfg.UseAgent = true
	default:
		// Password and keyboard-interactive both answer with the
		// password; sshcore always offers keyboard-interactive, so a
		// device that only exposes a password prompt through KI still
		// works.
		cfg.Password = c.Password
	}
}

// Dial is the Func.
func (d *Vault) Dial(ctx context.Context, t Target) (*sshcore.Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target := credres.Target{
		// Identity comes from the caller, already resolved and already
		// the key the device was claimed under. Recomputing it here
		// would risk a second DNS answer and a second binding-cache
		// entry for one device.
		Identity: t.Identity,
		Addr:     t.Addr,
		// Aliases widen the binding lookup to the other shapes this
		// device answers to: the string actually dialed, and the claim
		// before resolution. Neither is evidence about a credential —
		// they only stop one device warming two records because a
		// name-seeded run and an address-seeded run reached it
		// differently.
		Aliases: []string{t.Target, t.Reported},
		Tags:    d.tags,
		Pin:     t.Credential,
		// Platform is empty: the fingerprint does not exist until after
		// this connection is up, and the neighbor claim that does carry
		// a platform hint is dropped at enqueue. Platform-scoped
		// credentials are therefore skipped rather than guessed at,
		// which is credres's documented behavior for an unknown
		// platform.
	}

	var client *sshcore.Client
	_, err := d.res.Walk(target, func(c vault.Credential) error {
		cfg := d.base.ApplyFor(t.Target, t.Identity)
		applyCredential(&cfg, c)
		cl, derr := d.dialFn()(cfg)
		if derr != nil {
			return derr
		}
		client = cl
		return nil
	})
	if err != nil {
		return nil, err
	}
	if client == nil {
		// Walk reported success without a connection. Not reachable
		// today; guarded because returning (nil, nil) would panic the
		// caller on the next line rather than fail the device.
		return nil, fmt.Errorf("credential walk succeeded but no connection was established")
	}
	return client, nil
}
