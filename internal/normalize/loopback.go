package normalize

import (
	"net/netip"
	"strings"
)

// IsLoopback reports whether a name or address points back at whoever asks:
// "localhost" in its usual spellings, any name under the .localhost domain
// (RFC 6761), or a loopback or unspecified address. A device that advertises
// such a name or address says nothing about where it is. Dialing it dials the
// crawler's own host -- a recorded crawl offered SSH credentials to the laptop
// running it -- and naming a node by it merges every such device into one.
func IsLoopback(v string) bool {
	s := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(v), "."))
	switch {
	case s == "":
		return false
	case s == "localhost", s == "ip6-localhost", s == "ip6-loopback",
		strings.HasPrefix(s, "localhost."), strings.HasSuffix(s, ".localhost"):
		return true
	}
	if a, err := netip.ParseAddr(strings.Trim(s, "[]")); err == nil {
		return a.IsLoopback() || a.IsUnspecified()
	}
	return false
}
