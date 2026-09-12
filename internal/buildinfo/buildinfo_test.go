package buildinfo

import (
	"runtime"
	"strings"
	"testing"
)

func TestStampedVersionWins(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = "v1.2.3"
	if got := String(); got != "v1.2.3" {
		t.Fatalf("String() = %q", got)
	}
	line := Line("crawl")
	for _, want := range []string{"crawl v1.2.3", runtime.GOOS + "/" + runtime.GOARCH, runtime.Version()} {
		if !strings.Contains(line, want) {
			t.Errorf("Line() = %q, missing %q", line, want)
		}
	}
}

func TestUnstampedVersionIsNeverEmpty(t *testing.T) {
	old := Version
	t.Cleanup(func() { Version = old })
	Version = ""
	if String() == "" {
		t.Fatal("an unstamped binary must still report something")
	}
}
