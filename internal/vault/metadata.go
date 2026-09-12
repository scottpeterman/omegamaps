// internal/vault/metadata.go
//
// Ported from omegassh's vault package, where it was written for the same
// reason: an editing form holds no secret, so it cannot use Update.
//
// UpdateMetadata: the read-modify-write that makes a credential editable
// without a secret in hand.
//
// Update replaces a record wholesale, which is correct and is also the reason
// a UI cannot use it for an ordinary edit. The manager reads redacted metadata
// -- no password, no key path, no passphrase, by design -- so sending that
// record back with a corrected description would store a record whose secret
// fields are empty. The password is gone, nothing failed, and the next dial is
// the first anyone hears about it.
//
// Rename and SetDisabled already avoid that by doing the read-modify-write on
// this side, where the secret is reachable. This is the same move generalised
// to the rest of the non-secret fields, so the UI needs exactly two write
// paths and the sharp one is the one that says so:
//
//	UpdateMetadata -- the user edited fields; material is untouched.
//	Update/Add     -- the user supplied material; the record is replaced.
//
// What it deliberately does NOT touch: AuthType, Password, KeyPath,
// KeyPassphrase, CreatedAt, LastUsed, ID. AuthType is in that list because a
// credential's method and its material move together -- switching a record
// from password to publickey without supplying a key is a record that cannot
// authenticate, and the caller that has a key is the caller that should be
// using Update.
package vault

import "strings"

// UpdateMetadata merges the non-secret fields of c onto the stored credential
// with the same ID, leaving its material, timestamps and auth method as they
// were. Name uniqueness is enforced the same way Update enforces it.
//
// IsDefault is honoured in one direction only: true promotes this credential
// and clears any other, false leaves the current default alone. A metadata
// form that round-trips a redacted record would otherwise clear the default
// flag every time someone edited a different credential -- the record it is
// saving says is_default:false because it is not the default, which is not the
// same statement as "there should be no default".
func (v *Vault) UpdateMetadata(c Credential) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.key == nil {
		return ErrVaultLocked
	}

	name := strings.TrimSpace(c.Name)
	if name == "" {
		return ErrEmptyName
	}

	// Two passes rather than one. Update checks both in a single loop, so an
	// unknown id whose name matches an existing record reports a duplicate
	// name -- true, and not the answer to the question asked. Deciding "does
	// this record exist" before "is this name free" keeps the refusal aimed at
	// what the caller got wrong.
	idx := -1
	for i := range v.creds {
		if v.creds[i].ID == c.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ErrCredNotFound
	}
	for i := range v.creds {
		if i != idx && strings.EqualFold(v.creds[i].Name, name) {
			return ErrDuplicateName
		}
	}

	// Copy the stored record and overwrite only what the caller is allowed to
	// set. Written this way round on purpose: a field added to Credential
	// later is preserved by default rather than silently zeroed by an
	// assignment list nobody remembered to extend.
	merged := v.creds[idx]
	merged.Name = name
	merged.Username = c.Username
	merged.Description = c.Description
	merged.Priority = c.Priority
	merged.Tags = c.Tags
	merged.Scope = c.Scope
	merged.Disabled = c.Disabled

	if c.IsDefault && !merged.IsDefault && merged.IsSNMP() {
		return ErrSNMPDefault
	}
	if c.IsDefault && !merged.IsDefault {
		v.clearDefaultLocked()
		merged.IsDefault = true
	}

	v.creds[idx] = merged
	return v.saveLocked()
}
