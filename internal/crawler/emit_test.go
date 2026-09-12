// internal/crawler/emit_test.go
//
// The emitter is only worth anything if a real crawl populates a run. These
// drive the crawler and assert on the resulting table rather than on the
// events, because the table is what a person ends up looking at.
package crawler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/sshcore"
)

func TestCrawlPopulatesARun(t *testing.T) {
	run := crawlrun.New()
	c := New(Config{
		Dial: func(ctx context.Context, tgt DialTarget) (*sshcore.Client, error) {
			return nil, errors.New("dial: connection refused")
		},
		Concurrency: 2,
		Log:         func(string, ...any) {},
		Emit:        run.Emit(),
	})
	c.Crawl([]string{"lab-r1.lab.example", "lab-r2.lab.example"})
	run.Finish()

	rows := run.Rows()
	if len(rows) != 2 {
		t.Fatalf("run holds %d rows, want 2", len(rows))
	}
	c2 := run.Counts()
	if c2.Failed != 2 {
		t.Errorf("failed = %d, want 2", c2.Failed)
	}
	for _, r := range rows {
		if r.Detail == "" {
			t.Errorf("%s failed with no reason in the table", r.Display())
		}
		if r.State != crawlrun.StateFailed {
			t.Errorf("%s state = %s", r.Display(), r.State)
		}
	}
}

// A nil Emit must not change behavior — that is what keeps the CLI unaffected.
func TestNilEmitIsHarmless(t *testing.T) {
	c := New(Config{
		Dial: func(ctx context.Context, tgt DialTarget) (*sshcore.Client, error) {
			return nil, errors.New("dial: connection refused")
		},
		Log: func(string, ...any) {},
	})
	if got := len(c.Crawl([]string{"lab-r1.lab.example"})); got != 1 {
		t.Errorf("returned %d devices, want 1", got)
	}
}

// Two bugs shipped together and hid each other, so this pins both.
//
// The first: terminal events keyed on d.Hostname while queue and progress
// events keyed on the claim identity. With a domain suffix configured those
// differ, so one device produced two rows — a queued one that never finished
// and a failed one that appeared from nowhere.
//
// The second: nothing emitted KindReached at all, because emits were paired
// with log lines and the crawler has no log line for success. Every device
// that worked stayed mid-flight until Finish swept it into a failure reading
// "run ended before this device completed".
func TestEveryDeviceGetsExactlyOneRowAndARealReason(t *testing.T) {
	run := crawlrun.New()
	c := New(Config{
		Dial: func(ctx context.Context, tgt DialTarget) (*sshcore.Client, error) {
			return nil, errors.New("dial: connection refused")
		},
		// A suffix is what makes identity and Hostname diverge.
		Domains:     []string{"lab.example"},
		Concurrency: 2,
		Log:         func(string, ...any) {},
		Emit:        run.Emit(),
	})
	c.Crawl([]string{"lab-r1.lab.example", "lab-r2.lab.example"})
	run.Finish()

	rows := run.Rows()
	if len(rows) != 2 {
		names := make([]string, 0, len(rows))
		for _, r := range rows {
			names = append(names, r.Display()+"="+r.State.String())
		}
		t.Fatalf("2 devices produced %d rows: %v", len(rows), names)
	}
	for _, r := range rows {
		if r.Detail == "run ended before this device completed" {
			t.Errorf("%s was swept by Finish; no terminal event ever reached it",
				r.Display())
		}
		if r.State != crawlrun.StateFailed {
			t.Errorf("%s state = %s, want failed", r.Display(), r.State)
		}
	}
}

// A cancelled crawl must say so per device, not leave them to the sweep.
func TestCancelledDevicesReportTheirOwnReason(t *testing.T) {
	run := crawlrun.New()
	block := make(chan struct{})
	defer close(block)
	c := New(Config{
		Dial: func(ctx context.Context, tgt DialTarget) (*sshcore.Client, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		Concurrency: 1,
		Log:         func(string, ...any) {},
		Emit:        run.Emit(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	c.CrawlContext(ctx, []string{"lab-r1.lab.example", "lab-r2.lab.example"})
	run.Finish()

	for _, r := range run.Rows() {
		if r.Detail == "" || r.Detail == "run ended before this device completed" {
			t.Errorf("%s has no reason of its own: %q", r.Display(), r.Detail)
		}
	}
}
