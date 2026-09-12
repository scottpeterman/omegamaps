// internal/crawldial/build_test.go
//
// Build is where the two front ends meet, so what matters is that the same
// parameters produce the same crawl no matter who asked.
package crawldial

import (
	"context"
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/crawler"
	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/snmpprobe"
)

func labParams() crawlrun.Params {
	p := crawlrun.Defaults()
	p.Seeds = []string{"172.16.1.2"}
	p.Domains = []string{"lab.local"}
	return p
}

func TestBuildRefusesInvalidParamsWithEveryReason(t *testing.T) {
	p := labParams()
	p.Seeds = nil
	p.Concurrency = 0
	p.Timeout = 0

	_, err := Build(p, Options{})
	if err == nil {
		t.Fatal("invalid parameters were accepted")
	}
	// A form marks all the bad fields at once; a CLI prints them all. Either
	// way one-at-a-time is the wrong shape.
	for _, want := range []string{"seeds", "concurrency", "timeout"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s: %v", want, err)
		}
	}
}

func TestBuildWithoutAVaultUsesStaticCredentials(t *testing.T) {
	b, err := Build(labParams(), Options{
		Static: StaticCreds{Username: "admin", Password: "lab"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	if b.Crawler == nil {
		t.Fatal("no crawler built")
	}
	// No vault means no binding store and no resolver; the fold must then be
	// a no-op rather than a panic.
	if b.Bindings != nil || b.Resolver != nil {
		t.Errorf("static mode produced a resolver or bindings: %+v", b)
	}
	Fold(b.Bindings, nil, []string{"lab.local"}, nil)
}

func TestBuildRefusesInsecureHostKeys(t *testing.T) {
	p := labParams()
	p.HostKeys = "insecure"
	if _, err := Build(p, Options{}); err == nil {
		t.Error("insecure host-key mode was accepted through Build")
	}
}

// The two front ends must not drift: identical parameters have to yield
// identical crawl behavior, so defaults are shared rather than restated.
func TestDefaultsSurviveBuild(t *testing.T) {
	p := labParams()
	if p.Depth != 3 || p.Concurrency != 5 || p.Timeout != 30*time.Second {
		t.Fatalf("defaults changed: %+v", p)
	}
	if p.HostKeys != crawlrun.HostKeyTOFU {
		t.Errorf("default host key mode = %q, want tofu", p.HostKeys)
	}
	if _, err := Build(p, Options{Static: StaticCreds{Username: "admin"}}); err != nil {
		t.Fatalf("defaults did not build: %v", err)
	}
}

// A dropped option is invisible in the output — you do not see the links that
// were not drawn — so this fails whenever topo.Options grows a field that
// MapOptions does not set.
func TestMapOptionsCoversEveryTopoOption(t *testing.T) {
	p := crawlrun.Defaults()
	p.Seeds = []string{"172.16.1.2"}
	p.Domains = []string{"lab.local"}
	p.TrustUnidirectional = true

	got := reflect.ValueOf(MapOptions(p))
	for i := 0; i < got.NumField(); i++ {
		f := got.Type().Field(i)
		if got.Field(i).IsZero() {
			t.Errorf("topo.Options.%s is not derived from Params; "+
				"a map option that silently defaults changes the map "+
				"without changing anything you can see", f.Name)
		}
	}
}

// Unidirectional claims are what let an uncrawled box — a Linux server, a
// printer, anything without SSH — appear as a leaf on the strength of its
// neighbor's word alone.
func TestTrustUnidirectionalReachesTheMap(t *testing.T) {
	p := crawlrun.Defaults()
	p.Seeds = []string{"172.16.1.2"}
	if MapOptions(p).TrustUnidirectional {
		t.Error("default should be off, matching the CLI")
	}
	p.TrustUnidirectional = true
	if !MapOptions(p).TrustUnidirectional {
		t.Error("the option never reached topo.Options")
	}
}

// A run with no credentials at all must be refused rather than dialed. Without
// this, a locked vault and an empty credentials tab sends the crawler at every
// device offering nothing, and every one comes back "no handler for
// keyboard-interactive question" — which reads as a network problem rather
// than as a missing unlock.
func TestBuildRefusesARunWithNoCredentials(t *testing.T) {
	if _, err := Build(labParams(), Options{}); err == nil {
		t.Fatal("a run with no vault and no static credentials was accepted")
	}
}

// A username alone IS a credential when an agent is loaded, and the auth chain
// tries the agent first. Rejecting it would make agent-only crawling
// impossible.
func TestBuildAcceptsAUsernameAloneForAgentAuth(t *testing.T) {
	b, err := Build(labParams(), Options{Static: StaticCreds{Username: "admin"}})
	if err != nil {
		t.Fatalf("username-only build refused: %v", err)
	}
	b.Close()
}

func snmpParams(methods ...crawlrun.Method) crawlrun.Params {
	p := labParams()
	p.Methods = methods
	return p
}

// An SNMP-only run attempts no login, so it must not be refused for lacking
// one -- and must not open a vault it has no use for: a profile that still
// names one would otherwise stop the run at a master-password prompt.
func TestBuildSNMPOnlyNeedsNoSSHCredentialsOrVault(t *testing.T) {
	p := snmpParams(crawlrun.MethodSNMP)
	p.VaultPath = "/nonexistent/omegamaps-test/vault.json"
	b, err := Build(p, Options{SNMP: []snmpprobe.Credential{{Community: "lab"}}})
	if err != nil {
		t.Fatalf("snmp-only build refused: %v", err)
	}
	defer b.Close()
	if b.Crawler == nil || b.Resolver != nil || b.Bindings != nil {
		t.Fatalf("snmp-only build: %+v", b)
	}
}

func TestBuildRefusesSNMPWithoutCredentials(t *testing.T) {
	_, err := Build(snmpParams(crawlrun.MethodSNMP, crawlrun.MethodSSH),
		Options{Static: StaticCreds{Username: "admin"}})
	if err == nil || !strings.Contains(err.Error(), "no SNMP credentials") {
		t.Fatalf("want a missing-SNMP-credentials refusal, got %v", err)
	}
}

// SNMP credentials do not stand in for SSH ones: with ssh in the methods, a
// run that could not log in anywhere is refused exactly as before.
func TestBuildStillRefusesSSHWithoutCredentialsWhenSNMPIsFirst(t *testing.T) {
	_, err := Build(snmpParams(crawlrun.MethodSNMP, crawlrun.MethodSSH),
		Options{SNMP: []snmpprobe.Credential{{Community: "lab"}}})
	if err == nil || !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("want the SSH no-credentials refusal, got %v", err)
	}
}

func TestBuildRefusesUnknownMethod(t *testing.T) {
	_, err := Build(snmpParams("telnet"), Options{Static: StaticCreds{Username: "admin"}})
	if err == nil || !strings.Contains(err.Error(), "methods") {
		t.Fatalf("want a methods validation error, got %v", err)
	}
}

// A seed's port is SSH's. Left on, it reaches gosnmp as part of the host and
// fails at connect on a malformed address; stripped, the probe reaches the
// agent's port and fails at the system GET because nothing is listening.
func TestSNMPFuncStripsTheSSHPort(t *testing.T) {
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(c.LocalAddr().(*net.UDPAddr).Port)
	c.Close()

	fn := NewSNMPFunc([]snmpprobe.Credential{{Community: "lab"}},
		snmpprobe.Options{Port: port, Timeout: 200 * time.Millisecond, Retries: -1})
	d, _ := fn(context.Background(), crawler.DialTarget{Target: "127.0.0.1:2222"})
	if !d.Failed || !strings.HasPrefix(d.FailedWhy, "snmp system:") {
		t.Fatalf("FailedWhy = %q, want a system-stage failure at the SNMP port", d.FailedWhy)
	}
	if d.Hostname != "127.0.0.1" {
		t.Fatalf("probed %q, want 127.0.0.1", d.Hostname)
	}
}
