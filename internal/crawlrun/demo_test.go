package crawlrun

import "testing"

// The preview has to exercise every state the table can show, or it is not
// much of a preview.
func TestDemoCoversEveryState(t *testing.T) {
	run := New()
	Demo(run, DemoOptions{})
	run.Finish()

	c := run.Counts()
	if c.Reached == 0 || c.Failed == 0 || c.NotDialed == 0 {
		t.Errorf("demo missed a state: %+v", c)
	}
	if c.NewHostKeys == 0 {
		t.Error("no first-contact host keys in the demo")
	}
	if c.Rejections == 0 {
		t.Error("no ladder walk in the demo")
	}
	if got := c.AttemptsPerReached(); got <= 1.0 {
		t.Errorf("attempts per reached = %v; the demo should show a cost above 1", got)
	}

	seen := map[Kind]bool{}
	for _, ev := range run.Decisions() {
		seen[ev.Kind] = true
	}
	for _, want := range []Kind{KindNotDialed, KindFailed, KindRetryAddr,
		KindHostKeyNew, KindCredParked, KindAuthReject} {
		if !seen[want] {
			t.Errorf("decisions view never shows %s", want)
		}
	}
}

func TestDemoComparisonHasSomethingToShow(t *testing.T) {
	run := New()
	Demo(run, DemoOptions{})
	run.Finish()

	changes := Compare(DemoPrevious(), run.Rows())
	kinds := map[ChangeKind]int{}
	for _, ch := range changes {
		kinds[ch.Kind]++
	}
	for _, want := range []ChangeKind{Appeared, Vanished, StateMoved, PlatformMoved} {
		if kinds[want] == 0 {
			t.Errorf("comparison tab shows nothing for %s", want)
		}
	}
}

func TestDemoStopsWhenAsked(t *testing.T) {
	run := New()
	stop := make(chan struct{})
	close(stop)
	Demo(run, DemoOptions{Stop: stop})
	if got := len(run.Rows()); got > 2 {
		t.Errorf("demo ignored the stop signal and played %d rows", got)
	}
}
