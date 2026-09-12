// internal/normalize/platform.go
//
// Platform from an LLDP system description.
//
// CDP carries a Platform field. LLDP does not — it carries a free-text system
// description, and the platform is in there as prose. Every LLDP template in
// this codebase captures the description and none of them capture a platform,
// which is why a neighbor's platform came back empty on every LLDP-only
// device: Arista and Junos both speak LLDP, so an Arista looking at a Juniper
// (or at another Arista) reported nothing at all.
//
// The value matters before the connection exists. Platform-scoped credentials
// and platform-matched jump rules both want to know what a device is *before*
// dialing it, and the neighbor's claim is the only evidence available that
// early. A fingerprint is not a substitute — it arrives after the connection
// that the platform was supposed to inform.
//
// Matching is against the vendor's own boilerplate, which is stable in the
// parts used here: vendors change model numbers and version strings
// constantly, and the company name essentially never.
package normalize

import "regexp"

// platformPatterns maps a system description onto the same platform tokens
// the fingerprinter produces, so a claimed platform and a fingerprinted one
// are the same vocabulary and can be compared.
//
// Order matters. The more specific product lines come first, since "Cisco
// Nexus Operating System" also contains "Cisco".
var platformPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{"arista_eos", regexp.MustCompile(`(?i)\bArista\b`)},
	{"cisco_nxos", regexp.MustCompile(`(?i)NX-OS|\bNexus\b`)},
	{"cisco_iosxe", regexp.MustCompile(`(?i)IOS[ \-_]?XE`)},
	{"cisco_ios", regexp.MustCompile(`(?i)Cisco IOS`)},
	{"cisco_asa", regexp.MustCompile(`(?i)Adaptive Security Appliance`)},
	{"juniper_junos", regexp.MustCompile(`(?i)\bJunos\b|\bJuniper\b`)},
	{"linux", regexp.MustCompile(`(?i)\bLinux\b`)},
}

// PlatformFromDescription returns a platform token for an LLDP system
// description, or empty when nothing matches.
//
// Empty is a real answer and the caller must treat it as "unknown" rather
// than as a default. Guessing here would be worse than not knowing: a
// platform-scoped credential offered to a device that is not that platform is
// a wasted authentication attempt against every such device in the fabric.
func PlatformFromDescription(descr string) string {
	if descr == "" {
		return ""
	}
	for _, p := range platformPatterns {
		if p.re.MatchString(descr) {
			return p.name
		}
	}
	return ""
}
