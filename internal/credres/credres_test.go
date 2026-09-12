// internal/credres/credres_test.go
package credres

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/secfile"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

// fakeStore is a Store backed by a fixed slice.
type fakeStore struct {
	creds []vault.Credential
	err   error
}

func (f fakeStore) All() ([]vault.Credential, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.creds, nil
}

func cred(id, name string, opts ...func(*vault.Credential)) vault.Credential {
	c := vault.Credential{ID: id, Name: name, AuthType: "password", Username: "netops"}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func withPriority(p int) func(*vault.Credential) {
	return func(c *vault.Credential) { c.Priority = p }
}
func withKeyAuth() func(*vault.Credential) {
	return func(c *vault.Credential) { c.AuthType = "publickey" }
}
func withScope(s vault.Scope) func(*vault.Credential) {
	return func(c *vault.Credential) { c.Scope = s }
}
func withTags(t ...string) func(*vault.Credential) {
	return func(c *vault.Credential) { c.Tags = t }
}
func disabled() func(*vault.Credential) {
	return func(c *vault.Credential) { c.Disabled = true }
}

func ids(cands []Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.Cred.ID
	}
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestRankingOrder(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-pw-late", "zeta-password", withPriority(20)),
		cred("c-pw-early", "alpha-password", withPriority(10)),
		cred("c-key", "alpha-key", withPriority(10), withKeyAuth()),
		cred("c-scoped", "site-scoped", withPriority(99),
			withScope(vault.Scope{DomainSuffix: "lab.example.net"})),
	}}
	r := New(store, nil, Config{MaxPerHost: -1})

	got, err := r.Resolve(Target{Identity: "spine1.lab.example.net"})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Scoped wins on specificity despite priority 99. Then priority 10, key
	// before password. Then priority 20.
	want := []string{"c-scoped", "c-key", "c-pw-early", "c-pw-late"}
	if !equal(ids(got), want) {
		t.Fatalf("order = %v, want %v", ids(got), want)
	}
}

func TestDisabledAndTagFiltering(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-off", "turned-off", disabled()),
		cred("c-tagged", "lab-only", withTags("lab")),
		cred("c-plain", "no-tags"),
	}}
	r := New(store, nil, Config{MaxPerHost: -1})

	got, _ := r.Resolve(Target{Identity: "spine1.lab.example.net", Tags: []string{"lab"}})
	if !equal(ids(got), []string{"c-tagged"}) {
		t.Fatalf("tag filter = %v, want [c-tagged]", ids(got))
	}
}

func TestPlatformScopeSkippedBeforeFingerprint(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-eos", "eos-only", withScope(vault.Scope{Platforms: []string{"arista_eos"}})),
		cred("c-any", "unscoped"),
	}}
	r := New(store, nil, Config{MaxPerHost: -1})

	got, _ := r.Resolve(Target{Identity: "spine1.lab.example.net"})
	if !equal(ids(got), []string{"c-any"}) {
		t.Fatalf("unfingerprinted = %v, want [c-any]", ids(got))
	}

	got, _ = r.Resolve(Target{Identity: "spine1.lab.example.net", Platform: "arista_eos"})
	if !equal(ids(got), []string{"c-eos", "c-any"}) {
		t.Fatalf("fingerprinted = %v, want [c-eos c-any]", ids(got))
	}
}

func TestCIDRScope(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-mgmt", "mgmt-range", withScope(vault.Scope{CIDRs: []string{"10.20.0.0/16"}})),
		cred("c-any", "unscoped"),
	}}
	r := New(store, nil, Config{MaxPerHost: -1})

	got, _ := r.Resolve(Target{Identity: "spine1.lab.example.net", Addr: "10.20.4.9"})
	if !equal(ids(got), []string{"c-mgmt", "c-any"}) {
		t.Fatalf("in-range = %v", ids(got))
	}
	got, _ = r.Resolve(Target{Identity: "spine1.lab.example.net", Addr: "10.99.4.9"})
	if !equal(ids(got), []string{"c-any"}) {
		t.Fatalf("out-of-range = %v", ids(got))
	}
}

func TestPinnedCredentialGoesFirst(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-1", "first", withPriority(1)),
		cred("c-2", "second", withPriority(2)),
		cred("c-3", "third", withPriority(3)),
	}}
	b := NewMemoryBindings()
	_ = b.Record("c-3", "spine1.lab.example.net")
	r := New(store, b, Config{MaxPerHost: -1})

	got, _ := r.Resolve(Target{Identity: "spine1.lab.example.net"})
	if got[0].Cred.ID != "c-3" || got[0].Reason != ReasonPinned {
		t.Fatalf("head = %v (%v), want c-3 pinned", got[0].Cred.ID, got[0].Reason)
	}
	if !equal(ids(got), []string{"c-3", "c-1", "c-2"}) {
		t.Fatalf("order = %v", ids(got))
	}
}

func TestPromotionSpeedsUpUnknownHosts(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-1", "first", withPriority(1)),
		cred("c-2", "second", withPriority(2)),
		cred("c-3", "third", withPriority(3)),
	}}
	r := New(store, NewMemoryBindings(), Config{MaxPerHost: -1})

	// c-3 works on the first device.
	r.Report(Target{Identity: "spine1.lab.example.net"}, "c-3", OutcomeSuccess)

	// A different, never-seen device should now try c-3 first.
	got, _ := r.Resolve(Target{Identity: "spine2.lab.example.net"})
	if got[0].Cred.ID != "c-3" || got[0].Reason != ReasonPromoted {
		t.Fatalf("head = %v (%v), want c-3 promoted", got[0].Cred.ID, got[0].Reason)
	}
}

func TestNegativeCacheSkipsRejected(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-1", "first", withPriority(1)),
		cred("c-2", "second", withPriority(2)),
	}}
	r := New(store, nil, Config{MaxPerHost: -1})
	tgt := Target{Identity: "spine1.lab.example.net"}

	r.Report(tgt, "c-1", OutcomeAuthRejected)
	got, _ := r.Resolve(tgt)
	if !equal(ids(got), []string{"c-2"}) {
		t.Fatalf("after rejection = %v, want [c-2]", ids(got))
	}
	// A different device is unaffected.
	got, _ = r.Resolve(Target{Identity: "spine2.lab.example.net"})
	if !equal(ids(got), []string{"c-1", "c-2"}) {
		t.Fatalf("other device = %v", ids(got))
	}
}

func TestBreakerParksCredentialAcrossDistinctHosts(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-stale", "stale", withPriority(1)),
		cred("c-good", "good", withPriority(2)),
	}}
	r := New(store, nil, Config{BreakerThreshold: 3, MaxPerHost: -1})

	for i := 1; i <= 3; i++ {
		r.Report(Target{Identity: fmt.Sprintf("spine%d.lab.example.net", i)},
			"c-stale", OutcomeAuthRejected)
	}
	got, _ := r.Resolve(Target{Identity: "spine9.lab.example.net"})
	if !equal(ids(got), []string{"c-good"}) {
		t.Fatalf("after breaker = %v, want [c-good]", ids(got))
	}
	if _, ok := r.Stats().ParkedCreds["c-stale"]; !ok {
		t.Fatal("expected c-stale parked in stats")
	}
}

func TestBreakerIgnoresRepeatsOnSameHost(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{cred("c-1", "one"), cred("c-2", "two")}}
	r := New(store, nil, Config{BreakerThreshold: 3, MaxPerHost: -1})

	for i := 0; i < 5; i++ {
		r.Report(Target{Identity: "spine1.lab.example.net"}, "c-1", OutcomeAuthRejected)
	}
	if _, ok := r.Stats().ParkedCreds["c-1"]; ok {
		t.Fatal("one host repeating must not trip the breaker")
	}
}

func TestNonAuthOutcomesLeaveStateAlone(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-1", "first", withPriority(1)),
		cred("c-2", "second", withPriority(2)),
	}}
	b := NewMemoryBindings()
	_ = b.Record("c-1", "spine1.lab.example.net")
	r := New(store, b, Config{BreakerThreshold: 1, MaxPerHost: -1})
	tgt := Target{Identity: "spine1.lab.example.net"}

	for _, o := range []Outcome{OutcomeAlgoMismatch, OutcomeHostKey, OutcomeUnreachable, OutcomeOther} {
		r.Report(tgt, "c-1", o)
	}
	if len(r.Stats().ParkedCreds) != 0 {
		t.Fatalf("non-auth outcomes parked something: %v", r.Stats().ParkedCreds)
	}
	if _, ok := b.Lookup("spine1.lab.example.net"); !ok {
		t.Fatal("non-auth outcome dropped the pin")
	}
	got, _ := r.Resolve(tgt)
	if !equal(ids(got), []string{"c-1", "c-2"}) {
		t.Fatalf("non-auth outcome changed ordering: %v", ids(got))
	}
}

func TestStalePinIsDroppedOnAuthFailure(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{cred("c-1", "one"), cred("c-2", "two")}}
	b := NewMemoryBindings()
	_ = b.Record("c-1", "spine1.lab.example.net")
	r := New(store, b, Config{MaxPerHost: -1})

	r.Report(Target{Identity: "spine1.lab.example.net"}, "c-1", OutcomeAuthRejected)
	if _, ok := b.Lookup("spine1.lab.example.net"); ok {
		t.Fatal("stale pin should have been forgotten")
	}
}

func TestMaxPerHostCaps(t *testing.T) {
	var creds []vault.Credential
	for i := 0; i < 10; i++ {
		creds = append(creds, cred(fmt.Sprintf("c-%d", i), fmt.Sprintf("cred%d", i), withPriority(i)))
	}
	r := New(fakeStore{creds: creds}, nil, Config{MaxPerHost: 3})
	got, _ := r.Resolve(Target{Identity: "spine1.lab.example.net"})
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3", len(got))
	}
}

func TestWalkStopsOnNonRetryable(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-1", "one", withPriority(1)),
		cred("c-2", "two", withPriority(2)),
		cred("c-3", "three", withPriority(3)),
	}}
	r := New(store, NewMemoryBindings(), Config{MaxPerHost: -1})

	attempts := 0
	_, err := r.Walk(Target{Identity: "spine1.lab.example.net"}, func(vault.Credential) error {
		attempts++
		return errors.New("ssh: handshake failed: knownhosts: key mismatch")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (host-key failure must not walk the ladder)", attempts)
	}
}

func TestWalkFindsAndPinsWorkingCredential(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("c-1", "one", withPriority(1)),
		cred("c-2", "two", withPriority(2)),
	}}
	b := NewMemoryBindings()
	r := New(store, b, Config{MaxPerHost: -1})
	tgt := Target{Identity: "spine1.lab.example.net"}

	got, err := r.Walk(tgt, func(c vault.Credential) error {
		if c.ID == "c-2" {
			return nil
		}
		return errors.New("ssh: unable to authenticate, attempted methods [none password]")
	})
	if err != nil {
		t.Fatalf("Walk: %v", err)
	}
	if got.ID != "c-2" {
		t.Fatalf("got %v, want c-2", got.ID)
	}
	bind, ok := b.Lookup("spine1.lab.example.net")
	if !ok || bind.CredID != "c-2" {
		t.Fatalf("binding = %+v, want c-2", bind)
	}
}

func TestWalkNoCandidates(t *testing.T) {
	r := New(fakeStore{}, nil, Config{})
	_, err := r.Walk(Target{Identity: "spine1.lab.example.net"}, func(vault.Credential) error {
		t.Fatal("dial must not be called")
		return nil
	})
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("err = %v, want ErrNoCandidates", err)
	}
}

func TestResetRunKeepsBindings(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{cred("c-1", "one"), cred("c-2", "two")}}
	b := NewMemoryBindings()
	r := New(store, b, Config{BreakerThreshold: 1, MaxPerHost: -1})

	r.Report(Target{Identity: "spine1.lab.example.net"}, "c-1", OutcomeSuccess)
	r.Report(Target{Identity: "spine2.lab.example.net"}, "c-2", OutcomeAuthRejected)
	r.ResetRun()

	if s := r.Stats(); s.Promoted != "" || len(s.ParkedCreds) != 0 || s.NegativeHosts != 0 {
		t.Fatalf("run state survived reset: %+v", s)
	}
	if _, ok := b.Lookup("spine1.lab.example.net"); !ok {
		t.Fatal("ResetRun must not clear persisted bindings")
	}
}

// --- classification ---

func TestClassify(t *testing.T) {
	cases := []struct {
		err  string
		want Outcome
	}{
		{"ssh: handshake failed: ssh: no common algorithm for key exchange", OutcomeAlgoMismatch},
		{"ssh: unable to negotiate a host key algorithm", OutcomeAlgoMismatch},
		{"knownhosts: key mismatch", OutcomeHostKey},
		{"ssh: handshake failed: host key verification failed", OutcomeHostKey},
		{"ssh: unable to authenticate, attempted methods [none password]", OutcomeAuthRejected},
		{"ssh: handshake failed: permission denied", OutcomeAuthRejected},
		{"ssh: cannot decode encrypted private key: passphrase required", OutcomeKeyMaterial},
		{"dial tcp 10.20.4.9:22: connect: connection refused", OutcomeUnreachable},
		{"dial tcp 10.20.4.9:22: i/o timeout", OutcomeUnreachable},
		{"something nobody has seen before", OutcomeOther},
	}
	for _, tc := range cases {
		if got := Classify(errors.New(tc.err)); got != tc.want {
			t.Errorf("Classify(%q) = %v, want %v", tc.err, got, tc.want)
		}
	}
	if got := Classify(nil); got != OutcomeSuccess {
		t.Errorf("Classify(nil) = %v, want success", got)
	}
}

func TestOnlyAuthRejectionCountsAgainstCredential(t *testing.T) {
	for _, o := range []Outcome{
		OutcomeSuccess, OutcomeAlgoMismatch, OutcomeHostKey,
		OutcomeUnreachable, OutcomeKeyMaterial, OutcomeOther,
	} {
		if o.CountsAgainstCredential() {
			t.Errorf("%v must not count against the credential", o)
		}
	}
	if !OutcomeAuthRejected.CountsAgainstCredential() {
		t.Error("auth rejection must count against the credential")
	}
}

// --- identity ---

// stubResolver serves PTR and forward records; a missing entry is a failure.
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

// TestIdentityUsesTheSharedRule checks that this package gets normalize's
// answer rather than its own. The forward-confirm is the case that used to
// differ: credres adopted the first PTR unconditionally, so a device with a
// stale reverse record keyed on the name here and on the address in the
// crawler — one device, two cache entries, no error anywhere.
func TestIdentityUsesTheSharedRule(t *testing.T) {
	r := stubResolver{
		ptr: map[string][]string{
			"100.71.4.9": {"spine1.lab.example.net."},
			"100.71.5.9": {"stale.lab.example.net."},
		},
		forward: map[string][]string{"spine1.lab.example.net": {"100.71.4.9"}},
	}
	orig := normalize.DefaultResolver
	normalize.DefaultResolver = r
	t.Cleanup(func() { normalize.DefaultResolver = orig })

	suffixes := []string{"lab.example.net"}

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"confirmed PTR keys on the name", "100.71.4.9", "spine1"},
		{"stale PTR keys on the address", "100.71.5.9", "100.71.5.9"},
		{"no PTR keys on the address", "100.71.9.9", "100.71.9.9"},
		{"non-CGNAT address is left alone", "10.20.4.9", "10.20.4.9"},
		{"FQDN strips to the same key as the address form", "spine1.lab.example.net", "spine1"},
		{"case and trailing dot normalize", "SPINE1.Lab.Example.Net.", "spine1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Identity(tc.in, suffixes); got != tc.want {
				t.Errorf("Identity(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}

	// The two forms of the same device must land on one binding key.
	if Identity("100.71.4.9", suffixes) != Identity("spine1", suffixes) {
		t.Error("address form and short form disagree; the binding cache would warm twice")
	}
}

func TestMatchesSuffixIsLabelAligned(t *testing.T) {
	if matchesSuffix("notexample.net", "example.net") {
		t.Fatal("suffix match must respect label boundaries")
	}
	if !matchesSuffix("spine1.lab.example.net", "lab.example.net") {
		t.Fatal("expected label-aligned match")
	}
	if !matchesSuffix("example.net", "example.net") {
		t.Fatal("exact match should qualify")
	}
}

// --- binding store ---

func TestFileBindingsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	s, err := OpenFileBindings(path)
	if err != nil {
		t.Fatalf("OpenFileBindings: %v", err)
	}
	if err := s.Record("c-1", "spine1.lab.example.net"); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := s.Record("c-1", "spine1.lab.example.net"); err != nil {
		t.Fatalf("Record: %v", err)
	}

	s2, err := OpenFileBindings(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	b, ok := s2.Lookup("spine1.lab.example.net")
	if !ok || b.CredID != "c-1" || b.Hits != 2 {
		t.Fatalf("binding = %+v, want c-1 with 2 hits", b)
	}

	if err := secfile.Verify(path); err != nil {
		t.Fatalf("binding file is not private: %v", err)
	}

	if err := s2.Forget("spine1.lab.example.net"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, ok := s2.Lookup("spine1.lab.example.net"); ok {
		t.Fatal("Forget did not remove the binding")
	}
}

func TestFileBindingsMissingFileIsNotAnError(t *testing.T) {
	s, err := OpenFileBindings(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("missing file should be fine: %v", err)
	}
	if s.Len() != 0 {
		t.Fatal("expected empty store")
	}
}

func TestFileBindingsCorruptIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bindings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if _, err := OpenFileBindings(path); err == nil {
		t.Fatal("corrupt store should be reported, not silently discarded")
	}
}

// --- integration against a real vault ---

func TestResolverOverRealVault(t *testing.T) {
	v := vault.New(filepath.Join(t.TempDir(), "credentials.vault"))
	if err := v.Create("lab-master-passphrase"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := v.Add(vault.Credential{
		Name: "lab-key", AuthType: "publickey", Username: "netops",
		KeyPath: "/home/lab/.ssh/id_ed25519", Priority: 10,
		Description: "shared lab key", Tags: []string{"lab"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := v.Add(vault.Credential{
		Name: "lab-fallback", AuthType: "password", Username: "netops",
		Password: "x", Priority: 20, Tags: []string{"lab"},
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	r := New(v, NewMemoryBindings(), Config{MaxPerHost: -1})
	got, err := r.Resolve(Target{Identity: "spine1.lab.example.net", Tags: []string{"lab"}})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got) != 2 || got[0].Cred.Name != "lab-key" {
		t.Fatalf("order = %v, want the key credential first", ids(got))
	}

	v.Lock()
	if _, err := r.Resolve(Target{Identity: "spine1.lab.example.net"}); !errors.Is(err, vault.ErrVaultLocked) {
		t.Fatalf("locked vault should surface through the resolver, got %v", err)
	}
}

// The Cred and Try columns are only worth having if the reason comes with
// them: "pinned" means the binding store hit and one attempt was spent,
// "ranked" means it missed and the ladder was walked. Without that word an
// attempt count is just a number.
func TestWalkEmitsCredentialOutcomesWithTheirReason(t *testing.T) {
	run := crawlrun.New()
	store := fakeStore{creds: []vault.Credential{
		{ID: "c-1", Name: "wrong", Username: "admin", AuthType: "password", Password: "a"},
		{ID: "c-2", Name: "right", Username: "admin", AuthType: "password", Password: "b"},
	}}
	r := New(store, NewMemoryBindings(), Config{Emit: run.Emit()})

	// Reject whichever credential is offered first, accept the second.
	// Pinning on ID would depend on candidate ordering, which is exactly the
	// thing the resolver is allowed to change.
	target := Target{Identity: "lab-r1"}
	tried := 0
	_, err := r.Walk(target, func(c vault.Credential) error {
		tried++
		if tried == 1 {
			return errors.New("ssh: unable to authenticate")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	run.Finish()

	rows := run.Rows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one rejection then the winner)", rows[0].Attempts)
	}
	if rows[0].Credential == "" {
		t.Error("no credential recorded for the device that authenticated")
	}
	if rows[0].CredReason == "" {
		t.Error("no reason recorded; the Try count is uninterpretable without it")
	}
	if c := run.Counts(); c.Rejections != 1 {
		t.Errorf("rejections = %d, want 1", c.Rejections)
	}
}
