// internal/vaultcli/keyring_test.go
//
// These tests cover the logic this package owns: how an entry is keyed, when
// the keyring is skipped, and where DefaultPath points. They
// deliberately do NOT exercise a real OS keyring — there is no Secret Service
// on a CI box, macOS Keychain prompts, and a test that files a secret in the
// developer's login keyring is a test that leaves litter behind. The backend
// is a third-party module with its own suite; the part worth testing here is
// the part that is ours.
package vaultcli

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyringAccountIsPerVault(t *testing.T) {
	dir := t.TempDir()
	lab := filepath.Join(dir, "lab-vault.json")
	prod := filepath.Join(dir, "other-vault.json")

	a, err := keyringAccount(lab)
	if err != nil {
		t.Fatalf("keyringAccount(%q): %v", lab, err)
	}
	b, err := keyringAccount(prod)
	if err != nil {
		t.Fatalf("keyringAccount(%q): %v", prod, err)
	}
	if a == b {
		t.Fatalf("two vaults share a keyring account: %q", a)
	}
	if !filepath.IsAbs(a) {
		t.Errorf("account is not absolute: %q", a)
	}
}

func TestKeyringAccountNormalizes(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "lab-vault.json")
	noisy := filepath.Join(dir, "sub", "..", "lab-vault.json")

	a, err := keyringAccount(plain)
	if err != nil {
		t.Fatal(err)
	}
	b, err := keyringAccount(noisy)
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("equivalent paths keyed differently:\n  %q\n  %q", a, b)
	}
}

func TestKeyringAccountRejectsEmpty(t *testing.T) {
	if _, err := keyringAccount(""); err == nil {
		t.Error("empty vault path produced an account")
	}
}

func TestKeyringDisabledSkipsBackend(t *testing.T) {
	t.Setenv(NoKeyringEnvVar, "1")
	if !KeyringDisabled() {
		t.Fatal("KeyringDisabled() false with the variable set")
	}
	// Must report "nothing filed" rather than reaching a backend that may
	// not exist in this environment.
	if _, err := KeyringGet("/tmp/lab-vault.json"); !errors.Is(err, ErrNoKeyringEntry) {
		t.Errorf("KeyringGet with keyring disabled = %v, want ErrNoKeyringEntry", err)
	}
	st := Keyring("/tmp/lab-vault.json")
	if !st.Disabled || st.HasEntry {
		t.Errorf("Keyring() = %+v, want Disabled and no entry", st)
	}
}

func TestKeyringSetRejectsEmptyMaster(t *testing.T) {
	if err := KeyringSet("/tmp/lab-vault.json", ""); err == nil {
		t.Error("stored an empty master password")
	}
}

func TestMasterPrefersEnvOverPromptWhenKeyringDisabled(t *testing.T) {
	t.Setenv(NoKeyringEnvVar, "1")
	t.Setenv(MasterEnvVar, "lab-master-passphrase")

	got, src, err := Master("/tmp/lab-vault.json")
	if err != nil {
		t.Fatalf("Master: %v", err)
	}
	if got != "lab-master-passphrase" {
		t.Errorf("Master master = %q", got)
	}
	if src != SourceEnv {
		t.Errorf("Master source = %v, want %v", src, SourceEnv)
	}
}

func TestMasterSourceString(t *testing.T) {
	for _, tc := range []struct {
		src  MasterSource
		want string
	}{
		{SourceKeyring, "keyring"},
		{SourceEnv, "environment"},
		{SourcePrompt, "prompt"},
	} {
		if got := tc.src.String(); got != tc.want {
			t.Errorf("%d.String() = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// DefaultPath is ~/.omegamaps/vault.json, and a Pathfinder vault sitting in
// either of Pathfinder's directories must not be adopted in its place: that
// file is read by builds that treat SNMP credentials as SSH passwords.
func TestDefaultPathIsOmegamapsAndNeverAdoptsPathfinder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows

	want := filepath.Join(home, ".omegamaps", "vault.json")
	for _, dir := range []string{".pathfinderssh", ".pathfinder"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, dir, "vault.json"), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := DefaultPath(); got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
