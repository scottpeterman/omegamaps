package snmpprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/topo"
)

// freeUDPPort returns a local port with nothing listening on it.
func freeUDPPort(t *testing.T) uint16 {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	c.Close()
	return uint16(port)
}

func expectFailed(t *testing.T, stage string, d *topo.Device, err error) {
	t.Helper()
	if d == nil {
		t.Fatalf("Probe returned a nil device")
	}
	if !d.Failed || err == nil {
		t.Fatalf("expected failure at %q, got failed=%v err=%v", stage, d.Failed, err)
	}
	if !strings.HasPrefix(d.FailedWhy, "snmp "+stage) {
		t.Fatalf("FailedWhy = %q, want prefix %q", d.FailedWhy, "snmp "+stage)
	}
}

// A host that does not answer must come back failed, never as an empty
// device that looks like a switch with no neighbors.
func TestProbeUnreachableFails(t *testing.T) {
	p := New(Options{Port: freeUDPPort(t), Timeout: 300 * time.Millisecond, Retries: -1})
	start := time.Now()
	d, err := p.Probe(context.Background(), "127.0.0.1", Credential{Community: "public"})
	expectFailed(t, "system", d, err)
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("one request with no retries took %v", el)
	}
	if len(d.Neighbors) != 0 {
		t.Fatalf("failed device carries neighbors: %+v", d.Neighbors)
	}
}

func TestProbeCredentialErrors(t *testing.T) {
	p := New(Options{})
	cases := []Credential{
		{},
		{User: "u", PrivKey: "privsecret"}, // privacy without auth
		{User: "u", AuthKey: "authsecret", AuthProtocol: "RC4"}, // unknown auth
		{User: "u", AuthKey: "authsecret", PrivKey: "privsecret", PrivProtocol: "3DES"},
	}
	for _, c := range cases {
		d, err := p.Probe(context.Background(), "127.0.0.1", c)
		expectFailed(t, "credential", d, err)
	}
}

func TestProbeCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d, err := New(Options{}).Probe(ctx, "127.0.0.1", Credential{Community: "public"})
	expectFailed(t, "cancelled", d, err)
}

// TestProbeLive runs against a real agent when one is named:
//
//	PFSNMP_TEST_TARGET=10.0.0.11 PFSNMP_TEST_COMMUNITY=secret \
//	    go test ./internal/snmpprobe -run Live -v
//
// For v3 set PFSNMP_TEST_V3_USER, and optionally PFSNMP_TEST_V3_AUTH,
// PFSNMP_TEST_V3_AUTHPROTO, PFSNMP_TEST_V3_PRIV, PFSNMP_TEST_V3_PRIVPROTO.
// PFSNMP_TEST_PORT overrides 161. Against a switch it prints what the crawler
// would receive, which is the quickest way to check a platform before it is
// in a crawl.
func TestProbeLive(t *testing.T) {
	target := os.Getenv("PFSNMP_TEST_TARGET")
	if target == "" {
		t.Skip("PFSNMP_TEST_TARGET not set")
	}
	cred := Credential{
		Community:    os.Getenv("PFSNMP_TEST_COMMUNITY"),
		User:         os.Getenv("PFSNMP_TEST_V3_USER"),
		AuthKey:      os.Getenv("PFSNMP_TEST_V3_AUTH"),
		AuthProtocol: os.Getenv("PFSNMP_TEST_V3_AUTHPROTO"),
		PrivKey:      os.Getenv("PFSNMP_TEST_V3_PRIV"),
		PrivProtocol: os.Getenv("PFSNMP_TEST_V3_PRIVPROTO"),
	}
	opt := Options{Log: t.Logf}
	if s := os.Getenv("PFSNMP_TEST_PORT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatal(err)
		}
		opt.Port = uint16(n)
	}

	start := time.Now()
	d, err := New(opt).Probe(context.Background(), target, cred)
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	t.Logf("%s in %v: hostname=%q sysname=%q ip=%s platform=%q", target, time.Since(start).Round(time.Millisecond),
		d.Hostname, d.SysName, d.IPAddress, d.Platform)
	for _, n := range d.Neighbors {
		t.Logf("  %-5s %-16s -> %s %s  ip=%s platform=%q caps=%q", n.Protocol, n.LocalInterface,
			n.RemoteDevice, n.RemoteInterface, n.RemoteIP, n.RemotePlatform, n.Capabilities)
	}
	if d.SysName == "" && d.Version == "" {
		t.Fatalf("agent answered but returned neither sysName nor sysDescr")
	}
}

func TestCredentialRejected(t *testing.T) {
	opErr := &net.OpError{Op: "read", Net: "udp", Err: errors.New("connection refused")}
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"timeout on the first request", &Error{Stage: StageSystem, Err: errors.New("request timeout (after 1 retries)")}, true},
		{"v3 authentication", &Error{Stage: StageSystem, Err: errors.New("incoming packet is not authentic, discarding")}, true},
		{"malformed credential", &Error{Stage: StageCredential, Err: errors.New("unknown v3 auth protocol")}, true},
		{"nothing listening", &Error{Stage: StageSystem, Err: fmt.Errorf("error reading from socket: %w", opErr)}, false},
		{"name does not resolve", &Error{Stage: StageConnect, Err: &net.DNSError{Err: "no such host", Name: "x"}}, false},
		{"walk after the credential worked", &Error{Stage: "lldpRemTable walk", Err: errors.New("request timeout")}, false},
		{"cancelled", &Error{Stage: StageCancelled, Err: context.Canceled}, false},
		{"not a probe error", errors.New("anything"), false},
	}
	for _, c := range cases {
		if got := CredentialRejected(c.err); got != c.want {
			t.Errorf("%s: CredentialRejected = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestProbeCredentialsNone(t *testing.T) {
	d, err := New(Options{}).ProbeCredentials(context.Background(), "127.0.0.1", nil)
	expectFailed(t, "credential", d, err)
}

// A second credential is only worth trying when the first was not accepted.
// Against a closed port the first attempt fails either with ICMP
// port-unreachable, which is a network error and must end the ladder, or --
// on a stack that does not report it -- with a timeout, which must not.
// Either way the ladder has to agree with CredentialRejected about the first
// failure.
func TestProbeCredentialsStopsWhereAnotherCredentialCannotHelp(t *testing.T) {
	var rejected int
	p := New(Options{
		Port: freeUDPPort(t), Timeout: 300 * time.Millisecond, Retries: -1,
		Log: func(format string, args ...any) {
			if strings.Contains(format, "did not accept") {
				rejected++
			}
		},
	})
	first, firstErr := p.Probe(context.Background(), "127.0.0.1", Credential{Community: "a"})
	if !first.Failed {
		t.Fatal("closed port answered")
	}
	d, err := p.ProbeCredentials(context.Background(), "127.0.0.1",
		[]Credential{{Community: "a"}, {Community: "b"}})
	expectFailed(t, "system", d, err)
	want := 0
	if CredentialRejected(firstErr) {
		want = 2
	}
	if rejected != want {
		t.Fatalf("first failure %v (rejected=%v): ladder logged %d rejections, want %d",
			firstErr, CredentialRejected(firstErr), rejected, want)
	}
}

// TestProbeCredentialsLive puts a wrong community ahead of the right one
// against the agent TestProbeLive uses. The first must be passed over on its
// timeout and the second must answer.
func TestProbeCredentialsLive(t *testing.T) {
	target, community := os.Getenv("PFSNMP_TEST_TARGET"), os.Getenv("PFSNMP_TEST_COMMUNITY")
	if target == "" || community == "" {
		t.Skip("PFSNMP_TEST_TARGET and PFSNMP_TEST_COMMUNITY not set")
	}
	opt := Options{Timeout: time.Second, Retries: -1, Log: t.Logf}
	if s := os.Getenv("PFSNMP_TEST_PORT"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			t.Fatal(err)
		}
		opt.Port = uint16(n)
	}
	d, err := New(opt).ProbeCredentials(context.Background(), target,
		[]Credential{{Community: "definitely-not-" + community}, {Community: community}})
	if err != nil {
		t.Fatalf("ladder: %v", err)
	}
	if d.SysName == "" && d.Version == "" {
		t.Fatal("answered with neither sysName nor sysDescr")
	}
}
