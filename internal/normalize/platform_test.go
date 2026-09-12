// internal/normalize/platform_test.go
//
// The descriptions here are the vendor boilerplate as it appears in an LLDP
// system description, with lab hostnames and trimmed version strings. What is
// being pinned is the vendor identification, which is the stable part —
// model numbers and versions move constantly and are not matched on.
package normalize

import "testing"

func TestPlatformFromDescription(t *testing.T) {
	tests := []struct {
		name  string
		descr string
		want  string
	}{
		{
			name:  "arista eos",
			descr: "Arista Networks EOS version 4.28.3M running on an Arista DCS-7050SX3-48YC8",
			want:  "arista_eos",
		},
		{
			name:  "juniper junos",
			descr: "Juniper Networks, Inc. qfx5120-48y-8c , version 21.4R3-S4.9 Build date: 2023-01-01",
			want:  "juniper_junos",
		},
		{
			name:  "junos named without the company",
			descr: "Junos: 20.4R3-S5.3",
			want:  "juniper_junos",
		},
		{
			name:  "cisco nxos",
			descr: "Cisco Nexus Operating System (NX-OS) Software 9.3(10)",
			want:  "cisco_nxos",
		},
		{
			name:  "cisco ios-xe with a separator",
			descr: "Cisco IOS Software, IOS-XE Software, Version 17.3.5",
			want:  "cisco_iosxe",
		},
		{
			name:  "cisco ios-xe in a build tag",
			descr: "Cisco IOS Software [Bengaluru], Catalyst L3 Switch Software (CAT9K_IOSXE), Version 17.6.4",
			want:  "cisco_iosxe",
		},
		{
			name:  "cisco ios classic",
			descr: "Cisco IOS Software, C2960X Software (C2960X-UNIVERSALK9-M), Version 15.2(7)E3",
			want:  "cisco_ios",
		},
		{
			name:  "linux server",
			descr: "Ubuntu 22.04.3 LTS Linux 5.15.0-88-generic",
			want:  "linux",
		},
		{
			name:  "empty description",
			descr: "",
			want:  "",
		},
		{
			name:  "an unrecognized vendor is not guessed at",
			descr: "SomeVendor Switch OS, Version 1.2.3",
			want:  "",
		},
		{
			name:  "a chassis id is not a description",
			descr: "00:1c:73:11:22:33",
			want:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := PlatformFromDescription(tc.descr); got != tc.want {
				t.Errorf("PlatformFromDescription(%q) = %q, want %q", tc.descr, got, tc.want)
			}
		})
	}
}

// TestPlatformOrderingIsSpecificFirst guards the one ambiguity that actually
// occurs: NX-OS descriptions also say "Cisco", and IOS-XE descriptions also
// say "Cisco IOS Software". The more specific pattern has to win.
func TestPlatformOrderingIsSpecificFirst(t *testing.T) {
	if got := PlatformFromDescription("Cisco Nexus Operating System (NX-OS) Software"); got != "cisco_nxos" {
		t.Errorf("NX-OS classified as %q", got)
	}
	if got := PlatformFromDescription("Cisco IOS Software, IOS-XE Software"); got != "cisco_iosxe" {
		t.Errorf("IOS-XE classified as %q", got)
	}
}
