package snmpprobe

import "testing"

func TestDecodeString(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte("  Ethernet1\x00"), "Ethernet1"},
		{[]byte{'c', 'a', 'f', 0xe9}, "café"}, // invalid UTF-8: Latin-1
		{[]byte("0x1234"), "0x1234"},          // raw bytes are not pysnmp's hex rendering
		{nil, ""},
	}
	for _, c := range cases {
		if got := decodeString(c.in); got != c.want {
			t.Errorf("decodeString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestDecodeAddresses(t *testing.T) {
	cases := []struct {
		in   []byte
		want string
	}{
		{[]byte{10, 0, 0, 1}, "10.0.0.1"},
		{[]byte{1, 10, 0, 0, 1}, "10.0.0.1"},
		{[]byte{2, 10, 0, 0, 1}, ""}, // family 2 with 4 octets is not an address
		{append([]byte{2}, make([]byte, 16)...), "::"},
		{[]byte{1, 2, 3}, ""},
	}
	for _, c := range cases {
		if got := decodeNetAddr(c.in); got != c.want {
			t.Errorf("decodeNetAddr(%v) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := decodeIPv4(make([]byte, 16)); got != "" {
		t.Errorf("decodeIPv4 accepted IPv6: %q", got)
	}
	if got := decodeChassisID(chassisSubtypeMAC, macSrv); got != "00:25:90:e2:11:38" {
		t.Errorf("chassis MAC = %q", got)
	}
	if got := decodeChassisID(chassisSubtypeNetwork, []byte{1, 192, 0, 2, 7}); got != "192.0.2.7" {
		t.Errorf("chassis network address = %q", got)
	}
	if got := decodePortID(portSubtypeMAC, macSrvNIC); got != "00:25:90:e2:11:39" {
		t.Errorf("port MAC = %q", got)
	}
}

func TestCapabilities(t *testing.T) {
	lldp := []struct {
		in   []byte
		want string
	}{
		{[]byte{0x28}, "bridge, router"}, // BITS: bit 0 is the MSB
		{[]byte{0x80}, "other"},
		{[]byte{0x01}, "station"},
		{[]byte{0x20, 0x00}, "bridge"}, // agents that send two octets
		{nil, ""},
	}
	for _, c := range lldp {
		if got := lldpCapabilities(c.in); got != c.want {
			t.Errorf("lldpCapabilities(%x) = %q, want %q", c.in, got, c.want)
		}
	}
	cdp := []struct {
		in   []byte
		want string
	}{
		{[]byte{0, 0, 0, 0x29}, "router, switch, igmp"},
		{[]byte{0x08}, "switch"}, // short form, right-aligned
		{[]byte{0, 0, 0, 0, 1}, ""},
		{nil, ""},
	}
	for _, c := range cdp {
		if got := cdpCapabilities(c.in); got != c.want {
			t.Errorf("cdpCapabilities(%x) = %q, want %q", c.in, got, c.want)
		}
	}
}

// The cases are the field findings recorded in SC2.5 lldp.py.
func TestPortResolution(t *testing.T) {
	unusable := []struct {
		id      string
		subtype int
		want    bool
	}{
		{"689", 7, true},               // Juniper EX locally assigned
		{"00:25:90:e2:11:38", 5, true}, // MAC-shaped regardless of subtype
		{"anything", portSubtypeMAC, true},
		{"Management1", 5, true}, // Arista EOS <= 4.23
		{"mgmt0", 5, true},
		{"", 5, true},
		{"Ethernet1", 5, false},
		{"ge-0/0/30", 5, false},
	}
	for _, c := range unusable {
		if got := unusablePortID(c.id, c.subtype); got != c.want {
			t.Errorf("unusablePortID(%q, %d) = %v, want %v", c.id, c.subtype, got, c.want)
		}
	}
	iface := []struct {
		s    string
		want bool
	}{
		{"ge-1/0/30", true}, {"eth1", true}, {"tor0", true}, {"Te1/49", true},
		{"to spine1", false}, {"host::eth0", false}, {"123", false},
		{"00-25-90-e2-11-38", false}, {"Mgmt0", false}, {"", false},
	}
	for _, c := range iface {
		if got := looksLikeInterface(c.s); got != c.want {
			t.Errorf("looksLikeInterface(%q) = %v, want %v", c.s, got, c.want)
		}
	}
	res := []struct {
		id      string
		subtype int
		desc    string
		want    string
	}{
		{"Ethernet1", 5, "uplink to core", "Ethernet1"},
		{"689", 7, "ge-0/0/30", "ge-0/0/30"},
		{"00:25:90:e2:11:38", 3, "eth1", "eth1"},
		{"689", 7, "uplink to core", "689"}, // no usable fallback: as-is
		{"", 5, "", ""},
	}
	for _, c := range res {
		if got := resolvePort(c.id, c.subtype, c.desc); got != c.want {
			t.Errorf("resolvePort(%q, %d, %q) = %q, want %q", c.id, c.subtype, c.desc, got, c.want)
		}
	}
}
