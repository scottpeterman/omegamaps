// internal/crawler/crawler.go
// Depth-batched BFS network crawler: collect a device -> claim and enqueue
// its neighbors for the next depth. Concurrency is per depth level (worker
// pool), matching the Python engine's shape.
//
// A device is collected by one or more methods, tried in order. SSH is dial ->
// fingerprint -> run the platform's neighbor plan -> parse; the caller
// supplies a DialFunc so all auth/jump/host-key policy stays outside, and the
// crawler passes it a DialTarget describing which device it means and learns
// nothing about how the connection was made. SNMP goes through an SNMPFunc on
// the same terms. Everything about WHICH device a target is -- resolution,
// claiming, naming, exclusion -- stays on this side of both seams, so a device
// is identified by the same rules however it was collected.
package crawler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/dial"
	"github.com/scottpeterman/omegamaps/internal/netexec"
	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// DialTarget and DialFunc moved to internal/dial when capture became the
// second consumer of the dial layer. Aliases rather than a rename: every
// call site in the crawler and its tests still reads DialTarget, which is
// the right word here, and the type is genuinely the same one capture uses.
type DialTarget = dial.Target

// DialFunc opens an SSH connection to a target. See dial.Func.
type DialFunc = dial.Func

// SNMPFunc collects one device over SNMP: identity from the system group,
// neighbors from LLDP-MIB and CISCO-CDP-MIB. It receives the same target the
// SSH dial layer would, and credentials are entirely on its side of the call,
// as with DialFunc. On failure it should return a device carrying Failed and
// FailedWhy; a nil device is tolerated.
//
// Hostname is the crawler's, with one exception: when the target is an
// address, the collector may set Hostname to the device's own name, having
// judged that name fit to label a node. snmpprobe does, for a hostname-shaped
// sysName. The crawler reports the rename; it does not second-guess the name.
//
// The error matters beyond its text: one that wraps a net.Error is a reason
// to retry at the neighbor-reported address, exactly as for SSH.
type SNMPFunc func(ctx context.Context, t DialTarget) (*topo.Device, error)

// Logf receives progress lines; nil discards them.
type Logf func(format string, args ...any)

type Config struct {
	Dial DialFunc

	// SNMP collects a device over SNMP. Nil means SNMP is unavailable, and
	// MethodSNMP in Methods is dropped with a log line.
	SNMP SNMPFunc

	// Methods is the order collection methods are tried on each device.
	// Empty means SSH only. A device moves to the next method when one
	// fails, and also when one reaches it but finds no neighbors: a
	// monitoring community restricted to the system group answers SNMP and
	// returns empty LLDP tables, and a device that looks like a leaf
	// because of a view is the failure this exists to prevent. When every
	// method has run, the first to find neighbors wins; failing that, the
	// first to reach the device; failing that, the device fails with every
	// method's reason.
	Methods []crawlrun.Method

	MaxDepth    int // 0 = seeds only
	Concurrency int // workers per depth batch (default 5)
	// Domains are suffixes appended when a neighbor name does not resolve
	// as reported ("eng-spine-1" -> "eng-spine-1.<domain>"; first suffix that
	// resolves wins). The same list is stripped from identities for crawl
	// dedup, so a device claimed as "eng-spine-1" and dialed as
	// "eng-spine-1.<domain>" is one claim.
	Domains         []string
	ExcludePatterns []string // substring match vs platform/hostname/sysname
	// AllowDomains restricts which neighbors are DIALED. When non-empty,
	// only neighbor names suffix-matching an entry are enqueued; everything
	// else (including bare-IP fallback targets) is kept in the map as a
	// leaf but never connected to. Essential when seeds face an IX or any
	// shared fabric where LLDP sees third-party devices.
	AllowDomains []string
	// DisableIPFallback turns off retrying a failed dial against the
	// management address the neighbor reported. The zero value keeps the
	// fallback ON: a neighbor that told us both a name and an address has
	// given us two chances to reach it, and declining to use the second
	// one because the first did not resolve is throwing away information
	// the device handed us.
	DisableIPFallback bool

	// DisableNeighborDNS turns off completing a neighbor's management
	// address by forward-resolving the name it was reported under. The zero
	// value keeps it ON, matching DisableIPFallback above: a collection step
	// with no address column is the reason the field is empty, not a
	// statement by the device that it has no address. See neighboraddr.go.
	DisableNeighborDNS bool

	// DisablePerInterfaceDetail turns off re-asking for LLDP detail one
	// interface at a time when the bulk command is rejected. Zero value
	// keeps it ON. It costs a round trip per adjacency on the devices that
	// need it, and buys the system description — which is the field
	// ExcludePatterns matches, so turning it off means exclusion on those
	// devices degrades to hostname and port text. See perinterface.go.
	DisablePerInterfaceDetail bool

	SessionOpts netexec.Options
	Log         Logf

	// Emit receives the same events as structured values, for anything that
	// needs to accumulate state rather than watch output scroll past. It is
	// additive: every emit sits beside the Log call it mirrors, so adding a UI
	// can never change what the CLI prints. A nil Emit costs nothing.
	Emit crawlrun.Emit
}

type Crawler struct {
	cfg      Config
	resolver normalize.Resolver
	mu       sync.Mutex
	claimed  map[string]struct{}
	devices  []*topo.Device

	// claimIdx indexes claimed identities by first label, so a claim check
	// compares against the handful that could match instead of all of them.
	// Guarded by mu.
	claimIdx map[string][]string

	// excluded records targets an exclude pattern has matched, keyed on the
	// claim identity, with the pattern that did it. Guarded by mu.
	//
	// Exclusion is a decision about a DEVICE, but the evidence arrives per
	// EDGE, and the edges disagree. A host with two links to a leaf appears
	// twice; if detail came back for one port and not the other, one claim
	// carries the system description that matches "linux" and the other
	// carries nothing. Deciding per edge means the bare one admits the host
	// and it gets dialed anyway — with the two rows keyed differently, so
	// the run table shows it excluded AND running and looks self-
	// contradictory. Once anything says a target is excluded, it stays
	// excluded for the rest of the run.
	excluded map[string]string

	// addrCache memoizes neighbor-name lookups for the life of one crawl.
	// Guarded by mu. See neighboraddr.go.
	addrCache map[string]resolvedAddr
}

// dialAllowed applies the AllowDomains policy to a candidate target.
// A name passes when it suffix-matches an allowed domain as reported, OR
// when appending one of the resolution Domains (a) lands it under an
// allowed domain and (b) the completed name actually resolves — the DNS
// requirement is what stops third-party FQDNs from qualifying via a
// bogus appended suffix.
func (c *Crawler) dialAllowed(target string) bool {
	if len(c.cfg.AllowDomains) == 0 {
		return true
	}
	if _, err := netip.ParseAddr(target); err == nil {
		// bare-IP fallback target: no name to match the allowlist against,
		// so with an allowlist active it is not dialed.
		return false
	}
	matches := func(name string) bool {
		n := normalize.Identifier(name)
		for _, d := range c.cfg.AllowDomains {
			d = normalize.Identifier(strings.TrimPrefix(strings.TrimSpace(d), "."))
			if d == "" {
				continue
			}
			if n == d || strings.HasSuffix(n, "."+d) {
				return true
			}
		}
		return false
	}
	if matches(target) {
		return true
	}
	for _, d := range c.cfg.Domains {
		d = strings.TrimPrefix(strings.TrimSpace(d), ".")
		if d == "" {
			continue
		}
		cand := target + "." + d
		if !matches(cand) {
			continue
		}
		if _, err := c.resolver.LookupHost(cand); err == nil {
			return true
		}
	}
	return false
}

func New(cfg Config) *Crawler {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 5
	}
	if cfg.Log == nil {
		cfg.Log = func(string, ...any) {}
	}
	cfg.Methods = availableMethods(cfg)
	return &Crawler{
		cfg:       cfg,
		resolver:  normalize.DefaultResolver,
		claimed:   map[string]struct{}{},
		claimIdx:  map[string][]string{},
		excluded:  map[string]string{},
		addrCache: map[string]resolvedAddr{},
	}
}

// reportDone reports a device's outcome from its worker, as soon as it is
// known -- not after the batch. The batch has to wait for every device before
// claiming neighbors (see claimAll), but a device's own outcome depends on
// none of that, and holding it back left every device in a batch reading as
// running, phase frozen, until the slowest one -- usually a dead peer router
// timing out -- was done. A recorded crawl showed a depth's devices "reached"
// seventy seconds after they had in fact finished.
//
// Events key on the claim identity, never on Hostname: Hostname is the string
// dialed, identity is what the device was claimed under, and with a domain
// suffix configured the two differ.
func (c *Crawler) reportDone(identity string, d *topo.Device, m crawlrun.Method) {
	if d.Failed {
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindFailed,
			Identity: identity, Name: d.SysName, Detail: d.FailedWhy})
		c.cfg.Log("crawl: %s FAILED: %s", d.Hostname, d.FailedWhy)
		return
	}
	// Success has no log line of its own -- the crawler only narrates what
	// went wrong -- so this is an emit with no cfg.Log beside it. Without it
	// every device that worked stays mid-flight and Finish sweeps it into a
	// failure, which is the opposite of what happened.
	c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindReached,
		Identity: identity, Name: d.SysName, Platform: d.Platform, Method: m})
}

// availableMethods is cfg.Methods with the default applied and anything
// without an implementation removed, so crawlOne never calls a nil function.
// A dropped method is logged: a crawl asked for SNMP and quietly running SSH
// only is the kind of difference nothing in the result would reveal.
func availableMethods(cfg Config) []crawlrun.Method {
	ms := cfg.Methods
	if len(ms) == 0 {
		ms = []crawlrun.Method{crawlrun.MethodSSH}
	}
	var out []crawlrun.Method
	seen := map[crawlrun.Method]bool{}
	for _, m := range ms {
		if seen[m] {
			continue
		}
		seen[m] = true
		switch {
		case m == crawlrun.MethodSSH && cfg.Dial != nil,
			m == crawlrun.MethodSNMP && cfg.SNMP != nil:
			out = append(out, m)
		default:
			cfg.Log("crawl: method %q has no collector configured; not used", m)
		}
	}
	return out
}

// identity is the crawler's key for a device: the target with any configured
// domain suffix stripped, so a box seen short and fully qualified is one
// device. This is the value handed to the dial layer as DialTarget.Identity,
// and it must stay the same function the claim set is keyed on.
func (c *Crawler) identity(target string) string {
	return normalize.StripSuffixes(target, c.cfg.Domains)
}

// tryClaim registers a target exactly once, and reports whether this call is
// the one that got it.
//
// A claim covers every identity that names the same device — see
// normalize.SameDevice. It used to cover the target's first label as a claim
// in its own right, which is the same thing right up until an estate names
// devices by role and site: claiming "spine-1.usa" also claimed "spine-1", so
// "spine-1.eng" arrived looking like a device already crawled and was dropped
// without a word. Every <role>.<site1>/<role>.<site2> pair in the estate lost
// its second half that way, and the loss is invisible — a refused claim
// produces no failure, no log line, and no node.
func (c *Crawler) tryClaim(target string) bool {
	id := c.identity(target)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.claimedLocked(id) {
		return false
	}
	c.recordLocked(id)
	return true
}

// claimedLocked reports whether anything already claimed names this device.
// Callers hold c.mu.
func (c *Crawler) claimedLocked(id string) bool {
	if _, ok := c.claimed[id]; ok {
		return true
	}
	// Only identities sharing a first label can possibly match, which keeps
	// this from scanning the whole claim set on every neighbor of every
	// device.
	for _, cand := range c.claimIdx[normalize.ShortName(id)] {
		if normalize.SameDevice(cand, id) {
			return true
		}
	}
	return false
}

// recordLocked files an identity in both the exact set and the first-label
// index. Callers hold c.mu.
func (c *Crawler) recordLocked(id string) {
	if id == "" {
		return
	}
	if _, ok := c.claimed[id]; ok {
		return
	}
	c.claimed[id] = struct{}{}
	if c.claimIdx == nil {
		c.claimIdx = map[string][]string{}
	}
	k := normalize.ShortName(id)
	c.claimIdx[k] = append(c.claimIdx[k], id)
}

// registerAliases claims a discovered device's other identities so the same
// box reached by a different name later is not re-crawled.
//
// Only the most qualified form of each identity is filed. A device dialled as
// "spine-1.usa" that calls itself "spine-1" from its prompt hands us both, and
// filing the bare one would block "spine-1.eng" — SameDevice matches a bare
// label against every qualified name under it, which is correct as a question
// about two names and wrong as a claim over an estate. Dropping it costs
// nothing: anything the bare form would have matched, the qualified form
// matches too.
func (c *Crawler) registerAliases(d *topo.Device) {
	ids := make([]string, 0, 3)
	for _, id := range []string{d.Hostname, d.SysName, d.IPAddress} {
		if id != "" {
			ids = append(ids, normalize.StripSuffixes(id, c.cfg.Domains))
		}
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for _, id := range mostQualified(ids) {
		// A bare placeholder claimed earlier — a seed written as "qfx", say —
		// is superseded now that the device has told us its real name.
		// Leaving it in place would keep every sibling site locked out for
		// the rest of the run.
		c.supersedeLocked(id)
		c.recordLocked(id)
	}
}

// mostQualified drops any identity that is a strict label-prefix of another
// in the same set, keeping the longest form of each distinct name.
func mostQualified(ids []string) []string {
	out := make([]string, 0, len(ids))
	for i, a := range ids {
		covered := false
		for j, b := range ids {
			if i != j && len(b) > len(a) && normalize.SameDevice(a, b) {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, a)
		}
	}
	return out
}

// supersedeLocked removes claims that are strictly less specific than id and
// name the same device. Callers hold c.mu.
func (c *Crawler) supersedeLocked(id string) {
	k := normalize.ShortName(id)
	kept := c.claimIdx[k][:0]
	for _, cand := range c.claimIdx[k] {
		if len(cand) < len(id) && normalize.SameDevice(cand, id) {
			delete(c.claimed, cand)
			continue
		}
		kept = append(kept, cand)
	}
	c.claimIdx[k] = kept
}

// resolveName applies the CGNAT rule and logs what it decided. The rule
// itself lives in normalize because credres has to reach the same answer:
// the crawler's claim key and the binding cache key are the same string, or
// the cache is useless.
func (c *Crawler) resolveName(target string) string {
	res := normalize.ResolveWith(c.resolver, target)
	switch {
	case !res.CGNAT:
	case res.PTR == "":
		c.cfg.Log("crawl: %s is CGNAT (100.64/10) with no PTR; using address", target)
	case res.Confirmed:
		c.cfg.Log("crawl: %s is CGNAT (100.64/10) -> %s; using name", target, res.PTR)
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindResolved, Identity: target,
			Detail: "CGNAT -> " + res.PTR})
	default:
		c.cfg.Log("crawl: %s is CGNAT (100.64/10) -> %s but that name does not "+
			"resolve; using address", target, res.PTR)
	}
	return res.Name
}

// resolveViaDomains appends configured domain suffixes when the target
// does not resolve as reported; the first candidate with an A/AAAA record
// wins. Targets that already resolve (or are IPs) pass through.
func (c *Crawler) resolveViaDomains(target string) string {
	if len(c.cfg.Domains) == 0 {
		return target
	}
	if _, err := netip.ParseAddr(target); err == nil {
		return target
	}
	if _, err := c.resolver.LookupHost(target); err == nil {
		return target
	}
	for _, d := range c.cfg.Domains {
		d = strings.TrimPrefix(strings.TrimSpace(d), ".")
		if d == "" {
			continue
		}
		cand := target + "." + d
		if _, err := c.resolver.LookupHost(cand); err == nil {
			c.cfg.Log("crawl: resolved %s via domain suffix -> %s", target, cand)
			c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindResolved, Identity: target,
				Detail: "domain suffix -> " + cand})
			return cand
		}
	}
	return target
}

// nextTarget decides what to enqueue for a neighbor claim: prefer the
// reported name (with domain resolution left to DNS at dial time); fall
// back to the management IP when the "name" is really a chassis MAC.
//
// The second return is the management address the neighbor reported, kept
// alongside the name rather than discarded. It used to be read only in the
// chassis-MAC branch, which meant a device whose name simply did not resolve
// was a dead end even though the claim carried a working address. That is a
// common shape in a lab with no DNS, and it is not rare in production either
// — a device renamed after its A record was written looks identical.
func nextTarget(n topo.Neighbor, log Logf) (string, string, bool) {
	name := strings.TrimSpace(n.RemoteDevice)
	addr := strings.TrimSpace(n.RemoteIP)
	if normalize.IsMACAddress(addr) || normalize.IsLoopback(addr) {
		addr = ""
	}
	if normalize.IsArtifactName(name) {
		return "", "", false
	}
	// A neighbor calling itself localhost is dialed where it really is, or
	// not at all -- never at the crawler's own host. renameLoopbackNeighbors
	// has normally renamed it to its address already; this is the backstop.
	if normalize.IsLoopback(name) {
		if addr != "" {
			log("crawl: neighbor calls itself %q; queuing by its reported IP %s", name, addr)
			return addr, addr, true
		}
		log("crawl: neighbor calls itself %q and reports no usable IP; skipping", name)
		return "", "", false
	}
	if normalize.IsMACAddress(name) {
		if addr != "" {
			log("crawl: neighbor named by chassis MAC %s; queuing by IP %s", name, addr)
			return addr, addr, true
		}
		log("crawl: neighbor %s is a chassis MAC with no usable IP; skipping", name)
		return "", "", false
	}
	return name, addr, true
}

// shouldRetryByAddr decides whether a failed dial is worth one more attempt
// against the management address the neighbor reported.
//
// Only reachability failures qualify. An authentication rejection must never
// come back here: the dial layer walks a credential ladder, so retrying at a
// second address would spend the whole ladder twice against one account and
// double the lockout exposure this subsystem exists to bound. net.Error and
// *net.OpError cover DNS failures, refused connections and timeouts; an SSH
// auth failure is none of those, which is what makes the discriminator hold
// without the crawler having to know anything about credentials.
func (c *Crawler) shouldRetryByAddr(dt DialTarget, err error) bool {
	if c.cfg.DisableIPFallback || dt.Addr == "" || dt.Addr == dt.Target {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

// mergeNeighbor fills gaps in an already-recorded edge from a later step that
// describes the same link.
//
// Two commands routinely describe one link at different resolutions: a summary
// form that knows only names and ports, and a detail form that also carries a
// management address, a platform string and a port description. Whichever runs
// first used to win outright, so a plan that happened to list the summary first
// produced address-less edges while the address sat parsed and discarded one
// step later. Plan order still decides the PRIMARY record; this makes the
// order stop being the difference between having a management address and not.
//
// Only empty fields are filled. A later step never overwrites a value an
// earlier one supplied — the first record is still authoritative for anything
// it actually knows, which keeps this from quietly rewriting a good name with
// a truncated one.
func mergeNeighbor(dst *topo.Neighbor, src topo.Neighbor) {
	fill := func(d *string, s string) {
		if *d == "" {
			*d = strings.TrimSpace(s)
		}
	}
	fill(&dst.RemoteIP, src.RemoteIP)
	fill(&dst.RemotePlatform, src.RemotePlatform)
	fill(&dst.RemoteDescr, src.RemoteDescr)
	fill(&dst.Capabilities, src.Capabilities)
	fill(&dst.RemoteInterface, src.RemoteInterface)
}

// crawlOne collects an already-admitted device -- resolution and claiming
// happened in admit -- with each configured method in turn, and returns the
// device it keeps and the method that produced it (empty when none did).
//
// Collection is two phases and the order is load-bearing. The method runs
// first, and with SSH every step of the plan enriches edges an earlier step
// already claimed; only then, on the device that is kept, are the remaining
// gaps filled from DNS. Anything the device said about its own neighbors
// outranks anything a resolver says about them.
func (c *Crawler) crawlOne(ctx context.Context, it item) (*topo.Device, crawlrun.Method) {
	methods := c.cfg.Methods
	if len(methods) == 0 {
		return &topo.Device{Hostname: it.target, Depth: it.depth, Failed: true,
			FailedWhy: "no collection method configured"}, ""
	}

	var (
		keep       *topo.Device
		keepMethod crawlrun.Method
		reasons    []string
	)
	for i, m := range methods {
		d := c.collect(ctx, it, m)
		if d.Failed {
			reasons = append(reasons, labelReason(m, d.FailedWhy))
			if keep != nil && !keep.Failed {
				// An earlier method reached the device and found no
				// neighbors; this one was the hope of finding some. Its
				// failure is why the device ends up a leaf, so it is said
				// rather than swallowed.
				detail := fmt.Sprintf("%s failed: %s; keeping the %s result", m, d.FailedWhy, keepMethod)
				c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindCollectErr,
					Identity: it.identity, Method: m, Detail: detail})
				c.cfg.Log("crawl: %s: %s", it.target, detail)
			}
		}
		if !d.Failed && len(d.Neighbors) > 0 {
			keep, keepMethod = d, m
			break
		}
		// Nothing better yet: a device that was reached beats one that
		// was not, and the earlier method wins between equals.
		if keep == nil || (keep.Failed && !d.Failed) {
			keep, keepMethod = d, m
		}
		if i == len(methods)-1 || ctx.Err() != nil {
			break
		}
		why := "reached it but found no neighbors"
		if d.Failed {
			why = "failed: " + d.FailedWhy
		}
		detail := fmt.Sprintf("%s %s; trying %s", m, why, methods[i+1])
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindFallback,
			Identity: it.identity, Method: m, Detail: detail})
		c.cfg.Log("crawl: %s: %s", it.target, detail)
	}

	if keep.Failed {
		// Every method that ran failed. One reason is not the whole story
		// when there were several: "ssh auth rejected" alone sends someone
		// to the vault when SNMP also timed out and the host is down.
		if len(reasons) > 1 {
			keep.FailedWhy = strings.Join(reasons, "; ")
		}
		return keep, ""
	}
	// After collection, never during it: a step that carries a real
	// management address has to beat DNS, and it can only do that once every
	// step has had its turn to merge. See neighboraddr.go.
	c.renameLoopbackNeighbors(keep)
	c.phase(it, keepMethod, "dns fill")
	c.fillNeighborAddrs(it.identity, keep)
	return keep, keepMethod
}

// phase reports what a device is doing now. It is progress, not a decision,
// and the one emit without a log line beside it: the -v log records what each
// step produced, and a line before every step as well would bury that.
func (c *Crawler) phase(it item, m crawlrun.Method, what string) {
	c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindPhase, Identity: it.identity,
		Method: m, Detail: what})
}

// renameLoopbackNeighbors names a neighbor that calls itself localhost by the
// address it reported instead. The device at that address is crawled and
// named by address (its own "localhost" names nothing), so this is what makes
// the link from this side land on the same node. Before the DNS fill, which
// would otherwise resolve "localhost" to 127.0.0.1. Without an address there
// is nothing better to call it, and nextTarget will not dial it.
func (c *Crawler) renameLoopbackNeighbors(d *topo.Device) {
	for i := range d.Neighbors {
		n := &d.Neighbors[i]
		if !normalize.IsLoopback(n.RemoteDevice) {
			continue
		}
		ip := strings.TrimSpace(n.RemoteIP)
		if ip == "" || normalize.IsLoopback(ip) || normalize.IsMACAddress(ip) {
			continue
		}
		c.cfg.Log("crawl: %s: neighbor on %s calls itself %q; naming it by its reported address %s",
			d.Hostname, n.LocalInterface, n.RemoteDevice, ip)
		n.RemoteDevice = ip
	}
}

// labelReason prefixes a failure with its method unless it already says so.
func labelReason(m crawlrun.Method, why string) string {
	if strings.HasPrefix(why, string(m)+" ") || strings.HasPrefix(why, string(m)+":") {
		return why
	}
	return string(m) + " " + why
}

// collect runs one method. availableMethods guarantees m has a collector.
func (c *Crawler) collect(ctx context.Context, it item, m crawlrun.Method) *topo.Device {
	if m == crawlrun.MethodSNMP {
		return c.collectSNMP(ctx, it)
	}
	return c.collectSSH(ctx, it)
}

// dialTarget is what either collector is told about the device it means.
func (c *Crawler) dialTarget(it item) DialTarget {
	dt := DialTarget{
		Target:   it.target,
		Reported: it.reported,
		Identity: it.identity,
		Addr:     it.addr,
		Depth:    it.depth,
	}
	if _, err := netip.ParseAddr(it.target); err == nil {
		dt.Addr = it.target
	}
	return dt
}

// collectSNMP is the SNMP counterpart of collectSSH. The collector returns
// what the device said; the decisions about what that means for identity are
// made here, by the same rules and with the same events as the SSH path, so
// a device named from its sysName is named the way one named from its prompt
// would be.
func (c *Crawler) collectSNMP(ctx context.Context, it item) *topo.Device {
	dt := c.dialTarget(it)
	c.phase(it, crawlrun.MethodSNMP, "snmp probe")
	d, err := c.cfg.SNMP(ctx, dt)
	if err != nil && c.shouldRetryByAddr(dt, err) {
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindRetryAddr,
			Identity: it.identity, Detail: it.addr})
		c.cfg.Log("crawl: %s unreachable by name over snmp (%v); retrying at reported address %s",
			it.target, err, dt.Addr)
		byAddr := dt
		byAddr.Target = dt.Addr
		c.phase(it, crawlrun.MethodSNMP, "snmp probe at "+dt.Addr)
		d, err = c.cfg.SNMP(ctx, byAddr)
	}
	if d == nil {
		d = &topo.Device{}
	}
	if err != nil && !d.Failed {
		d.Failed, d.FailedWhy = true, "snmp: "+err.Error()
	}
	d.Depth = it.depth
	// Whatever the collector did with it, a loopback name names nothing:
	// the node keeps the address it was reached at (see normalize.IsLoopback).
	if normalize.IsLoopback(d.SysName) {
		d.SysName = ""
	}
	if normalize.IsLoopback(d.Hostname) {
		d.Hostname = ""
	}

	// The node is named for the string it was claimed and dialed under,
	// exactly as on the SSH path -- including after a retry by address,
	// where the collector saw only the address. When that string is itself
	// an address the collector has already promoted sysName, which is the
	// SSH rule for a prompt.
	_, byAddr := netip.ParseAddr(it.target)
	targetIsAddr := byAddr == nil
	if !targetIsAddr || d.Hostname == "" {
		d.Hostname = it.target
	}
	if d.Failed {
		return d
	}
	if targetIsAddr && d.SysName != "" && d.Hostname == d.SysName {
		if !strings.EqualFold(normalize.Canonical(d.SysName, c.cfg.Domains), it.identity) {
			c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindRenamed,
				Identity: it.identity, Name: d.SysName})
		}
		c.cfg.Log("crawl: %s identifies itself as %q; naming the node from sysName",
			it.target, d.SysName)
	}
	c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindCollect, Identity: it.identity,
		Detail: "snmp", Parsed: len(d.Neighbors), New: len(d.Neighbors)})
	c.cfg.Log("crawl: %s: snmp -> %d neighbors", it.target, len(d.Neighbors))
	return d
}

// collectSSH dials, fingerprints, and runs the platform's neighbor plan.
func (c *Crawler) collectSSH(ctx context.Context, it item) *topo.Device {
	target := it.target
	d := &topo.Device{Hostname: target, Depth: it.depth}

	dt := c.dialTarget(it)
	c.phase(it, crawlrun.MethodSSH, "ssh dial")
	client, err := c.cfg.Dial(ctx, dt)
	if err != nil && c.shouldRetryByAddr(dt, err) {
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindRetryAddr,
			Identity: it.identity, Detail: it.addr})
		c.cfg.Log("crawl: %s unreachable by name (%v); retrying at reported address %s",
			it.target, err, dt.Addr)
		byAddr := dt
		byAddr.Target = dt.Addr
		// Identity deliberately does NOT change. The device is the same
		// device; only the route to it is. Re-keying on the address here
		// would split every cache downstream — a binding written under the
		// address would never be found again by a caller holding the name.
		client, err = c.cfg.Dial(ctx, byAddr)
	}
	if err != nil {
		d.Failed, d.FailedWhy = true, fmt.Sprintf("dial: %v", err)
		return d
	}
	defer client.Close()
	// Record the address the device actually answered on, not the string
	// that was dialed. registerAliases claims this, so a device reached by
	// name here and by address from a neighbor's claim later is recognized
	// as one device instead of being crawled and mapped twice.
	if host, _, err := net.SplitHostPort(client.RemoteAddr()); err == nil {
		d.IPAddress = host
	} else if host, _, err := net.SplitHostPort(client.Addr()); err == nil {
		d.IPAddress = host
	}

	sess, err := netexec.Open(ctx, client, c.cfg.SessionOpts)
	if err != nil {
		d.Failed, d.FailedWhy = true, fmt.Sprintf("session: %v", err)
		return d
	}
	defer sess.Close()

	c.phase(it, crawlrun.MethodSSH, "ssh fingerprint")
	fp, err := netexec.Fingerprint(ctx, sess)
	if err != nil || fp == nil {
		d.Failed, d.FailedWhy = true, fmt.Sprintf("fingerprint: %v", err)
		return d
	}
	d.Platform = fp.Name
	d.Version = fp.VersionOutput

	// The device's own name, out of its prompt. Always recorded as SysName
	// so the claim set and the topology pass can alias on it. Promoted to
	// Hostname only when the device was reached by address, because that is
	// the case where the alternative is a map node labelled 10.0.0.1.
	if sys := normalize.HostnameFromPrompt(sess.Prompt()); sys != "" {
		d.SysName = sys
		if _, err := netip.ParseAddr(target); err == nil {
			// Only when the name actually differs from what it was claimed
			// under. "x identifies itself as x" is not a decision, and a
			// decisions list that fills with no-ops stops being read.
			if !strings.EqualFold(normalize.Canonical(sys, c.cfg.Domains), it.identity) {
				c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindRenamed,
					Identity: it.identity, Name: sys})
			}
			c.cfg.Log("crawl: %s identifies itself as %q; naming the node from its prompt",
				target, sys)
			d.Hostname = sys
		}
	}

	plan, ok := planFor(fp.Name)
	if !ok {
		// discovered but not crawlable (e.g. linux, unknown): keep as a
		// mapped leaf with no neighbors.
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindPlatform,
			Identity: it.identity, Platform: fp.Name, Detail: "no neighbor plan; leaf"})
		c.cfg.Log("crawl: %s platform %q has no neighbor plan; leaf", target, fp.Name)
		return d
	}

	// edge dedup within the device: (local_if, peer, remote_if)
	// seen maps an edge key to its index in d.Neighbors, so a later step
	// describing the same link can ENRICH the record rather than being
	// dropped. See mergeNeighbor.
	seen := map[[3]string]int{}

	// The per-interface retry command, remembered from whichever step
	// declares one. It runs after the whole plan and only against edges that
	// still have no system description — see interfacesMissingDetail. Not
	// armed by the bulk step failing: a partial answer leaves some edges
	// bare and those are the ones worth asking about.
	var fallbackCmd, fallbackKey string
	for _, st := range plan {
		if st.PerInterfaceFallback != "" {
			fallbackCmd, fallbackKey = st.PerInterfaceFallback, st.Key
		}
		c.phase(it, crawlrun.MethodSSH, "ssh: "+st.Command)
		out, err := sess.Run(ctx, st.Command)
		if err != nil {
			if st.BestEffort {
				c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindCollectErr,
					Identity: it.identity, Detail: st.Command + ": " + err.Error()})
				c.cfg.Log("crawl: %s: %q failed (best-effort): %v", target, st.Command, err)
				continue
			}
			d.Failed, d.FailedWhy = true, fmt.Sprintf("%q: %v", st.Command, err)
			return d
		}
		// Repair CLI hard-wrapping before the template sees it. A row cut at
		// the screen width leaves a bare fragment on the next line that no
		// template matches: against a strict Error rule that is a whole-device
		// parse failure, and against a loose one it is a truncated neighbor
		// name that then fails to resolve. Done here rather than inside
		// parseStep so the join count can be logged against the device and
		// the command it came from. See unwrap.go.
		if unwrapped, joins := unwrapWrapped(out); joins > 0 {
			out = unwrapped
			c.cfg.Log("crawl: %s: %q was hard-wrapped by the CLI; rejoined %d line(s)",
				target, st.Command, joins)
		}
		// Then drop whatever still cannot be a row, for steps that opted in.
		// One unrecognized line failing the parse for a whole device is a
		// disproportionate outcome when the table has ninety good rows in it.
		// Reported, never silent: a scrub nobody hears about is how a device
		// ends up with a neighbor list that is missing links.
		if st.ScrubToRows {
			scrubbed, dropped, suspect := scrubToRows(out)
			if len(dropped) > 0 {
				out = scrubbed
				detail := fmt.Sprintf("%s: dropped %d unparsable line(s), first: %s",
					st.Command, len(dropped), truncate(dropped[0], 60))
				if suspect > 0 {
					detail += fmt.Sprintf(" (%d followed an over-long line and may be "+
						"wrapped continuations; a neighbor name may be truncated)", suspect)
				}
				c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindCollectErr,
					Identity: it.identity, Detail: detail})
				c.cfg.Log("crawl: %s: %s", target, detail)
			}
		}
		recs, err := parseStep(fp.Name, st, out)
		if err != nil {
			if st.BestEffort {
				c.cfg.Log("crawl: %s: parse %q (best-effort): %v", target, st.Command, err)
				continue
			}
			// A required step that ran and would not parse is not a quiet
			// outcome. Without the emit the device reports zero neighbors
			// and the run looks clean — which is exactly how a wrapped
			// Junos table hid for as long as it did.
			c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindCollectErr,
				Identity: it.identity, Detail: st.Command + ": " + err.Error()})
			c.cfg.Log("crawl: %s: parse %q: %v", target, st.Command, err)
			continue
		}
		// Per-step accounting. A step that runs, parses cleanly and still
		// contributes nothing used to be completely silent — the crawl
		// reported success while running on whatever the other steps
		// happened to supply. That silence cost real debugging time: a
		// plan whose first step carries no management address looks
		// identical to a device that never advertised one.
		var added, enriched, skipped int
		for _, n := range recs {
			if !st.EdgeSource && n.LocalInterface == "" {
				// enrichment-only record (e.g. IOS lldp detail without
				// Local Intf) — merge fields into an existing edge later;
				// for now it cannot create an edge on its own.
				skipped++
				continue
			}
			key := [3]string{
				normalize.Interface(n.LocalInterface),
				normalize.Identifier(n.RemoteDevice),
				normalize.Interface(n.RemoteInterface),
			}
			if idx, dup := seen[key]; dup {
				mergeNeighbor(&d.Neighbors[idx], n)
				enriched++
				continue
			}
			seen[key] = len(d.Neighbors)
			d.Neighbors = append(d.Neighbors, n)
			added++
		}
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindCollect, Identity: it.identity,
			Detail: st.Command, Parsed: len(recs), New: added,
			Enriched: enriched, Skipped: skipped})
		c.cfg.Log("crawl: %s: %q -> %d parsed, %d new, %d enriched, %d skipped",
			target, st.Command, len(recs), added, enriched, skipped)
	}

	// Detail per interface, for the builds that only take it that way.
	if fallbackCmd != "" {
		c.phase(it, crawlrun.MethodSSH, "ssh per-interface: "+fallbackCmd)
		c.collectPerInterface(ctx, sess, d, it.identity, fallbackCmd, fallbackKey)
	}
	return d
}

// item is one admitted device: resolved, claimed, and ready to dial.
type item struct {
	target   string // resolved: what to dial
	reported string // as claimed by a neighbor, before resolution
	identity string // the claim key
	addr     string // management address from the neighbor claim, if any
	depth    int

	// parent is the identity of the device whose neighbor table produced
	// this one. Empty for a seed.
	//
	// This is the answer to the first question anyone asks about an
	// unexpected row — "where did that come from" — and without it the only
	// way to find out is to correlate log lines by hand. It is also the BFS
	// parentage the jump-host wiring needs: a device discovered behind a
	// bastion is reachable the way its parent was reachable.
	parent string

	// descr and caps are what the neighbor claim advertised about this
	// target: the LLDP or CDP system description and capability string.
	// Empty for a seed.
	//
	// They are carried rather than looked up because by the time the device
	// is dialed the claim that produced it is several batches behind, and
	// they are reported at admission rather than after the fingerprint
	// because their whole value is being known BEFORE a credential is
	// offered. The platform column already answers the same question
	// afterwards.
	descr string
	caps  string
}

// admit resolves a reported target to the string that will actually be dialed,
// derives the identity from THAT, and claims it.
//
// The order matters and used to be wrong. Resolution ran inside crawlOne,
// after the claim, so a CGNAT address whose PTR resolved was claimed under the
// address and dialed by name — two keys for one device the moment anything
// downstream started caching on identity. Resolve, then claim, then dial.
func (c *Crawler) admit(reported, addr string, depth int, parent string) (item, bool) {
	target := c.resolveViaDomains(c.resolveName(reported))
	if !c.tryClaim(target) {
		return item{}, false
	}
	return item{
		target:   target,
		reported: reported,
		identity: c.identity(target),
		addr:     addr,
		depth:    depth,
		parent:   parent,
	}, true
}

// Crawl runs the BFS from the seeds and returns all crawled devices.
// Crawl runs to completion. Equivalent to CrawlContext with a background
// context; kept so the CLI, which has signal handling of its own, is
// unchanged.
func (c *Crawler) Crawl(seeds []string) []*topo.Device {
	return c.CrawlContext(context.Background(), seeds)
}

// CrawlContext runs until the frontier empties or ctx is cancelled.
//
// Cancellation is checked in two places: between depth batches, and inside
// each worker once it holds a slot. The second check is what makes a stop
// feel immediate — every device in a batch spawns a goroutine straight away
// and then blocks on the semaphore, so on cancel the queued ones fall through
// without dialing and only the handful actually in flight have to drain.
//
// Devices abandoned this way are returned marked failed with a reason rather
// than dropped. A device that silently vanishes from a stopped run is
// indistinguishable from one that was never discovered, and the whole point of
// reporting a crawl is that the two are not the same.
func (c *Crawler) CrawlContext(ctx context.Context, seeds []string) []*topo.Device {
	if ctx == nil {
		ctx = context.Background()
	}
	var batch []item
	for _, s := range seeds {
		if it, ok := c.admit(s, "", 0, ""); ok {
			batch = append(batch, it)
		}
	}

	for len(batch) > 0 {
		if err := ctx.Err(); err != nil {
			c.cfg.Log("crawl: stopped before depth %d with %d device(s) pending",
				batch[0].depth, len(batch))
			c.recordCancelled(batch, err)
			break
		}
		depth := batch[0].depth
		c.cfg.Log("crawl: depth %d, %d device(s)", depth, len(batch))
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindDepth, Depth: depth})
		for _, it := range batch {
			c.cfg.Emit.Send(crawlrun.Event{
				Kind: crawlrun.KindQueued, Identity: it.identity,
				Depth: it.depth, Via: it.parent,
				Descr: it.descr, Caps: it.caps,
			})
		}

		results := make([]*topo.Device, len(batch))
		methods := make([]crawlrun.Method, len(batch))
		sem := make(chan struct{}, c.cfg.Concurrency)
		var wg sync.WaitGroup
		for i, it := range batch {
			wg.Add(1)
			go func(i int, it item) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				if err := ctx.Err(); err != nil {
					results[i] = cancelledDevice(it, err)
				} else {
					results[i], methods[i] = c.crawlOne(ctx, it)
				}
				c.reportDone(it.identity, results[i], methods[i])
			}(i, it)
		}
		wg.Wait()

		// Two passes, and the order matters. Every device in this batch has
		// to register its own names BEFORE any neighbor list is walked,
		// because a batch routinely contains a device that another device in
		// the same batch also reports as a neighbor. Registering and admitting
		// in one pass means the earlier device's neighbors are admitted while
		// the later device has not yet claimed the names it answers to — so it
		// is claimed a second time, dialed a second time, and spends a second
		// set of credential attempts on an account that already worked.
		c.claimAll(results)

		// Third pass, before any admission: settle exclusion for every
		// target this batch mentions. An exclude verdict is about a device
		// and the evidence arrives per edge, so a target has to be judged
		// against everything the batch knows about it before anything is
		// allowed to dial it. Judging inside the admission loop lets a bare
		// edge admit a host that a later edge would have excluded.
		c.markExcluded(results)

		var next []item
		for i, d := range results {
			// Events key on the claim identity, never on Hostname. Hostname
			// is the string dialed; identity is what the device was claimed
			// under, and with a domain suffix configured the two differ — so
			// keying terminal events on Hostname files them against a second
			// row and leaves the first one looking unfinished.
			identity := batch[i].identity

			// Reached or failed was already reported by the worker, the
			// moment the device finished; see reportDone.
			if d.Failed {
				continue
			}
			if excl, pat := normalize.ShouldExclude(
				[]string{d.Platform, d.Hostname, d.SysName},
				c.cfg.ExcludePatterns); excl {
				c.cfg.Log("crawl: %s excluded from propagation (pattern %q)", d.Hostname, pat)
				continue
			}
			if depth >= c.cfg.MaxDepth {
				continue
			}
			for _, n := range d.Neighbors {
				t, addr, ok := nextTarget(n, c.cfg.Log)
				if !ok {
					continue
				}
				// Pre-dial exclusion. The verdict was settled in
				// markExcluded above, against every claim in the batch
				// rather than just this one — see the note there and on
				// Crawler.excluded.
				if pat, excl := c.exclusionFor(t); excl {
					c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindNotDialed,
						Identity: c.identity(t), Via: identity, Depth: depth + 1,
						Detail: "matches exclude " + pat,
						Descr:  n.RemoteDescr, Caps: n.Capabilities})
					c.cfg.Log("crawl: %s matches exclude %q (from neighbor claim); mapped as leaf, not dialed", t, pat)
					continue
				}
				if !c.dialAllowed(t) {
					c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindNotDialed,
						Identity: t, Via: identity, Depth: depth + 1,
						Detail: "outside allowed domains",
						Descr:  n.RemoteDescr, Caps: n.Capabilities})
					c.cfg.Log("crawl: %s outside allowed domains; mapped as leaf, not dialed", t)
					continue
				}
				if it, ok := c.admit(t, addr, depth+1, identity); ok {
					// Set here rather than inside admit: the advertisement
					// belongs to the EDGE, and admit is also the seed path,
					// where there is no edge and no advertisement.
					it.descr, it.caps = n.RemoteDescr, n.Capabilities
					next = append(next, it)
				}
			}
		}
		batch = next
	}
	return c.devices
}

// parseStep is separated for testability against captured output.
func parseStep(platform string, st step, output string) ([]topo.Neighbor, error) {
	recs, err := tfsmParse(platform, st.Key, output)
	if err != nil {
		return nil, err
	}
	out := make([]topo.Neighbor, 0, len(recs))
	for _, r := range recs {
		out = append(out, recordToNeighbor(r, st.Protocol))
	}
	return out, nil
}

// claimAll registers every device in a finished batch and files it, before any
// neighbor list from that batch is walked. See the comment at the call site:
// splitting this out of the admission loop is the whole point.
func (c *Crawler) claimAll(results []*topo.Device) {
	for _, d := range results {
		if d == nil {
			continue
		}
		c.registerAliases(d)
	}
	c.mu.Lock()
	for _, d := range results {
		if d != nil {
			c.devices = append(c.devices, d)
		}
	}
	c.mu.Unlock()
}

// cancelledDevice is the record for a device the crawl gave up on because it
// was stopped, as distinct from one that was tried and failed.
func cancelledDevice(it item, err error) *topo.Device {
	return &topo.Device{
		Hostname:  it.target,
		Depth:     it.depth,
		Failed:    true,
		FailedWhy: "crawl stopped before this device was attempted: " + err.Error(),
	}
}

// recordCancelled files everything still queued when a crawl is stopped, so
// the run's device count still accounts for the whole frontier.
func (c *Crawler) recordCancelled(batch []item, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, it := range batch {
		c.devices = append(c.devices, cancelledDevice(it, err))
		c.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindFailed, Identity: it.identity,
			Detail: "crawl stopped before this device was attempted"})
	}
}
