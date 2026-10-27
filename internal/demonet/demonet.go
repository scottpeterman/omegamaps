// internal/demonet/demonet.go
//
// A company-free demo network, crawled for real.
//
// For README screenshots and demos that must not show anybody's production
// network. The network is synthetic -- two datacenters, an internet edge and
// a set of branches on example.net, about 150 nodes with the leaves -- but
// the crawl is not: Run drives the real crawler (internal/crawler) with only
// the per-device collection replaced, through the SNMPFunc seam the crawler
// already has. Discovery order, depths, claims, exclusions, failures, the
// event stream, the recording and the map all come from the same code a real
// crawl runs. What a demo shows is what the product does.
//
// The idea and the network's shape are carried over from securecartography2's
// tools/demo_screenshots.py.
//
// Every device answers with what a real one would say over SNMP: a sysDescr,
// a sysName, and LLDP/CDP neighbours with their management addresses and the
// platform text the neighbour advertises. The display platform in the map is
// then derived from that sysDescr by normalize.Describe, the same as for a
// real device, so the demo exercises the icon tables end to end.
package demonet

import (
	"context"
	"fmt"
	"hash/fnv"
	"math/rand"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/scottpeterman/omegamaps/internal/crawldial"
	"github.com/scottpeterman/omegamaps/internal/crawler"
	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// Kind is one device type: what it says about itself and how its ports are
// named.
type Kind struct {
	SysDescr string // what SNMP sysDescr returns
	Platform string // what it advertises to neighbours over CDP/LLDP
	IfName   string // port name pattern, %d for the port number
	Protocol string // "cdp" or "lldp", as its neighbours hear it
}

// Kinds, written in the form real devices give them. Endpoints at the bottom
// are never dialed: they appear as neighbours only.
var Kinds = map[string]Kind{
	"core": {"Juniper Networks, Inc. mx10003 internet router, kernel JUNOS 23.4R2-S3, Build date: 2024-05-10 08:12:44 UTC Copyright (c) 1996-2024 Juniper Networks, Inc.",
		"Juniper Networks, Inc. mx10003 internet router", "et-0/0/%d", "lldp"},
	"wan": {"Juniper Networks, Inc. mx204 internet router, kernel JUNOS 22.4R3-S2, Build date: 2024-02-02 11:40:01 UTC Copyright (c) 1996-2024 Juniper Networks, Inc.",
		"Juniper Networks, Inc. mx204 internet router", "et-0/0/%d", "lldp"},
	"edge": {"Cisco IOS Software [Cupertino], ASR1000 Software (X86_64_LINUX_IOSD-UNIVERSALK9-M), Version 17.9.4a, RELEASE SOFTWARE (fc3)",
		"cisco ASR1001-X", "TenGigabitEthernet0/0/%d", "cdp"},
	"fw": {"Palo Alto Networks PA-3220 series firewall",
		"Palo Alto Networks PA-3220", "ethernet1/%d", "lldp"},
	"spine": {"Arista Networks EOS version 4.31.2F running on an Arista Networks DCS-7280SR3-48YC8",
		"Arista Networks EOS version 4.31.2F running on an Arista Networks DCS-7280SR3-48YC8", "Ethernet%d/1", "lldp"},
	"leaf": {"Arista Networks EOS version 4.30.4M running on an Arista Networks DCS-7050SX3-48YC8",
		"Arista Networks EOS version 4.30.4M running on an Arista Networks DCS-7050SX3-48YC8", "Ethernet%d", "lldp"},
	"access": {"Cisco IOS Software, C3750E Software (C3750E-UNIVERSALK9-M), Version 15.2(4)E10, RELEASE SOFTWARE (fc2)",
		"cisco WS-C3750X-48P", "GigabitEthernet1/0/%d", "cdp"},
	"oob": {"Cisco IOS Software, C2960X Software (C2960X-UNIVERSALK9-M), Version 15.2(7)E7, RELEASE SOFTWARE (fc1)",
		"cisco WS-C2960X-24TS-L", "GigabitEthernet1/0/%d", "cdp"},
	"nxcore": {"Cisco NX-OS(tm) n9000, Software (n9000-dk9), Version 10.3(4a), RELEASE SOFTWARE Copyright (c) 2002-2023 by Cisco Systems, Inc.",
		"N9K-C9364C", "Ethernet1/%d", "cdp"},
	"nxleaf": {"Cisco NX-OS(tm) n9000, Software (n9000-dk9), Version 10.3(4a), RELEASE SOFTWARE Copyright (c) 2002-2023 by Cisco Systems, Inc.",
		"N9K-C93180YC-FX", "Ethernet1/%d", "cdp"},
	"branch": {"Cisco IOS Software [Cupertino], ISR Software (X86_64_LINUX_IOSD-UNIVERSALK9-M), Version 17.9.4a, RELEASE SOFTWARE (fc3)",
		"cisco ISR4451-X/K9", "GigabitEthernet0/0/%d", "cdp"},
	"brsw": {"Cisco IOS Software, C2960X Software (C2960X-UNIVERSALK9-M), Version 15.2(7)E7, RELEASE SOFTWARE (fc1)",
		"cisco WS-C2960X-48FPD-L", "GigabitEthernet1/0/%d", "cdp"},

	// Never dialed: excluded by platform, or simply endpoints.
	"server": {"", "Ubuntu 22.04.4 LTS Linux 5.15.0-105-generic x86_64", "eno%d", "lldp"},
	"ap":     {"", "cisco AIR-AP2802I-B-K9", "GigabitEthernet%d", "cdp"},
	"phone":  {"", "Cisco IP Phone 8851", "Port %d", "cdp"},
}

// Device is one node of the synthetic network.
type Device struct {
	Name string
	Kind string
	IP   string
	// Fail, when set, is why collection fails: the device is claimed and
	// dialed like any other and ends up failed on the map.
	Fail string
}

// Network is the whole synthetic network.
type Network struct {
	Domain  string
	Devices map[string]*Device
	links   [][2]string // one entry per physical link; parallel members repeat
	ports   map[string]int
	// adj is filled by wire: device -> its neighbour claims as a collector
	// would read them off that device.
	adj map[string][]topo.Neighbor
}

func (n *Network) add(name, kind, ip, fail string) {
	n.Devices[name] = &Device{Name: name, Kind: kind, IP: ip, Fail: fail}
}

func (n *Network) link(a, b string, members int) {
	for i := 0; i < members; i++ {
		n.links = append(n.links, [2]string{a, b})
	}
}

// Seed is where the crawl starts.
const Seed = "core1.dc1"

// Build returns the demo network. ~80 devices are dialable; the rest are
// servers, access points and phones that appear as neighbours.
func Build() *Network {
	n := &Network{Domain: "example.net", Devices: map[string]*Device{}, ports: map[string]int{}}

	// ---- DC1: core / WAN / firewall / internet edge / spines / leaves / access
	for i := 1; i <= 2; i++ {
		n.add(fmt.Sprintf("core%d.dc1", i), "core", fmt.Sprintf("10.1.0.%d", i), "")
	}
	n.link("core1.dc1", "core2.dc1", 4)
	n.add("wan1.dc1", "wan", "10.1.0.10", "")
	n.add("fw1.dc1", "fw", "10.1.0.20", "")
	n.add("inet1.dc1", "edge", "10.1.0.31", "")
	for _, c := range []string{"core1.dc1", "core2.dc1"} {
		n.link(c, "wan1.dc1", 2)
		n.link(c, "fw1.dc1", 2)
	}
	n.link("wan1.dc1", "inet1.dc1", 2)
	for s := 1; s <= 4; s++ {
		spine := fmt.Sprintf("spine%d.dc1", s)
		n.add(spine, "spine", fmt.Sprintf("10.1.1.%d", s), "")
		n.link("core1.dc1", spine, 2)
		n.link("core2.dc1", spine, 2)
	}
	for l := 1; l <= 16; l++ {
		leaf := fmt.Sprintf("leaf%d.dc1", l)
		fail := ""
		if l == 11 {
			fail = "SNMP timeout: no response from 10.1.2.11"
		}
		n.add(leaf, "leaf", fmt.Sprintf("10.1.2.%d", l), fail)
		n.link(fmt.Sprintf("spine%d.dc1", 1+(l-1)%2), leaf, 1)
		n.link(fmt.Sprintf("spine%d.dc1", 3+(l-1)%2), leaf, 1)
		for srv := 1; srv <= 2; srv++ {
			name := fmt.Sprintf("app%02d%d.dc1", l, srv)
			n.add(name, "server", fmt.Sprintf("10.1.20.%d", l*2+srv), "")
			n.link(leaf, name, 1)
		}
	}
	for a := 1; a <= 12; a++ {
		acc := fmt.Sprintf("access%d.dc1", a)
		fail := ""
		if a == 10 {
			fail = "SNMP: no credential answered (2 tried)"
		}
		n.add(acc, "access", fmt.Sprintf("10.1.3.%d", a), fail)
		n.link(fmt.Sprintf("leaf%d.dc1", a), acc, 1)
		ap := fmt.Sprintf("ap%d.dc1", a)
		n.add(ap, "ap", fmt.Sprintf("10.1.30.%d", a), "")
		n.link(acc, ap, 1)
	}
	for o := 1; o <= 2; o++ {
		oob := fmt.Sprintf("oob%d.dc1", o)
		n.add(oob, "oob", fmt.Sprintf("10.1.9.%d", o), "")
		n.link(fmt.Sprintf("core%d.dc1", o), oob, 1)
	}

	// ---- DC2 over the WAN: WAN edge / NX-OS core / leaves / access
	n.add("wan1.dc2", "wan", "10.2.0.10", "")
	n.add("inet2.dc2", "edge", "10.2.0.31", "")
	n.link("wan1.dc1", "wan1.dc2", 2)
	n.link("wan1.dc2", "inet2.dc2", 2)
	for i := 1; i <= 2; i++ {
		core := fmt.Sprintf("core%d.dc2", i)
		n.add(core, "nxcore", fmt.Sprintf("10.2.0.%d", i), "")
		n.link("wan1.dc2", core, 2)
	}
	n.link("core1.dc2", "core2.dc2", 4)
	for l := 1; l <= 8; l++ {
		leaf := fmt.Sprintf("leaf%d.dc2", l)
		fail := ""
		if l == 6 {
			fail = "SNMP timeout: no response from 10.2.2.6"
		}
		n.add(leaf, "nxleaf", fmt.Sprintf("10.2.2.%d", l), fail)
		n.link("core1.dc2", leaf, 1)
		n.link("core2.dc2", leaf, 1)
		db := fmt.Sprintf("db%d.dc2", l)
		n.add(db, "server", fmt.Sprintf("10.2.20.%d", l), "")
		n.link(leaf, db, 1)
	}
	for a := 1; a <= 8; a++ {
		acc := fmt.Sprintf("access%d.dc2", a)
		n.add(acc, "brsw", fmt.Sprintf("10.2.3.%d", a), "")
		n.link(fmt.Sprintf("leaf%d.dc2", a), acc, 1)
		ap := fmt.Sprintf("ap%d.dc2", a)
		n.add(ap, "ap", fmt.Sprintf("10.2.30.%d", a), "")
		n.link(acc, ap, 1)
	}

	// ---- Branches, dual-homed to both WAN routers
	for b := 1; b <= 10; b++ {
		rtr := fmt.Sprintf("rtr1.br%d", b)
		sw := fmt.Sprintf("sw1.br%d", b)
		n.add(rtr, "branch", fmt.Sprintf("10.10.%d.1", b), "")
		n.add(sw, "brsw", fmt.Sprintf("10.10.%d.2", b), "")
		n.link("wan1.dc1", rtr, 1)
		n.link("wan1.dc2", rtr, 1)
		n.link(rtr, sw, 1)
		ap := fmt.Sprintf("ap1.br%d", b)
		ph := fmt.Sprintf("sep00a1b2c3d4%02x", b)
		n.add(ap, "ap", fmt.Sprintf("10.10.%d.30", b), "")
		n.add(ph, "phone", fmt.Sprintf("10.10.%d.50", b), "")
		n.link(sw, ap, 1)
		n.link(sw, ph, 1)
	}

	n.wire()
	return n
}

func (n *Network) port(dev string) string {
	n.ports[dev]++
	return fmt.Sprintf(Kinds[n.Devices[dev].Kind].IfName, n.ports[dev])
}

// wire turns the link list into the neighbour tables each device reports.
// Links are wired in a fixed order so port numbers are stable run to run.
func (n *Network) wire() {
	n.adj = map[string][]topo.Neighbor{}
	for _, l := range n.links {
		a, b := n.Devices[l[0]], n.Devices[l[1]]
		pa, pb := n.port(a.Name), n.port(b.Name)
		n.adj[a.Name] = append(n.adj[a.Name], n.claim(pa, b, pb))
		n.adj[b.Name] = append(n.adj[b.Name], n.claim(pb, a, pa))
	}
}

// claim is what dev's neighbour table says about peer, the way snmpprobe
// fills it: CDP carries the platform, LLDP a description the platform is
// derived from.
func (n *Network) claim(localIf string, peer *Device, peerIf string) topo.Neighbor {
	k := Kinds[peer.Kind]
	nb := topo.Neighbor{
		LocalInterface: localIf,
		// Short names, as most LLDP sysNames are. A device's identity is
		// then the same string in the crawl and in the map, which is what
		// the preview matches the finished map against.
		RemoteDevice:    peer.Name,
		RemoteInterface: peerIf,
		RemoteIP:        peer.IP,
		Protocol:        k.Protocol,
	}
	if k.Protocol == "cdp" {
		nb.RemotePlatform = k.Platform
	} else {
		nb.RemoteDescr = k.Platform
		nb.RemotePlatform = normalize.PlatformFromDescription(k.Platform)
		if nb.RemotePlatform == "" {
			nb.RemotePlatform = k.Platform
		}
	}
	return nb
}

// Count reports dialable devices and endpoints, for a summary line.
func (n *Network) Count() (dialable, endpoints int) {
	for _, d := range n.Devices {
		if Kinds[d.Kind].SysDescr == "" {
			endpoints++
		} else {
			dialable++
		}
	}
	return dialable, endpoints
}

// lookup finds a device by any name the crawler may dial it under: the
// claimed FQDN, the short name, or its address.
func (n *Network) lookup(target string) *Device {
	t := strings.TrimSuffix(strings.ToLower(target), "."+n.Domain)
	if d, ok := n.Devices[t]; ok {
		return d
	}
	for _, d := range n.Devices {
		if d.IP == target {
			return d
		}
	}
	return nil
}

// Options shapes the simulated collection.
type Options struct {
	// Speed scales the per-device collection time: 1 takes about as long as
	// real SNMP collection on a healthy network (0.4-2.5 s a device), 0.2 is
	// five times quicker. Zero answers instantly, which tests want.
	Speed float64
}

// SNMPFunc returns a crawler.SNMPFunc that answers for this network. Timing
// is derived from the device name, so the same network crawled twice takes
// the same shape of time.
func (n *Network) SNMPFunc(opt Options) crawler.SNMPFunc {
	return func(ctx context.Context, t crawler.DialTarget) (*topo.Device, error) {
		d := n.lookup(t.Target)
		wait := func(base time.Duration) error {
			if opt.Speed <= 0 {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(float64(base) * opt.Speed)):
				return nil
			}
		}
		if d == nil || Kinds[d.Kind].SysDescr == "" {
			_ = wait(3 * time.Second)
			return &topo.Device{Hostname: t.Target, Failed: true,
				FailedWhy: "SNMP timeout: no response from " + t.Target}, nil
		}
		if err := wait(collectTime(d.Name)); err != nil {
			return nil, err
		}
		if d.Fail != "" {
			return &topo.Device{Hostname: t.Target, IPAddress: d.IP, Failed: true, FailedWhy: d.Fail}, nil
		}
		k := Kinds[d.Kind]
		host := t.Target
		if _, err := netip.ParseAddr(host); err == nil {
			// As snmpprobe does: reached by address, the device is labelled
			// by its own name, and the crawler reports the rename.
			host = d.Name
		}
		dev := &topo.Device{
			Hostname:  host,
			SysName:   d.Name,
			IPAddress: d.IP,
			Platform:  normalize.PlatformFromDescription(k.SysDescr),
			Version:   k.SysDescr,
		}
		dev.Neighbors = append(dev.Neighbors, n.adj[d.Name]...)
		return dev, nil
	}
}

// collectTime is 0.4-2.5 s, fixed per device name.
func collectTime(name string) time.Duration {
	h := fnv.New64a()
	_, _ = h.Write([]byte(name))
	r := rand.New(rand.NewSource(int64(h.Sum64())))
	return 400*time.Millisecond + time.Duration(r.Int63n(int64(2100*time.Millisecond)))
}

// Names returns every device name, sorted.
func (n *Network) Names() []string {
	out := make([]string, 0, len(n.Devices))
	for k := range n.Devices {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// CrawlOptions shapes a demo crawl.
type CrawlOptions struct {
	Options
	// Tap, when set, receives every event the run records -- a
	// crawlrun.EventWriter's Write, for a recording.
	Tap func(crawlrun.Event)
	Log crawler.Logf
}

// Exclude is the exclusion list the demo runs with: servers and phones are
// never dialed, the way a real crawl would be set up.
var Exclude = []string{"linux", "phone", "AIR-AP"}

// Crawl runs the real crawler over the network from Seed and returns what it
// collected, with the run that tracked it. Collection is SNMP only, as the
// network only answers SNMP.
func (n *Network) Crawl(ctx context.Context, opt CrawlOptions) ([]*topo.Device, *crawlrun.Run) {
	run := crawlrun.New()
	if opt.Tap != nil {
		run.Tap(opt.Tap)
	}
	c := crawler.New(crawler.Config{
		SNMP:               n.SNMPFunc(opt.Options),
		Methods:            []crawlrun.Method{crawlrun.MethodSNMP},
		Emit:               run.Emit(),
		MaxDepth:           7,
		Concurrency:        10,
		ExcludePatterns:    Exclude,
		DisableNeighborDNS: true, // every claim carries its address; nothing to look up
		Log:                opt.Log,
	})
	devices := c.CrawlContext(ctx, []string{n.Devices[Seed].IP})
	run.Finish()
	return devices, run
}

// Map is the map.json a crawl of this network produces, through the same
// helper the CLI and the window use.
func (n *Network) Map(devices []*topo.Device) ([]byte, error) {
	return topo.MarshalMap(topo.Generate(devices, crawldial.MapOptions(crawlrun.Params{
		Domains: []string{n.Domain},
	})))
}
