// internal/crawler/methods_test.go
//
// The method seam. SNMP is faked at the SNMPFunc boundary; SSH, where a test
// needs it to succeed, is a real session against fakedev, so a fallback is
// proved by the SSH pipeline actually producing the device rather than by a
// stub claiming it did.
package crawler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/fakedev"
	"github.com/scottpeterman/omegamaps/internal/sshcore"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// snmpFake answers per dialed target. A target in fail gets that error; one
// in devices gets a copy of the device; anything else fails as unreachable.
type snmpFake struct {
	mu      sync.Mutex
	targets []string
	devices map[string]*topo.Device
	fail    map[string]error
}

func (f *snmpFake) collect(ctx context.Context, t DialTarget) (*topo.Device, error) {
	f.mu.Lock()
	f.targets = append(f.targets, t.Target)
	f.mu.Unlock()
	if err := f.fail[t.Target]; err != nil {
		return &topo.Device{Hostname: t.Target, Failed: true, FailedWhy: "snmp system: " + err.Error()}, err
	}
	tmpl, ok := f.devices[t.Target]
	if !ok {
		err := errors.New("request timeout (after 1 retries)")
		return &topo.Device{Hostname: t.Target, Failed: true, FailedWhy: "snmp system: " + err.Error()}, err
	}
	d := *tmpl
	d.Neighbors = append([]topo.Neighbor(nil), tmpl.Neighbors...)
	d.Hostname = t.Target
	// What snmpprobe does for a device reached by address.
	if _, err := netip.ParseAddr(t.Target); err == nil && d.SysName != "" {
		d.Hostname = d.SysName
	}
	return &d, nil
}

func (f *snmpFake) saw() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.targets...)
}

func lldpEdge(local, peer, peerIf, ip string) topo.Neighbor {
	return topo.Neighbor{LocalInterface: local, RemoteDevice: peer, RemoteInterface: peerIf,
		RemoteIP: ip, Protocol: "lldp"}
}

// eosWithNeighbor is a fakedev EOS whose LLDP table names one neighbor.
func eosWithNeighbor(t *testing.T, name string) DialFunc {
	t.Helper()
	cfg := fakedev.EOS(name)
	cfg.Commands["show lldp neighbors detail"] = labEOSDetail
	srv, err := fakedev.Start(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	return func(ctx context.Context, _ DialTarget) (*sshcore.Client, error) {
		return srv.Dial("lab", "lab")
	}
}

func refuseDial(ctx context.Context, _ DialTarget) (*sshcore.Client, error) {
	return nil, errors.New("dial: connection refused")
}

func rowFor(t *testing.T, run *crawlrun.Run, id string) crawlrun.DeviceRow {
	t.Helper()
	for _, r := range run.Rows() {
		if r.Identity == id {
			return r
		}
	}
	t.Fatalf("no row for %q", id)
	return crawlrun.DeviceRow{}
}

func fallbacks(run *crawlrun.Run) []crawlrun.Event {
	var out []crawlrun.Event
	for _, e := range run.Decisions() {
		if e.Kind == crawlrun.KindFallback {
			out = append(out, e)
		}
	}
	return out
}

// An SNMP-only crawl needs no SSH at all and walks the network exactly as an
// SSH crawl does: neighbors admitted, the next depth collected.
func TestSNMPOnlyCrawlFollowsNeighbors(t *testing.T) {
	fake := &snmpFake{devices: map[string]*topo.Device{
		"lab-r1": {SysName: "lab-r1", Platform: "arista_eos", IPAddress: "192.0.2.1",
			Neighbors: []topo.Neighbor{lldpEdge("Ethernet1", "lab-r2", "Ethernet1", "192.0.2.2")}},
		"lab-r2": {SysName: "lab-r2", Platform: "arista_eos", IPAddress: "192.0.2.2",
			Neighbors: []topo.Neighbor{lldpEdge("Ethernet1", "lab-r1", "Ethernet1", "192.0.2.1")}},
	}}
	run := crawlrun.New()
	c := New(Config{
		SNMP: fake.collect, Methods: []crawlrun.Method{crawlrun.MethodSNMP},
		MaxDepth: 2, Emit: run.Emit(),
	})
	c.resolver = stubResolver{forward: map[string][]string{
		"lab-r1": {"192.0.2.1"}, "lab-r2": {"192.0.2.2"},
	}}
	devices := c.Crawl([]string{"lab-r1"})
	run.Finish()

	if len(devices) != 2 {
		t.Fatalf("crawled %d devices, want 2 (targets %v)", len(devices), fake.saw())
	}
	for _, d := range devices {
		if d.Failed {
			t.Errorf("%s failed: %s", d.Hostname, d.FailedWhy)
		}
	}
	for _, id := range []string{"lab-r1", "lab-r2"} {
		if r := rowFor(t, run, id); r.State != crawlrun.StateReached || r.Method != crawlrun.MethodSNMP {
			t.Errorf("%s: state=%s method=%q", id, r.State, r.Method)
		}
	}
	m := topo.Generate(devices, topo.Options{})
	if got := m["lab-r1"].Peers["lab-r2"].Connections; len(got) != 1 {
		t.Errorf("lab-r1 -> lab-r2 connections = %v, want one validated link", got)
	}
}

func TestSNMPFailureFallsBackToSSH(t *testing.T) {
	fake := &snmpFake{}
	run := crawlrun.New()
	c := New(Config{
		Dial: eosWithNeighbor(t, "lab-r1"), SNMP: fake.collect,
		Methods: []crawlrun.Method{crawlrun.MethodSNMP, crawlrun.MethodSSH},
		Emit:    run.Emit(),
	})
	c.resolver = stubResolver{forward: map[string][]string{"lab-r1": {"192.0.2.1"}}}
	devices := c.Crawl([]string{"lab-r1"})
	run.Finish()

	d := devices[0]
	if d.Failed || len(d.Neighbors) != 1 || d.Platform != "arista_eos" {
		t.Fatalf("want the SSH-collected device, got %+v", d)
	}
	if r := rowFor(t, run, "lab-r1"); r.Method != crawlrun.MethodSSH || r.State != crawlrun.StateReached {
		t.Errorf("row: state=%s method=%q", r.State, r.Method)
	}
	fb := fallbacks(run)
	if len(fb) != 1 || fb[0].Method != crawlrun.MethodSNMP ||
		!strings.Contains(fb[0].Detail, "failed") || !strings.HasSuffix(fb[0].Detail, "trying ssh") {
		t.Fatalf("fallback decisions: %+v", fb)
	}
}

// The reason the zero-neighbor rule exists: a community whose view stops at
// the system group answers, and would otherwise map the device as a leaf.
func TestSNMPReachedWithoutNeighborsFallsBackToSSH(t *testing.T) {
	fake := &snmpFake{devices: map[string]*topo.Device{
		"lab-r1": {SysName: "lab-r1", Platform: "arista_eos"},
	}}
	run := crawlrun.New()
	c := New(Config{
		Dial: eosWithNeighbor(t, "lab-r1"), SNMP: fake.collect,
		Methods: []crawlrun.Method{crawlrun.MethodSNMP, crawlrun.MethodSSH},
		Emit:    run.Emit(),
	})
	c.resolver = stubResolver{forward: map[string][]string{"lab-r1": {"192.0.2.1"}}}
	d := c.Crawl([]string{"lab-r1"})[0]
	run.Finish()

	if d.Failed || len(d.Neighbors) != 1 {
		t.Fatalf("want SSH's neighbors, got %+v", d)
	}
	fb := fallbacks(run)
	if len(fb) != 1 || !strings.Contains(fb[0].Detail, "found no neighbors") {
		t.Fatalf("fallback decisions: %+v", fb)
	}
	if r := rowFor(t, run, "lab-r1"); r.Method != crawlrun.MethodSSH {
		t.Errorf("row method = %q, want ssh", r.Method)
	}
}

// When nothing finds neighbors, a device that answered at all is kept rather
// than failed: it is a real node, it just has nothing to say about links.
func TestReachedLeafKeptWhenLaterMethodFails(t *testing.T) {
	fake := &snmpFake{devices: map[string]*topo.Device{
		"lab-host": {SysName: "lab-host", Platform: "linux"},
	}}
	run := crawlrun.New()
	c := New(Config{
		Dial: refuseDial, SNMP: fake.collect,
		Methods: []crawlrun.Method{crawlrun.MethodSNMP, crawlrun.MethodSSH},
		Emit:    run.Emit(),
	})
	c.resolver = stubResolver{forward: map[string][]string{"lab-host": {"192.0.2.50"}}}
	d := c.Crawl([]string{"lab-host"})[0]
	run.Finish()

	if d.Failed || d.Platform != "linux" {
		t.Fatalf("want the SNMP leaf kept, got %+v", d)
	}
	if r := rowFor(t, run, "lab-host"); r.Method != crawlrun.MethodSNMP || r.State != crawlrun.StateReached {
		t.Errorf("row: state=%s method=%q", r.State, r.Method)
	}
	// Why it is a leaf has to be on record: SSH was tried and failed.
	var said bool
	for _, e := range run.Decisions() {
		said = said || (e.Kind == crawlrun.KindCollectErr && e.Method == crawlrun.MethodSSH &&
			strings.Contains(e.Detail, "keeping the snmp result"))
	}
	if !said {
		t.Errorf("the SSH failure behind the kept leaf was not reported: %+v", run.Decisions())
	}
}

func TestEveryMethodFailingReportsEachReason(t *testing.T) {
	fake := &snmpFake{}
	c := New(Config{
		Dial: refuseDial, SNMP: fake.collect,
		Methods: []crawlrun.Method{crawlrun.MethodSNMP, crawlrun.MethodSSH},
	})
	c.resolver = stubResolver{forward: map[string][]string{"lab-r1": {"192.0.2.1"}}}
	d := c.Crawl([]string{"lab-r1"})[0]

	if !d.Failed {
		t.Fatal("device should have failed")
	}
	if !strings.Contains(d.FailedWhy, "snmp system: request timeout") ||
		!strings.Contains(d.FailedWhy, "ssh dial: dial: connection refused") {
		t.Fatalf("FailedWhy = %q, want both methods' reasons", d.FailedWhy)
	}
}

// SSH is the default, and a method with no collector is dropped rather than
// called: SNMP requested with no SNMPFunc must not panic or change an SSH run.
func TestMethodsDefaultAndUnavailable(t *testing.T) {
	c := New(Config{Dial: refuseDial})
	if len(c.cfg.Methods) != 1 || c.cfg.Methods[0] != crawlrun.MethodSSH {
		t.Fatalf("default methods = %v", c.cfg.Methods)
	}
	var logged []string
	c = New(Config{
		Dial:    refuseDial,
		Methods: []crawlrun.Method{crawlrun.MethodSNMP, crawlrun.MethodSSH},
		Log:     func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) },
	})
	if len(c.cfg.Methods) != 1 || c.cfg.Methods[0] != crawlrun.MethodSSH {
		t.Fatalf("methods = %v, want [ssh] with snmp dropped", c.cfg.Methods)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], `"snmp"`) {
		t.Fatalf("dropping snmp should be logged once, got %q", logged)
	}

	c = New(Config{})
	d, m := c.crawlOne(context.Background(), item{target: "lab-r1", identity: "lab-r1"})
	if !d.Failed || m != "" {
		t.Fatalf("no methods at all: device %+v method %q", d, m)
	}
}

// A device reached by address is named from sysName and the rename is
// reported, as it is for one named from its prompt.
func TestSNMPDeviceReachedByAddressIsRenamed(t *testing.T) {
	fake := &snmpFake{devices: map[string]*topo.Device{
		"192.0.2.9": {SysName: "lab-r9", Platform: "cisco_ios"},
	}}
	run := crawlrun.New()
	c := New(Config{SNMP: fake.collect, Methods: []crawlrun.Method{crawlrun.MethodSNMP}, Emit: run.Emit()})
	c.resolver = stubResolver{}
	d := c.Crawl([]string{"192.0.2.9"})[0]
	run.Finish()

	if d.Hostname != "lab-r9" {
		t.Fatalf("hostname = %q, want lab-r9", d.Hostname)
	}
	var renamed bool
	for _, e := range run.Decisions() {
		renamed = renamed || (e.Kind == crawlrun.KindRenamed && e.Name == "lab-r9")
	}
	if !renamed {
		t.Fatal("rename was not reported")
	}
}

// A neighbor whose DNS record is stale: SNMP fails at the name with a network
// error, succeeds at the address the neighbor reported, and the node keeps
// the name it was claimed under rather than the address it answered on.
func TestSNMPRetriesAtReportedAddress(t *testing.T) {
	unreachable := fmt.Errorf("error establishing connection to host: %w",
		&net.OpError{Op: "dial", Net: "udp", Err: errors.New("no route to host")})
	fake := &snmpFake{
		devices: map[string]*topo.Device{
			"lab-r1": {SysName: "lab-r1", Platform: "arista_eos",
				Neighbors: []topo.Neighbor{lldpEdge("Ethernet1", "lab-r2", "Ethernet1", "192.0.2.2")}},
			"192.0.2.2": {SysName: "lab-r2", Platform: "arista_eos",
				Neighbors: []topo.Neighbor{lldpEdge("Ethernet1", "lab-r1", "Ethernet1", "192.0.2.1")}},
		},
		fail: map[string]error{"lab-r2": unreachable},
	}
	run := crawlrun.New()
	c := New(Config{SNMP: fake.collect, Methods: []crawlrun.Method{crawlrun.MethodSNMP},
		MaxDepth: 1, Emit: run.Emit()})
	c.resolver = stubResolver{forward: map[string][]string{
		"lab-r1": {"192.0.2.1"}, "lab-r2": {"198.51.100.2"}, // stale
	}}
	devices := c.Crawl([]string{"lab-r1"})
	run.Finish()

	var r2 *topo.Device
	for _, d := range devices {
		if d.SysName == "lab-r2" {
			r2 = d
		}
	}
	if r2 == nil || r2.Failed {
		t.Fatalf("lab-r2 not reached: %+v (targets %v)", r2, fake.saw())
	}
	if r2.Hostname != "lab-r2" {
		t.Errorf("hostname = %q, want the claimed name lab-r2", r2.Hostname)
	}
	saw := strings.Join(fake.saw(), " ")
	if !strings.Contains(saw, "lab-r2") || !strings.Contains(saw, "192.0.2.2") {
		t.Errorf("expected a try at the name then the address, saw %v", fake.saw())
	}
	var retried bool
	for _, e := range run.Decisions() {
		retried = retried || (e.Kind == crawlrun.KindRetryAddr && e.Identity == "lab-r2")
	}
	if !retried {
		t.Error("retry by address was not reported")
	}
}

// What a front end shows beside a device while it works: the phases arrive
// in order, and the row carries none once the device is done.
func TestPhasesAreReportedInOrder(t *testing.T) {
	fake := &snmpFake{}
	run := crawlrun.New()
	var phases []string
	run.Tap(func(ev crawlrun.Event) {
		if ev.Kind == crawlrun.KindPhase && ev.Identity == "lab-r1" {
			phases = append(phases, ev.Detail)
		}
	})
	c := New(Config{
		Dial: eosWithNeighbor(t, "lab-r1"), SNMP: fake.collect,
		Methods: []crawlrun.Method{crawlrun.MethodSNMP, crawlrun.MethodSSH},
		Emit:    run.Emit(),
	})
	c.resolver = stubResolver{forward: map[string][]string{"lab-r1": {"192.0.2.1"}}}
	c.Crawl([]string{"lab-r1"})
	run.Finish()

	want := []string{"snmp probe", "ssh dial", "ssh fingerprint", "ssh: show lldp neighbors detail", "dns fill"}
	i := 0
	for _, p := range phases {
		if i < len(want) && p == want[i] {
			i++
		}
	}
	if i != len(want) {
		t.Fatalf("phases %q do not contain %q in order", phases, want)
	}
	if row := rowFor(t, run, "lab-r1"); row.Phase != "" || row.State != crawlrun.StateReached {
		t.Fatalf("finished row: state=%s phase=%q", row.State, row.Phase)
	}
}

// A device's outcome is reported the moment it finishes, not when its batch
// does. The slow device's collector waits for its batch-mate's reached event:
// held until the batch ended, that event could not arrive, and the wait
// times out.
func TestReachedIsReportedWhenTheDeviceFinishesNotTheBatch(t *testing.T) {
	run := crawlrun.New()
	fastDone := make(chan struct{})
	var once sync.Once
	run.Tap(func(ev crawlrun.Event) {
		if ev.Kind == crawlrun.KindReached && ev.Identity == "lab-fast" {
			once.Do(func() { close(fastDone) })
		}
	})
	var waitedOut bool
	collect := func(ctx context.Context, dt DialTarget) (*topo.Device, error) {
		if dt.Target == "lab-slow" {
			select {
			case <-fastDone:
			case <-time.After(3 * time.Second):
				waitedOut = true
			}
		}
		return &topo.Device{Hostname: dt.Target, SysName: dt.Target, Platform: "arista_eos"}, nil
	}
	c := New(Config{SNMP: collect, Methods: []crawlrun.Method{crawlrun.MethodSNMP},
		Concurrency: 2, Emit: run.Emit()})
	c.resolver = stubResolver{forward: map[string][]string{
		"lab-fast": {"192.0.2.1"}, "lab-slow": {"192.0.2.2"},
	}}
	c.Crawl([]string{"lab-slow", "lab-fast"})
	run.Finish()

	if waitedOut {
		t.Fatal("lab-fast's reached event was held until its batch finished")
	}
	for _, id := range []string{"lab-fast", "lab-slow"} {
		if r := rowFor(t, run, id); r.State != crawlrun.StateReached {
			t.Errorf("%s: %s", id, r.State)
		}
	}
}

func TestNextTargetNeverDialsLoopback(t *testing.T) {
	cases := []struct {
		name, ip     string
		target, addr string
		ok           bool
	}{
		{"localhost", "192.0.2.50", "192.0.2.50", "192.0.2.50", true},
		{"localhost", "", "", "", false},
		{"localhost", "127.0.0.1", "", "", false},
		{"lab-r1", "127.0.0.1", "lab-r1", "", true}, // a real name keeps; the loopback address does not
		{"127.0.0.1", "", "", "", false},
	}
	for _, c := range cases {
		target, addr, ok := nextTarget(topo.Neighbor{RemoteDevice: c.name, RemoteIP: c.ip}, nopLog)
		if target != c.target || addr != c.addr || ok != c.ok {
			t.Errorf("nextTarget(%q, %q) = %q, %q, %v; want %q, %q, %v",
				c.name, c.ip, target, addr, ok, c.target, c.addr, c.ok)
		}
	}
}

func nopLog(string, ...any) {}

// Two boxes left at hostname "localhost" behind one device: each is dialed at
// its own address, never at the crawler's host, each is its own node, and the
// links from the device land on them.
func TestLocalhostNeighborsAreDialedByAddressAndStayDistinct(t *testing.T) {
	fake := &snmpFake{devices: map[string]*topo.Device{
		"lab-r1": {SysName: "lab-r1", Platform: "arista_eos", Neighbors: []topo.Neighbor{
			lldpEdge("Ethernet1", "localhost", "eth0", "192.0.2.50"),
			lldpEdge("Ethernet2", "localhost", "eth0", "192.0.2.51"),
			lldpEdge("Ethernet3", "localhost", "eth0", ""),
		}},
		"192.0.2.50": {SysName: "localhost", Platform: "linux",
			Neighbors: []topo.Neighbor{lldpEdge("eth0", "lab-r1", "Ethernet1", "")}},
		"192.0.2.51": {SysName: "localhost", Platform: "linux",
			Neighbors: []topo.Neighbor{lldpEdge("eth0", "lab-r1", "Ethernet2", "")}},
	}}
	run := crawlrun.New()
	c := New(Config{SNMP: fake.collect, Methods: []crawlrun.Method{crawlrun.MethodSNMP},
		MaxDepth: 1, Emit: run.Emit()})
	c.resolver = stubResolver{forward: map[string][]string{
		"lab-r1": {"192.0.2.1"}, "localhost": {"127.0.0.1"},
	}}
	devices := c.Crawl([]string{"lab-r1"})
	run.Finish()

	for _, target := range fake.saw() {
		if target == "localhost" || strings.HasPrefix(target, "127.") {
			t.Fatalf("dialed %q (saw %v)", target, fake.saw())
		}
	}
	m := topo.Generate(devices, topo.Options{})
	if _, merged := m["localhost"]; merged {
		t.Fatalf("a node named localhost exists: %v", m["localhost"])
	}
	for _, ip := range []string{"192.0.2.50", "192.0.2.51"} {
		if _, ok := m[ip]; !ok {
			t.Errorf("no node %s; nodes: %v", ip, keys(m))
			continue
		}
		if len(m["lab-r1"].Peers[ip].Connections) != 1 {
			t.Errorf("lab-r1 -> %s: %+v", ip, m["lab-r1"].Peers[ip])
		}
	}
	for _, e := range run.Decisions() {
		if e.Kind == crawlrun.KindRenamed && e.Name == "localhost" {
			t.Errorf("reported a rename to localhost: %+v", e)
		}
	}
}

func keys(m map[string]topo.MapNode) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
