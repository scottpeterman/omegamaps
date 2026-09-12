// internal/crawler/parent_test.go
//
// Every device except a seed was reported by some other device. Carrying that
// answers "where did this row come from", which is the first thing an
// unexpected row provokes, and it is the same parentage the jump-host wiring
// needs — a device found behind a bastion is reachable the way its parent was.
package crawler

import (
	"context"
	"errors"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/sshcore"
)

func TestSeedsHaveNoParent(t *testing.T) {
	run := crawlrun.New()
	c := New(Config{
		Dial: func(ctx context.Context, tgt DialTarget) (*sshcore.Client, error) {
			return nil, errors.New("dial: connection refused")
		},
		Log:  func(string, ...any) {},
		Emit: run.Emit(),
	})
	c.Crawl([]string{"lab-r1.lab.example"})
	run.Finish()

	rows := run.Rows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows", len(rows))
	}
	if rows[0].Via != "" {
		t.Errorf("a seed reported a parent: %q", rows[0].Via)
	}
}

func TestAdmittedNeighborRemembersWhoReportedIt(t *testing.T) {
	c := New(Config{Log: func(string, ...any) {}})

	it, ok := c.admit("route-server", "", 4, "usa-leaf-1")
	if !ok {
		t.Fatal("neighbor was not admitted")
	}
	if it.parent != "usa-leaf-1" {
		t.Errorf("parent = %q, want usa-leaf-1", it.parent)
	}
	if it.depth != 4 {
		t.Errorf("depth = %d, want 4", it.depth)
	}
}

// A device several neighbors report keeps its first reporter, so the value
// does not flip between runs and turn the comparison tab into noise.
func TestFirstReporterWins(t *testing.T) {
	run := crawlrun.New()
	e := run.Emit()
	e.Send(crawlrun.Event{Kind: crawlrun.KindQueued, Identity: "eng-leaf-1",
		Depth: 2, Via: "eng-spine-1"})
	e.Send(crawlrun.Event{Kind: crawlrun.KindQueued, Identity: "eng-leaf-1",
		Depth: 2, Via: "eng-spine-2"})
	e.Send(crawlrun.Event{Kind: crawlrun.KindReached, Identity: "eng-leaf-1"})
	run.Finish()

	rows := run.Rows()
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Via != "eng-spine-1" {
		t.Errorf("via = %q, want the first reporter", rows[0].Via)
	}
}

// A neighbor that is deliberately never dialed still records who claimed it —
// that is the row where the question matters most, since nothing else about
// the device will ever be learned.
func TestNotDialedNeighborsStillNameTheirReporter(t *testing.T) {
	run := crawlrun.New()
	e := run.Emit()
	e.Send(crawlrun.Event{Kind: crawlrun.KindNotDialed, Identity: "ix-peer-1.example.net",
		Via: "wan-core-1", Detail: "outside allowed domains"})
	run.Finish()

	rows := run.Rows()
	if len(rows) != 1 || rows[0].Via != "wan-core-1" {
		t.Fatalf("via lost on a not-dialed row: %+v", rows)
	}
	var described bool
	for _, ev := range run.Decisions() {
		if ev.Kind == crawlrun.KindNotDialed {
			described = true
			if got := ev.Describe(); got == "" ||
				!contains(got, "wan-core-1") {
				t.Errorf("decision line does not name the reporter: %q", got)
			}
		}
	}
	if !described {
		t.Error("not-dialed event missing from decisions")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
