// Package buildinfo is the one place a binary's version lives, so every front
// end reports the same thing and build.sh stamps one symbol rather than one
// per command.
package buildinfo

import (
	"runtime"
	"runtime/debug"
)

// Version is stamped by build.sh:
//
//	-ldflags "-X github.com/scottpeterman/omegamaps/internal/buildinfo.Version=v0.1.0"
//
// It is empty in a plain go build or go run, where String falls back to what
// the toolchain recorded.
var Version = ""

// String is the version a binary reports: the stamped version; else the git
// revision go build recorded, marked -dirty for uncommitted changes; else
// "dev". Never a guess dressed up as a release number.
func String() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		var rev string
		var dirty bool
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if rev != "" {
			if len(rev) > 12 {
				rev = rev[:12]
			}
			if dirty {
				rev += "-dirty"
			}
			return rev
		}
	}
	return "dev"
}

// Line is a binary's -version output: name, version, toolchain, and platform.
// The platform is there because a build script produces five of each binary,
// and "which one is this" is the first question about a file that was copied.
func Line(name string) string {
	return name + " " + String() + " (" + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + ")"
}
