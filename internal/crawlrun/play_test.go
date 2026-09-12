package crawlrun

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// recordDemo captures the demo crawl as a recorded stream would hold it.
func recordDemo(t *testing.T) []Event {
	t.Helper()
	run := New()
	var events []Event
	run.Tap(func(ev Event) { events = append(events, ev) })
	Demo(run, DemoOptions{})
	run.Finish()
	if len(events) == 0 {
		t.Fatal("demo recorded nothing")
	}
	return events
}

func waitFinished(t *testing.T, r *Run, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !r.Progress().Finished {
		if time.Now().After(deadline) {
			t.Fatalf("run not finished after %v", within)
		}
		time.Sleep(time.Millisecond)
	}
}

// The point of Play is that a view sees the same run Replay builds, only
// spread over time. If the two disagree about any row, a view built against
// Play is being built against something the real run never was.
func TestPlayEndsWhereReplayDoes(t *testing.T) {
	events := recordDemo(t)
	want := Replay(events)

	got := New()
	Play(context.Background(), got, events, 0)
	waitFinished(t, got, 5*time.Second)

	if w, g := want.Counts(), got.Counts(); w != g {
		t.Fatalf("counts differ:\n replay %+v\n play   %+v", w, g)
	}
	wr, gr := want.Rows(), got.Rows()
	if len(wr) != len(gr) {
		t.Fatalf("rows: replay %d, play %d", len(wr), len(gr))
	}
	for i := range wr {
		w, g := wr[i], gr[i]
		if w.Identity != g.Identity || w.State != g.State || w.Depth != g.Depth ||
			w.Name != g.Name || w.Method != g.Method || w.Neighbors != g.Neighbors {
			t.Errorf("row %d differs:\n replay %+v\n play   %+v", i, w, g)
		}
	}
	if len(want.Decisions()) != len(got.Decisions()) {
		t.Errorf("decisions: replay %d, play %d", len(want.Decisions()), len(got.Decisions()))
	}
}

// Timestamps land on the present and keep the recorded spacing divided by
// the speed; a device that took 400ms at 10x shows 40ms.
func TestPlayRestampsAndScales(t *testing.T) {
	then := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{At: then, Kind: KindDepth, Depth: 0},
		{At: then, Kind: KindQueued, Identity: "a", Depth: 0},
		{At: then.Add(10 * time.Millisecond), Kind: KindPhase, Identity: "a", Detail: "ssh dial"},
		{At: then.Add(400 * time.Millisecond), Kind: KindReached, Identity: "a", Name: "a", Method: MethodSSH},
	}

	began := time.Now()
	r := New()
	Play(context.Background(), r, events, 10)
	waitFinished(t, r, 5*time.Second)
	took := time.Since(began)

	if took < 35*time.Millisecond {
		t.Errorf("finished in %v; 400ms at 10x should take about 40ms", took)
	}
	rows := r.Rows()
	if len(rows) != 1 || rows[0].State != StateReached {
		t.Fatalf("rows = %+v", rows)
	}
	d := rows[0].Duration()
	if d < 30*time.Millisecond || d > 60*time.Millisecond {
		t.Errorf("duration %v; want the recorded 400ms scaled to about 40ms", d)
	}
	if rows[0].FirstSeen.Before(began.Add(-time.Second)) {
		t.Errorf("first seen %v was not restamped onto the present", rows[0].FirstSeen)
	}
}

// Speed zero keeps the recorded spacing in the timestamps, so a finished run
// reports the recording's real durations and elapsed time.
func TestPlayInstantKeepsRecordedDurations(t *testing.T) {
	then := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{At: then, Kind: KindQueued, Identity: "a"},
		{At: then.Add(3 * time.Second), Kind: KindReached, Identity: "a", Name: "a"},
	}
	r := New()
	Play(context.Background(), r, events, 0)
	waitFinished(t, r, time.Second)
	if d := r.Rows()[0].Duration(); d != 3*time.Second {
		t.Errorf("duration %v, want the recorded 3s", d)
	}
	if e := r.Progress().Elapsed; e != 3*time.Second {
		t.Errorf("elapsed %v, want the recorded 3s", e)
	}
}

// A cancelled replay stops delivering and ends in-flight devices the way a
// cancelled crawl does, rather than leaving them running forever.
func TestPlayCancelFinishesTheRun(t *testing.T) {
	then := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	events := []Event{
		{At: then, Kind: KindQueued, Identity: "a"},
		{At: then, Kind: KindPhase, Identity: "a", Detail: "ssh dial"},
		{At: then.Add(time.Hour), Kind: KindReached, Identity: "a", Name: "a"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := New()
	Play(ctx, r, events, 1)

	deadline := time.Now().Add(time.Second)
	for len(r.Rows()) == 0 || r.Rows()[0].State != StateRunning {
		if time.Now().After(deadline) {
			t.Fatal("first events never arrived")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	waitFinished(t, r, time.Second)

	row := r.Rows()[0]
	if row.State != StateFailed || row.Detail == "" {
		t.Errorf("cancelled device = %+v; want failed with a reason", row)
	}
}

// Views are woken through OnChange, the same hook a live crawl drives, and
// a hook installed before Play sees every event.
func TestPlayDrivesOnChange(t *testing.T) {
	events := recordDemo(t)
	var calls atomic.Int64

	r := New()
	r.OnChange(func() { calls.Add(1) })
	Play(context.Background(), r, events, 0)
	waitFinished(t, r, 5*time.Second)

	// One per event, plus the one Finish sends.
	if got, want := calls.Load(), int64(len(events)+1); got != want {
		t.Errorf("OnChange fired %d times for %d events; want %d", got, len(events), want)
	}
}

// A view that keeps its own history must be able to read every decision,
// however many land between two pulls. The default bound drops the oldest.
func TestKeepNotesZeroKeepsEveryDecision(t *testing.T) {
	const n = 1200
	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		events = append(events, Event{Kind: KindNotDialed, Identity: fmt.Sprintf("srv%d", i),
			Detail: "matches exclude linux"})
	}

	bounded := Replay(events)
	if got := len(bounded.Decisions()); got >= n {
		t.Fatalf("default run kept %d of %d decisions; the bound this test guards is gone", got, n)
	}

	r := New()
	r.KeepNotes(0)
	Play(context.Background(), r, events, 0)
	waitFinished(t, r, 5*time.Second)
	got, _ := r.DecisionsSince(0)
	if len(got) != n {
		t.Errorf("KeepNotes(0) kept %d of %d decisions", len(got), n)
	}
}

func TestPlayEmptyStreamIsFinished(t *testing.T) {
	r := New()
	Play(context.Background(), r, nil, 1)
	if !r.Progress().Finished {
		t.Error("an empty replay should be finished at once")
	}
}

// Counts are run totals. They must not depend on how many decisions the run
// happens to be holding: a rejection early in a run that later produces
// hundreds of not-dialed decisions is still a rejection.
func TestCountsSurviveTheNotesBound(t *testing.T) {
	events := []Event{
		{Kind: KindQueued, Identity: "a"},
		{Kind: KindAuthReject, Identity: "a", Credential: "old"},
		{Kind: KindHostKeyNew, Identity: "a", Detail: "SHA256:x"},
		{Kind: KindAuthOK, Identity: "a", Credential: "new", CredReason: "ladder"},
		{Kind: KindReached, Identity: "a", Name: "a"},
	}
	for i := 0; i < 1200; i++ {
		events = append(events, Event{Kind: KindNotDialed, Identity: fmt.Sprintf("srv%d", i),
			Detail: "matches exclude linux", Via: "a"})
	}
	c := Replay(events).Counts()
	if c.Rejections != 1 || c.NewHostKeys != 1 {
		t.Errorf("rejections %d, new host keys %d; want 1 and 1 after the notes bound trimmed",
			c.Rejections, c.NewHostKeys)
	}
}
