// internal/crawler/dialtarget_test.go
//
// The DialTarget contract. Everything downstream of the dial seam — the
// credential resolver, the jump binder — caches per device, and every one of
// those caches keys on DialTarget.Identity. If Identity and the crawler's own
// claim key ever drift apart, nothing fails: the caches simply warm twice and
// help neither path. That is the failure this file exists to make loud.
package crawler

import (
	"context"
	"errors"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/sshcore"
)

// stubResolver serves PTR and forward records from two maps; a missing entry
// is a lookup failure. Shared by the tests in this package.
type stubResolver struct {
	ptr     map[string][]string
	forward map[string][]string
}

func (s stubResolver) LookupAddr(addr string) ([]string, error) {
	if n, ok := s.ptr[addr]; ok {
		return n, nil
	}
	return nil, errors.New("no such host")
}

func (s stubResolver) LookupHost(host string) ([]string, error) {
	if a, ok := s.forward[host]; ok {
		return a, nil
	}
	return nil, errors.New("no such host")
}

// recordDial captures the DialTarget and refuses the connection, which stops
// crawlOne immediately after the seam. No SSH, no session, no fingerprint.
func recordDial(into *[]DialTarget) DialFunc {
	return func(_ context.Context, t DialTarget) (*sshcore.Client, error) {
		*into = append(*into, t)
		return nil, errors.New("refused by test")
	}
}

func TestDialTargetFields(t *testing.T) {
	tests := []struct {
		name    string
		domains []string
		ptr     map[string][]string
		forward map[string][]string
		seed    string
		want    DialTarget
		note    string
	}{
		{
			name: "name dialed as reported",
			seed: "lab-agg1.lab.example.net",
			want: DialTarget{
				Target:   "lab-agg1.lab.example.net",
				Reported: "lab-agg1.lab.example.net",
				Identity: "lab-agg1.lab.example.net",
				Depth:    0,
			},
		},
		{
			name:    "configured domain is stripped from the identity only",
			domains: []string{"lab.example.net"},
			forward: map[string][]string{"lab-agg1.lab.example.net": {"192.0.2.10"}},
			seed:    "lab-agg1.lab.example.net",
			want: DialTarget{
				Target:   "lab-agg1.lab.example.net",
				Reported: "lab-agg1.lab.example.net",
				Identity: "lab-agg1",
			},
			note: "Target keeps the FQDN because that is what resolves; " +
				"Identity is short so the same box seen either way is one key",
		},
		{
			name: "literal address populates Addr",
			seed: "192.0.2.10",
			want: DialTarget{
				Target:   "192.0.2.10",
				Reported: "192.0.2.10",
				Identity: "192.0.2.10",
				Addr:     "192.0.2.10",
			},
		},
		{
			name:    "CGNAT with a forward-confirmed PTR keys on the name",
			ptr:     map[string][]string{"100.64.12.7": {"lab-agg1.lab.example.net."}},
			forward: map[string][]string{"lab-agg1.lab.example.net": {"100.64.12.7"}},
			seed:    "100.64.12.7",
			want: DialTarget{
				Target:   "lab-agg1.lab.example.net",
				Reported: "100.64.12.7",
				Identity: "lab-agg1.lab.example.net",
				Addr:     "",
			},
			note: "resolution now happens before the claim, so Identity is " +
				"derived from what is actually dialed. Reported keeps the " +
				"address so the substitution stays visible in a log.",
		},
		{
			name:    "CGNAT resolved and then suffix-stripped",
			domains: []string{"lab.example.net"},
			ptr:     map[string][]string{"100.64.12.7": {"lab-agg1.lab.example.net."}},
			forward: map[string][]string{"lab-agg1.lab.example.net": {"100.64.12.7"}},
			seed:    "100.64.12.7",
			want: DialTarget{
				Target:   "lab-agg1.lab.example.net",
				Reported: "100.64.12.7",
				Identity: "lab-agg1",
			},
			note: "the CGNAT address and a neighbor's short claim of lab-agg1 " +
				"now land on one key",
		},
		{
			name: "CGNAT whose PTR does not forward-resolve stays on the address",
			ptr:  map[string][]string{"100.64.12.8": {"stale.lab.example.net."}},
			seed: "100.64.12.8",
			want: DialTarget{
				Target:   "100.64.12.8",
				Reported: "100.64.12.8",
				Identity: "100.64.12.8",
				Addr:     "100.64.12.8",
			},
			note: "a stale reverse record must not produce a name that will " +
				"fail to dial where the address would have worked",
		},
		{
			name: "non-CGNAT private address is not reverse-resolved",
			ptr:  map[string][]string{"10.1.1.1": {"should-not-be-used.lab."}},
			seed: "10.1.1.1",
			want: DialTarget{
				Target:   "10.1.1.1",
				Reported: "10.1.1.1",
				Identity: "10.1.1.1",
				Addr:     "10.1.1.1",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var seen []DialTarget
			c := New(Config{
				Dial:    recordDial(&seen),
				Domains: tc.domains,
			})
			c.resolver = stubResolver{ptr: tc.ptr, forward: tc.forward}
			c.Crawl([]string{tc.seed})

			if len(seen) != 1 {
				t.Fatalf("dial called %d time(s), want 1", len(seen))
			}
			if got := seen[0]; got != tc.want {
				t.Errorf("DialTarget mismatch\n got: %+v\nwant: %+v", got, tc.want)
				if tc.note != "" {
					t.Logf("note: %s", tc.note)
				}
			}
		})
	}
}

// TestDialTargetIdentityMatchesClaim is the invariant the whole seam rests on:
// the Identity handed to the dial layer is the same key the crawler used to
// claim the device. Anything caching on Identity is then caching per crawled
// device, exactly once.
func TestDialTargetIdentityMatchesClaim(t *testing.T) {
	var seen []DialTarget
	c := New(Config{
		Dial:    recordDial(&seen),
		Domains: []string{"lab.example.net"},
	})
	c.resolver = stubResolver{forward: map[string][]string{
		"lab-agg1.lab.example.net": {"192.0.2.10"},
	}}
	c.Crawl([]string{"lab-agg1.lab.example.net"})

	if len(seen) != 1 {
		t.Fatalf("dial called %d time(s), want 1", len(seen))
	}
	id := seen[0].Identity

	// The claim set is keyed on the same value, so re-claiming it fails.
	if c.tryClaim(id) {
		t.Errorf("identity %q was not already claimed; DialTarget.Identity has "+
			"drifted from the crawler's claim key", id)
	}
	// And the short form is claimed too, so the device is not re-crawled
	// under a name discovered later.
	if c.tryClaim("lab-agg1") {
		t.Error("short name was not claimed alongside the identity")
	}
}

// TestCGNATSeedIsClaimedUnderTheResolvedName is gap 4 stated as a test: a
// device reached first by CGNAT address and later claimed by a neighbor under
// its name must be crawled once. Before resolution moved ahead of the claim,
// the address and the name were two different keys and the device was crawled
// twice — with two credential-cache entries and two map nodes.
func TestCGNATSeedIsClaimedUnderTheResolvedName(t *testing.T) {
	var seen []DialTarget
	c := New(Config{
		Dial:    recordDial(&seen),
		Domains: []string{"lab.example.net"},
	})
	c.resolver = stubResolver{
		ptr:     map[string][]string{"100.64.12.7": {"lab-agg1.lab.example.net."}},
		forward: map[string][]string{"lab-agg1.lab.example.net": {"100.64.12.7"}},
	}
	c.Crawl([]string{"100.64.12.7"})

	if len(seen) != 1 {
		t.Fatalf("dial called %d time(s), want 1", len(seen))
	}
	for _, later := range []string{"lab-agg1", "lab-agg1.lab.example.net", "LAB-AGG1.Lab.Example.Net."} {
		if _, ok := c.admit(later, "", 1, ""); ok {
			t.Errorf("%q was admitted after the CGNAT form was crawled; "+
				"the device would be crawled twice", later)
		}
	}
}
