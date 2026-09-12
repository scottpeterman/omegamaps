// vault/metadata_test.go
//
// The one thing worth proving about UpdateMetadata is that it does not lose a
// secret, so that is what most of this file checks -- from the direction the
// bug would actually arrive: a redacted record round-tripped through a form.

package vault

import (
	"path/filepath"
	"testing"
)

func metaVault(t *testing.T) *Vault {
	t.Helper()
	v := New(filepath.Join(t.TempDir(), "lab-vault.json"))
	if err := v.Create("labmaster02"); err != nil {
		t.Fatalf("create: %v", err)
	}
	return v
}

func TestUpdateMetadataKeepsMaterial(t *testing.T) {
	v := metaVault(t)

	added, err := v.Add(Credential{
		Name:          "lab-admin",
		Username:      "labadmin",
		AuthType:      "password",
		Password:      "labsecret",
		KeyPath:       "/home/lab/.ssh/id_ed25519",
		KeyPassphrase: "labphrase",
		Description:   "lab gear",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// Exactly what a manager UI has in hand: the redacted view, edited.
	edited := added.Redact()
	edited.Description = "lab access switches"
	edited.Username = "labops"
	edited.Tags = []string{"lab", "access"}
	edited.Priority = 5

	if err := v.UpdateMetadata(edited); err != nil {
		t.Fatalf("update metadata: %v", err)
	}

	got, err := v.Get(added.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Password != "labsecret" {
		t.Errorf("password was lost: %q", got.Password)
	}
	if got.KeyPath != "/home/lab/.ssh/id_ed25519" {
		t.Errorf("key path was lost: %q", got.KeyPath)
	}
	if got.KeyPassphrase != "labphrase" {
		t.Errorf("key passphrase was lost: %q", got.KeyPassphrase)
	}
	if got.AuthType != "password" {
		t.Errorf("auth type changed: %q", got.AuthType)
	}
	if got.Description != "lab access switches" || got.Username != "labops" ||
		got.Priority != 5 || len(got.Tags) != 2 {
		t.Errorf("edited fields did not land: %+v", got)
	}
	if got.CreatedAt != added.CreatedAt {
		t.Errorf("created_at moved: %v -> %v", added.CreatedAt, got.CreatedAt)
	}
}

// The counter-check: Update is still allowed to clear a secret, because that
// is what "the user supplied material" has to mean. If this ever starts
// passing material through, the two write paths have collapsed into one and
// the UI's choice between them stopped meaning anything.
func TestUpdateStillReplacesWholesale(t *testing.T) {
	v := metaVault(t)
	added, err := v.Add(Credential{
		Name: "lab-admin", Username: "labadmin",
		AuthType: "password", Password: "labsecret",
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	blanked := added.Redact()
	if err := v.Update(blanked); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := v.Get(added.ID)
	if got.Password != "" {
		t.Errorf("Update kept a secret the caller omitted: %q", got.Password)
	}
}

func TestUpdateMetadataPreservesLastUsed(t *testing.T) {
	v := metaVault(t)
	added, _ := v.Add(Credential{Name: "lab-admin", AuthType: "password", Password: "x"})
	v.MarkUsed(added.ID)

	before, _ := v.Get(added.ID)
	if before.LastUsed.IsZero() {
		t.Fatal("MarkUsed did not set last_used")
	}

	edited := before.Redact()
	edited.Description = "edited"
	if err := v.UpdateMetadata(edited); err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	after, _ := v.Get(added.ID)
	if !after.LastUsed.Equal(before.LastUsed) {
		t.Errorf("last_used moved: %v -> %v", before.LastUsed, after.LastUsed)
	}
}

// A redacted record says is_default:false because it is not the default, not
// because the user asked for no default. Editing a non-default credential must
// not silently unset the default that exists.
func TestUpdateMetadataDoesNotClearAnotherDefault(t *testing.T) {
	v := metaVault(t)
	first, _ := v.Add(Credential{Name: "lab-admin", AuthType: "password", Password: "x", IsDefault: true})
	second, _ := v.Add(Credential{Name: "lab-readonly", AuthType: "password", Password: "y"})

	edited := second.Redact()
	edited.Description = "read only"
	if err := v.UpdateMetadata(edited); err != nil {
		t.Fatalf("update metadata: %v", err)
	}

	if name := v.DefaultName(); name != "lab-admin" {
		t.Errorf("default changed to %q; first was %q", name, first.Name)
	}

	// Promotion in the other direction still works.
	promote := second.Redact()
	promote.IsDefault = true
	if err := v.UpdateMetadata(promote); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if name := v.DefaultName(); name != "lab-readonly" {
		t.Errorf("promotion did not take: default is %q", name)
	}
	if again, _ := v.Get(first.ID); again.IsDefault {
		t.Error("old default was not cleared")
	}
}

func TestUpdateMetadataRefusals(t *testing.T) {
	v := metaVault(t)
	a, _ := v.Add(Credential{Name: "lab-admin", AuthType: "password", Password: "x"})
	_, _ = v.Add(Credential{Name: "lab-readonly", AuthType: "password", Password: "y"})

	clash := a.Redact()
	clash.Name = "LAB-READONLY"
	if err := v.UpdateMetadata(clash); err != ErrDuplicateName {
		t.Errorf("duplicate name: got %v", err)
	}

	empty := a.Redact()
	empty.Name = "   "
	if err := v.UpdateMetadata(empty); err != ErrEmptyName {
		t.Errorf("empty name: got %v", err)
	}

	missing := a.Redact()
	missing.ID = "no-such-id"
	if err := v.UpdateMetadata(missing); err != ErrCredNotFound {
		t.Errorf("unknown id: got %v", err)
	}

	v.Lock()
	if err := v.UpdateMetadata(a.Redact()); err != ErrVaultLocked {
		t.Errorf("locked vault: got %v", err)
	}
}
