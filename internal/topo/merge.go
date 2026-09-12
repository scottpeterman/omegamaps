// internal/topo/merge.go
//
// Merging two topology maps into one.
//
// A crawl stops where it cannot go: an ACL, a credential that does not work on
// the far side, a management network that does not route. The way through is to
// crawl again from a seed on the other side of the wall, and that produces two
// maps of one estate rather than one. They overlap — the devices near the seam
// are in both — so pasting them together produces a map where half the core
// appears twice and every edge across the seam is drawn to the wrong copy.
//
// This file is the fold. It is deliberately separate from Generate: Generate
// turns crawled devices into a map, and nothing about it knows or cares that a
// map might later be merged with another. A merge takes two finished maps,
// which is what somebody actually has on disk a week apart.
//
// # The hard part is the peer keys, not the nodes
//
// Recognising that "core-1" and "core-1.lab.example" are one device is the
// obvious half and it is easy. The half that breaks a map is that both maps
// also REFER to that device from their peers, under whichever name their own
// crawl saw. Merge the nodes and stop there, and the merged map has one node
// called core-1 and a dozen edges pointing at a core-1.lab.example that no
// longer exists — the viewer draws a phantom node with no details and the real
// one loses its links. So every peer key goes through the same resolution the
// node keys do.
//
// # Matching is layered, and the risky layer is optional
//
// Three rules, applied in order of how much they can be trusted:
//
//  1. Canonical name. The identifier with StripDomains applied, which is the
//     same normalization Generate uses. Two maps crawled with the same domain
//     list agree here without any guessing.
//  2. Management IP. Two nodes with the same non-empty node_details.ip are the
//     same box even when one crawl reached it by name and the other by address.
//  3. Short name — the first label, so "core-1.site-a" matches "core-1". This
//     is the one that is usually right and occasionally catastrophic, because
//     core-1.site-a and core-1.site-b are two devices in two buildings and
//     collapsing them fuses two sites into one. It is a flag.
//
// # Ambiguity is refused rather than guessed
//
// If an incoming node matches more than one node in the base — two site-a and
// site-b cores against a bare core-1 — nothing is merged. The node is kept
// separate under its own name and named in the report. A merge that silently
// picks one of two candidates produces a map that is wrong in a way nobody can
// see; a merge that says "these two were ambiguous, look at them" produces a
// map that is honest and a short list of things to check.
//
// # Nothing is lost, and disagreements are reported
//
// Where the two maps disagree about a node's IP or platform, one value is kept
// and the other is recorded in the report rather than dropped silently. That
// disagreement is usually interesting on its own: it means one of the crawls
// saw a device differently, which is worth knowing before it is averaged away.
package topo

import (
	"sort"
	"strings"

	"github.com/scottpeterman/omegamaps/internal/normalize"
)

// MergeOptions controls how aggressively two maps are folded together.
type MergeOptions struct {
	// StripDomains are the suffixes removed before matching, exactly as in
	// Options.StripDomains. Pass the same list both crawls were run with.
	StripDomains []string

	// MatchShortName collapses names to their first label before matching,
	// so "core-1.lab.example" and "core-1" are one device even when the
	// suffix was not in StripDomains.
	//
	// This is the case that motivates a merge at all — one crawl learns a
	// device by FQDN and the other by the name the far side reported — and
	// it is also the case that fuses core-1.site-a with core-1.site-b.
	// Ambiguity is refused rather than guessed, which makes the flag safe
	// to leave on for an estate with unique short names and safe to turn
	// off for one where a site label is load-bearing.
	MatchShortName bool

	// MatchIP treats a shared, non-empty node_details.ip as proof of
	// identity. Off by default only because a map full of empty or
	// duplicated IPs — a placeholder written into every entry, a NAT
	// address reused per site — would merge everything into one node.
	MatchIP bool
}

// MergeConflict is one field two maps disagreed about.
type MergeConflict struct {
	Node    string // key in the merged map
	Field   string // "ip" or "platform"
	Kept    string // the base map's value
	Dropped string // what the incoming map said
}

// MergeReport is what a merge did, in the terms someone checking it thinks in.
//
// It is returned rather than logged because the interesting question after a
// merge is not "did it work" but "which nodes did it decide were the same" —
// that is the judgment call, and it is the thing to eyeball.
type MergeReport struct {
	// Nodes is how many nodes are in the merged map.
	Nodes int

	// Added came only from the incoming map. Merged matched a node that
	// was already there.
	Added  int
	Merged int

	// Aliases maps each incoming node key that was folded into an existing
	// node onto the key it ended up under. This is the list to read when a
	// merged map looks wrong.
	Aliases map[string]string

	// Ambiguous are incoming keys that matched more than one base node.
	// They were NOT merged; each is in the map under its own name.
	Ambiguous []string

	// Conflicts are fields the two maps disagreed about.
	Conflicts []MergeConflict

	// EdgesAdded counts peer entries the incoming map contributed that the
	// base did not have — the actual yield of the second crawl.
	EdgesAdded int
}

// Merge folds incoming into base and returns a new map. Neither input is
// modified.
//
// The base map's names win. A merge is "fold B into A", the caller chose which
// is which, and a rule that sometimes preferred the longer name would mean the
// merged map's keys depended on which crawl happened to use an FQDN.
func Merge(base, incoming map[string]MapNode, opt MergeOptions) (map[string]MapNode, MergeReport) {
	rep := MergeReport{Aliases: map[string]string{}}

	out := make(map[string]MapNode, len(base)+len(incoming))
	for k, v := range base {
		out[k] = copyNode(v)
	}

	idx := newMergeIndex(base, opt)

	// Resolve every incoming node key first, before merging anything. The
	// peer rewriting below needs the whole mapping — an incoming node's
	// peers routinely name other incoming nodes, and those have to land on
	// their merged keys too.
	resolved := make(map[string]string, len(incoming))
	incomingKeys := sortedKeys(incoming)
	for _, k := range incomingKeys {
		target, ambiguous := idx.lookup(k, incoming[k].NodeDetails.IP)
		switch {
		case ambiguous:
			rep.Ambiguous = append(rep.Ambiguous, k)
			resolved[k] = freeKey(out, k)
		case target != "":
			resolved[k] = target
			rep.Aliases[k] = target
		default:
			resolved[k] = freeKey(out, k)
		}
		// A placeholder reserves the key so a later ambiguous node cannot
		// collide with it. It is deliberately NOT added to the matching
		// index: nodes from one map are already distinct devices — that
		// map's own generator settled that — and letting them match each
		// other is how core-1.site-a and core-1.site-b fuse on the way in.
		// Only the base map is matchable.
		if _, exists := out[resolved[k]]; !exists {
			out[resolved[k]] = MapNode{Peers: map[string]PeerEntry{}}
		}
	}

	// resolveName maps ANY name — node key or peer key — onto its key in
	// the merged map. A peer that neither map has as a node still gets
	// normalized, so a leaf seen as "sw-4" by one crawl and "sw-4.lab" by
	// the other is one leaf.
	resolveName := func(name string) string {
		if to, ok := resolved[name]; ok {
			return to
		}
		if to, _ := idx.lookup(name, ""); to != "" {
			return to
		}
		return name
	}

	for _, k := range incomingKeys {
		key := resolved[k]
		src := incoming[k]
		dst := out[key]

		if _, wasInBase := base[key]; wasInBase {
			rep.Merged++
		} else {
			rep.Added++
		}

		// Details: keep the base's value, take the incoming one only to
		// fill a gap, and report a real disagreement either way.
		if src.NodeDetails.IP != "" {
			switch {
			case dst.NodeDetails.IP == "":
				dst.NodeDetails.IP = src.NodeDetails.IP
			case !strings.EqualFold(dst.NodeDetails.IP, src.NodeDetails.IP):
				rep.Conflicts = append(rep.Conflicts, MergeConflict{
					Node: key, Field: "ip",
					Kept: dst.NodeDetails.IP, Dropped: src.NodeDetails.IP,
				})
			}
		}
		if src.NodeDetails.Platform != "" {
			switch {
			case dst.NodeDetails.Platform == "":
				dst.NodeDetails.Platform = src.NodeDetails.Platform
			case !strings.EqualFold(dst.NodeDetails.Platform, src.NodeDetails.Platform):
				rep.Conflicts = append(rep.Conflicts, MergeConflict{
					Node: key, Field: "platform",
					Kept: dst.NodeDetails.Platform, Dropped: src.NodeDetails.Platform,
				})
			}
		}

		if dst.Peers == nil {
			dst.Peers = map[string]PeerEntry{}
		}
		for _, peerName := range sortedPeerKeys(src.Peers) {
			peer := src.Peers[peerName]
			pk := resolveName(peerName)

			// A node must not become its own peer. Merging can do that:
			// the incoming map has core-1 as a peer of core-1.lab, and
			// both resolve to the same node.
			if pk == key {
				continue
			}

			existing, had := dst.Peers[pk]
			if !had {
				rep.EdgesAdded++
				existing = PeerEntry{}
			}
			if existing.IP == "" {
				existing.IP = peer.IP
			}
			if existing.Platform == "" {
				existing.Platform = peer.Platform
			}
			existing.Connections = mergeConnections(existing.Connections, peer.Connections)
			dst.Peers[pk] = existing
		}

		out[key] = dst
	}

	rep.Nodes = len(out)
	sort.Strings(rep.Ambiguous)
	return out, rep
}

// MergeAll folds a series of maps left to right. The first map's names win
// throughout, which is what makes the result independent of how many maps were
// passed rather than only of their order.
func MergeAll(maps []map[string]MapNode, opt MergeOptions) (map[string]MapNode, []MergeReport) {
	if len(maps) == 0 {
		return map[string]MapNode{}, nil
	}
	out := maps[0]
	reports := make([]MergeReport, 0, len(maps)-1)
	for _, next := range maps[1:] {
		var rep MergeReport
		out, rep = Merge(out, next, opt)
		reports = append(reports, rep)
	}
	return out, reports
}

// ── identity index ───────────────────────────────────────────────────

// mergeIndex answers "which existing node is this name" under the three rules.
type mergeIndex struct {
	opt MergeOptions

	canon map[string]string   // canonical name -> key
	short map[string][]string // short name    -> keys
	ip    map[string][]string // management ip -> keys
}

func newMergeIndex(base map[string]MapNode, opt MergeOptions) *mergeIndex {
	ix := &mergeIndex{
		opt:   opt,
		canon: map[string]string{},
		short: map[string][]string{},
		ip:    map[string][]string{},
	}
	for _, k := range sortedKeys(base) {
		ix.add(k, base[k].NodeDetails.IP)
	}
	return ix
}

func (ix *mergeIndex) add(key, ip string) {
	c := ix.canonical(key)
	if _, seen := ix.canon[c]; !seen {
		ix.canon[c] = key
	}
	s := normalize.ShortName(key)
	ix.short[s] = appendUnique(ix.short[s], key)
	if ip = strings.TrimSpace(ip); ip != "" {
		ix.ip[ip] = appendUnique(ix.ip[ip], key)
	}
}

func (ix *mergeIndex) canonical(name string) string {
	return normalize.StripSuffixes(name, ix.opt.StripDomains)
}

// lookup returns the existing key this name refers to, or "" for a new node.
// ambiguous is true when more than one existing node is a candidate, in which
// case nothing should be merged.
func (ix *mergeIndex) lookup(name, ip string) (key string, ambiguous bool) {
	// 1. Canonical name. Exact, cheap, and never ambiguous — the index
	// holds one key per canonical form.
	if k, ok := ix.canon[ix.canonical(name)]; ok {
		return k, false
	}

	// 2. Management IP. Strong evidence, and a duplicated IP across two
	// nodes is exactly the case to refuse rather than resolve.
	if ix.opt.MatchIP {
		if ip = strings.TrimSpace(ip); ip != "" {
			switch ks := ix.ip[ip]; len(ks) {
			case 1:
				return ks[0], false
			case 0:
			default:
				return "", true
			}
		}
	}

	// 3. Short name, last because it is the guess.
	if ix.opt.MatchShortName {
		switch ks := ix.short[normalize.ShortName(name)]; len(ks) {
		case 1:
			return ks[0], false
		case 0:
		default:
			return "", true
		}
	}

	return "", false
}

// ── helpers ──────────────────────────────────────────────────────────

// mergeConnections unions two connection lists, dropping pairs that describe
// the same physical link.
//
// The comparison is on normalized interface names, because "Gi0/0" and
// "GigabitEthernet0/0" are one port and two crawls of one estate routinely
// disagree about which form they report. The ORIGINAL strings are kept — the
// map is read by people, and rewriting every port name into a normalized form
// would make it agree with nothing an operator sees on the device.
func mergeConnections(base, extra [][]string) [][]string {
	seen := map[string]bool{}
	key := func(c []string) string {
		parts := make([]string, 0, len(c))
		for _, s := range c {
			parts = append(parts, strings.ToLower(normalize.Interface(s)))
		}
		return strings.Join(parts, "\x00")
	}

	out := make([][]string, 0, len(base)+len(extra))
	for _, c := range base {
		k := key(c)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, append([]string(nil), c...))
	}
	for _, c := range extra {
		k := key(c)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, append([]string(nil), c...))
	}
	return out
}

// freeKey returns name, or name with a numeric suffix when it is taken.
//
// This is only reached for a node the merge decided is NOT the same device as
// the one already under that name — an ambiguous match, or two genuinely
// different boxes that share a name across maps. Overwriting would lose one of
// them; suffixing keeps both and makes the collision visible in the map itself.
func freeKey(out map[string]MapNode, name string) string {
	if _, taken := out[name]; !taken {
		return name
	}
	for i := 2; ; i++ {
		candidate := name + "~" + itoa(i)
		if _, taken := out[candidate]; !taken {
			return candidate
		}
	}
}

func copyNode(n MapNode) MapNode {
	out := MapNode{NodeDetails: n.NodeDetails, Peers: make(map[string]PeerEntry, len(n.Peers))}
	for k, p := range n.Peers {
		cp := PeerEntry{IP: p.IP, Platform: p.Platform}
		for _, c := range p.Connections {
			cp.Connections = append(cp.Connections, append([]string(nil), c...))
		}
		out.Peers[k] = cp
	}
	return out
}

func sortedKeys(m map[string]MapNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// Map iteration order is random, and a merge that produced different
	// keys or a different report on the same two inputs would be unusable
	// as a check.
	sort.Strings(out)
	return out
}

func sortedPeerKeys(m map[string]PeerEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func appendUnique(list []string, v string) []string {
	for _, s := range list {
		if s == v {
			return list
		}
	}
	return append(list, v)
}

// itoa avoids importing strconv for one call site in a package that otherwise
// does not need it.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
