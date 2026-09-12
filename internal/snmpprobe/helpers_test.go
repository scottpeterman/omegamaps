package snmpprobe

import (
	"fmt"
	"strings"

	"github.com/gosnmp/gosnmp"

	"github.com/scottpeterman/omegamaps/internal/topo"
)

func octPDU(name string, b []byte) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: name, Type: gosnmp.OctetString, Value: b}
}

func strPDU(name, s string) gosnmp.SnmpPDU { return octPDU(name, []byte(s)) }

func intPDU(name string, v int) gosnmp.SnmpPDU {
	return gosnmp.SnmpPDU{Name: name, Type: gosnmp.Integer, Value: v}
}

func oid(root string, parts ...any) string {
	var sb strings.Builder
	sb.WriteString(root)
	for _, p := range parts {
		fmt.Fprintf(&sb, ".%v", p)
	}
	return sb.String()
}

func nopLog(string, ...any) {}

var (
	macQFX    = []byte{0x40, 0xa6, 0x77, 0x10, 0x20, 0x30}
	macSrv    = []byte{0x00, 0x25, 0x90, 0xe2, 0x11, 0x38}
	macSrvNIC = []byte{0x00, 0x25, 0x90, 0xe2, 0x11, 0x39}
	macSpine  = []byte{0x44, 0x4c, 0xa8, 0x01, 0x02, 0x03}
	macLeaf   = []byte{0x44, 0x4c, 0xa8, 0x0a, 0x0b, 0x0c}
)

const (
	descrJunos  = "Juniper Networks, Inc. qfx5100-48s-6q Ethernet Switch, kernel JUNOS 18.4R2.7"
	descrLinux  = "Linux server1 5.15.0-91-generic #101-Ubuntu SMP x86_64"
	descrArista = "Arista Networks EOS version 4.28.3M running on an Arista Networks DCS-7280SR-48C6"
)

// leafLLDP is what an Arista leaf ("leaf1") would return: a Junos QFX
// advertising a locally-assigned numeric port ID, a Linux host advertising a
// MAC port ID, and an Arista spine advertising a real ifName. Management
// addresses cover both index encodings and one address with no remote-table
// row at all.
func leafLLDP() (rem, loc, man []gosnmp.SnmpPDU, ifn ifNames) {
	r := func(col int, idx string) string { return oid(oidLldpRemEntry, col, idx) }
	rem = []gosnmp.SnmpPDU{
		intPDU(r(4, "0.10.1"), 4), intPDU(r(4, "0.11.2"), 4), intPDU(r(4, "0.12.3"), 4),
		octPDU(r(5, "0.10.1"), macQFX), octPDU(r(5, "0.11.2"), macSrv), octPDU(r(5, "0.12.3"), macSpine),
		intPDU(r(6, "0.10.1"), 7), intPDU(r(6, "0.11.2"), 3), intPDU(r(6, "0.12.3"), 5),
		strPDU(r(7, "0.10.1"), "689"), octPDU(r(7, "0.11.2"), macSrvNIC), strPDU(r(7, "0.12.3"), "Ethernet49/1"),
		strPDU(r(8, "0.10.1"), "ge-0/0/30"), strPDU(r(8, "0.11.2"), "eth1"), strPDU(r(8, "0.12.3"), "to-leaf1 uplink"),
		strPDU(r(9, "0.10.1"), "qfx1"), strPDU(r(9, "0.11.2"), "server1"), strPDU(r(9, "0.12.3"), "spine1"),
		strPDU(r(10, "0.10.1"), descrJunos), strPDU(r(10, "0.11.2"), descrLinux), strPDU(r(10, "0.12.3"), descrArista),
		octPDU(r(12, "0.10.1"), []byte{0x28}), octPDU(r(12, "0.11.2"), []byte{0x01}), octPDU(r(12, "0.12.3"), []byte{0x28}),
	}
	l := func(col, num int) string { return oid(oidLldpLocPortEntry, col, num) }
	loc = []gosnmp.SnmpPDU{
		intPDU(l(2, 10), 5), intPDU(l(2, 11), 5),
		strPDU(l(3, 10), "Ethernet10"), strPDU(l(3, 11), "Ethernet11"),
		strPDU(l(4, 10), "to qfx1"), strPDU(l(4, 11), ""),
		// no rows for 12 or 13: those resolve through ifName
	}
	v6 := "32.1.13.184.0.0.0.0.0.0.0.0.0.0.0.1" // 2001:db8::1
	man = []gosnmp.SnmpPDU{
		intPDU(oid(oidLldpRemManAddrEntry, 3, "0.10.1.1.4.10.0.0.21"), 2), // with length prefix
		intPDU(oid(oidLldpRemManAddrEntry, 3, "0.12.3.1.10.0.0.1"), 2),    // without
		intPDU(oid(oidLldpRemManAddrEntry, 3, "0.12.3.2.16."+v6), 2),      // v6 beside v4: v4 wins
		intPDU(oid(oidLldpRemManAddrEntry, 3, "0.13.4.1.4.10.0.0.99"), 2), // no remote-table row
	}
	ifn = ifNames{10: "Ethernet10", 11: "Ethernet11", 12: "Ethernet12", 13: "Ethernet13"}
	return
}

// qfxLLDP is the other end of leaf1's Ethernet10: a Junos QFX whose own
// lldpLocPortTable carries the numeric ID it advertises, with the interface
// name only in the description.
func qfxLLDP() (rem, loc, man []gosnmp.SnmpPDU, ifn ifNames) {
	r := func(col int) string { return oid(oidLldpRemEntry, col, "0.689.1") }
	rem = []gosnmp.SnmpPDU{
		intPDU(r(4), 4), octPDU(r(5), macLeaf),
		intPDU(r(6), 5), strPDU(r(7), "Ethernet10"),
		strPDU(r(8), "to qfx1"), strPDU(r(9), "leaf1"),
		strPDU(r(10), descrArista), octPDU(r(12), []byte{0x28}),
	}
	loc = []gosnmp.SnmpPDU{
		intPDU(oid(oidLldpLocPortEntry, 2, 689), 7),
		strPDU(oid(oidLldpLocPortEntry, 3, 689), "689"),
		strPDU(oid(oidLldpLocPortEntry, 4, 689), "ge-0/0/30"),
	}
	man = []gosnmp.SnmpPDU{
		intPDU(oid(oidLldpRemManAddrEntry, 3, "0.689.1.1.4.10.0.0.11"), 2),
	}
	ifn = ifNames{689: "ge-0/0/30"}
	return
}

// lldpFrom runs the full LLDP path over captured walks.
func lldpFrom(rem, loc, man []gosnmp.SnmpPDU, ifn ifNames) []topo.Neighbor {
	t := parseRemTable(rem)
	applyManAddrs(t, man)
	return lldpNeighbors(t, parseLocPorts(loc), ifn, nopLog)
}
