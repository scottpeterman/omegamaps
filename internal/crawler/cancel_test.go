// internal/crawler/cancel_test.go
//
// A stop button that does not stop is worse than no stop button, so these test
// the behavior rather than the plumbing: that cancelling ends the crawl
// promptly, that devices never attempted are reported rather than dropped, and
// that a dial already in flight is the only thing a stop has to wait for.
package crawler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/sshcore"
)

// blockingDial stalls until its context is cancelled, counting how many
// devices actually reached the dial layer.
func blockingDial(started *atomic.Int32, release <-chan struct{}) DialFunc {
	return func(ctx context.Context, t DialTarget) (*sshcore.Client, error) {
		started.Add(1)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return nil, errors.New("released")
		}
	}
}

func TestCancelStopsACrawlInFlight(t *testing.T) {
	var started atomic.Int32
	release := make(chan struct{})
	defer close(release)

	c := New(Config{
		Dial:        blockingDial(&started, release),
		MaxDepth:    3,
		Concurrency: 2,
		Log:         func(string, ...any) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []string, 1)
	go func() {
		devs := c.CrawlContext(ctx, []string{
			"lab-r1.lab.example", "lab-r2.lab.example",
			"lab-r3.lab.example", "lab-r4.lab.example",
		})
		names := make([]string, 0, len(devs))
		for _, d := range devs {
			names = append(names, d.Hostname)
		}
		done <- names
	}()

	// Let the first two claim the two worker slots.
	waitFor(t, func() bool { return started.Load() == 2 })
	cancel()

	select {
	case names := <-done:
		// Every seed is accounted for: two were attempted, two were queued
		// behind the semaphore and abandoned. Neither may vanish.
		if len(names) != 4 {
			t.Errorf("crawl returned %d devices, want all 4 seeds accounted for: %v",
				len(names), names)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("crawl did not stop within 5s of cancel")
	}

	// The two that never got a slot must not have dialed.
	if got := started.Load(); got != 2 {
		t.Errorf("%d devices reached the dial layer, want 2", got)
	}
}

func TestCancelledDevicesCarryAReason(t *testing.T) {
	var started atomic.Int32
	release := make(chan struct{})
	defer close(release)

	c := New(Config{
		Dial:        blockingDial(&started, release),
		Concurrency: 1,
		Log:         func(string, ...any) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan []*deviceSummary, 1)
	go func() {
		devs := c.CrawlContext(ctx, []string{"lab-r1.lab.example", "lab-r2.lab.example"})
		out := make([]*deviceSummary, 0, len(devs))
		for _, d := range devs {
			out = append(out, &deviceSummary{d.Hostname, d.Failed, d.FailedWhy})
		}
		done <- out
	}()

	waitFor(t, func() bool { return started.Load() == 1 })
	cancel()

	select {
	case got := <-done:
		for _, d := range got {
			if !d.failed {
				t.Errorf("%s reported as succeeding after a cancel", d.name)
			}
			// A blank reason is the failure mode this guards: an abandoned
			// device that looks identical to one that was tried and refused.
			if d.why == "" {
				t.Errorf("%s failed with no reason given", d.name)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("crawl did not stop within 5s of cancel")
	}
}

// A context already cancelled must not open a single connection.
func TestPreCancelledContextDialsNothing(t *testing.T) {
	var started atomic.Int32
	release := make(chan struct{})
	defer close(release)

	c := New(Config{
		Dial:        blockingDial(&started, release),
		Concurrency: 4,
		Log:         func(string, ...any) {},
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	devs := c.CrawlContext(ctx, []string{"lab-r1.lab.example", "lab-r2.lab.example"})
	if got := started.Load(); got != 0 {
		t.Errorf("%d dials attempted under a cancelled context, want 0", got)
	}
	if len(devs) != 2 {
		t.Errorf("returned %d devices, want both seeds accounted for", len(devs))
	}
}

// Crawl without a context still behaves as it always did.
func TestCrawlWithoutContextIsUnchanged(t *testing.T) {
	var started atomic.Int32
	release := make(chan struct{})
	close(release) // dial returns immediately

	c := New(Config{
		Dial:        blockingDial(&started, release),
		Concurrency: 2,
		Log:         func(string, ...any) {},
	})

	devs := c.Crawl([]string{"lab-r1.lab.example"})
	if len(devs) != 1 {
		t.Fatalf("returned %d devices, want 1", len(devs))
	}
	if started.Load() != 1 {
		t.Errorf("dial was not attempted")
	}
}

type deviceSummary struct {
	name   string
	failed bool
	why    string
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met within 5s")
}
