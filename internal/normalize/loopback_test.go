package normalize

import "testing"

func TestIsLoopback(t *testing.T) {
	for v, want := range map[string]bool{
		"localhost": true, "LOCALHOST.": true, "localhost.localdomain": true,
		"ip6-localhost": true, "box.localhost": true,
		"127.0.0.1": true, "127.10.0.5": true, "::1": true, "[::1]": true, "0.0.0.0": true, "::": true,
		"": false, "lab-r1": false, "localhost-sw1": false, "mylocalhost": false,
		"10.0.0.1": false, "2001:db8::1": false, "usa-spine-2.lab.local": false,
	} {
		if got := IsLoopback(v); got != want {
			t.Errorf("IsLoopback(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestHostnameFromPromptRefusesLocalhost(t *testing.T) {
	if got := HostnameFromPrompt("localhost#"); got != "" {
		t.Errorf("localhost# named the device %q", got)
	}
	if got := HostnameFromPrompt("lab-r1#"); got != "lab-r1" {
		t.Errorf("lab-r1# = %q", got)
	}
}
