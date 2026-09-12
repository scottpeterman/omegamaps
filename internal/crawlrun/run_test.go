// internal/crawlrun/run_test.go
package crawlrun

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// labRun replays a small crawl of the lab: two devices reached, one that
// fails, and one that is deliberately never dialed.
func labRun() *Run {
	r := New()
	e := r.Emit()

	e.Send(Event{Kind: KindDepth, Depth: 0})
	e.Send(Event{Kind: KindQueued, Identity: "wan-core-1", Depth: 0})
	e.Send(Event{Kind: KindAuthOK, Identity: "wan-core-1",
		Credential: "lab-admin", CredReason: "ranked"})
	e.Send(Event{Kind: KindPlatform, Identity: "wan-core-1", Platform: "cisco_ios"})
	e.Send(Event{Kind: KindRenamed, Identity: "wan-core-1", Name: "wan-core-1.lab.local"})
	e.Send(Event{Kind: KindCollect, Identity: "wan-core-1", Parsed: 6, New: 4})
	e.Send(Event{Kind: KindReached, Identity: "wan-core-1"})

	e.Send(Event{Kind: KindDepth, Depth: 1})
	e.Send(Event{Kind: KindQueued, Identity: "eng-spine-1", Depth: 1})
	e.Send(Event{Kind: KindAuthReject, Identity: "eng-spine-1", Credential: "lab-admin"})
	e.Send(Event{Kind: KindAuthOK, Identity: "eng-spine-1",
		Credential: "lab-arista", CredReason: "ranked"})
	e.Send(Event{Kind: KindPlatform, Identity: "eng-spine-1", Platform: "arista_eos"})
	e.Send(Event{Kind: KindCollect, Identity: "eng-spine-1", Parsed: 3, New: 1})
	e.Send(Event{Kind: KindReached, Identity: "eng-spine-1"})

	e.Send(Event{Kind: KindQueued, Identity: "eng-leaf-9", Depth: 1})
	e.Send(Event{Kind: KindFailed, Identity: "eng-leaf-9", Detail: "dial: i/o timeout"})

	e.Send(Event{Kind: KindQueued, Identity: "ix-peer-1", Depth: 1})
	e.Send(Event{Kind: KindNotDialed, Identity: "ix-peer-1",
		Detail: "outside allowed domains; mapped as leaf"})

	r.Finish()
	return r
}

func TestCountsSeparateTheThirdOutcome(t *testing.T) {
	c := labRun().Counts()

	if c.Reached != 2 {
		t.Errorf("reached = %d, want 2", c.Reached)
	}
	if c.Failed != 1 {
		t.Errorf("failed = %d, want 1", c.Failed)
	}
	if c.NotDialed != 1 {
		t.Errorf("not dialed = %d, want 1", c.NotDialed)
	}
	// The point of the third bucket: a device that was never connected to
	// must not be counted as either a success or a failure.
	if c.Total() != 4 {
		t.Errorf("total = %d, want 4", c.Total())
	}
	if c.Attempts != 3 {
		t.Errorf("attempts = %d, want 3 (one device took two rungs)", c.Attempts)
	}
	if c.Rejections != 1 {
		t.Errorf("rejections = %d, want 1", c.Rejections)
	}
	if got := c.AttemptsPerReached(); got != 1.5 {
		t.Errorf("attempts per reached = %v, want 1.5", got)
	}
}

func TestRowsCarryTheAnswerToWhatHappenedToX(t *testing.T) {
	rows := map[string]DeviceRow{}
	for _, d := range labRun().Rows() {
		rows[d.Identity] = d
	}

	if got := rows["ix-peer-1"]; got.State != StateNotDialed ||
		got.Detail == "" {
		t.Errorf("not-dialed device lost its reason: %+v", got)
	}
	if got := rows["eng-leaf-9"]; got.State != StateFailed ||
		got.Detail != "dial: i/o timeout" {
		t.Errorf("failure reason lost: %+v", got)
	}
	if got := rows["wan-core-1"]; got.Display() != "wan-core-1.lab.local" {
		t.Errorf("display name = %q, want the reported name", got.Display())
	}
	if got := rows["eng-spine-1"]; got.Attempts != 2 || got.Credential != "lab-arista" {
		t.Errorf("credential outcome lost: %+v", got)
	}
	if got := rows["wan-core-1"]; got.Neighbors != 6 || got.New != 4 {
		t.Errorf("neighbor counts = %d/%d, want 6/4", got.Neighbors, got.New)
	}
}

func TestDecisionsAreTheFewThingsWorthLookingAt(t *testing.T) {
	notes := labRun().Decisions()
	if len(notes) == 0 {
		t.Fatal("no decisions recorded")
	}

	var sawNotDialed, sawCollect bool
	for _, ev := range notes {
		if ev.Kind == KindNotDialed {
			sawNotDialed = true
		}
		// Routine collection must not reach this list, or it stops being a
		// short list and stops being read.
		if ev.Kind == KindCollect || ev.Kind == KindQueued || ev.Kind == KindDepth {
			sawCollect = true
		}
	}
	if !sawNotDialed {
		t.Error("the silently-skipped device did not reach the decisions view")
	}
	if sawCollect {
		t.Error("routine events leaked into decisions")
	}
	if len(notes) > 8 {
		t.Errorf("decisions list is %d long for a four-device crawl", len(notes))
	}
}

// A pinned credential that worked first try is the happy path and should not
// clutter the view; a ladder walk should.
func TestPinnedAuthIsNotNotable(t *testing.T) {
	pinned := Event{Kind: KindAuthOK, Identity: "a", Credential: "c", CredReason: "pinned"}
	if pinned.Notable() {
		t.Error("a pin that hit was reported as notable")
	}
	walked := Event{Kind: KindAuthOK, Identity: "a", Credential: "c", CredReason: "ranked"}
	if !walked.Notable() {
		t.Error("a ladder walk was not reported as notable")
	}
}

func TestUnfinishedDevicesDoNotStayRunning(t *testing.T) {
	r := New()
	e := r.Emit()
	e.Send(Event{Kind: KindQueued, Identity: "eng-rtr-1"})
	e.Send(Event{Kind: KindAuthOK, Identity: "eng-rtr-1", Credential: "c", CredReason: "pinned"})
	r.Finish()

	rows := r.Rows()
	if len(rows) != 1 || rows[0].State != StateFailed || rows[0].Detail == "" {
		t.Errorf("a device left mid-flight stayed ambiguous: %+v", rows)
	}
}

func TestRowsByStateBacksTheCounterClickThrough(t *testing.T) {
	got := labRun().RowsByState(StateNotDialed)
	if len(got) != 1 || got[0].Identity != "ix-peer-1" {
		t.Errorf("click-through returned %+v", got)
	}
}

func TestCompareAnswersWhatChangedSinceLastRun(t *testing.T) {
	prev := labRun().Snapshot([]string{"172.16.1.2"}, []string{"lab.local"})

	// Today: the spine now needs two extra rungs, a leaf that used to answer
	// has stopped, one device is gone entirely, and one is new.
	r := New()
	e := r.Emit()
	e.Send(Event{Kind: KindQueued, Identity: "wan-core-1"})
	e.Send(Event{Kind: KindAuthOK, Identity: "wan-core-1", Credential: "lab-admin", CredReason: "pinned"})
	e.Send(Event{Kind: KindPlatform, Identity: "wan-core-1", Platform: "arista_eos"})
	e.Send(Event{Kind: KindReached, Identity: "wan-core-1"})

	e.Send(Event{Kind: KindQueued, Identity: "eng-spine-1"})
	for i := 0; i < 4; i++ {
		e.Send(Event{Kind: KindAuthReject, Identity: "eng-spine-1", Credential: "x"})
	}
	e.Send(Event{Kind: KindReached, Identity: "eng-spine-1"})

	e.Send(Event{Kind: KindQueued, Identity: "eng-leaf-9"})
	e.Send(Event{Kind: KindNotDialed, Identity: "eng-leaf-9", Detail: "matches exclude"})

	e.Send(Event{Kind: KindQueued, Identity: "usa-leaf-1"})
	e.Send(Event{Kind: KindReached, Identity: "usa-leaf-1"})
	r.Finish()

	changes := Compare(prev, r.Rows())
	byKind := map[ChangeKind][]Change{}
	for _, c := range changes {
		byKind[c.Kind] = append(byKind[c.Kind], c)
	}

	if len(byKind[Appeared]) != 1 || byKind[Appeared][0].Identity != "usa-leaf-1" {
		t.Errorf("appeared = %+v", byKind[Appeared])
	}
	if len(byKind[Vanished]) != 1 || byKind[Vanished][0].Identity != "ix-peer-1" {
		t.Errorf("vanished = %+v", byKind[Vanished])
	}
	if len(byKind[PlatformMoved]) != 1 {
		t.Errorf("platform change not caught: %+v", byKind[PlatformMoved])
	}
	// eng-leaf-9 went failed -> not dialed: nothing broke, a policy changed.
	var sawPolicyMove bool
	for _, c := range byKind[StateMoved] {
		if c.Identity == "eng-leaf-9" && c.Was == "failed" && c.Now == "not dialed" {
			sawPolicyMove = true
		}
	}
	if !sawPolicyMove {
		t.Errorf("state moves = %+v", byKind[StateMoved])
	}
	// The ladder cost rise is the binding store having stopped matching.
	if len(byKind[LadderCost]) != 1 || byKind[LadderCost][0].Identity != "eng-spine-1" {
		t.Errorf("ladder cost = %+v", byKind[LadderCost])
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.json")
	snap := labRun().Snapshot([]string{"172.16.1.2"}, []string{"lab.local"})
	if err := snap.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Devices) != len(snap.Devices) {
		t.Fatalf("device count %d != %d", len(back.Devices), len(snap.Devices))
	}
	if back.Counts.NotDialed != 1 {
		t.Errorf("not-dialed count lost across the round trip: %+v", back.Counts)
	}
	// A reloaded run must still diff cleanly against itself.
	if got := Compare(back, labRun().Rows()); len(got) != 0 {
		t.Errorf("a run compared to itself produced %d changes: %+v", len(got), got)
	}
}

func TestConcurrentEmitIsSafe(t *testing.T) {
	r := New()
	e := r.Emit()
	var wg sync.WaitGroup
	var redraws int
	var mu sync.Mutex
	r.OnChange(func() { mu.Lock(); redraws++; mu.Unlock() })

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := string(rune('a' + n))
			for j := 0; j < 40; j++ {
				e.Send(Event{Kind: KindCollect, Identity: id, Parsed: 1, At: time.Now()})
				_ = r.Counts()
				_ = r.Rows()
			}
		}(i)
	}
	wg.Wait()

	if got := len(r.Rows()); got != 8 {
		t.Errorf("rows = %d, want 8", got)
	}
	mu.Lock()
	defer mu.Unlock()
	if redraws == 0 {
		t.Error("no redraw signalled")
	}
}
