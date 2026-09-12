package crawldial

import (
	"context"
	"errors"
	"strings"

	"github.com/scottpeterman/omegamaps/internal/crawler"
	"github.com/scottpeterman/omegamaps/internal/credres"
	"github.com/scottpeterman/omegamaps/internal/dial"
	"github.com/scottpeterman/omegamaps/internal/snmpprobe"
	"github.com/scottpeterman/omegamaps/internal/topo"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

// NewSNMPFunc builds the crawler's SNMP collector from a fixed credential
// list: each device is probed with creds in order, first answer wins (see
// snmpprobe.ProbeCredentials). This is the no-vault path, the SNMP
// counterpart of StaticDialer.
//
// Exported for cmd/crawl, which assembles its own crawler from flags, so the
// CLI and Build cannot collect SNMP differently.
//
// A target may carry a port, and that port is SSH's -- a seed written as
// host:2222 names where to log in. It is stripped here; SNMP goes to the
// prober's port.
func NewSNMPFunc(creds []snmpprobe.Credential, opt snmpprobe.Options) crawler.SNMPFunc {
	p := snmpprobe.New(opt)
	return func(ctx context.Context, t crawler.DialTarget) (*topo.Device, error) {
		host, _ := dial.SplitTarget(t.Target)
		return p.ProbeCredentials(ctx, host, creds)
	}
}

// NewVaultSNMPFunc builds the crawler's SNMP collector on a credential
// resolver, which must have been built with Method snmp and Classify
// SNMPOutcome. Candidates are ordered and filtered exactly as SSH candidates
// are -- binding first, then scope, priority and tags -- so after one crawl the
// community that answered a device is the first one offered to it.
//
// The resolver's Walk knows nothing about SNMP. What it is told is the
// outcome of each attempt, and SNMPOutcome is what makes those truthful.
func NewVaultSNMPFunc(res *credres.Resolver, tags []string, opt snmpprobe.Options) crawler.SNMPFunc {
	p := snmpprobe.New(opt)
	return func(ctx context.Context, t crawler.DialTarget) (*topo.Device, error) {
		host, _ := dial.SplitTarget(t.Target)
		var d *topo.Device
		_, err := res.Walk(credres.Target{
			Identity: t.Identity,
			Addr:     t.Addr,
			Aliases:  []string{t.Target, t.Reported},
			Tags:     tags,
			Pin:      t.Credential,
		}, func(c vault.Credential) error {
			sc, err := snmpprobe.FromVault(c)
			if err != nil {
				d = nil
				return &snmpprobe.Error{Target: host, Stage: snmpprobe.StageCredential, Err: err}
			}
			var perr error
			d, perr = p.Probe(ctx, host, sc)
			return perr
		})
		if err == nil {
			return d, nil
		}
		if d == nil {
			// Nothing was probed: no eligible credential, or the last
			// one could not be used at all.
			d = &topo.Device{Hostname: host, Failed: true,
				FailedWhy: "snmp " + snmpprobe.StageCredential + ": " + err.Error()}
		}
		return d, err
	}
}

// SNMPOutcome maps a probe error onto the resolver's outcomes.
//
//   - no answer to the first request: OutcomeNoAnswer. Try the next
//     credential; count nothing, because a host that is down says the same.
//   - a v3 USM refusal: OutcomeAuthRejected. That one is evidence.
//   - a credential that cannot be used (bad protocol name, not an SNMP kind):
//     OutcomeKeyMaterial -- misconfigured, not rejected.
//   - anything else -- no route, nothing listening, a walk failing after a
//     credential was accepted: OutcomeUnreachable, which ends the ladder.
func SNMPOutcome(err error) credres.Outcome {
	if err == nil {
		return credres.OutcomeSuccess
	}
	var pe *snmpprobe.Error
	switch {
	case !errors.As(err, &pe):
		return credres.OutcomeOther
	case pe.Stage == snmpprobe.StageCredential:
		return credres.OutcomeKeyMaterial
	case !snmpprobe.CredentialRejected(err):
		return credres.OutcomeUnreachable
	case errors.Is(err, snmpprobe.ErrNoResponse):
		return credres.OutcomeNoAnswer
	default:
		return credres.OutcomeAuthRejected
	}
}

// SNMPBindingsPath is where SNMP credential bindings live, beside the SSH
// ones. They cannot share a file: a binding record holds one credential per
// device, and a switch with both a login and a community would have the two
// overwrite each other on every crawl that used both methods.
func SNMPBindingsPath(bindingsPath string) string {
	if strings.HasSuffix(bindingsPath, ".bindings.json") {
		return strings.TrimSuffix(bindingsPath, ".bindings.json") + ".snmp.bindings.json"
	}
	return strings.TrimSuffix(bindingsPath, ".json") + ".snmp.json"
}

// VaultHasSNMP reports whether the vault holds an enabled SNMP credential.
func VaultHasSNMP(v *vault.Vault) bool {
	all, err := v.All()
	if err != nil {
		return false
	}
	for _, c := range all {
		if c.IsSNMP() && !c.Disabled {
			return true
		}
	}
	return false
}
