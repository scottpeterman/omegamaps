package snmpprobe

import (
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/gosnmp/gosnmp"

	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

func TestLLDPNeighbors(t *testing.T) {
	got := lldpFrom(leafLLDP())
	want := []topo.Neighbor{
		{
			// Junos advertises "689"; the description carries the name.
			LocalInterface: "Ethernet10", RemoteDevice: "qfx1", RemoteInterface: "ge-0/0/30",
			RemoteIP: "10.0.0.21", RemotePlatform: "juniper_junos", RemoteDescr: descrJunos,
			Capabilities: "bridge, router", Protocol: "lldp",
		},
		{
			// A Linux NIC advertises its MAC; the description carries eth1.
			LocalInterface: "Ethernet11", RemoteDevice: "server1", RemoteInterface: "eth1",
			RemotePlatform: "linux", RemoteDescr: descrLinux,
			Capabilities: "station", Protocol: "lldp",
		},
		{
			// A usable ifName wins over a prose description; no local-port
			// row, so the local side comes from ifName; the management
			// address was indexed without a length prefix, and the IPv4
			// address wins over the IPv6 one beside it.
			LocalInterface: "Ethernet12", RemoteDevice: "spine1", RemoteInterface: "Ethernet49/1",
			RemoteIP: "10.0.0.1", RemotePlatform: "arista_eos", RemoteDescr: descrArista,
			Capabilities: "bridge, router", Protocol: "lldp",
		},
		{
			// Management address with no lldpRemTable row: kept, as in
			// SC2.5, as a crawl target with no remote port.
			LocalInterface: "Ethernet13", RemoteDevice: "10.0.0.99", RemoteIP: "10.0.0.99",
			Protocol: "lldp",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("neighbors:\n got %+v\nwant %+v", got, want)
	}
}

// SC2.5 decoded chassis and port IDs as values arrived, relying on a
// lexicographic walk returning each subtype column first. The port decodes
// after the walk, so the result must not depend on PDU order.
func TestLLDPOrderIndependent(t *testing.T) {
	rem, loc, man, ifn := leafLLDP()
	fwd := lldpFrom(rem, loc, man, ifn)

	rev := slices.Clone(rem)
	slices.Reverse(rev)
	back := lldpFrom(rev, loc, man, ifn)

	byLocal := func(ns []topo.Neighbor) {
		sort.Slice(ns, func(i, j int) bool { return ns[i].LocalInterface < ns[j].LocalInterface })
	}
	byLocal(fwd)
	byLocal(back)
	if !reflect.DeepEqual(fwd, back) {
		t.Fatalf("reversed walk changed the result:\n fwd %+v\nback %+v", fwd, back)
	}
}

func TestLLDPEmptyAndPlaceholderRowsSkipped(t *testing.T) {
	r := func(col int, idx string) string { return oid(oidLldpRemEntry, col, idx) }
	rem := []gosnmp.SnmpPDU{
		// no name, no chassis, no management address: nothing to identify
		strPDU(r(8, "0.1.1"), "Ethernet1"),
		// the "(" placeholder SC2.5 filters, for both name and chassis
		intPDU(r(4, "0.2.2"), 7), strPDU(r(5, "0.2.2"), "("), strPDU(r(9, "0.2.2"), "("),
	}
	if got := lldpFrom(rem, nil, nil, ifNames{}); len(got) != 0 {
		t.Fatalf("expected no neighbors, got %+v", got)
	}
}

// A chassis ID is the remote device name when there is no sysName, which is
// what lets the crawler's MAC-name fallback to the management address apply.
func TestLLDPChassisNameFallback(t *testing.T) {
	r := func(col int) string { return oid(oidLldpRemEntry, col, "0.5.1") }
	rem := []gosnmp.SnmpPDU{
		intPDU(r(4), 4), octPDU(r(5), macSrv),
		intPDU(r(6), 5), strPDU(r(7), "eth0"),
	}
	got := lldpFrom(rem, nil, nil, ifNames{5: "Ethernet5"})
	if len(got) != 1 || got[0].RemoteDevice != "00:25:90:e2:11:38" || !normalize.IsMACAddress(got[0].RemoteDevice) {
		t.Fatalf("got %+v", got)
	}
}

func TestLLDPLocalFallsBackToIfIndexPlaceholder(t *testing.T) {
	r := func(col int) string { return oid(oidLldpRemEntry, col, "0.77.1") }
	rem := []gosnmp.SnmpPDU{strPDU(r(9), "peer1"), intPDU(r(6), 5), strPDU(r(7), "Ethernet1")}
	got := lldpFrom(rem, nil, nil, ifNames{})
	if len(got) != 1 || got[0].LocalInterface != "ifIndex_77" {
		t.Fatalf("got %+v", got)
	}
}

// The reason the local side is resolved like the remote side. Both ends of
// leaf1 Ethernet10 <-> qfx1 ge-0/0/30 are collected over SNMP and handed to
// the real topo.Generate with bidirectional validation on. The link has to
// appear from both sides. Then the SC2.5 behavior -- local port ID verbatim --
// is substituted on the Junos side, and the same link has to disappear, which
// is the failure this guards against.
func TestJunosLinkSurvivesBidirectionalValidation(t *testing.T) {
	leaf := &topo.Device{
		Hostname: "leaf1", SysName: "leaf1", IPAddress: "10.0.0.11", Platform: "arista_eos",
		Neighbors: lldpFrom(leafLLDP()),
	}
	qfx := &topo.Device{
		Hostname: "qfx1", SysName: "qfx1", IPAddress: "10.0.0.21", Platform: "juniper_junos",
		Neighbors: lldpFrom(qfxLLDP()),
	}
	if got := qfx.Neighbors[0].LocalInterface; got != "ge-0/0/30" {
		t.Fatalf("junos local interface = %q, want ge-0/0/30", got)
	}

	link := func(m map[string]topo.MapNode, from, to, lif, rif string) bool {
		want := []string{normalize.Interface(lif), normalize.Interface(rif)}
		for _, c := range m[from].Peers[to].Connections {
			if reflect.DeepEqual(c, want) {
				return true
			}
		}
		return false
	}

	m := topo.Generate([]*topo.Device{leaf, qfx}, topo.Options{})
	if !link(m, "leaf1", "qfx1", "Ethernet10", "ge-0/0/30") {
		t.Errorf("leaf1 -> qfx1 missing: %+v", m["leaf1"].Peers["qfx1"])
	}
	if !link(m, "qfx1", "leaf1", "ge-0/0/30", "Ethernet10") {
		t.Errorf("qfx1 -> leaf1 missing: %+v", m["qfx1"].Peers["leaf1"])
	}

	verbatim := *qfx
	verbatim.Neighbors = slices.Clone(qfx.Neighbors)
	verbatim.Neighbors[0].LocalInterface = "689"
	m = topo.Generate([]*topo.Device{leaf, &verbatim}, topo.Options{})
	if link(m, "leaf1", "qfx1", "Ethernet10", "ge-0/0/30") {
		t.Errorf("control: with the verbatim local port ID the link should fail validation")
	}
}
