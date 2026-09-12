// internal/crawler/neighboraddr.go
//
// Management addresses for neighbors learned from a terse table.
//
// Some collection steps have no address column at all. Junos `show lldp
// neighbors` is the case that forced this: on builds that reject `show lldp
// neighbors detail` the terse table is the only thing that runs, and it
// reports a local interface, a chassis MAC, a port and a system name — no
// management address anywhere. The same shape shows up on IOS boxes whose
// lldp detail is unusable and on any device whose detail command errors out.
//
// Losing the address costs two things, and only one of them is obvious. The
// visible cost is the map: a neighbor that is never itself crawled is a leaf
// node with no address on it. The quieter cost is the dial path — an item
// admitted with an empty addr can never take the reported-address retry in
// crawlOne, so a device whose name simply does not resolve is a dead end even
// when an address for it was one lookup away.
//
// So: forward-resolve the reported name and fill the gap from DNS. This is a
// weaker source than an address the device advertised about its neighbor, and
// it is treated that way — it only ever fills a field that is already empty,
// and it runs after the whole plan has been collected and merged, so a later
// step that DID carry an address always wins.
package crawler

import (
	"net/netip"
	"strings"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// resolvedAddr is one memoized DNS answer about a neighbor name.
type resolvedAddr struct {
	// addr is the forward-resolved address, empty when nothing resolved.
	addr string

	// dialable reports whether addr passed the CGNAT rule below. An address
	// that did not is deliberately dropped rather than recorded: RemoteIP is
	// read by nextTarget as a dial target, so a field that sometimes means
	// "connect here" and sometimes means "display only" would need every
	// reader to know which, and one of them would eventually not.
	dialable bool
}

// fillNeighborAddrs completes RemoteIP from DNS for every neighbor that was
// reported by name with no address. Returns the number of fields filled.
//
// Neighbors already carrying an address are left alone, as are the ones with
// no usable name: a chassis MAC or a collection artifact resolves to nothing
// and asking DNS about it just spends a timeout per device per crawl.
func (c *Crawler) fillNeighborAddrs(identity string, d *topo.Device) int {
	if c.cfg.DisableNeighborDNS {
		return 0
	}
	filled := 0
	for i := range d.Neighbors {
		n := &d.Neighbors[i]
		if strings.TrimSpace(n.RemoteIP) != "" {
			continue
		}
		name := strings.TrimSpace(n.RemoteDevice)
		if name == "" || normalize.IsMACAddress(name) || normalize.IsArtifactName(name) ||
			normalize.IsLoopback(name) {
			continue
		}
		// A neighbor already named by address needs no lookup; copying it
		// across is what makes the retry path available for those too.
		if _, err := netip.ParseAddr(name); err == nil {
			n.RemoteIP = name
			filled++
			continue
		}

		r := c.lookupNeighborAddr(name)
		switch {
		case r.addr == "":
			continue
		case normalize.IsLoopback(r.addr):
			// A name that resolves to loopback -- a hosts-file entry on
			// the crawling machine -- is no address for a device.
			c.cfg.Log("crawl: %s: neighbor %s resolves to %s here; leaving it address-less",
				d.Hostname, name, r.addr)
			continue
		case !r.dialable:
			c.cfg.Log("crawl: %s: neighbor %s resolves to %s in shared address "+
				"space and the name does not confirm; leaving it address-less",
				d.Hostname, name, r.addr)
			continue
		}
		n.RemoteIP = r.addr
		filled++
	}
	if filled > 0 {
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindResolved, Identity: identity,
			Detail: "neighbor addresses from DNS"})
		c.cfg.Log("crawl: %s: filled %d neighbor management address(es) from DNS",
			d.Hostname, filled)
	}
	return filled
}

// lookupNeighborAddr is lookupNeighborAddrUncached behind a crawl-wide memo.
//
// A spine appears in the neighbor table of every leaf attached to it, so the
// same handful of names is looked up once per device without this. The memo
// is keyed on the normalized identifier so "lab-spine-01" and "LAB-SPINE-01"
// are one entry, and negative answers are cached too — a name with no record
// is the expensive case, not the cheap one.
func (c *Crawler) lookupNeighborAddr(name string) resolvedAddr {
	key := normalize.Identifier(name)

	c.mu.Lock()
	if r, ok := c.addrCache[key]; ok {
		c.mu.Unlock()
		return r
	}
	c.mu.Unlock()

	// Deliberately outside the lock. Two workers at the same depth can
	// duplicate one lookup; that is cheaper than holding the mutex that also
	// guards the claim set across a DNS round trip.
	r := c.lookupNeighborAddrUncached(name)

	c.mu.Lock()
	if c.addrCache == nil {
		c.addrCache = map[string]resolvedAddr{}
	}
	c.addrCache[key] = r
	c.mu.Unlock()
	return r
}

// lookupNeighborAddrUncached tries the name as reported, then each configured
// domain suffix, mirroring resolveViaDomains. First candidate with a record
// wins; a name that already carries the suffix is not asked about twice.
func (c *Crawler) lookupNeighborAddrUncached(name string) resolvedAddr {
	for _, cand := range c.addrCandidates(name) {
		addrs, err := c.resolver.LookupHost(cand)
		if err != nil || len(addrs) == 0 {
			continue
		}
		addr := preferIPv4(addrs)
		if addr == "" {
			continue
		}
		return resolvedAddr{addr: addr, dialable: c.addrDialable(addr, cand)}
	}
	return resolvedAddr{}
}

// addrCandidates is the name as reported followed by suffix completions.
func (c *Crawler) addrCandidates(name string) []string {
	cands := []string{name}
	lower := strings.ToLower(name)
	for _, d := range c.cfg.Domains {
		d = strings.TrimPrefix(strings.TrimSpace(d), ".")
		if d == "" || strings.HasSuffix(lower, "."+strings.ToLower(d)) {
			continue
		}
		cands = append(cands, name+"."+d)
	}
	return cands
}

// addrDialable applies the CGNAT rule to a forward-resolved address.
//
// Anything outside 100.64.0.0/10 is usable as-is. Inside it, the address is
// RFC 6598 shared space, which may be reused at every site, so an
// answer from a DNS view broader than the site we are crawling points at
// whichever device happens to hold it somewhere else. Everywhere else in this
// codebase a name is trusted over a shared address, so the address is reverse
// resolved and kept only when the PTR forward-confirms AND agrees with the
// name the lookup started from.
//
// Disagreement is not an error and not a failure to report loudly. It just
// means DNS did not give us a second way to reach this device, which is the
// same position we were in before the lookup.
func (c *Crawler) addrDialable(addr, name string) bool {
	if !normalize.IsCGNAT(addr) {
		return true
	}
	res := normalize.ResolveWith(c.resolver, addr)
	if !res.Confirmed {
		return false
	}
	return strings.EqualFold(normalize.ShortName(res.Name), normalize.ShortName(name))
}

// preferIPv4 picks the address to record. IPv4 first, deliberately: the dial
// layer, the jump bindings and the credential cache are all keyed on strings
// that in this fleet are v4, and a v6 answer for a dual-stacked device would
// key the same box twice. Falls back to whatever came back when there is no
// v4 record at all.
func preferIPv4(addrs []string) string {
	for _, a := range addrs {
		if ip, err := netip.ParseAddr(strings.TrimSpace(a)); err == nil && ip.Is4() {
			return ip.String()
		}
	}
	for _, a := range addrs {
		if ip, err := netip.ParseAddr(strings.TrimSpace(a)); err == nil {
			return ip.String()
		}
	}
	return ""
}
