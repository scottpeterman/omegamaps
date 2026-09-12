package secfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	want := []byte(`{"k":"v"}`)

	if err := WriteAtomic(path, want); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if err := Verify(path); err != nil {
		t.Fatalf("freshly written file is not private: %v", err)
	}
}

func TestWriteAtomicReplaces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.json")
	if err := WriteAtomic(path, []byte("first")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	if err := WriteAtomic(path, []byte("second")); err != nil {
		t.Fatalf("WriteAtomic over an existing file: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("content = %q, want %q", got, "second")
	}
	if err := Verify(path); err != nil {
		t.Fatalf("replaced file is not private: %v", err)
	}
}

// The temp file must land in the target's directory: os.Rename is only atomic
// within a filesystem, and on Windows it fails outright across volumes.
func TestWriteAtomicLeavesNoTempBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.json")
	if err := WriteAtomic(path, []byte("x")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(ents) != 1 || ents[0].Name() != "secret.json" {
		names := make([]string, len(ents))
		for i, e := range ents {
			names[i] = e.Name()
		}
		t.Fatalf("directory = %v, want just secret.json", names)
	}
}

// Verify has to fail on a file nothing restricted, or it proves nothing about
// the files it passes.
func TestVerifyRejectsAnOpenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "open.json")
	if err := os.WriteFile(path, []byte("x"), 0o666); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	// Explicitly, because the mode passed to WriteFile is masked by umask: a
	// developer running with umask 077 would otherwise get 0600 here and the
	// test would pass without testing anything. A no-op on Windows.
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	if err := Verify(path); err == nil {
		t.Fatal("Verify accepted a file written 0666 with an inherited ACL")
	}
	if err := Restrict(path); err != nil {
		t.Fatalf("Restrict: %v", err)
	}
	if err := Verify(path); err != nil {
		t.Fatalf("Verify rejected a restricted file: %v", err)
	}
}

func TestVerifyOnMissingFileIsAnError(t *testing.T) {
	if err := Verify(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("Verify accepted a path that does not exist")
	}
}
