//go:build !windows

package secfile

import (
	"fmt"
	"os"
)

// restrict is chmod 0600. os.CreateTemp already creates 0600, but it is
// subject to umask on some libcs and says nothing about a file that already
// existed, so the mode is set rather than assumed.
func restrict(path string) error {
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("secfile: chmod %s: %w", path, err)
	}
	return nil
}

// verify accepts 0600 and 0400. It rejects any group or other bit; that is the
// property, not the exact word.
func verify(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("secfile: stat %s: %w", path, err)
	}
	perm := fi.Mode().Perm()
	if perm&0o077 != 0 {
		return fmt.Errorf("secfile: %s is mode %04o, want no group or other access", path, perm)
	}
	return nil
}
