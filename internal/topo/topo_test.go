// internal/topo/topo_test.go
// Lab scenario: lab-r1 <-> lab-spine-1 is claimed by both sides (kept),
// lab-r1 -> lab-ghost is claimed one-sided by a *discovered* peer that does
// not claim it back (dropped under validation, kept under
// TrustUnidirectional), and lab-spine-1 -> lab-leafbox is a claim toward an
// undiscovered device (kept as a leaf either way).
package topo

import "testing"

func labDevices() []*Device {
	r1 := &Device{
		Hostname: "lab-r1.lab.example", SysName: "lab-r1", IPAddress: "10.20.0.1",
		Platform: "cisco_ios",
		Neighbors: []Neighbor{
			{LocalInterface: "GigabitEthernet0/0", RemoteDevice: "lab-spine-1",
				RemoteInterface: "Ethernet49/1", Protocol: "lldp"},
			{LocalInterface: "GigabitEthernet0/1", RemoteDevice: "lab-ghost",
				RemoteInterface: "Eth1", Protocol: "lldp"},
		},
	}
	spine := &Device{
		Hostname: "lab-spine-1.lab.example", SysName: "lab-spine-1", IPAddress: "10.20.0.2",
		Platform: "arista_eos",
		Neighbors: []Neighbor{
			// reverse claim in long/short mixed forms — normalization must match
			{LocalInterface: "Et49/1", RemoteDevice: "lab-r1.lab.example",
				RemoteInterface: "Gi0/0", Protocol: "lldp"},
			{LocalInterface: "Ethernet2", RemoteDevice: "lab-leafbox",
				RemoteInterface: "eth0", Protocol: "lldp"},
		},
	}
	ghost := &Device{ // discovered, but claims nothing back toward lab-r1
		Hostname: "lab-ghost.lab.example", SysName: "lab-ghost", IPAddress: "10.20.0.9",
		Platform: "cisco_ios",
	}
	return []*Device{r1, spine, ghost}
}

func TestGenerateBidirectional(t *testing.T) {
	m := Generate(labDevices(), Options{})

	r1 := m["lab-r1"]
	if _, ok := r1.Peers["lab-spine-1"]; !ok {
		t.Fatal("bidirectional link lab-r1<->lab-spine-1 missing")
	}
	got := r1.Peers["lab-spine-1"].Connections
	if len(got) != 1 || got[0][0] != "Gi0/0" || got[0][1] != "Eth49/1" {
		t.Errorf("connection = %v, want [[Gi0/0 Eth49/1]]", got)
	}
	if _, ok := r1.Peers["lab-ghost"]; ok {
		t.Error("one-sided claim toward a discovered peer survived validation")
	}
	if _, ok := m["lab-spine-1"].Peers["lab-leafbox"]; !ok {
		t.Error("leaf claim toward undiscovered peer was dropped")
	}
}

func TestGenerateTrustUnidirectional(t *testing.T) {
	m := Generate(labDevices(), Options{TrustUnidirectional: true})
	if _, ok := m["lab-r1"].Peers["lab-ghost"]; !ok {
		t.Error("TrustUnidirectional did not keep one-sided claim")
	}
}

// FQDN-discovered device claimed by peers under its bare/site-only name
// must collapse to one node when the domain is stripped.
func TestGenerateDomainStripMerge(t *testing.T) {
	r1 := &Device{Hostname: "lab-r1.lab.example", IPAddress: "10.20.0.1", Platform: "cisco_ios",
		Neighbors: []Neighbor{{LocalInterface: "Gi0/0", RemoteDevice: "lab-spine-1", RemoteInterface: "Eth49/1", Protocol: "lldp"}}}
	spine := &Device{Hostname: "lab-spine-1.lab.example", IPAddress: "10.20.0.2", Platform: "arista_eos",
		Neighbors: []Neighbor{{LocalInterface: "Ethernet49/1", RemoteDevice: "lab-r1.lab.example", RemoteInterface: "Gi0/0", Protocol: "lldp"}}}
	m := Generate([]*Device{r1, spine}, Options{StripDomains: []string{"lab.example"}})
	if len(m) != 2 {
		t.Fatalf("nodes = %d (%v), want 2", len(m), keys(m))
	}
	if _, ok := m["lab-r1"].Peers["lab-spine-1"]; !ok {
		t.Errorf("stripped-name peer key missing: %v", m["lab-r1"].Peers)
	}
}

func keys(m map[string]MapNode) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// A device that was dialed and failed reported no neighbors, so a link toward
// it can only ever be one-sided. It stays in the map as a leaf of whoever
// reported it; bidirectional validation is for devices that answered. The
// ghost above still loses its link -- it answered and did not claim one back.
func TestGenerateKeepsLinksTowardFailedDevices(t *testing.T) {
	spine := &Device{
		Hostname: "eng-spine-1", SysName: "eng-spine-1", IPAddress: "172.16.2.2",
		Platform: "cisco_ios",
		Neighbors: []Neighbor{
			{LocalInterface: "Gi0/2", RemoteDevice: "eng-leaf-1", RemoteIP: "172.16.3.2",
				RemoteInterface: "Gi0/0", Protocol: "lldp"},
		},
	}
	leaf := &Device{Hostname: "eng-leaf-1", IPAddress: "172.16.3.2", Failed: true,
		FailedWhy: "dial: unable to authenticate"}
	m := Generate([]*Device{spine, leaf}, Options{})

	pe, ok := m["eng-spine-1"].Peers["eng-leaf-1"]
	if !ok {
		t.Fatalf("link toward the failed eng-leaf-1 was dropped; peers: %+v", m["eng-spine-1"].Peers)
	}
	if len(pe.Connections) != 1 || pe.Connections[0][0] != "Gi0/2" || pe.IP != "172.16.3.2" {
		t.Errorf("peer entry = %+v", pe)
	}
	if _, ok := m["eng-leaf-1"]; ok {
		t.Error("a failed device became a node of its own; it has nothing to say")
	}
}
