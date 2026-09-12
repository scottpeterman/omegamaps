// internal/tfsm/concurrency_test.go
//
// The regression test for the crash that produced load()'s cache-the-source
// design: two devices at the same crawl depth parsing the same template at
// once, panicking with "concurrent map iteration and map write" inside
// gotextfsm's appendRecord.
//
// The failure is not in this package's code. It is in gotextfsm, which keeps
// per-parse working state inside the template's own Values map — so a
// TextFSM is scratch space, not a reusable compiled artifact, and sharing
// one across goroutines races however you hold the pointer. That is exactly
// the kind of thing a future reader "optimizes": caching the parsed template
// instead of the source looks like an obvious win and reintroduces the
// crash. This test is what stops that, so it needs -race to mean anything.
package tfsm

import (
	"strings"
	"sync"
	"testing"
)

// iosLLDPDetail is one neighbor block in the shape cisco_ios lldp detail
// emits. Lab names throughout.
const iosLLDPDetail = `------------------------------------------------
Local Intf: Gi0/1
Chassis id: 0c1d.5e2f.0001
Port id: Ethernet1
Port Description: to lab-r1
System Name: lab-spine-1

System Description:
Arista Networks EOS version 4.33.1F

Time remaining: 97 seconds
System Capabilities: B,R
Enabled Capabilities: R
Management Addresses:
    IP: 172.16.2.2
Auto Negotiation - not supported
Physical media capabilities - not advertised
Media Attachment Unit type - not advertised
Vlan ID: - not advertised

Total entries displayed: 1
`

// The crash needed two goroutines to reach the parse step at the same
// moment on the same template. Reproduce that deliberately: a start barrier
// so the parses overlap rather than merely being concurrent on paper.
func TestParseIsSafeUnderConcurrentUseOfOneTemplate(t *testing.T) {
	const goroutines = 32

	var (
		start sync.WaitGroup
		done  sync.WaitGroup
	)
	start.Add(1)
	errs := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			recs, err := Parse("cisco_ios", "lldp_detail", iosLLDPDetail)
			if err != nil {
				errs <- err
				return
			}
			// Assert the parse actually produced the neighbor. A race
			// that silently drops records would otherwise pass, and a
			// silently empty parse is how a crawl loses an edge.
			if len(recs) != 1 {
				t.Errorf("got %d records, want 1", len(recs))
				return
			}
			if got := recs[0]["NEIGHBOR"]; got != "" && !strings.Contains(got, "lab-spine-1") {
				t.Errorf("neighbor = %q, want lab-spine-1", got)
			}
		}()
	}

	start.Done()
	done.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("parse: %v", err)
	}
}

// Different templates in flight at once is the other half of a depth batch:
// a crawl rarely finds one platform. This also exercises the cache's write
// path from several goroutines, which the single-template test does not
// once the entry is warm.
func TestParseIsSafeAcrossDifferentTemplatesAtOnce(t *testing.T) {
	cases := []struct{ platform, key string }{
		{"cisco_ios", "lldp_detail"},
		{"cisco_ios", "cdp_detail"},
		{"cisco_nxos", "lldp_detail"},
		{"arista_eos", "lldp_detail"},
		{"juniper_junos", "lldp_detail"},
	}

	var (
		start sync.WaitGroup
		done  sync.WaitGroup
	)
	start.Add(1)

	for i := 0; i < 40; i++ {
		tc := cases[i%len(cases)]
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			// Output shape does not match every template here, and that
			// is fine: zero records is a legitimate result. What must
			// not happen is a panic or an error, either of which means
			// the templates are being shared.
			if _, err := Parse(tc.platform, tc.key, iosLLDPDetail); err != nil {
				t.Errorf("%s/%s: %v", tc.platform, tc.key, err)
			}
		}()
	}

	start.Done()
	done.Wait()
}

// The cache holds source text. If someone changes it to hold parsed
// templates, this fails to compile — which is the point: the design
// constraint becomes a build error rather than an intermittent panic under
// load that only shows up on a wide crawl.
func TestCacheHoldsSourceTextNotParsedTemplates(t *testing.T) {
	if _, err := Parse("cisco_ios", "lldp_detail", iosLLDPDetail); err != nil {
		t.Fatalf("warm the cache: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(cache) == 0 {
		t.Fatal("cache is empty after a successful parse")
	}
	for file, src := range cache {
		// A compile error here means the cache type changed. A runtime
		// failure means it still holds strings but not template source.
		if !strings.Contains(src, "Value") || !strings.Contains(src, "Start") {
			t.Errorf("cache[%q] does not look like TextFSM source", file)
		}
	}
}

func TestLookupMissIsAnError(t *testing.T) {
	if _, err := Parse("nonexistent_platform", "lldp", ""); err == nil {
		t.Error("unknown platform returned no error")
	}
	if _, err := Parse("cisco_ios", "nonexistent_key", ""); err == nil {
		t.Error("unknown command key returned no error")
	}
}
