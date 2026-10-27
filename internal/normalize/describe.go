// internal/normalize/describe.go
//
// A display platform -- "Arista DCS-7280SR3-48YC8 EOS", "Cisco C9300-48P
// IOS-XE", "Juniper MX960 JUNOS" -- from the text a device already gave us:
// `show version` on the SSH path, sysDescr on the SNMP path. Both land in
// Device.Version, so this needs no extra round trip.
//
// WHY IT EXISTS. The fingerprint token ("arista_eos") is what the parsers key
// on and has to stay that way, but it is the wrong thing to put in a map. The
// icon tables (platform_icon_map.json, platform_map.json) are written against
// MODEL strings -- DCS-7050, C9300, QFX5, MX960, Nexus 9 -- and a driver name
// can never match one of them, so every crawled node used to fall through to
// the coarse switch/router/firewall fallback. The patterns here follow
// securecartography2's extract_platform(), which is what those tables were
// tuned against.
//
// NO VERSION in the result, deliberately. The map's platform is compared
// between runs (crawlrun/diff.go) and merged across maps (topo/merge.go);
// with a version in it every upgrade would read as a platform change. The
// version is still in Device.Version for anyone who wants it.
package normalize

import (
	"regexp"
	"strings"
)

var (
	// "Arista DCS-7280SR3-48YC8", "Arista vEOS-lab", "Arista cEOSLab". The
	// sysDescr form starts "Arista Networks EOS version ... running on an
	// Arista DCS-...", and some agents say "an Arista Networks DCS-...", so
	// "Networks" is stepped over and an "EOS" capture is not a model.
	reAristaModel = regexp.MustCompile(`\bArista\s+(?:Networks\s+)?([A-Za-z0-9][\w.\-/]*)`)

	// "cisco Nexus9000 C9396PX Chassis", "cisco Nexus 3048 Chassis".
	reNexusChassis = regexp.MustCompile(`(?i)cisco\s+(Nexus\s?\d+[\w-]*(?:\s+[A-Z0-9][\w-]*)?)\s+(?:\(.*?\)\s+)?[Cc]hassis`)
	// sysDescr: "Cisco NX-OS(tm) n9000, Software (n9000-dk9)".
	reNexusSysDescr = regexp.MustCompile(`(?i)NX-OS\(tm\)\s+n(\d+)`)

	// "cisco WS-C3750X-48P (PowerPC405) processor", "cisco C9300-48P (X86)
	// processor", "Cisco IOSv (revision 1.0) with ...".
	reCiscoProcessor = regexp.MustCompile(`(?im)^\s*cisco\s+(\S+)\s+\(.*?\)\s+(?:processor|with)`)
	// sysDescr: "Cisco IOS Software, C3750E Software (C3750E-UNIVERSALK9-M)".
	reCiscoSoftware = regexp.MustCompile(`(?i)Cisco IOS Software(?:\s*\[[^\]]*\])?,\s+([\w-]+)\s+Software`)

	// "Hardware:   ASA5516, 8192 MB RAM".
	reASAHardware = regexp.MustCompile(`(?im)^\s*Hardware:\s+(ASA[\w-]*)`)

	// "Model: mx960", "Model: qfx5100-48s-6q".
	reJunosModel = regexp.MustCompile(`(?im)^\s*Model:\s*(\S+)`)
	// sysDescr: "Juniper Networks, Inc. qfx5100-48s-6q Ethernet Switch".
	reJunosSysDescr = regexp.MustCompile(`(?i)Juniper Networks,?\s+Inc\.?\s+(\S+)`)

	// PAN-OS "show system info": "model: PA-3220".
	rePaloModel = regexp.MustCompile(`(?im)^\s*model:\s*(PA-\S+|VM-\S+)`)
	// sysDescr: "Palo Alto Networks PA-3200 series firewall".
	rePaloSysDescr = regexp.MustCompile(`(?i)Palo Alto Networks\s+((?:PA|VM)-\S+)`)
	// FortiOS "get system status": "Version: FortiGate-100F v7.2.5".
	reFortiModel = regexp.MustCompile(`(?i)\b(Forti(?:Gate|WiFi)-?[\w-]+)`)
	// AOS-CX "show version" / ProCurve: "Product Name : JL658A 6300M".
	reArubaModel = regexp.MustCompile(`(?im)(?:Product Name|Product)\s*:\s*(\S+(?:\s+\S+)?)`)
)

// Describe returns a display platform for a device, or "" when the text says
// nothing more specific than the token already does. token is the
// fingerprint ("cisco_iosxe"); text is show version output or a sysDescr.
func Describe(token, text string) string {
	t := strings.ToLower(token)
	switch {
	case strings.HasPrefix(t, "arista") || strings.Contains(text, "Arista"):
		return describeArista(text)
	case t == "cisco_nxos" || strings.Contains(text, "NX-OS") || strings.Contains(text, "Nexus"):
		return describeNexus(text)
	case t == "cisco_asa" || strings.Contains(text, "Adaptive Security Appliance"):
		if m := reASAHardware.FindStringSubmatch(text); m != nil {
			return "Cisco " + m[1] + " ASA"
		}
		return "Cisco ASA"
	case strings.HasPrefix(t, "cisco") || strings.Contains(text, "Cisco IOS"):
		return describeCiscoIOS(t, text)
	case strings.HasPrefix(t, "juniper") || reJunosSysDescr.MatchString(text):
		return describeJunos(text)
	case strings.HasPrefix(t, "paloalto") || strings.Contains(text, "PAN-OS") || strings.Contains(text, "Palo Alto Networks"):
		if m := rePaloModel.FindStringSubmatch(text); m != nil {
			return "Palo Alto " + m[1] + " PAN-OS"
		}
		if m := rePaloSysDescr.FindStringSubmatch(text); m != nil {
			return "Palo Alto " + m[1] + " PAN-OS"
		}
		return "Palo Alto PAN-OS"
	case strings.HasPrefix(t, "fortinet") || strings.Contains(text, "FortiGate"):
		if m := reFortiModel.FindStringSubmatch(text); m != nil {
			return "Fortinet " + m[1] + " FortiOS"
		}
		return "Fortinet FortiOS"
	case strings.HasPrefix(t, "aruba") || strings.Contains(text, "ArubaOS") || strings.Contains(text, "ProCurve"):
		if m := reArubaModel.FindStringSubmatch(text); m != nil {
			return "Aruba " + strings.TrimSpace(m[1])
		}
		return "Aruba"
	}
	return ""
}

// DescribeOr is Describe falling back to the token, for the places that want
// some platform string whatever happens.
func DescribeOr(token, text string) string {
	if d := Describe(token, text); d != "" {
		return d
	}
	return token
}

func describeArista(text string) string {
	for _, m := range reAristaModel.FindAllStringSubmatch(text, -1) {
		if strings.EqualFold(m[1], "Networks") || strings.EqualFold(m[1], "EOS") {
			continue
		}
		return "Arista " + m[1] + " EOS"
	}
	return "Arista EOS"
}

func describeNexus(text string) string {
	if m := reNexusChassis.FindStringSubmatch(text); m != nil {
		return "Cisco " + strings.Join(strings.Fields(m[1]), " ") + " NX-OS"
	}
	if m := reNexusSysDescr.FindStringSubmatch(text); m != nil {
		return "Cisco Nexus" + m[1] + " NX-OS"
	}
	return "Cisco Nexus NX-OS"
}

func describeCiscoIOS(token, text string) string {
	osName := "IOS"
	lower := strings.ToLower(text)
	if token == "cisco_iosxe" || strings.Contains(lower, "ios-xe") || strings.Contains(lower, "ios xe") || strings.Contains(lower, "iosxe") {
		osName = "IOS-XE"
	} else if token == "cisco_xr" || token == "cisco_iosxr" || strings.Contains(lower, "ios xr") || strings.Contains(lower, "ios-xr") {
		osName = "IOS-XR"
	}

	model := ""
	switch {
	// The virtual switch reports the router image's hardware ("IOSv"); the
	// image name is the only thing that tells them apart.
	case strings.Contains(lower, "vios_l2") || strings.Contains(lower, "viosl2"):
		model = "IOSvL2"
	default:
		if m := reCiscoProcessor.FindStringSubmatch(text); m != nil {
			model = m[1]
		} else if m := reCiscoSoftware.FindStringSubmatch(text); m != nil {
			model = m[1]
		}
	}
	// "IOSv" as a model already says the OS; "Cisco IOSv IOS" reads as a typo.
	if model == "" {
		return "Cisco " + osName
	}
	if strings.EqualFold(model, "IOSv") || strings.EqualFold(model, "IOSvL2") {
		return "Cisco " + model
	}
	return "Cisco " + model + " " + osName
}

func describeJunos(text string) string {
	if m := reJunosModel.FindStringSubmatch(text); m != nil {
		return "Juniper " + strings.ToUpper(m[1]) + " JUNOS"
	}
	if m := reJunosSysDescr.FindStringSubmatch(text); m != nil {
		return "Juniper " + strings.ToUpper(m[1]) + " JUNOS"
	}
	return "Juniper JUNOS"
}
