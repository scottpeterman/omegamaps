// internal/crawler/crawler_test.go
// parseStep tested against lab-named output in the exact shapes the
// validated templates expect (command-scoped: no echo, no trailing prompt).
package crawler

import (
	"fmt"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/topo"
)

const labEOSDetail = `Interface Ethernet1 detected 1 LLDP neighbors:

  Neighbor 0011.2233.4455 age 10 seconds
  Discovered 1 day, 00:00:00 ago; Last changed 1 day ago
  - Chassis ID type: MAC address (4)
    Chassis ID     : 0011.2233.4455
  - Port ID type: Interface name(5)
    Port ID     : "Ethernet49/1"
  - System Name: "lab-r1.lab.example"
  - System Description: "Lab NOS 4.20"
  Management Address        : 10.20.0.1
`

const labJunosTerse = `Local Interface    Parent Interface    Chassis Id          Port info          System Name
xe-0/0/0           -                   00:11:22:33:44:55   xe-1/2/3           lab-core-1
`

func TestParseStepEOS(t *testing.T) {
	ns, err := parseStep("arista_eos",
		step{Key: "lldp_detail", Protocol: "lldp", EdgeSource: true}, labEOSDetail)
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 {
		t.Fatalf("records = %d, want 1", len(ns))
	}
	n := ns[0]
	if n.LocalInterface != "Ethernet1" || n.RemoteDevice != "lab-r1.lab.example" ||
		n.RemoteInterface != "Ethernet49/1" || n.RemoteIP != "10.20.0.1" {
		t.Errorf("unexpected neighbor: %+v", n)
	}
}

func TestParseStepJunosTerse(t *testing.T) {
	ns, err := parseStep("juniper_junos",
		step{Key: "lldp", Protocol: "lldp", EdgeSource: true}, labJunosTerse)
	if err != nil {
		t.Fatal(err)
	}
	if len(ns) != 1 || ns[0].RemoteDevice != "lab-core-1" || ns[0].LocalInterface != "xe-0/0/0" {
		t.Fatalf("unexpected: %+v", ns)
	}
}

func TestNextTargetMACFallback(t *testing.T) {
	log := func(string, ...any) {}
	if tgt, addr, ok := nextTarget(topoNeighbor("aa:bb:cc:dd:ee:ff", "10.20.0.7"), log); !ok ||
		tgt != "10.20.0.7" || addr != "10.20.0.7" {
		t.Errorf("MAC neighbor with IP: got %q %q %v", tgt, addr, ok)
	}
	if _, _, ok := nextTarget(topoNeighbor("aa:bb:cc:dd:ee:ff", ""), log); ok {
		t.Error("MAC neighbor without IP should be skipped")
	}
	if tgt, addr, ok := nextTarget(topoNeighbor("lab-core-1", ""), log); !ok ||
		tgt != "lab-core-1" || addr != "" {
		t.Errorf("named neighbor: got %q %q %v", tgt, addr, ok)
	}
	// The regression this guards: a named neighbor that also reported a
	// management address must carry that address forward, not drop it.
	if tgt, addr, ok := nextTarget(topoNeighbor("lab-core-1", "10.20.0.7"), log); !ok ||
		tgt != "lab-core-1" || addr != "10.20.0.7" {
		t.Errorf("named neighbor with IP: got %q %q %v", tgt, addr, ok)
	}
}

// topoNeighbor is a tiny fixture helper.
func topoNeighbor(name, ip string) (n topo.Neighbor) {
	n.RemoteDevice = name
	n.RemoteIP = ip
	return
}

func TestDialAllowedDomainCompletion(t *testing.T) {
	c := New(Config{
		AllowDomains: []string{"lab.example"},
		Domains:      []string{"lab.example"},
	})
	c.resolver = stubResolver{forward: map[string][]string{
		"spine-1.usa.lab.example": {"10.20.0.3"},
	}}
	if !c.dialAllowed("lab-r1.lab.example") {
		t.Error("as-is FQDN under allowed domain rejected")
	}
	if !c.dialAllowed("spine-1.usa") {
		t.Error("site-only name completable+resolvable via -domain rejected")
	}
	if c.dialAllowed("peer-rtr1.net.thirdparty.example") {
		t.Error("third-party FQDN allowed (completion does not resolve)")
	}
	if c.dialAllowed("10.20.0.9") {
		t.Error("bare IP allowed with allowlist active")
	}
}

var errFake = fmt.Errorf("no such host")
