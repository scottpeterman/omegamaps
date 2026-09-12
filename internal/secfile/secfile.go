// Package secfile writes files that only the current user can read, on every
// platform this builds for.
//
// os.WriteFile(path, data, 0600) does that on Unix and does NOTHING on Windows.
// Go's Windows syscall layer maps the whole permission word onto one bit, the
// read-only attribute: 0600 and 0666 and 0644 all produce the same file, and
// os.Stat reports 0666 back for any writable file regardless of what was asked
// for. Access is decided by the ACL the file inherits from its parent
// directory, which nothing in the write path sets.
//
// In practice a vault under %USERPROFILE% inherits an ACL that is close enough
// to 0600 to be easy to miss -- the user, SYSTEM and Administrators -- and a
// vault anywhere else inherits whatever that directory grants, which for
// anything under C:\ that is not a profile includes Users:(RX). The file is
// AES-GCM sealed either way, so this is a second line rather than the only one,
// but "the ciphertext and the Argon2id parameters are world-readable" is not
// the property the 0600 was there to assert.
//
// So: WriteAtomic writes through a temp file in the same directory, restricts
// it before any bytes go in, and renames. On Unix that restriction is chmod
// 0600; on Windows it is an explicit, non-inherited DACL granting the calling
// user and nobody else. Verify checks the same property and is what the tests
// assert on instead of a raw mode word.
package secfile

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic replaces path with data, readable only by the current user.
//
// The temp file is created in the same directory as path -- os.Rename is only
// atomic within a filesystem, and on Windows a cross-volume MoveFileEx without
// COPY_ALLOWED fails outright. It is restricted while still empty, so the
// contents are never briefly world-readable under a wider directory ACL.
func WriteAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("secfile: create temp in %s: %w", dir, err)
	}
	tmp := f.Name()
	cleanup := func() {
		f.Close()
		os.Remove(tmp)
	}

	// Before the data, not after. Restricting an empty file leaves no window
	// in which the bytes exist under the inherited ACL.
	//
	// On Windows this sets the DACL on a path we hold open. That is allowed:
	// share-mode checks cover FILE_READ_DATA, FILE_WRITE_DATA and DELETE, and
	// WRITE_DAC is a standard right that is not share-checked -- which is why
	// you can always re-ACL a file somebody else has open.
	if err := restrict(tmp); err != nil {
		cleanup()
		return err
	}

	if _, err := f.Write(data); err != nil {
		cleanup()
		return fmt.Errorf("secfile: write %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return fmt.Errorf("secfile: sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("secfile: close %s: %w", tmp, err)
	}

	// The DACL set above is explicit, so it travels with the file across the
	// rename; an inherited one would be recomputed from the target directory.
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("secfile: commit %s: %w", path, err)
	}
	return nil
}

// Restrict applies the private-file permissions to a file that already exists,
// for paths not written through WriteAtomic.
func Restrict(path string) error { return restrict(path) }

// Verify reports whether path is readable only by the current user. It returns
// a nil error when it is, and an error naming what is wrong when it is not.
//
// This is the platform-independent form of the 0600 assertion. On Windows there
// is no mode word to compare, so it reads the DACL back.
func Verify(path string) error { return verify(path) }
