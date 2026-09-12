// internal/crawler/neighbor_test.go
//
// Field mapping from a parsed template record onto the topology model. The
// platform fallback is the case worth pinning: CDP carries a platform field
// and LLDP does not, so without it every neighbor discovered over LLDP — all
// of them, on an Arista or Junos fabric — reports an empty platform, and
// anything that wanted to know a device's type before dialing it gets nothing.
package crawler

import "testing"

func TestRecordToNeighborPlatform(t *testing.T) {
	tests := []struct {
		name string
		rec  map[string]string
		want string
	}{
		{
			name: "CDP reports the platform directly",
			rec:  map[string]string{"PLATFORM": "cisco WS-C2960X-48FPD-L"},
			want: "cisco WS-C2960X-48FPD-L",
		},
		{
			name: "LLDP falls back to the system description",
			rec: map[string]string{
				"NEIGHBOR_DESCRIPTION": "Juniper Networks, Inc. qfx5120-48y-8c , version 21.4R3-S4.9",
			},
			want: "juniper_junos",
		},
		{
			name: "an Arista seeing an Arista also resolves",
			rec: map[string]string{
				"NEIGHBOR_DESCRIPTION": "Arista Networks EOS version 4.28.3M running on an Arista DCS-7050SX3",
			},
			want: "arista_eos",
		},
		{
			name: "the alternate description field is used too",
			rec: map[string]string{
				"SYSTEM_DESCRIPTION": "Cisco Nexus Operating System (NX-OS) Software 9.3(10)",
			},
			want: "cisco_nxos",
		},
		{
			name: "an explicit platform wins over the description",
			rec: map[string]string{
				"PLATFORM":             "cisco N9K-C93180YC-EX",
				"NEIGHBOR_DESCRIPTION": "Arista Networks EOS",
			},
			want: "cisco N9K-C93180YC-EX",
		},
		{
			name: "an unrecognized description yields nothing rather than a guess",
			rec:  map[string]string{"NEIGHBOR_DESCRIPTION": "SomeVendor Switch OS 1.2.3"},
			want: "",
		},
		{
			name: "no platform and no description",
			rec:  map[string]string{"NEIGHBOR_NAME": "lab-r2"},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := recordToNeighbor(tc.rec, "lldp").RemotePlatform; got != tc.want {
				t.Errorf("RemotePlatform = %q, want %q", got, tc.want)
			}
		})
	}
}
