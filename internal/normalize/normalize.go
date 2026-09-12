// internal/normalize/normalize.go
// Ports of the discovery engine's normalization logic: interface short-form
// canonicalization (for edge dedup), identifier normalization (for the crawl
// claim set), and substring-based device exclusion. Pure functions, no I/O.
//
// The interface replacement table is accumulated field knowledge — do not
// "simplify" it; every entry exists because two sides of a real link
// described the same port differently.
package normalize

import (
	"net/netip"
	"regexp"
	"strings"
)

// cisco long-form -> short-form. Order matters: longer variants first,
// bare "Ethernet" last so it doesn't shadow them.
var ciscoReplacements = []struct{ long, short string }{
	{"TenGigabitEthernet", "Te"},
	{"TenGigE", "Te"}, // IOS-XR style
	{"FortyGigabitEthernet", "Fo"},
	{"FortyGigE", "Fo"},
	{"HundredGigabitEthernet", "Hu"},
	{"HundredGigE", "Hu"},
	{"TwentyFiveGigE", "Twe"},
	{"GigabitEthernet", "Gi"},
	{"FastEthernet", "Fa"},
	{"Ethernet", "Eth"},
}

var (
	portChannelRe = regexp.MustCompile(`^[Pp]ort-[Cc]hannel(\d+.*)$`)
	vlanRe        = regexp.MustCompile(`^[Vv][Ll][Aa][Nn]-?(\d+.*)$`)
	aristaShortRe = regexp.MustCompile(`^Et(\d)`)
	junosUnit0Re  = regexp.MustCompile(`(?i)^((?:xe|ge|et|ae|irb|em|me|fxp)-?\d+(?:/\d+)*)\.0$`)
)

// Interface canonicalizes an interface name for display and edge dedup.
func Interface(in string) string {
	if in == "" {
		return ""
	}
	r := strings.TrimSpace(in)

	for _, rep := range ciscoReplacements {
		if strings.HasPrefix(r, rep.long) {
			r = rep.short + r[len(rep.long):]
			break
		}
	}
	if m := portChannelRe.FindStringSubmatch(r); m != nil {
		r = "Po" + m[1]
	}
	if m := vlanRe.FindStringSubmatch(r); m != nil {
		r = "Vl" + m[1]
	}
	if strings.HasPrefix(r, "Null") {
		r = "Nu" + r[4:]
	}
	if strings.HasPrefix(r, "Loopback") {
		r = "Lo" + r[8:]
	}
	// Arista LLDP short form: Et1/1 -> Eth1/1
	r = aristaShortRe.ReplaceAllString(r, "Eth$1")
	// Junos default unit: xe-0/0/0.0 -> xe-0/0/0 (non-zero units kept)
	r = junosUnit0Re.ReplaceAllString(r, "$1")
	return r
}

// Identifier normalizes a hostname/sysname/IP for the crawl claim set:
// lowercase, trailing-dot (FQDN) stripped.
func Identifier(id string) string {
	return strings.TrimRight(strings.ToLower(id), ".")
}

// ShortName returns the first label of a hostname ("lab-r1.lab.example"
// -> "lab-r1"), for claim-set aliasing.
//
// An address is returned whole. The dots in 172.16.128.2 are not label
// separators, and splitting on the first one yields "172" — a key every other
// 172.x device in the estate also yields. In the claim set that collides one
// device onto another and the second is silently never crawled; in the
// credential binding store it would hand one device's credential to another.
func ShortName(id string) string {
	id = Identifier(id)
	if _, err := netip.ParseAddr(id); err == nil {
		return id
	}
	if i := strings.IndexByte(id, '.'); i > 0 {
		return id[:i]
	}
	return id
}

// macNameRe matches the common chassis-ID-as-name forms LLDP hands back
// instead of a sysname: aa:bb:cc:dd:ee:ff, aabb.ccdd.eeff, aa-bb-cc-dd-ee-ff.
var macNameRe = regexp.MustCompile(
	`^([0-9A-Fa-f]{2}([:\-])){5}[0-9A-Fa-f]{2}$|^([0-9A-Fa-f]{4}\.){2}[0-9A-Fa-f]{4}$`)

// IsMACAddress reports whether a neighbor "name" is really a chassis MAC.
func IsMACAddress(v string) bool { return macNameRe.MatchString(strings.TrimSpace(v)) }

// parsing artifacts that show up as neighbor names when a template
// mis-anchors on echoed text.
var artifactNames = map[string]struct{}{
	"detail": {}, "^": {}, "%": {}, "sho": {}, "": {},
}

// IsArtifactName filters neighbor names that are parsing debris.
func IsArtifactName(v string) bool {
	v = strings.ToLower(strings.Trim(strings.TrimSpace(v), `'"`))
	if _, ok := artifactNames[v]; ok {
		return true
	}
	return len(v) < 2
}

// ShouldExclude reports whether any pattern (comma-separable, matched as a
// lowercase substring) hits any of the given identity fields
// (description, hostname, sysname).
func ShouldExclude(fields []string, patterns []string) (bool, string) {
	var expanded []string
	for _, p := range patterns {
		for _, part := range strings.Split(p, ",") {
			if part = strings.TrimSpace(part); part != "" {
				expanded = append(expanded, strings.ToLower(part))
			}
		}
	}
	if len(expanded) == 0 {
		return false, ""
	}
	for _, pat := range expanded {
		for _, f := range fields {
			if f != "" && strings.Contains(strings.ToLower(f), pat) {
				return true, pat
			}
		}
	}
	return false, ""
}

// StripSuffixes removes the first matching domain suffix from an
// identifier: StripSuffixes("spine-1.usa.lab.local", ["lab.local"])
// -> "spine-1.usa". Site labels below the stripped suffix survive, so
// same-named devices in different sites stay distinct. Input and output
// are Identifier-normalized.
func StripSuffixes(name string, suffixes []string) string {
	n := Identifier(name)
	for _, s := range suffixes {
		s = Identifier(strings.TrimPrefix(strings.TrimSpace(s), "."))
		if s == "" {
			continue
		}
		if strings.HasSuffix(n, "."+s) {
			return strings.TrimSuffix(n, "."+s)
		}
	}
	return n
}
