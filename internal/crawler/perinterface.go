// internal/crawler/perinterface.go
//
// Detail, one interface at a time, for devices that reject it in bulk.
//
// Old Junos rejects `show lldp neighbors detail` outright and the crawl falls
// back to the terse table. That table has names and ports and nothing else —
// no system description, no neighbor platform, no management address. The
// missing address is recoverable from DNS; the missing DESCRIPTION is not,
// and it is the field exclusion runs on. "-exclude linux,broadcom" matches a
// system description, so on the terse path it silently matches nothing and
// every server the fabric can see gets dialed and walks the credential
// ladder. That is the opposite of what the flag is for.
//
// The same devices that reject the bulk form accept the per-interface one.
// Every platform has one; they disagree only about word order, which is why
// the plan carries a format string rather than a prefix:
//
//	show lldp neighbors interface xe-0/0/23           (Junos)
//	show lldp neighbors Ethernet1/1 detail            (IOS, EOS)
//	show lldp neighbors interface Ethernet1/1 detail  (NX-OS)
//
// It matters as much off Junos as on it. A server behind an IOS or NX-OS ToR
// speaks LLDP and not CDP, so when that platform's lldp detail is unsupported
// the only record of the server is a table with no description column — and
// an exclude pattern has nothing to match.
//
// which returns the full detail block. The existing detail template already
// parses a run of those blocks concatenated — see the note in tfsm.go, which
// describes a collector that loops interfaces. This is that collector; it
// was described before it was written.
//
// It is expensive on purpose-built hardware: one command per adjacency, so a
// ToR with ninety neighbors costs ninety round trips against a slow CLI. That
// is the price of the data, and it is only paid by devices that could not
// answer in bulk. It never runs on a device whose detail command worked.
package crawler

import (
	"context"
	"fmt"
	"strings"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/netexec"
	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// collectPerInterface re-asks for detail once per interface already known to
// have a neighbor, and merges the result into the edges the terse table
// created. Returns the number of edges enriched.
//
// It only ever ENRICHES. The interfaces it queries come from edges that
// already exist, so a record that matches nothing is a record about a link
// this pass did not go looking for, and appending it would double the edge
// the terse table already recorded under a different port spelling — the
// terse table reports the port DESCRIPTION where detail reports the port ID,
// and on this fleet those differ often enough to matter.
func (c *Crawler) collectPerInterface(
	ctx context.Context,
	sess *netexec.Session,
	d *topo.Device,
	identity, cmdFormat, key string,
) int {
	if c.cfg.DisablePerInterfaceDetail || len(d.Neighbors) == 0 {
		return 0
	}

	ifaces := interfacesMissingDetail(d.Neighbors)
	if len(ifaces) == 0 {
		return 0
	}
	c.cfg.Log("crawl: %s: %d adjacency(ies) have no system description; asking per interface",
		d.Hostname, len(ifaces))

	asked, refused, parsed, enriched := 0, 0, 0, 0
	for _, iface := range ifaces {
		if ctx.Err() != nil {
			break
		}
		out, err := sess.Run(ctx, fmt.Sprintf(cmdFormat, iface))
		if err != nil {
			// One interface refusing is not the device refusing. Keep going:
			// a partial set of descriptions is worth more than none, and the
			// count below says how partial it was.
			refused++
			continue
		}
		asked++

		// Parsed per interface rather than concatenated and parsed once.
		// That costs a few more parses and buys the thing that makes this
		// work across platforms: we know which interface the output is
		// about, so a record that does not name its own local interface can
		// still be placed. IOS is the case — plan.go already records that
		// some builds omit Local Intf from lldp detail entirely, and those
		// are exactly the builds that need this fallback.
		recs, err := parseStep(d.Platform, step{Key: key, Protocol: "lldp"}, out)
		if err != nil {
			continue
		}
		parsed += len(recs)
		for _, r := range recs {
			if strings.TrimSpace(r.LocalInterface) == "" {
				r.LocalInterface = iface
			}
			idx, ok := matchByInterface(d.Neighbors, r)
			if !ok {
				continue
			}
			before := d.Neighbors[idx].RemoteDescr
			mergeNeighbor(&d.Neighbors[idx], r)
			if before == "" && d.Neighbors[idx].RemoteDescr != "" {
				enriched++
			}
		}
	}
	if asked == 0 {
		c.cfg.Log("crawl: %s: per-interface detail refused on every interface", d.Hostname)
		return 0
	}

	detail := "per-interface detail"
	if refused > 0 {
		detail += " (" + itoa(refused) + " interface(s) refused)"
	}
	c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindCollect, Identity: identity,
		Detail: detail, Parsed: parsed, Enriched: enriched})
	c.cfg.Log("crawl: %s: per-interface detail -> %d asked, %d parsed, %d edge(s) gained a description",
		d.Hostname, asked, parsed, enriched)
	return enriched
}

// interfacesMissingDetail lists the local ports whose neighbor still has no
// system description after the whole plan has run, in the order the table
// reported them, without repeats.
//
// The trigger used to be "the bulk detail step produced nothing", which is
// the all-or-nothing reading of a command that does not always fail that way.
// A device can answer detail for some interfaces and not others — a partial
// response, output cut short by the command timeout on a chassis with ninety
// adjacencies, a build that skips the ports it has nothing to say about — and
// any single record coming back was enough to disarm the fallback for the
// whole device. The neighbors detail never covered then kept an empty
// description, which is the field ExcludePatterns matches, so those and only
// those got dialed. A gap that size is invisible next to a device that failed
// outright.
//
// Asking per MISSING interface instead subsumes the old behaviour — detail
// rejected outright means every edge lacks a description, so every interface
// is queried — and costs nothing when detail worked: the list is empty and no
// commands are sent.
//
// A LAG reports the same member port once per neighbor claim, and there is no
// reason to ask about it twice.
func interfacesMissingDetail(ns []topo.Neighbor) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ns))
	for _, n := range ns {
		if strings.TrimSpace(n.RemoteDescr) != "" {
			continue
		}
		iface := strings.TrimSpace(n.LocalInterface)
		if iface == "" {
			continue
		}
		k := normalize.Interface(iface)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, iface)
	}
	return out
}

// matchByInterface finds the edge a per-interface record belongs to.
//
// The local interface is the primary key, because that is what was asked
// about. When one port has several neighbors — a shared segment, or a phone
// with a PC behind it — the remote device name disambiguates. A record that
// matches no edge, or matches several with no name to separate them, is left
// alone rather than merged into a guess.
func matchByInterface(ns []topo.Neighbor, r topo.Neighbor) (int, bool) {
	want := normalize.Interface(r.LocalInterface)
	if want == "" {
		return 0, false
	}
	var hits []int
	for i := range ns {
		if normalize.Interface(ns[i].LocalInterface) == want {
			hits = append(hits, i)
		}
	}
	switch len(hits) {
	case 0:
		return 0, false
	case 1:
		return hits[0], true
	}
	name := normalize.Identifier(r.RemoteDevice)
	if name == "" {
		return 0, false
	}
	for _, i := range hits {
		if normalize.Identifier(ns[i].RemoteDevice) == name {
			return i, true
		}
	}
	return 0, false
}

// itoa avoids pulling strconv in for one call site in a log string.
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
