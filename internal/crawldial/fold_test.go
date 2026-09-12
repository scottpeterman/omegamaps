package crawldial

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/credres"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

const foldCred = "04c73fe3-96d9-4cd1-9de6-2b15b3e0c32f"

// The real v1 store from the lab, migrated and then folded the way a crawl
// folds it. This is the end-to-end claim: 15 records in, 13 out.
func TestFoldIdentitiesCollapsesTheLabStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.bindings.json")
	v1 := `{"version":1,"bindings":[
	 {"identity":"eng-leaf-3.lab.local","cred_id":"` + foldCred + `","hits":2},
	 {"identity":"usa-leaf-2.lab.local","cred_id":"` + foldCred + `","hits":1},
	 {"identity":"eng-leaf-2.lab.local","cred_id":"` + foldCred + `","hits":2},
	 {"identity":"wan-core-1.lab.local","cred_id":"` + foldCred + `","hits":2},
	 {"identity":"eng-rtr-1.lab.local","cred_id":"` + foldCred + `","hits":14},
	 {"identity":"172.16.1.2","cred_id":"` + foldCred + `","hits":21},
	 {"identity":"172.16.128.2","cred_id":"` + foldCred + `","hits":2},
	 {"identity":"eng-spine-1","cred_id":"` + foldCred + `","hits":2},
	 {"identity":"eng-spine-2","cred_id":"` + foldCred + `","hits":1},
	 {"identity":"usa-leaf-1.lab.local","cred_id":"` + foldCred + `","hits":1},
	 {"identity":"usa-leaf-3.lab.local","cred_id":"` + foldCred + `","hits":1},
	 {"identity":"usa-rtr-1.lab.local","cred_id":"` + foldCred + `","hits":16},
	 {"identity":"usa-spine-2","cred_id":"` + foldCred + `","hits":1},
	 {"identity":"usa-spine-1","cred_id":"` + foldCred + `","hits":1},
	 {"identity":"eng-leaf-1.lab.local","cred_id":"` + foldCred + `","hits":2}]}`
	if err := os.WriteFile(path, []byte(v1), 0600); err != nil {
		t.Fatal(err)
	}
	b, err := credres.OpenFileBindings(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.Len() != 15 {
		t.Fatalf("migration produced %d records, want 15", b.Len())
	}

	devices := []*topo.Device{
		{Hostname: "172.16.1.2", SysName: "wan-core-1.lab.local", IPAddress: "172.16.1.2"},
		{Hostname: "172.16.128.2", SysName: "usa-rtr-1.lab.local", IPAddress: "172.16.128.2"},
		{Hostname: "eng-spine-1", SysName: "eng-spine-1", IPAddress: "172.16.1.21"},
		// Failed devices carry a neighbor's claim, not first-hand names, and
		// must not contribute aliases.
		{Hostname: "eng-leaf-9", SysName: "wan-core-1.lab.local", IPAddress: "172.16.1.2",
			Failed: true, FailedWhy: "dial: i/o timeout"},
	}
	Fold(b, devices, []string{"lab.local"}, func(string, ...any) {})

	if got := b.Len(); got != 13 {
		t.Fatalf("after fold: %d records, want 13", got)
	}
	bind, ok := b.Lookup("172.16.1.2")
	if !ok {
		t.Fatal("wan-core-1 lost")
	}
	if bind.Hits != 23 {
		t.Errorf("wan-core-1 hits = %d, want 21+2=23", bind.Hits)
	}
	if bind.Canonical != "wan-core-1.lab.local" {
		t.Errorf("canonical = %q", bind.Canonical)
	}
	// The failed device must not have been welded onto wan-core-1's record.
	if _, ok := b.Lookup("eng-leaf-9"); ok {
		t.Error("a failed device's neighbor-claimed names entered the store")
	}
}

func TestFoldIsSafeWithoutAVault(t *testing.T) {
	// No vault means no binding store; the fold must be a no-op, not a panic.
	Fold(nil, []*topo.Device{{Hostname: "lab-r1"}}, nil, func(string, ...any) {})
}
