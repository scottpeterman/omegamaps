// internal/vaultcli/keyring.go
//
// OS-backed storage for the vault master password: Windows Credential
// Manager, macOS Keychain, Linux Secret Service. This is the "no plaintext
// path" half of the credential-storage requirement — the vault file has
// always been encrypted, but until now the only way to open it without a
// human was an environment variable, which is a plaintext path wearing a
// different hat.
//
// What is stored is the master password itself, not a wrapping key. The
// earlier design sketch had the keyring hold a 32-byte wrapping key with the
// master sealed under it in a sidecar file; that buys nothing. Both the
// keyring entry and the sidecar live on the same machine under the same user,
// so anything that can read one can read the other, and the indirection only
// adds a file that can go missing. The vault's on-disk format is untouched
// either way, which was the actual requirement: Argon2id still derives the
// AES key from the master, and the keyring only answers "where did the master
// come from".
//
// Entries are keyed by the ABSOLUTE VAULT PATH, so a lab vault and a
// production vault on the same machine do not share an entry. That is the
// whole reason the account field is not a constant.
package vaultcli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

// KeyringService is the service name every entry is filed under. It is what
// the user sees in Keychain Access or Credential Manager, so it is the
// product name rather than a package name.
const KeyringService = "omegamaps"

// NoKeyringEnvVar disables the keyring for a run without removing the stored
// entry. Set it to any non-empty value. This exists for two cases that are
// both real: debugging an unlock problem without destroying the entry, and a
// lab box where the Secret Service daemon is present but not something you
// want in the loop.
const NoKeyringEnvVar = "OMEGAMAPS_NO_KEYRING"

// ErrNoKeyringEntry means the keyring is reachable and simply has nothing
// filed for this vault. It is not an error condition on the unlock path —
// Master falls through to the next source.
var ErrNoKeyringEntry = errors.New("no keyring entry for this vault")

// KeyringDisabled reports whether NoKeyringEnvVar is set.
func KeyringDisabled() bool {
	return os.Getenv(NoKeyringEnvVar) != ""
}

// keyringAccount is the per-vault key. The path is not a secret, and using it
// verbatim means a user inspecting their own keyring can tell which entry
// belongs to which vault without a lookup table.
func keyringAccount(vaultPath string) (string, error) {
	if vaultPath == "" {
		return "", errors.New("keyring account requires a vault path")
	}
	abs, err := filepath.Abs(vaultPath)
	if err != nil {
		return "", fmt.Errorf("resolve vault path %s: %w", vaultPath, err)
	}
	return filepath.Clean(abs), nil
}

// KeyringGet returns the stored master password for the vault at vaultPath.
// It returns ErrNoKeyringEntry when nothing is filed, and a wrapped error
// when the keyring itself is unavailable — a headless Linux box with no
// D-Bus session being the common case, and exactly the case the environment
// variable still exists for.
func KeyringGet(vaultPath string) (string, error) {
	if KeyringDisabled() {
		return "", ErrNoKeyringEntry
	}
	acct, err := keyringAccount(vaultPath)
	if err != nil {
		return "", err
	}
	secret, err := keyring.Get(KeyringService, acct)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", ErrNoKeyringEntry
		}
		return "", fmt.Errorf("read keyring: %w", err)
	}
	if secret == "" {
		return "", ErrNoKeyringEntry
	}
	return secret, nil
}

// KeyringSet files the master password for the vault at vaultPath. The caller
// is responsible for having verified the password against the vault first —
// storing an unverified string is how a keyring entry becomes a lockout.
func KeyringSet(vaultPath, master string) error {
	if master == "" {
		return errors.New("refusing to store an empty master password")
	}
	acct, err := keyringAccount(vaultPath)
	if err != nil {
		return err
	}
	if err := keyring.Set(KeyringService, acct, master); err != nil {
		return fmt.Errorf("write keyring: %w", err)
	}
	return nil
}

// KeyringClear removes the entry for the vault at vaultPath. Clearing an
// entry that is not there is not an error — the caller wanted it gone and it
// is gone.
func KeyringClear(vaultPath string) error {
	acct, err := keyringAccount(vaultPath)
	if err != nil {
		return err
	}
	if err := keyring.Delete(KeyringService, acct); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("clear keyring: %w", err)
	}
	return nil
}

// KeyringStatus describes what the unlock path would find, for a status
// command and for a future GUI that wants to show where a session's
// credentials came from.
type KeyringStatus struct {
	Disabled  bool   // NoKeyringEnvVar is set
	Available bool   // the OS keyring answered
	HasEntry  bool   // an entry exists for this vault
	Account   string // the key it is filed under
	Err       error  // why the keyring did not answer, if it did not
}

// Keyring reports the keyring state for the vault at vaultPath without
// returning the secret.
func Keyring(vaultPath string) KeyringStatus {
	st := KeyringStatus{Disabled: KeyringDisabled()}
	acct, err := keyringAccount(vaultPath)
	if err != nil {
		st.Err = err
		return st
	}
	st.Account = acct
	if st.Disabled {
		return st
	}
	switch _, err := keyring.Get(KeyringService, acct); {
	case err == nil:
		st.Available, st.HasEntry = true, true
	case errors.Is(err, keyring.ErrNotFound):
		st.Available = true
	default:
		st.Err = err
	}
	return st
}
