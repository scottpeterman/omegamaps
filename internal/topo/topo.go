// internal/topo/topo.go
// Device/Neighbor model and topology-map generation. The output structure is
// byte-compatible with the Python discovery engine's map.json so downstream
// viewers and seed-artifact consumers work unchanged.
//
// One deliberate departure: bidirectional link validation is IMPLEMENTED
// here (the Python original's check is a dead-code bypass that trusts every
// one-sided claim). TrustUnidirectional restores parity if wanted.
package topo

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/scottpeterman/omegamaps/internal/normalize"
)

// Neighbor is one CDP/LLDP claim as parsed from a device.
type Neighbor struct {
	LocalInterface  string `json:"local_interface"`
	RemoteDevice    string `json:"remote_device"`
	RemoteInterface string `json:"remote_interface"`
	RemoteIP        string `json:"remote_ip,omitempty"`
	RemotePlatform  string `json:"remote_platform,omitempty"`
	RemoteDescr     string `json:"remote_description,omitempty"`
	Capabilities    string `json:"remote_capabilities,omitempty"`
	Protocol        string `json:"protocol"` // "cdp" | "lldp"
}

// Device is one crawled node.
type Device struct {
	Hostname  string     `json:"hostname"`           // name we connected by
	SysName   string     `json:"sys_name,omitempty"` // name the device reports
	IPAddress string     `json:"ip_address,omitempty"`
	Platform  string     `json:"platform,omitempty"` // fingerprint result
	Version   string     `json:"version,omitempty"`
	Neighbors []Neighbor `json:"neighbors"`
	Depth     int        `json:"depth"`
	Failed    bool       `json:"failed,omitempty"`
	FailedWhy string     `json:"failure_reason,omitempty"`
}

// Canonical returns the map key for a device: sys_name > hostname > ip.
func (d *Device) Canonical() string {
	if d.SysName != "" {
		return d.SysName
	}
	if d.Hostname != "" {
		return d.Hostname
	}
	return d.IPAddress
}

// map.json shapes (match the Python generator's output).
type PeerEntry struct {
	IP          string     `json:"ip"`
	Platform    string     `json:"platform"`
	Connections [][]string `json:"connections"`
}

type NodeDetails struct {
	IP       string `json:"ip"`
	Platform string `json:"platform"`
}

type MapNode struct {
	NodeDetails NodeDetails          `json:"node_details"`
	Peers       map[string]PeerEntry `json:"peers"`
}

type Options struct {
	// StripDomains are domain suffixes removed from every identity before
	// matching and from node/peer keys, so "usa-spine-1" and
	// "usa-spine-1.lab.local" merge into one node. Site labels beneath
	// the suffix survive.
	StripDomains []string

	// TrustUnidirectional accepts every one-sided claim (Python-parity
	// behavior). Default false: a link between two *discovered* devices
	// must be claimed by both sides; claims toward undiscovered peers
	// (leaves) are trusted as the only evidence available.
	TrustUnidirectional bool
}

type claim struct {
	peer     string // canonical
	remoteIf string // normalized
	n        Neighbor
}

// Generate builds the topology map from crawled devices.
func Generate(devices []*Device, opt Options) map[string]MapNode {
	// canon: identity normalization with domain stripping applied.
	canon := func(id string) string {
		return normalize.StripSuffixes(id, opt.StripDomains)
	}

	// identity lookup: any known name/ip (stripped and short forms) -> device
	info := map[string]*Device{}
	// known is every device in the run; discovered is the ones that
	// answered. The difference is what bidirectional validation may ask of
	// a peer: a device that failed never reported a neighbor list, so a link
	// toward it can only ever be one-sided. Treating it as discovered made
	// every such link fail validation, and the failed device dropped out of
	// the map altogether -- the device you could not log into being exactly
	// the one the map most needs to show. It is kept as a leaf instead, the
	// way an undialed neighbor is.
	known := map[string]struct{}{}
	discovered := map[string]struct{}{}
	for _, d := range devices {
		for _, id := range []string{d.Hostname, d.SysName, d.IPAddress} {
			if id == "" {
				continue
			}
			info[canon(id)] = d
			known[canon(id)] = struct{}{}
			known[normalize.ShortName(id)] = struct{}{}
			if !d.Failed {
				discovered[canon(id)] = struct{}{}
				discovered[normalize.ShortName(id)] = struct{}{}
			}
		}
	}

	canonOf := func(name string) string {
		if d, ok := info[canon(name)]; ok {
			return canon(d.Canonical())
		}
		if d, ok := info[normalize.ShortName(name)]; ok {
			return canon(d.Canonical())
		}
		return canon(name)
	}
	wasDiscovered := func(name string) bool {
		if _, ok := discovered[canon(name)]; ok {
			return true
		}
		_, ok := discovered[normalize.ShortName(name)]
		return ok
	}

	// namesADevice reports whether s is the name of something already in this
	// map. A port description is very often just the far-end hostname, which
	// is shaped like an interface name and so invisible to interfaceLike.
	//
	// Equality only, never containment: a device named after a number would
	// match half the interfaces on the network, and losing a real interface
	// label is worse than keeping a wrong one.
	namesADevice := func(s string) bool {
		if s == "" {
			return false
		}
		if _, ok := known[canon(s)]; ok {
			return true
		}
		_, ok := known[normalize.ShortName(s)]
		return ok
	}

	// pass 1: collect claims keyed on (canonical device, normalized local if)
	allClaims := map[[2]string][]claim{}
	for _, d := range devices {
		dc := canon(d.Canonical())
		if dc == "" {
			continue
		}
		for _, n := range d.Neighbors {
			if normalize.IsArtifactName(n.RemoteDevice) {
				continue
			}
			lif := normalize.Interface(n.LocalInterface)
			rif := normalize.Interface(n.RemoteInterface)
			if lif == "" || rif == "" {
				continue
			}
			key := [2]string{dc, lif}
			allClaims[key] = append(allClaims[key], claim{canonOf(n.RemoteDevice), rif, n})
		}
	}

	hasReverse := func(dc, lif, peer, rif string) bool {
		for _, c := range allClaims[[2]string{peer, rif}] {
			if c.peer == dc && c.remoteIf == lif {
				return true
			}
			// peer may claim us by an alternate identifier
			if d, ok := info[canon(dc)]; ok {
				for _, alt := range []string{d.Hostname, d.SysName, d.IPAddress} {
					if alt != "" && canon(c.peer) == canon(alt) && c.remoteIf == lif {
						return true
					}
				}
			}
		}
		return false
	}

	// pass 2: emit
	out := map[string]MapNode{}
	for _, d := range devices {
		dc := canon(d.Canonical())
		if dc == "" || d.Failed {
			continue
		}
		node := MapNode{
			NodeDetails: NodeDetails{IP: d.IPAddress, Platform: d.Platform},
			Peers:       map[string]PeerEntry{},
		}
		for key, claims := range allClaims {
			if key[0] != dc {
				continue
			}
			lif := key[1]
			for _, c := range claims {
				accept := opt.TrustUnidirectional ||
					!wasDiscovered(c.peer) || // leaf: one-sided is all we get
					hasReverse(dc, lif, c.peer, c.remoteIf)
				if !accept {
					continue
				}
				pe := node.Peers[c.peer]
				if pe.Connections == nil {
					pe = PeerEntry{IP: c.n.RemoteIP, Platform: c.n.RemotePlatform}
				}
				conn := []string{lif, c.remoteIf}
				dup := false
				for _, ex := range pe.Connections {
					if ex[0] == conn[0] && ex[1] == conn[1] {
						dup = true
						break
					}
				}
				if !dup {
					pe.Connections = append(pe.Connections, conn)
				}
				node.Peers[c.peer] = pe
			}
		}
		for p := range node.Peers {
			pe := node.Peers[p]
			pe.Connections = dropDescriptionClaims(pe.Connections, namesADevice)
			sort.Slice(pe.Connections, func(i, j int) bool {
				if pe.Connections[i][0] != pe.Connections[j][0] {
					return pe.Connections[i][0] < pe.Connections[j][0]
				}
				return pe.Connections[i][1] < pe.Connections[j][1]
			})
			node.Peers[p] = pe
		}
		out[dc] = node
	}
	return out
}

// MarshalMap renders the topology with stable formatting.
func MarshalMap(m map[string]MapNode) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}

// dropDescriptionClaims removes connections whose remote side is not a remote
// interface at all.
//
// CDP and LLDP frequently both see the same adjacency, and when they disagree
// it is usually because one of them puts the remote port's DESCRIPTION where
// the other puts its port ID. One physical link then arrives as two
// connections that share a local interface:
//
//	["Gi0/0", "Eth3"]
//	["Gi0/0", "To eng-leaf-1"]
//
// The pair-level dedup in Generate cannot see this — the two entries differ.
//
// Two signals mark the useless half, because neither catches the other's case.
// SHAPE: port names on every platform this crawls are one unbroken token
// containing a digit (Gi0/0, xe-0/0/1, Po12), while a description is written
// for people and has spaces in it. NAME: a description is very often just the
// far-end hostname, which is shaped exactly like nothing and passes the first
// test, but is recognisable because the map already knows every device in it.
//
// Dropping the last connection on a link does NOT drop the link. The peer entry
// is written independently of its connections, so the edge still renders — it
// loses its interface label, which is the right trade against labelling it
// wrongly.
func dropDescriptionClaims(conns [][]string, namesADevice func(string) bool) [][]string {
	out := conns[:0:0]
	for _, c := range conns {
		if len(c) < 2 || !interfaceLike(c[1]) || namesADevice(c[1]) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// interfaceLike reports whether s reads as a port name rather than as prose.
//
// The test is shape, not a vendor table: port names across every platform this
// crawls are one unbroken token containing a digit (Gi0/0, Eth3, xe-0/0/1,
// TenGigE0/0/0/3, Po12). Descriptions are written for people and contain
// spaces. An empty remote fails too — it names nothing.
func interfaceLike(s string) bool {
	if s == "" || strings.ContainsAny(s, " \t") {
		return false
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}
