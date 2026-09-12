// Package snmpprobe collects one device's identity and CDP/LLDP neighbors over
// SNMP and returns them as a topo.Device: the record the SSH path produces, so
// the claim set, the BFS, exclusion, and topo's bidirectional validation work
// on it unchanged.
//
// It is a port of SecureCartography 2.5's SNMP discovery collectors (system,
// interfaces, lldp, cdp), whose OIDs and field handling were validated against
// Junos, IOS and EOS. Two parts are deliberately not ported:
//
//   - The walker. gosnmp's BulkWalk replaces it. SC2.5's walker ended a walk
//     when a GETBULK response carried fewer varbinds than max-repetitions,
//     but agents trim responses to fit their message size, and LLDP rows
//     carry full system descriptions -- so on a device with many neighbors
//     the walk stopped early and reported success. It also returned partial
//     results on a mid-walk timeout. gosnmp continues until the OID leaves
//     the requested subtree and returns errors as errors.
//
//   - pysnmp value handling. gosnmp returns octet strings as bytes, so the
//     decoders reduce to what was ever about SNMP. See decode.go.
//
// One behavior is deliberately different: the local side of an LLDP link is
// resolved with the same rules as the remote side. See resolvePort.
package snmpprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/gosnmp/gosnmp"

	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// Credential is one SNMP credential: v2c when User is empty, v3 otherwise.
type Credential struct {
	Community string

	User         string
	AuthProtocol string // MD5, SHA, SHA224, SHA256, SHA384, SHA512; SHA when empty
	AuthKey      string
	PrivProtocol string // DES, AES, AES192, AES256, AES192C, AES256C; AES when empty
	PrivKey      string
	ContextName  string
}

// Options configure a Prober. The zero value is usable.
type Options struct {
	Port uint16 // 161 when zero

	// Timeout is per request, not per device: a walk is many requests.
	// 5s when zero.
	Timeout time.Duration

	// Retries per request. Zero means the default of 1; negative means none.
	Retries int

	// MaxRepetitions per GETBULK. 25 when zero, SC2.5's validated setting.
	MaxRepetitions uint32

	// Log receives progress lines. Nil discards them.
	Log func(format string, args ...any)
}

// Prober collects devices over SNMP. It holds only configuration; every Probe
// opens its own session, so one Prober is safe for concurrent use by a
// crawler's worker pool.
type Prober struct {
	opt Options
}

// Stages a probe can fail at, as they appear in Error.Stage and FailedWhy.
// Walk failures carry the table's name instead.
const (
	StageCancelled  = "cancelled"
	StageCredential = "credential"
	StageConnect    = "connect"
	StageSystem     = "system"
)

// ErrNoResponse is wrapped into a probe error when the first request got no
// answer at all. That is everything a wrong v2c community produces, and also
// what a host that is down produces; nothing distinguishes the two, and a
// caller counting failures against a credential needs to know which kind of
// failure it cannot count. gosnmp reports it only as text ("request timeout"),
// so this is recognized once, here, and never again by string.
var ErrNoResponse = errors.New("no response")

// Error is a failed probe. Stage says what was being attempted. Err is the
// underlying cause and stays in the chain, which is the point: gosnmp wraps
// connect and socket errors, so errors.As still finds the net.Error beneath
// a DNS failure or an ICMP port-unreachable, while a timeout or a v3
// authentication failure is a plain error. Callers deciding whether to retry
// at another address, or with another credential, turn on that difference.
type Error struct {
	Target string
	Stage  string
	Err    error
}

func (e *Error) Error() string { return e.Target + ": snmp " + e.Stage + ": " + e.Err.Error() }

func (e *Error) Unwrap() error { return e.Err }

// New returns a Prober with defaults applied.
func New(opt Options) *Prober {
	if opt.Port == 0 {
		opt.Port = 161
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 5 * time.Second
	}
	switch {
	case opt.Retries == 0:
		opt.Retries = 1
	case opt.Retries < 0:
		opt.Retries = 0
	}
	if opt.MaxRepetitions == 0 {
		opt.MaxRepetitions = 25
	}
	if opt.Log == nil {
		opt.Log = func(string, ...any) {}
	}
	return &Prober{opt: opt}
}

var authProtocols = map[string]gosnmp.SnmpV3AuthProtocol{
	"MD5": gosnmp.MD5, "SHA": gosnmp.SHA, "SHA224": gosnmp.SHA224,
	"SHA256": gosnmp.SHA256, "SHA384": gosnmp.SHA384, "SHA512": gosnmp.SHA512,
}

var privProtocols = map[string]gosnmp.SnmpV3PrivProtocol{
	"DES": gosnmp.DES, "AES": gosnmp.AES, "AES192": gosnmp.AES192,
	"AES256": gosnmp.AES256, "AES192C": gosnmp.AES192C, "AES256C": gosnmp.AES256C,
}

// Validate reports whether the credential can be used: v2c needs a community,
// v3 a user, known protocol names, and no privacy without authentication. A
// vault editor calls it when a credential is added, so a typo in a protocol
// name is refused then rather than failing every device in a crawl.
func (c Credential) Validate() error {
	if c.User == "" {
		if c.Community == "" {
			return errors.New("credential has neither a community nor a v3 user")
		}
		return nil
	}
	_, _, _, err := c.usm()
	return err
}

// usm resolves a v3 credential's protocols and security level.
func (c Credential) usm() (gosnmp.SnmpV3AuthProtocol, gosnmp.SnmpV3PrivProtocol, gosnmp.SnmpV3MsgFlags, error) {
	ap, pp, flags := gosnmp.NoAuth, gosnmp.NoPriv, gosnmp.NoAuthNoPriv
	if c.AuthKey != "" {
		name := strings.ToUpper(firstNonEmpty(c.AuthProtocol, "SHA"))
		p, ok := authProtocols[name]
		if !ok {
			return 0, 0, 0, fmt.Errorf("unknown v3 auth protocol %q", c.AuthProtocol)
		}
		ap, flags = p, gosnmp.AuthNoPriv
	}
	if c.PrivKey != "" {
		if c.AuthKey == "" {
			return 0, 0, 0, errors.New("v3 privacy without authentication is not a USM security level")
		}
		name := strings.ToUpper(firstNonEmpty(c.PrivProtocol, "AES"))
		p, ok := privProtocols[name]
		if !ok {
			return 0, 0, 0, fmt.Errorf("unknown v3 privacy protocol %q", c.PrivProtocol)
		}
		pp, flags = p, gosnmp.AuthPriv
	}
	return ap, pp, flags, nil
}

// session builds an unconnected gosnmp session for target.
func (p *Prober) session(ctx context.Context, target string, c Credential) (*gosnmp.GoSNMP, error) {
	g := &gosnmp.GoSNMP{
		Target:         target,
		Port:           p.opt.Port,
		Transport:      "udp",
		Context:        ctx,
		Timeout:        p.opt.Timeout,
		Retries:        p.opt.Retries,
		MaxRepetitions: p.opt.MaxRepetitions,
	}
	if c.User == "" {
		if c.Community == "" {
			return nil, errors.New("credential has neither a community nor a v3 user")
		}
		g.Version = gosnmp.Version2c
		g.Community = c.Community
		return g, nil
	}

	ap, pp, flags, err := c.usm()
	if err != nil {
		return nil, err
	}
	usm := &gosnmp.UsmSecurityParameters{
		UserName:                 c.User,
		AuthenticationProtocol:   ap,
		AuthenticationPassphrase: c.AuthKey,
		PrivacyProtocol:          pp,
		PrivacyPassphrase:        c.PrivKey,
	}
	g.Version = gosnmp.Version3
	g.SecurityModel = gosnmp.UserSecurityModel
	g.MsgFlags = flags
	g.SecurityParameters = usm
	g.ContextName = c.ContextName
	return g, nil
}

// Probe collects target with one credential. The returned device is never
// nil; on failure it carries Failed and FailedWhy exactly as the crawler's
// SSH path does, and the error is an *Error. Depth is the caller's to set.
//
// Any walk error fails the whole device. A neighbor table that stopped partway
// parses cleanly into a device with some of its links, which is the worst
// failure shape a map can have: nothing about it looks wrong.
func (p *Prober) Probe(ctx context.Context, target string, cred Credential) (*topo.Device, error) {
	d := &topo.Device{Hostname: target}
	fail := func(stage string, err error) (*topo.Device, error) {
		d.Failed = true
		d.FailedWhy = "snmp " + stage + ": " + err.Error()
		return d, &Error{Target: target, Stage: stage, Err: err}
	}
	if err := ctx.Err(); err != nil {
		return fail(StageCancelled, err)
	}

	g, err := p.session(ctx, target, cred)
	if err != nil {
		return fail(StageCredential, err)
	}
	if err := g.Connect(); err != nil {
		return fail(StageConnect, err)
	}
	defer g.Conn.Close()
	if host, _, err := net.SplitHostPort(g.Conn.RemoteAddr().String()); err == nil {
		d.IPAddress = host
	}

	// One GET for the system group. It is also the reachability and
	// credential check: a wrong v2c community is indistinguishable from a
	// dead host, and either should cost one timeout, not one per scalar.
	res, err := g.Get([]string{oidSysName, oidSysDescr})
	if err != nil {
		if strings.Contains(err.Error(), "request timeout") {
			err = fmt.Errorf("%w: %v", ErrNoResponse, err)
		}
		return fail(StageSystem, err)
	}
	if res.Error != gosnmp.NoError {
		return fail(StageSystem, fmt.Errorf("agent returned error-status %d", res.Error))
	}
	var descr string
	for _, v := range res.Variables {
		switch v.Name {
		case oidSysName:
			d.SysName = decodeString(pduBytes(v))
		case oidSysDescr:
			descr = decodeString(pduBytes(v))
		}
	}
	// A sysName of "localhost" names nothing: every box left at its default
	// would become one node. The device keeps the address it answered on.
	if normalize.IsLoopback(d.SysName) {
		d.SysName = ""
	}
	d.Version = descr
	d.Platform = normalize.PlatformFromDescription(descr)

	// As on the SSH path: the device's own name is always SysName, and is
	// promoted to Hostname only when it was reached by address, because
	// that is the case where the alternative is a node labelled 10.0.0.1.
	// Not logged here: a crawler reports renames as identity decisions, with
	// an event, and a second line from the collector would narrate it twice.
	if _, err := netip.ParseAddr(target); err == nil && hostnameShaped(d.SysName) {
		d.Hostname = d.SysName
	}

	pdus, err := g.BulkWalkAll(oidIfName)
	if err != nil {
		return fail("ifName walk", err)
	}
	ifn := parseIfNames(pdus, oidIfName)
	if len(ifn) == 0 {
		if pdus, err = g.BulkWalkAll(oidIfDescr); err != nil {
			return fail("ifDescr walk", err)
		}
		ifn = parseIfNames(pdus, oidIfDescr)
	}

	var neighbors []topo.Neighbor

	rem, err := g.BulkWalkAll(oidLldpRemEntry)
	if err != nil {
		return fail("lldpRemTable walk", err)
	}
	if len(rem) > 0 {
		t := parseRemTable(rem)
		loc, err := g.BulkWalkAll(oidLldpLocPortEntry)
		if err != nil {
			return fail("lldpLocPortTable walk", err)
		}
		man, err := g.BulkWalkAll(oidLldpRemManAddrEntry)
		if err != nil {
			return fail("lldpRemManAddrTable walk", err)
		}
		applyManAddrs(t, man)
		logf := func(format string, args ...any) { p.opt.Log("snmp: %s: "+format, append([]any{target}, args...)...) }
		neighbors = append(neighbors, lldpNeighbors(t, parseLocPorts(loc), ifn, logf)...)
	}

	ids, err := g.BulkWalkAll(cdpColumnRoot(cdpColDeviceID))
	if err != nil {
		return fail("cdpCacheDeviceId walk", err)
	}
	if len(ids) > 0 {
		t := parseCDPDeviceIDs(ids)
		for _, col := range cdpColumns[1:] {
			pdus, err := g.BulkWalkAll(cdpColumnRoot(col))
			if err != nil {
				return fail(fmt.Sprintf("cdpCacheEntry column %d walk", col), err)
			}
			applyCDPColumn(t, col, pdus)
		}
		neighbors = append(neighbors, cdpNeighbors(t, ifn)...)
	}

	d.Neighbors = dedupe(neighbors)
	return d, nil
}

// ProbeCredentials probes target with each credential in turn and returns the
// first device that answers.
//
// It moves on only when the agent did not accept the credential: a timeout on
// the first request, which is all a wrong v2c community ever produces, or a
// v3 authentication error. Anything else ends the ladder. A network error --
// no DNS answer, no route, nothing listening -- is the same for every
// credential, and a failure after the system GET means one was accepted and
// the device itself is the problem.
//
// Against a host that is down, a v2c ladder costs one timeout per community.
// v2c has no way to say "wrong community", so there is no cheaper answer; put
// the credential most devices use first.
func (p *Prober) ProbeCredentials(ctx context.Context, target string, creds []Credential) (*topo.Device, error) {
	if len(creds) == 0 {
		err := &Error{Target: target, Stage: StageCredential, Err: errors.New("no SNMP credentials configured")}
		return &topo.Device{Hostname: target, Failed: true,
			FailedWhy: "snmp " + StageCredential + ": " + err.Err.Error()}, err
	}
	var (
		d   *topo.Device
		err error
	)
	for i, c := range creds {
		d, err = p.Probe(ctx, target, c)
		if err == nil {
			if i > 0 {
				p.opt.Log("snmp: %s answered credential %d of %d", target, i+1, len(creds))
			}
			return d, nil
		}
		if !CredentialRejected(err) || ctx.Err() != nil {
			return d, err
		}
		if len(creds) > 1 {
			p.opt.Log("snmp: %s did not accept credential %d of %d: %v", target, i+1, len(creds), errors.Unwrap(err))
		}
	}
	if len(creds) > 1 {
		d.FailedWhy = fmt.Sprintf("snmp %s: no credential accepted (%d tried); last: %v",
			StageSystem, len(creds), errors.Unwrap(err))
	}
	return d, err
}

// CredentialRejected reports whether err is a probe failure that another
// credential could change: a malformed credential, or no acceptable answer to
// the first request that was not a network error.
func CredentialRejected(err error) bool {
	var pe *Error
	if !errors.As(err, &pe) {
		return false
	}
	switch pe.Stage {
	case StageCredential:
		return true
	case StageSystem:
		var ne net.Error
		return !errors.As(pe.Err, &ne)
	}
	return false
}

// hostnameShaped rejects sysName values that cannot be a node name.
func hostnameShaped(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, c := range s {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			return false
		}
	}
	return true
}

// dedupe collapses records describing the same edge, keyed exactly as
// crawlOne keys them, and merges with the same fill-if-empty rule as
// crawler.mergeNeighbor. LLDP comes first, so on a Cisco box running both
// protocols the LLDP record is kept and CDP fills the platform it lacks.
func dedupe(in []topo.Neighbor) []topo.Neighbor {
	seen := map[[3]string]int{}
	var out []topo.Neighbor
	for _, n := range in {
		key := [3]string{
			normalize.Interface(n.LocalInterface),
			normalize.Identifier(n.RemoteDevice),
			normalize.Interface(n.RemoteInterface),
		}
		if i, dup := seen[key]; dup {
			merge(&out[i], n)
			continue
		}
		seen[key] = len(out)
		out = append(out, n)
	}
	return out
}

func merge(dst *topo.Neighbor, src topo.Neighbor) {
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
