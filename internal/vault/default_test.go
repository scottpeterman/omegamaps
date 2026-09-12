// internal/vault/default_test.go
//
// The default credential: what a session gets when it names none.
//
// Rebuilt Aug 13 2026 after the original was lost with the rest of the Aug 3
// slice. The behaviour under test is the store's half of that feature — the
// dial layer's half is pinned by internal/sessiondial/default_test.go.
package vault

import (
	"path/filepath"
	"testing"
)

func addCred(t *testing.T, v *Vault, name, user string) string {
	t.Helper()
	c, err := v.Add(Credential{
		Name:     name,
		Username: user,
		AuthType: "password",
		Password: "lab",
	})
	if err != nil {
		t.Fatalf("add %s: %v", name, err)
	}
	return c.ID
}

func TestNoDefaultIsSetOnAFreshVault(t *testing.T) {
	v := newTestVault(t)
	addCred(t, v, "lab-admin", "admin")

	if _, ok := v.Default(); ok {
		t.Fatal("a fresh vault reports a default")
	}
	if name := v.DefaultName(); name != "" {
		t.Fatalf("DefaultName = %q, want empty", name)
	}
}

func TestSettingADefaultMovesItRatherThanAddingOne(t *testing.T) {
	v := newTestVault(t)
	first := addCred(t, v, "lab-admin", "admin")
	second := addCred(t, v, "lab-readonly", "ro")

	if err := v.SetDefault(first); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if name := v.DefaultName(); name != "lab-admin" {
		t.Fatalf("DefaultName = %q, want lab-admin", name)
	}

	if err := v.SetDefault(second); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if name := v.DefaultName(); name != "lab-readonly" {
		t.Fatalf("DefaultName = %q, want lab-readonly", name)
	}

	// The first must not still be flagged: two defaults is a state nothing
	// downstream can resolve, and Default() returning whichever is first in
	// the file would make the answer depend on insertion order.
	metas, err := v.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	defaults := 0
	for _, m := range metas {
		if m.IsDefault {
			defaults++
		}
	}
	if defaults != 1 {
		t.Fatalf("%d credentials flagged default, want 1", defaults)
	}
}

// "None of them" is a legitimate thing to want, and without this the only way
// to express it is to promote a credential nobody wanted promoted.
func TestADefaultCanBeUnset(t *testing.T) {
	v := newTestVault(t)
	id := addCred(t, v, "lab-admin", "admin")

	if err := v.SetDefault(id); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if err := v.ClearDefault(); err != nil {
		t.Fatalf("ClearDefault: %v", err)
	}
	if _, ok := v.Default(); ok {
		t.Fatal("a default survived ClearDefault")
	}
	if name := v.DefaultName(); name != "" {
		t.Fatalf("DefaultName = %q, want empty", name)
	}
}

func TestClearingWhenThereIsNoDefaultIsNotAnError(t *testing.T) {
	v := newTestVault(t)
	addCred(t, v, "lab-admin", "admin")

	if err := v.ClearDefault(); err != nil {
		t.Fatalf("ClearDefault on a vault with no default: %v", err)
	}
}

// Disabled means "out of automatic selection", and being the default is the
// most automatic there is. A session naming nothing must not authenticate with
// the one credential somebody deliberately took out of service.
func TestADisabledCredentialIsNotOfferedAsTheDefault(t *testing.T) {
	v := newTestVault(t)
	id := addCred(t, v, "lab-admin", "admin")

	if err := v.SetDefault(id); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if err := v.SetDisabled(id, true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}

	if _, ok := v.Default(); ok {
		t.Fatal("a disabled credential was offered as the default")
	}
	if name := v.DefaultName(); name != "" {
		t.Fatalf("DefaultName = %q, want empty for a disabled default", name)
	}

	// Still fetchable by name — that is what disabling is meant to leave
	// working, and it is how re-enabling can be offered.
	if _, err := v.Get(id); err != nil {
		t.Fatalf("a disabled credential is no longer fetchable: %v", err)
	}

	// And it comes back when re-enabled: disabling must not silently
	// discard the flag.
	if err := v.SetDisabled(id, false); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	if name := v.DefaultName(); name != "lab-admin" {
		t.Fatalf("DefaultName = %q after re-enabling, want lab-admin", name)
	}
}

func TestSettingAnUnknownCredentialAsDefaultIsRefused(t *testing.T) {
	v := newTestVault(t)
	addCred(t, v, "lab-admin", "admin")

	if err := v.SetDefault("no-such-id"); err == nil {
		t.Fatal("SetDefault accepted an id that is not in the vault")
	}
	if _, ok := v.Default(); ok {
		t.Fatal("a failed SetDefault left a default behind")
	}
}

// The flag lives in the file, not in memory: a default set in one process is
// the default in the next one, which is the whole point.
func TestTheDefaultSurvivesAReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.vault")
	v := New(path)
	if err := v.Create("lab-master-passphrase"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	id := addCred(t, v, "lab-admin", "admin")
	if err := v.SetDefault(id); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	v.Lock()

	again := New(path)
	if err := again.Unlock("lab-master-passphrase"); err != nil {
		t.Fatalf("reopen vault: %v", err)
	}
	defer again.Lock()

	if name := again.DefaultName(); name != "lab-admin" {
		t.Fatalf("DefaultName after reopen = %q, want lab-admin", name)
	}
}

func TestALockedVaultReportsNoDefault(t *testing.T) {
	v := newTestVault(t)
	id := addCred(t, v, "lab-admin", "admin")
	if err := v.SetDefault(id); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	v.Lock()

	if _, ok := v.Default(); ok {
		t.Fatal("a locked vault handed out its default credential")
	}
	if name := v.DefaultName(); name != "" {
		t.Fatalf("DefaultName on a locked vault = %q, want empty", name)
	}
	if err := v.ClearDefault(); err == nil {
		t.Fatal("ClearDefault worked on a locked vault")
	}
}
