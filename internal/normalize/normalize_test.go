// internal/normalize/normalize_test.go
package normalize

import "testing"

func TestInterface(t *testing.T) {
	cases := map[string]string{
		"GigabitEthernet0/0":    "Gi0/0",
		"TenGigabitEthernet1/1": "Te1/1",
		"TenGigE0/0/0/1":        "Te0/0/0/1",
		"HundredGigE0/0/0/0":    "Hu0/0/0/0",
		"FastEthernet0/1":       "Fa0/1",
		"Ethernet49/1":          "Eth49/1",
		"Et1/1":                 "Eth1/1", // Arista LLDP short form
		"Port-channel10":        "Po10",
		"port-Channel2":         "Po2",
		"Vlan666":               "Vl666",
		"VLAN-42":               "Vl42",
		"Null0":                 "Nu0",
		"Loopback0":             "Lo0",
		"xe-0/0/0.0":            "xe-0/0/0", // junos default unit stripped
		"xe-0/0/0.123":          "xe-0/0/0.123",
		"ae10.0":                "ae10",
		"et-0/0/48":             "et-0/0/48", // junos lowercase untouched
		"":                      "",
	}
	for in, want := range cases {
		if got := Interface(in); got != want {
			t.Errorf("Interface(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIdentifierAndShortName(t *testing.T) {
	if got := Identifier("Lab-R1.Lab.Example."); got != "lab-r1.lab.example" {
		t.Errorf("Identifier = %q", got)
	}
	if got := ShortName("lab-r1.lab.example"); got != "lab-r1" {
		t.Errorf("ShortName = %q", got)
	}
}

func TestIsMACAddress(t *testing.T) {
	for _, mac := range []string{"aa:bb:cc:dd:ee:ff", "AABB.CCDD.EEFF", "aa-bb-cc-dd-ee-ff"} {
		if !IsMACAddress(mac) {
			t.Errorf("IsMACAddress(%q) = false", mac)
		}
	}
	for _, name := range []string{"lab-r1", "lab-r1.lab.example", "10.20.0.5"} {
		if IsMACAddress(name) {
			t.Errorf("IsMACAddress(%q) = true", name)
		}
	}
}

func TestIsArtifactName(t *testing.T) {
	for _, bad := range []string{"detail", "^", "%", "sho", "", " ", "'x'"} {
		if !IsArtifactName(bad) {
			t.Errorf("IsArtifactName(%q) = false", bad)
		}
	}
	if IsArtifactName("lab-spine-1") {
		t.Error("real name flagged as artifact")
	}
}

func TestShouldExclude(t *testing.T) {
	fields := []string{"Linux lab-host 6.1", "lab-host", ""}
	if excl, pat := ShouldExclude(fields, []string{"idrac,linux"}); !excl || pat != "linux" {
		t.Errorf("expected exclusion on linux, got %v %q", excl, pat)
	}
	if excl, _ := ShouldExclude(fields, []string{"junos"}); excl {
		t.Error("unexpected exclusion")
	}
	if excl, _ := ShouldExclude(fields, nil); excl {
		t.Error("nil patterns excluded")
	}
}

func TestStripSuffixes(t *testing.T) {
	sfx := []string{"lab.local"}
	cases := map[string]string{
		"spine-1.usa.lab.local": "spine-1.usa",
		"Spine-1.USA.Lab.Local": "spine-1.usa",
		"rtr-1.eng.lab.local":   "rtr-1.eng",
		"spine-1.usa":           "spine-1.usa", // already stripped
		"172.16.1.2":            "172.16.1.2",
		"other.example.net":     "other.example.net",
	}
	for in, want := range cases {
		if got := StripSuffixes(in, sfx); got != want {
			t.Errorf("StripSuffixes(%q) = %q, want %q", in, got, want)
		}
	}
}
