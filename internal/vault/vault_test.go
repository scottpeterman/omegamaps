// internal/vault/vault_test.go
package vault

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/secfile"
)

func newTestVault(t *testing.T) *Vault {
	t.Helper()
	v := New(filepath.Join(t.TempDir(), "credentials.vault"))
	if err := v.Create("lab-master-passphrase"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return v
}

func TestCreateUnlockRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.vault")

	v := New(path)
	if v.Exists() {
		t.Fatal("vault should not exist yet")
	}
	if err := v.Create("lab-master-passphrase"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := v.Add(Credential{
		Name: "lab-readonly", Username: "netops", AuthType: "password", Password: "s3cret",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	v.Lock()
	if !v.IsLocked() {
		t.Fatal("expected locked after Lock")
	}

	v2 := New(path)
	if err := v2.Unlock("lab-master-passphrase"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	c, err := v2.Get("lab-readonly")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if c.Password != "s3cret" || c.Username != "netops" {
		t.Fatalf("round-trip lost fields: %+v", c.Redact())
	}
}

func TestWrongMasterPassword(t *testing.T) {
	v := newTestVault(t)
	v.Lock()

	v2 := New(v.Path())
	if err := v2.Unlock("not-the-passphrase"); err != ErrWrongPassword {
		t.Fatalf("want ErrWrongPassword, got %v", err)
	}
}

func TestVaultFileHasNoPlaintext(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Add(Credential{
		Name: "lab-edge", Username: "netops", AuthType: "password",
		Password: "uniqueplaintextmarker",
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	raw, err := os.ReadFile(v.Path())
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, needle := range []string{"uniqueplaintextmarker", "netops", "lab-edge"} {
		if containsBytes(raw, needle) {
			t.Fatalf("vault file leaks %q in plaintext", needle)
		}
	}
	// Not a mode comparison: Windows has no mode word to compare and reports
	// 0666 for every writable file. secfile.Verify checks the property -- only
	// this user can open it -- in whatever terms the platform expresses it.
	if err := secfile.Verify(v.Path()); err != nil {
		t.Fatalf("vault is not private: %v", err)
	}
}

func TestTamperedEnvelopeFailsToOpen(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Add(Credential{Name: "lab-edge", AuthType: "password", Password: "x"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	v.Lock()

	raw, _ := os.ReadFile(v.Path())
	var f vaultFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	ct, _ := base64.StdEncoding.DecodeString(f.Ciphertext)
	ct[0] ^= 0xFF
	f.Ciphertext = base64.StdEncoding.EncodeToString(ct)
	out, _ := json.Marshal(f)
	if err := os.WriteFile(v.Path(), out, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v2 := New(v.Path())
	if err := v2.Unlock("lab-master-passphrase"); err != ErrWrongPassword {
		t.Fatalf("tampered vault should fail auth, got %v", err)
	}
}

func TestLegacyAppTagMigrates(t *testing.T) {
	// Seal a vault under the upstream terminal's app tag, then confirm this
	// package opens it and re-seals under the current tag.
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.vault")

	salt := make([]byte, vaultSaltLen)
	for i := range salt {
		salt[i] = byte(i)
	}
	key := deriveKey("lab-master-passphrase", salt, argon2Time, argon2Memory, argon2Threads)
	plain, _ := json.Marshal(vaultData{Credentials: []Credential{
		{ID: "legacy-1", Name: "lab-legacy", Username: "netops", AuthType: "password", Password: "p"},
	}})
	nonce, ct, err := sealGCM(key, plain, []byte(legacyAppTag))
	if err != nil {
		t.Fatalf("sealGCM: %v", err)
	}
	envelope, _ := json.Marshal(vaultFile{
		Version: vaultVersion, KDF: vaultKDFName,
		KDFTime: argon2Time, KDFMemory: argon2Memory, KDFThreads: argon2Threads,
		Salt:       base64.StdEncoding.EncodeToString(salt),
		Nonce:      base64.StdEncoding.EncodeToString(nonce),
		Ciphertext: base64.StdEncoding.EncodeToString(ct),
	})
	if err := os.WriteFile(path, envelope, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	v := New(path)
	if err := v.Unlock("lab-master-passphrase"); err != nil {
		t.Fatalf("legacy vault should open: %v", err)
	}
	if _, err := v.Get("lab-legacy"); err != nil {
		t.Fatalf("legacy credential missing: %v", err)
	}
	// Force a save, which re-seals under the current tag.
	if err := v.SetDisabled("legacy-1", true); err != nil {
		t.Fatalf("SetDisabled: %v", err)
	}
	v.Lock()

	v2 := New(path)
	if err := v2.Unlock("lab-master-passphrase"); err != nil {
		t.Fatalf("migrated vault should open: %v", err)
	}
}

func TestDuplicateNameRejected(t *testing.T) {
	v := newTestVault(t)
	if _, err := v.Add(Credential{Name: "lab-readonly", AuthType: "password"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := v.Add(Credential{Name: "LAB-READONLY", AuthType: "password"}); err != ErrDuplicateName {
		t.Fatalf("want ErrDuplicateName, got %v", err)
	}
}

func TestLockedOperationsRefuse(t *testing.T) {
	v := newTestVault(t)
	v.Lock()
	if _, err := v.All(); err != ErrVaultLocked {
		t.Fatalf("All: want ErrVaultLocked, got %v", err)
	}
	if _, err := v.List(); err != ErrVaultLocked {
		t.Fatalf("List: want ErrVaultLocked, got %v", err)
	}
	if _, err := v.Add(Credential{Name: "x"}); err != ErrVaultLocked {
		t.Fatalf("Add: want ErrVaultLocked, got %v", err)
	}
}

func TestScopeSpecificity(t *testing.T) {
	cases := []struct {
		name  string
		a, b  Scope
		aWins bool
	}{
		{"cidr beats platform",
			Scope{CIDRs: []string{"10.0.0.0/8"}}, Scope{Platforms: []string{"arista_eos"}}, true},
		{"platform beats domain",
			Scope{Platforms: []string{"arista_eos"}}, Scope{DomainSuffix: "lab.example.net"}, true},
		{"longer suffix beats shorter",
			Scope{DomainSuffix: "site1.lab.example.net"}, Scope{DomainSuffix: "lab.example.net"}, true},
		{"anything beats unscoped",
			Scope{DomainSuffix: "example.net"}, Scope{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Specificity() > tc.b.Specificity(); got != tc.aWins {
				t.Fatalf("a=%d b=%d, want aWins=%v",
					tc.a.Specificity(), tc.b.Specificity(), tc.aWins)
			}
		})
	}
}

func TestRedactClearsSecrets(t *testing.T) {
	c := Credential{Password: "p", KeyPassphrase: "k", KeyPath: "/home/lab/.ssh/id_ed25519"}
	r := c.Redact()
	if r.Password != "" || r.KeyPassphrase != "" {
		t.Fatal("Redact left secret material")
	}
	if r.KeyPath == "" {
		t.Fatal("Redact should keep the non-secret key path")
	}
}

func containsBytes(haystack []byte, needle string) bool {
	n := []byte(needle)
	for i := 0; i+len(n) <= len(haystack); i++ {
		match := true
		for j := range n {
			if haystack[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
