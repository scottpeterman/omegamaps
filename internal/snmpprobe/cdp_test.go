package snmpprobe

import (
	"reflect"
	"testing"

	"github.com/gosnmp/gosnmp"

	"github.com/scottpeterman/omegamaps/internal/topo"
)

// cdpFrom runs the full CDP path over captured column walks.
func cdpFrom(cols map[int][]gosnmp.SnmpPDU, ifn ifNames) []topo.Neighbor {
	t := parseCDPDeviceIDs(cols[cdpColDeviceID])
	for _, col := range cdpColumns[1:] {
		applyCDPColumn(t, col, cols[col])
	}
	return cdpNeighbors(t, ifn)
}

func TestCDPNeighbors(t *testing.T) {
	c := func(col int, idx string) string { return oid(oidCdpCacheEntry, col, idx) }
	cols := map[int][]gosnmp.SnmpPDU{
		cdpColDeviceID: {
			strPDU(c(6, "5.1"), "core1.lab.example"),
			strPDU(c(6, "7.2"), "CW_"),  // SC2.5 skip list
			strPDU(c(6, "9.3"), "N/A"),  // named by its address
			strPDU(c(6, "11.4"), "N/A"), // no address either: dropped
			strPDU(c(6, "13.5"), "ap1"),
		},
		cdpColDevicePort: {
			strPDU(c(7, "5.1"), "GigabitEthernet1/0/1"),
			strPDU(c(7, "9.3"), "Gi0/1"),
			strPDU(c(7, "13.5"), "GigabitEthernet0"),
			strPDU(c(7, "99.9"), "Gi9/9"), // no DeviceId row: ignored
		},
		cdpColAddress: {
			octPDU(c(4, "5.1"), []byte{10, 1, 1, 1}),
			octPDU(c(4, "9.3"), []byte{10, 1, 3, 3}),
			octPDU(c(4, "13.5"), []byte{1, 10, 1, 5, 5}), // with family byte
		},
		cdpColPlatform: {
			strPDU(c(8, "5.1"), "cisco WS-C3850-24T"),
			strPDU(c(8, "13.5"), "cisco AIR-AP2802I-B-K9"),
		},
		cdpColVersion: {
			strPDU(c(5, "5.1"), "Cisco IOS Software [Everest], Catalyst L3 Switch Software (CAT3K_CAA-UNIVERSALK9-M)"),
		},
		cdpColCapabilities: {
			octPDU(c(9, "5.1"), []byte{0, 0, 0, 0x29}),
			octPDU(c(9, "13.5"), []byte{0, 0, 0, 0x02}),
		},
		cdpColPrimaryMgmtAddr: {
			octPDU(c(20, "5.1"), []byte{10, 9, 9, 1}), // preferred over the link address
		},
	}
	ifn := ifNames{5: "Gi1/0/5", 9: "Gi1/0/9", 13: "Gi1/0/13"}

	got := cdpFrom(cols, ifn)
	want := []topo.Neighbor{
		{
			LocalInterface: "Gi1/0/5", RemoteDevice: "core1.lab.example", RemoteInterface: "GigabitEthernet1/0/1",
			RemoteIP: "10.9.9.1", RemotePlatform: "cisco WS-C3850-24T",
			RemoteDescr:  "Cisco IOS Software [Everest], Catalyst L3 Switch Software (CAT3K_CAA-UNIVERSALK9-M)",
			Capabilities: "router, switch, igmp", Protocol: "cdp",
		},
		{
			LocalInterface: "Gi1/0/9", RemoteDevice: "10.1.3.3", RemoteInterface: "Gi0/1",
			RemoteIP: "10.1.3.3", Protocol: "cdp",
		},
		{
			LocalInterface: "Gi1/0/13", RemoteDevice: "ap1", RemoteInterface: "GigabitEthernet0",
			RemoteIP: "10.1.5.5", RemotePlatform: "cisco AIR-AP2802I-B-K9",
			Capabilities: "bridge", Protocol: "cdp",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("neighbors:\n got %+v\nwant %+v", got, want)
	}
}

// A Cisco box running both protocols reports each link twice. The records
// collapse on the crawler's key, LLDP kept, CDP filling what LLDP lacks.
func TestDedupeMergesLLDPAndCDP(t *testing.T) {
	lldp := topo.Neighbor{
		LocalInterface: "Gi1/0/5", RemoteDevice: "core1.lab.example", RemoteInterface: "Gi1/0/1",
		RemoteDescr: "Cisco IOS Software", Protocol: "lldp",
	}
	cdp := topo.Neighbor{
		LocalInterface: "GigabitEthernet1/0/5", RemoteDevice: "CORE1.lab.example.", RemoteInterface: "GigabitEthernet1/0/1",
		RemoteIP: "10.9.9.1", RemotePlatform: "cisco WS-C3850-24T", RemoteDescr: "ignored: already set",
		Protocol: "cdp",
	}
	other := topo.Neighbor{LocalInterface: "Gi1/0/6", RemoteDevice: "core2", RemoteInterface: "Gi1/0/1", Protocol: "cdp"}

	got := dedupe([]topo.Neighbor{lldp, cdp, other})
	if len(got) != 2 {
		t.Fatalf("want 2 edges, got %d: %+v", len(got), got)
	}
	m := got[0]
	if m.Protocol != "lldp" || m.RemoteIP != "10.9.9.1" || m.RemotePlatform != "cisco WS-C3850-24T" ||
		m.RemoteDescr != "Cisco IOS Software" {
		t.Fatalf("merge: %+v", m)
	}
}
