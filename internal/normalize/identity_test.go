// internal/normalize/identity_test.go
//
// The identity rule is shared by the crawler, the reach CLI, and credres, so
// these cases are the contract all three depend on. The forward-confirm cases
// are the ones that matter most: they are the behavior credres used to get
// wrong, and getting them wrong produces no error at all.
package normalize

import (
	"errors"
	"testing"
)

// stubResolver serves PTR and forward records from two maps. A missing entry
// is a lookup failure.
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

func TestResolveWith(t *testing.T) {
	r := stubResolver{
		ptr: map[string][]string{
			"100.71.4.9":  {"spine1.lab.example.net."},
			"100.71.5.9":  {"stale.lab.example.net."},
			"100.71.6.9":  {""},
			"10.20.4.9":   {"never-consulted.lab.example.net."},
			"192.0.2.7":   {"also-never.lab.example.net."},
			"100.71.99.1": {},
		},
		forward: map[string][]string{
			"spine1.lab.example.net": {"100.71.4.9"},
		},
	}

	tests := []struct {
		name      string
		in        string
		want      string
		cgnat     bool
		ptr       string
		confirmed bool
	}{
		{
			name:      "CGNAT with a forward-confirmed PTR adopts the name",
			in:        "100.71.4.9",
			want:      "spine1.lab.example.net",
			cgnat:     true,
			ptr:       "spine1.lab.example.net",
			confirmed: true,
		},
		{
			name:  "CGNAT with a stale PTR keeps the address",
			in:    "100.71.5.9",
			want:  "100.71.5.9",
			cgnat: true,
			ptr:   "stale.lab.example.net",
		},
		{
			name:  "CGNAT with no PTR keeps the address",
			in:    "100.71.7.7",
			want:  "100.71.7.7",
			cgnat: true,
		},
		{
			name:  "CGNAT with an empty PTR list keeps the address",
			in:    "100.71.99.1",
			want:  "100.71.99.1",
			cgnat: true,
		},
		{
			name:  "CGNAT with an empty PTR string keeps the address",
			in:    "100.71.6.9",
			want:  "100.71.6.9",
			cgnat: true,
		},
		{
			name: "RFC1918 address is never reverse-resolved",
			in:   "10.20.4.9",
			want: "10.20.4.9",
		},
		{
			name: "public address is never reverse-resolved",
			in:   "192.0.2.7",
			want: "192.0.2.7",
		},
		{
			name: "a name passes through untouched",
			in:   "spine1.lab.example.net",
			want: "spine1.lab.example.net",
		},
		{
			// 100.64/10 is not all of 100/8. 100.128.0.1 is public space and
			// must not be treated as shared.
			name: "an address just outside the CGNAT prefix is public",
			in:   "100.128.0.1",
			want: "100.128.0.1",
		},
		{
			name:  "the low edge of the CGNAT prefix is inside it",
			in:    "100.64.0.0",
			want:  "100.64.0.0",
			cgnat: true,
		},
		{
			name:  "the high edge of the CGNAT prefix is inside it",
			in:    "100.127.255.255",
			want:  "100.127.255.255",
			cgnat: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveWith(r, tc.in)
			if got.Name != tc.want {
				t.Errorf("Name = %q, want %q", got.Name, tc.want)
			}
			if got.CGNAT != tc.cgnat {
				t.Errorf("CGNAT = %v, want %v", got.CGNAT, tc.cgnat)
			}
			if got.PTR != tc.ptr {
				t.Errorf("PTR = %q, want %q", got.PTR, tc.ptr)
			}
			if got.Confirmed != tc.confirmed {
				t.Errorf("Confirmed = %v, want %v", got.Confirmed, tc.confirmed)
			}
		})
	}
}

func TestCanonicalWith(t *testing.T) {
	r := stubResolver{
		ptr:     map[string][]string{"100.71.4.9": {"spine1.lab.example.net."}},
		forward: map[string][]string{"spine1.lab.example.net": {"100.71.4.9"}},
	}
	suffixes := []string{"lab.example.net", "mgmt.example.net"}

	tests := []struct {
		name     string
		in       string
		suffixes []string
		want     string
	}{
		{"CGNAT resolves then strips", "100.71.4.9", suffixes, "spine1"},
		{"CGNAT resolves, no suffixes configured", "100.71.4.9", nil, "spine1.lab.example.net"},
		{"unresolvable CGNAT keys on the address", "100.71.9.9", suffixes, "100.71.9.9"},
		{"FQDN strips to the same key as the CGNAT form", "spine1.lab.example.net", suffixes, "spine1"},
		{"second suffix in the list also strips", "leaf2.mgmt.example.net", suffixes, "leaf2"},
		{"case and trailing dot normalize", "SPINE1.Lab.Example.Net.", nil, "spine1.lab.example.net"},
		{"case and trailing dot normalize with stripping", "SPINE1.Lab.Example.Net.", suffixes, "spine1"},
		{"a non-matching suffix leaves the name whole", "spine1.other.example.net", suffixes, "spine1.other.example.net"},
		{"empty input stays empty", "", suffixes, ""},
		{"whitespace-only input stays empty", "   ", suffixes, ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanonicalWith(r, tc.in, tc.suffixes); got != tc.want {
				t.Errorf("CanonicalWith(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestCanonicalAgreesAcrossForms is the invariant the caches rest on: the same
// device, named three different ways, produces one key.
func TestCanonicalAgreesAcrossForms(t *testing.T) {
	r := stubResolver{
		ptr:     map[string][]string{"100.71.4.9": {"spine1.lab.example.net."}},
		forward: map[string][]string{"spine1.lab.example.net": {"100.71.4.9"}},
	}
	suffixes := []string{"lab.example.net"}

	forms := []string{
		"100.71.4.9",              // reached by CGNAT address on one hop
		"spine1.lab.example.net",  // claimed fully qualified by a neighbor
		"SPINE1.Lab.Example.Net.", // and again with different case and a dot
		"spine1",                  // and claimed short by another
	}

	want := CanonicalWith(r, forms[0], suffixes)
	for _, f := range forms[1:] {
		if got := CanonicalWith(r, f, suffixes); got != want {
			t.Errorf("%q keyed as %q, want %q — one device, two cache entries", f, got, want)
		}
	}
}

// TestResolveDoesNotLookUpNonCGNAT guards the cost promise: a crawl of named
// devices must not generate a reverse lookup per device.
func TestResolveDoesNotLookUpNonCGNAT(t *testing.T) {
	var calls int
	r := countingResolver{n: &calls}

	for _, in := range []string{"spine1.lab.example.net", "10.0.0.1", "192.0.2.1", "100.128.0.1"} {
		ResolveWith(r, in)
	}
	if calls != 0 {
		t.Errorf("%d lookup(s) for non-CGNAT input, want 0", calls)
	}
}

type countingResolver struct{ n *int }

func (c countingResolver) LookupAddr(string) ([]string, error) {
	*c.n++
	return nil, errors.New("no")
}

func (c countingResolver) LookupHost(string) ([]string, error) {
	*c.n++
	return nil, errors.New("no")
}
