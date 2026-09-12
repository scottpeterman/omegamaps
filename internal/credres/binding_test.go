// internal/credres/binding_test.go
//
// The cases here are the ones that used to split a record silently. Each one
// is written as "two shapes of one device" or "one shape of two devices",
// because those are the only two ways this store can be wrong.
package credres

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/normalize"
)

const labCred = "04c73fe3-96d9-4cd1-9de6-2b15b3e0c32f"

func TestShortNameLeavesAddressesWhole(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"172.16.128.2", "172.16.128.2"},
		{"172.16.1.2", "172.16.1.2"},
		{"2001:db8::1", "2001:db8::1"},
		{"eng-spine-1.lab.local", "eng-spine-1"},
		{"eng-spine-1", "eng-spine-1"},
		{"WAN-CORE-1.Lab.Local.", "wan-core-1"},
	} {
		if got := normalize.ShortName(tc.in); got != tc.want {
			t.Errorf("ShortName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestAddressAndNameSeededRunsConverge(t *testing.T) {
	s := NewMemoryBindings()

	// Run one seeds by address; the fold adds what the device reported.
	if err := s.Record(labCred, "172.16.1.2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("wan-core-1", "wan-core-1.lab.local", "172.16.1.2"); err != nil {
		t.Fatal(err)
	}

	// Run two seeds by name and never sees the address.
	if err := s.Record(labCred, "wan-core-1.lab.local"); err != nil {
		t.Fatal(err)
	}

	if got := s.Len(); got != 1 {
		t.Fatalf("two shapes of one device produced %d records, want 1", got)
	}
	for _, id := range []string{
		"172.16.1.2", "wan-core-1", "wan-core-1.lab.local", "WAN-CORE-1.LAB.LOCAL",
	} {
		b, ok := s.Lookup(id)
		if !ok {
			t.Errorf("lookup by %q missed", id)
			continue
		}
		if b.CredID != labCred {
			t.Errorf("lookup by %q returned cred %q", id, b.CredID)
		}
	}
	b, _ := s.Lookup("172.16.1.2")
	if b.Canonical != "wan-core-1.lab.local" {
		t.Errorf("canonical = %q, want the qualified name", b.Canonical)
	}
	if b.Hits != 2 {
		t.Errorf("hits = %d, want 2", b.Hits)
	}
}

// The Arista spines answer with a bare prompt name and nothing appends a
// suffix, so the pre-auth write and the post-crawl fold share only that label.
func TestBarePromptNameJoinsItsRecord(t *testing.T) {
	s := NewMemoryBindings()

	if err := s.Record(labCred, "eng-spine-1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("eng-spine-1", "172.16.1.9"); err != nil {
		t.Fatal(err)
	}
	if got := s.Len(); got != 1 {
		t.Fatalf("bare name and address produced %d records, want 1", got)
	}
	if b, ok := s.Lookup("172.16.1.9"); !ok || b.CredID != labCred {
		t.Errorf("address lookup did not inherit the pin: %+v", b)
	}
}

// One label, two domains. Merging these would offer one device's credential to
// the other, so the short name is refused and both records stand.
func TestShortNameCollisionAcrossDomainsIsRefused(t *testing.T) {
	s := NewMemoryBindings()
	var logged int
	s.SetLogger(func(string, ...any) { logged++ })

	if err := s.Record("cred-eng", "eng-leaf-1.lab.local", "172.16.1.11"); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("cred-usa", "eng-leaf-1.usa.lab.local", "172.16.128.11"); err != nil {
		t.Fatal(err)
	}

	if got := s.Len(); got != 2 {
		t.Fatalf("two devices collapsed into %d record(s); the short name merged them", got)
	}
	if logged == 0 {
		t.Error("refusal was not logged")
	}
	a, _ := s.Lookup("172.16.1.11")
	b, _ := s.Lookup("172.16.128.11")
	if a.CredID != "cred-eng" || b.CredID != "cred-usa" {
		t.Errorf("credentials crossed: %q / %q", a.CredID, b.CredID)
	}
	// The first owner keeps the short name; the second is reachable by its
	// own qualified name and address, which is the intended degradation.
	if _, ok := s.Lookup("eng-leaf-1.usa.lab.local"); !ok {
		t.Error("second device lost its qualified name")
	}
}

func TestStrongAliasMergesTwoRecords(t *testing.T) {
	s := NewMemoryBindings()

	// Two records that do not yet know they are one device.
	if err := s.Record(labCred, "172.16.1.2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Record(labCred, "wan-core-1.lab.local"); err != nil {
		t.Fatal(err)
	}
	if got := s.Len(); got != 2 {
		t.Fatalf("setup: want 2 records, got %d", got)
	}

	// The fold supplies both, which is the evidence they are one.
	if err := s.Bind("wan-core-1", "wan-core-1.lab.local", "172.16.1.2"); err != nil {
		t.Fatal(err)
	}
	if got := s.Len(); got != 1 {
		t.Fatalf("merge left %d records, want 1", got)
	}
	b, _ := s.Lookup("wan-core-1")
	if b.Hits != 2 {
		t.Errorf("hits = %d, want the sum 2", b.Hits)
	}
}

func TestParsingDebrisAndChassisMACsAreNotAliases(t *testing.T) {
	s := NewMemoryBindings()
	if err := s.Record(labCred, "eng-rtr-1.lab.local", "detail", "^", "aabb.ccdd.eeff", "x"); err != nil {
		t.Fatal(err)
	}
	for _, junk := range []string{"detail", "^", "aabb.ccdd.eeff", "x"} {
		if _, ok := s.Lookup(junk); ok {
			t.Errorf("%q was admitted as an alias", junk)
		}
	}
	if _, ok := s.Lookup("eng-rtr-1"); !ok {
		t.Error("the real name was lost")
	}
}

func TestForgetClearsPinAndKeepsAliases(t *testing.T) {
	s := NewMemoryBindings()
	if err := s.Record(labCred, "eng-rtr-1.lab.local", "172.16.1.3"); err != nil {
		t.Fatal(err)
	}
	if err := s.Forget("eng-rtr-1"); err != nil {
		t.Fatal(err)
	}
	// The pin is gone as far as any caller is concerned.
	if _, ok := s.Lookup("172.16.1.3"); ok {
		t.Error("pin survived Forget")
	}
	// But the identity did not go with it: re-binding under one alias must
	// land on the SAME record, not start a new one.
	if err := s.Record(labCred, "eng-rtr-1.lab.local"); err != nil {
		t.Fatal(err)
	}
	if got := s.Len(); got != 1 {
		t.Fatalf("Forget discarded the alias set; re-bind made %d records", got)
	}
	b, ok := s.Lookup("172.16.1.3")
	if !ok {
		t.Fatal("the address alias was lost across Forget")
	}
	if len(b.Aliases) < 3 {
		t.Errorf("aliases were discarded: %v", b.Aliases)
	}
}

// The real store from the lab crawl, in the v1 format, migrated and then
// folded the way cmd/crawl folds it after each device completes.
func TestMigrateV1StoreAndFold(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.bindings.json")

	v1 := `{
  "version": 1,
  "bindings": [
    {"identity":"eng-leaf-3.lab.local","cred_id":"` + labCred + `","hits":2},
    {"identity":"usa-leaf-2.lab.local","cred_id":"` + labCred + `","hits":1},
    {"identity":"eng-leaf-2.lab.local","cred_id":"` + labCred + `","hits":2},
    {"identity":"wan-core-1.lab.local","cred_id":"` + labCred + `","hits":2},
    {"identity":"eng-rtr-1.lab.local","cred_id":"` + labCred + `","hits":14},
    {"identity":"172.16.1.2","cred_id":"` + labCred + `","hits":21},
    {"identity":"172.16.128.2","cred_id":"` + labCred + `","hits":2},
    {"identity":"eng-spine-1","cred_id":"` + labCred + `","hits":2},
    {"identity":"eng-spine-2","cred_id":"` + labCred + `","hits":1},
    {"identity":"usa-leaf-1.lab.local","cred_id":"` + labCred + `","hits":1},
    {"identity":"usa-leaf-3.lab.local","cred_id":"` + labCred + `","hits":1},
    {"identity":"usa-rtr-1.lab.local","cred_id":"` + labCred + `","hits":16},
    {"identity":"usa-spine-2","cred_id":"` + labCred + `","hits":1},
    {"identity":"usa-spine-1","cred_id":"` + labCred + `","hits":1},
    {"identity":"eng-leaf-1.lab.local","cred_id":"` + labCred + `","hits":2}
  ]
}`
	if err := os.WriteFile(path, []byte(v1), 0600); err != nil {
		t.Fatal(err)
	}

	s, err := OpenFileBindings(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Len(); got != 15 {
		t.Fatalf("migration produced %d records, want 15 (migration is not a merge)", got)
	}

	// The fold: canonical, then the device's own names and answering address.
	folds := []struct {
		canonical string
		aliases   []string
	}{
		{"wan-core-1", []string{"wan-core-1.lab.local", "172.16.1.2"}},
		{"usa-rtr-1", []string{"usa-rtr-1.lab.local", "172.16.128.2"}},
		{"eng-spine-1", []string{"eng-spine-1", "172.16.1.21"}},
		{"eng-spine-2", []string{"eng-spine-2", "172.16.1.22"}},
	}
	for _, f := range folds {
		if err := s.Bind(f.canonical, f.aliases...); err != nil {
			t.Fatal(err)
		}
	}

	if got := s.Len(); got != 13 {
		t.Fatalf("after fold: %d records, want 13", got)
	}
	b, ok := s.Lookup("172.16.1.2")
	if !ok {
		t.Fatal("wan-core-1 lost")
	}
	if b.Hits != 23 {
		t.Errorf("wan-core-1 hits = %d, want 21+2=23", b.Hits)
	}
	if b.Canonical != "wan-core-1.lab.local" {
		t.Errorf("canonical = %q, want the qualified name over the address", b.Canonical)
	}

	// Reopen: the file is now v2 and survives the round trip.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	if f.Version != bindingFormatVersion {
		t.Errorf("on-disk version = %d, want %d", f.Version, bindingFormatVersion)
	}

	again, err := OpenFileBindings(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Len(); got != 13 {
		t.Errorf("reopened store has %d records, want 13", got)
	}
	if b2, ok := again.Lookup("wan-core-1"); !ok || b2.Hits != 23 {
		t.Errorf("round trip lost the merge: %+v", b2)
	}
}

func TestNoteContextReportsNewSuffix(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenFileBindings(filepath.Join(dir, "vault.bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if prior, isNew := s.NoteContext([]string{"lab.local"}); len(prior) != 0 || !isNew {
		t.Errorf("first context: prior=%v isNew=%v", prior, isNew)
	}
	if _, isNew := s.NoteContext([]string{"lab.local"}); isNew {
		t.Error("same context reported as new")
	}
	prior, isNew := s.NoteContext([]string{"usa.lab.local"})
	if !isNew || len(prior) != 1 {
		t.Errorf("new context: prior=%v isNew=%v", prior, isNew)
	}
}

func TestConcurrentRecordIsSafe(t *testing.T) {
	s := NewMemoryBindings()
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			defer func() { done <- struct{}{} }()
			for j := 0; j < 50; j++ {
				_ = s.Record(labCred, "eng-leaf-1.lab.local", "172.16.1.11")
				_, _ = s.Lookup("eng-leaf-1")
			}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if got := s.Len(); got != 1 {
		t.Errorf("concurrent writes produced %d records, want 1", got)
	}
	_ = time.Now
}

// Two qualified names on one record must not let map iteration pick the label.
func TestCanonicalIsStableAcrossRuns(t *testing.T) {
	want := ""
	for i := 0; i < 200; i++ {
		s := NewMemoryBindings()
		if err := s.Bind("eng-leaf-1", "eng-leaf-1.lab.local", "eng-leaf-1.mgmt.lab.local", "172.16.1.11"); err != nil {
			t.Fatal(err)
		}
		b, _ := s.Lookup("172.16.1.11")
		if want == "" {
			want = b.Canonical
			continue
		}
		if b.Canonical != want {
			t.Fatalf("canonical varies between runs: %q then %q", want, b.Canonical)
		}
	}
	t.Logf("stable canonical: %q", want)
}

// A carrier-assigned address names a route, not a device: it may point
// somewhere else tomorrow, so a credential must never be pinned to one.
func TestCGNATAddressesAreNotAliases(t *testing.T) {
	s := NewMemoryBindings()
	if err := s.Record(labCred, "lab-r1.lab.local", "100.64.0.9", "172.16.1.2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.Lookup("100.64.0.9"); ok {
		t.Error("a CGNAT address was admitted as an alias")
	}
	// The stable shapes are unaffected.
	for _, id := range []string{"lab-r1.lab.local", "lab-r1", "172.16.1.2"} {
		if _, ok := s.Lookup(id); !ok {
			t.Errorf("%q was lost", id)
		}
	}
	// And a second device reached through the same recycled address does not
	// inherit the first one's credential.
	if err := s.Record("other-cred", "lab-r2.lab.local", "100.64.0.9"); err != nil {
		t.Fatal(err)
	}
	if s.Len() != 2 {
		t.Errorf("a recycled CGNAT address merged two devices into %d record(s)", s.Len())
	}
	if b, _ := s.Lookup("lab-r1.lab.local"); b.CredID != labCred {
		t.Errorf("first device's credential changed to %q", b.CredID)
	}
}
