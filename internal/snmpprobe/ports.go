package snmpprobe

import (
	"strings"
	"unicode"
)

// Port ID resolution, ported from SC2.5 lldp.py. The field findings behind it,
// from the Python:
//
//	subtype 7 (local)      -> numeric "689"             (Juniper EX)
//	subtype 3 (macAddress) -> "00:25:90:e2:11:38"       (Linux NICs)
//	subtype 5 (ifName)     -> "Management1"             (Arista EOS <= 4.23)
//
// and in those cases the port description usually carries the real name.

// isMACString reports whether s is a MAC in any common notation.
func isMACString(s string) bool {
	clean := strings.NewReplacer(":", "", "-", "", ".", "").Replace(strings.ToLower(s))
	if len(clean) != 12 {
		return false
	}
	for _, c := range clean {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isManagement(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "management") || strings.HasPrefix(l, "mgmt")
}

// unusablePortID is _is_unusable_port_id: true when a decoded port ID cannot
// serve as a topology interface name.
func unusablePortID(id string, subtype int) bool {
	s := strings.TrimSpace(id)
	if s == "" || isDigits(s) || subtype == portSubtypeMAC {
		return true
	}
	if (strings.Contains(s, ":") || strings.Contains(s, "-")) && isMACString(s) {
		return true
	}
	return isManagement(s)
}

// looksLikeInterface is _looks_like_interface: does a port description read as
// a raw interface name rather than prose?
func looksLikeInterface(name string) bool {
	s := strings.TrimSpace(name)
	if s == "" || len(s) > 64 {
		return false
	}
	if strings.Contains(s, "::") || strings.Contains(s, " ") {
		return false
	}
	if isDigits(s) {
		return false
	}
	if (strings.Contains(s, ":") || strings.Contains(s, "-")) && isMACString(s) {
		return false
	}
	hasLetter := false
	for _, c := range s {
		if unicode.IsLetter(c) {
			hasLetter = true
			break
		}
	}
	if !hasLetter {
		return false
	}
	return !isManagement(s)
}

// resolvePort is _resolve_remote_port: the port ID when it is usable, else the
// description when it reads as an interface, else the port ID as-is.
//
// It is also how the LOCAL side of an LLDP adjacency is named (see
// lldpNeighbors), which SC2.5 does not do: it took lldpLocPortId verbatim. The
// remote table's port ID and description are, by definition, the far device's
// lldpLocPortId and lldpLocPortDesc -- so resolving both ends with this one
// function means the two devices on a link name each port identically, from
// the same TLVs, by construction. topo's bidirectional validation compares
// those names exactly; taking the local ID verbatim would record a Junos port
// as "689" while its peer names it "ge-0/0/30" through the fallback, and the
// link would be dropped as one-sided.
func resolvePort(id string, subtype int, desc string) string {
	if !unusablePortID(id, subtype) {
		return strings.TrimSpace(id)
	}
	if d := strings.TrimSpace(desc); d != "" && looksLikeInterface(d) {
		return d
	}
	return strings.TrimSpace(id)
}
