// internal/normalize/prompt_test.go
//
// A wrong hostname is worse than no hostname: it merges two devices into one
// map node silently. So the negative cases here matter as much as the
// positive ones — anything that is not clearly a prompt has to come back
// empty and leave the caller with what it already had.
package normalize

import "testing"

func TestHostnameFromPrompt(t *testing.T) {
	tests := []struct {
		name   string
		prompt string
		want   string
	}{
		// --- operational and enable prompts ---
		{"operational", "lab-r1>", "lab-r1"},
		{"enable", "lab-r1#", "lab-r1"},
		{"trailing space", "lab-r1#  ", "lab-r1"},
		{"fully qualified", "lab-r1.site1.lab.example>", "lab-r1.site1.lab.example"},
		{"uppercase normalizes", "LAB-R1#", "lab-r1"},

		// --- configuration modes ---
		{"config", "lab-r1(config)#", "lab-r1"},
		{"config sub-mode", "lab-r1(config-if)#", "lab-r1"},
		{"config interface instance", "lab-r1(config-if-Et1)#", "lab-r1"},
		{"vrf decoration", "lab-r1(vrf:mgmt)#", "lab-r1"},

		// --- Junos ---
		{"junos operational", "admin@lab-qfx1>", "lab-qfx1"},
		{"junos configuration", "admin@lab-qfx1#", "lab-qfx1"},
		{"junos with RE banner above", "{master:0}\nadmin@lab-qfx1>", "lab-qfx1"},
		{"junos edit banner above", "[edit]\nadmin@lab-qfx1#", "lab-qfx1"},

		// --- surrounding output ---
		{"prompt is the last line", "some command output\nmore output\nlab-r1#", "lab-r1"},
		{"blank lines after the prompt", "lab-r1#\n\n\n", "lab-r1"},
		{"CRLF", "lab-r1#\r\n", "lab-r1"},

		// --- refusals ---
		{"empty", "", ""},
		{"whitespace only", "   \n\t\n", ""},
		{"no prompt character", "lab-r1", ""},
		{"a line of output is not a prompt", "Interface Et1 is up, line protocol is up", ""},
		{"bare prompt character", "#", ""},
		{"bare shell prompt", "$", ""},
		{"prompt character only after user@", "admin@>", ""},
		{"spaces inside are not a hostname", "some words here#", ""},
		{"leading punctuation is not a hostname", "-lab-r1#", ""},
		{"brackets are not a hostname", "[edit]#", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HostnameFromPrompt(tc.prompt); got != tc.want {
				t.Errorf("HostnameFromPrompt(%q) = %q, want %q", tc.prompt, got, tc.want)
			}
		})
	}
}

// TestHostnameFromPromptFeedsTheSameIdentity checks the recovered name lands
// on the same key as the fully qualified form, which is the whole point: a
// device reached by address must not become a second node.
func TestHostnameFromPromptFeedsTheSameIdentity(t *testing.T) {
	suffixes := []string{"site1.lab.example"}

	fromPrompt := StripSuffixes(HostnameFromPrompt("lab-r1#"), suffixes)
	fromClaim := StripSuffixes("lab-r1.site1.lab.example", suffixes)

	if fromPrompt != fromClaim {
		t.Errorf("prompt gives %q, neighbor claim gives %q — two nodes for one device",
			fromPrompt, fromClaim)
	}
}
