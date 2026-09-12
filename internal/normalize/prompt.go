// internal/normalize/prompt.go
//
// Recovering a device's own name from its shell prompt.
//
// A crawl reaches plenty of devices by address: a neighbor claimed by chassis
// MAC falls back to its management IP, and seeds are often typed as addresses.
// Naming the map node after the address is technically true and practically
// useless — the same box appears twice if anything else reaches it by name,
// and the operator reading the map has to translate.
//
// The device already told us its name. It is in the prompt, wrapped in
// whatever decoration the vendor adds:
//
//	lab-r1>                     operational
//	lab-r1#                     enable
//	lab-r1(config)#             configuration
//	lab-r1(config-if-Et1)#      sub-configuration
//	admin@lab-r1>               Junos, user@host
//	admin@lab-r1#               Junos configuration
//	{master:0}                  Junos routing-engine banner, on its own line
//
// Stripping that is what this file does. It is deliberately conservative:
// anything that does not come out looking like a hostname returns empty, and
// the caller keeps whatever it had. A wrong name is worse than no name,
// because a wrong name silently merges two devices in the map.
package normalize

import (
	"regexp"
	"strings"
)

var (
	// promptTail is the prompt character and anything after it.
	promptTail = regexp.MustCompile(`[#>$%]\s*$`)

	// modeSuffix is the parenthesized mode a device appends while in
	// configuration, e.g. "(config)", "(config-if-Et1)", "(vrf:mgmt)".
	modeSuffix = regexp.MustCompile(`\([^)]*\)\s*$`)

	// hostnameShape is what a plausible device name looks like. Leading
	// alphanumeric, then the characters a hostname may carry. Anything else
	// — spaces, brackets, colons, punctuation — means we did not actually
	// find a name.
	hostnameShape = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

// HostnameFromPrompt extracts the device name from a prompt line, or returns
// empty when the line does not yield something hostname-shaped.
//
// Only the last line is considered, since Junos prints a routing-engine
// banner like "{master:0}" above the prompt and IOS-family devices can leave
// a blank line.
func HostnameFromPrompt(prompt string) string {
	// "localhost#" names nothing: every box left at its default would be
	// the same node. The device keeps the address it was reached at.
	if name := hostnameFromPrompt(prompt); !IsLoopback(name) {
		return name
	}
	return ""
}

func hostnameFromPrompt(prompt string) string {
	line := lastNonEmptyLine(prompt)
	if line == "" {
		return ""
	}

	// Trailing prompt character.
	if loc := promptTail.FindStringIndex(line); loc != nil {
		line = line[:loc[0]]
	} else {
		// No prompt character at all. This is not a prompt line; refusing
		// here is what keeps a stray line of command output from being
		// adopted as the device's name.
		return ""
	}

	// Configuration-mode decoration, possibly nested: "(config-if-Et1)".
	for {
		trimmed := modeSuffix.ReplaceAllString(line, "")
		if trimmed == line {
			break
		}
		line = trimmed
	}

	// Junos "user@host": the part after the last @ is the device.
	if i := strings.LastIndexByte(line, '@'); i >= 0 {
		line = line[i+1:]
	}

	line = strings.TrimSpace(line)
	// A hostname may be fully qualified; the caller strips domains with
	// StripSuffixes if it wants the short form.
	line = strings.Trim(line, ".")

	if line == "" || !hostnameShape.MatchString(line) {
		return ""
	}
	return strings.ToLower(line)
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
