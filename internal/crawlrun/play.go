// internal/crawlrun/play.go
//
// Play: a recorded crawl fed back into a Run at the pace it happened.
//
// Replay rebuilds the finished state in one call, which is what a test wants.
// A view under construction wants the opposite: the run as it unfolded, so
// that progress bars move, depths open one after another and the "waiting on"
// list changes hands -- through exactly the path a live crawl uses (Handle,
// OnChange, RowsSince), so a view that works against Play works against a
// crawl with nothing changed but where the events come from.
//
// Every timestamp is restamped onto the present. A recording's own times are
// yesterday's, and a Run measures phases and elapsed time against the clock:
// fed unchanged, every running device would report having been in its phase
// for a day. Restamping keeps the recorded gaps between events, divided by
// the speed, so at 10x a device that took 1.3s to collect shows 130ms -- the
// durations a view shows during Play are the recorded ones, scaled.
package crawlrun

import (
	"context"
	"time"
)

// Play feeds events into r and returns immediately; the events arrive from a
// goroutine, spaced as they were recorded and divided by speed. r should be
// fresh from New, with any KeepNotes and OnChange already set: delivery
// starts at once, and a hook installed afterwards misses the first events.
// A speed of zero or below delivers them as fast as Handle takes them, with
// the recorded spacing kept in the timestamps, so the finished run reports
// the recording's real durations.
//
// Cancelling ctx stops delivery and finishes the run at that moment, which
// ends anything still in flight the way a cancelled crawl does. The Run is
// finished exactly once either way; Progress().Finished says when.
//
// Recorded sequence numbers are discarded: Handle numbers events as it
// receives them, which is the numbering RowsSince and DecisionsSince use.
func Play(ctx context.Context, r *Run, events []Event, speed float64) {
	start := time.Now()
	r.mu.Lock()
	r.begun = start
	r.mu.Unlock()
	if len(events) == 0 {
		r.finishAt(start)
		return
	}

	first := events[0].At
	stamp := func(at time.Time) time.Time {
		off := at.Sub(first)
		if off < 0 {
			// Events are recorded in Seq order, and Seq is assigned under
			// the Run's lock after the event was stamped, so two workers
			// can land a few microseconds out of time order. Clamping keeps
			// a restamped event from predating the run.
			off = 0
		}
		if speed > 0 {
			off = time.Duration(float64(off) / speed)
		}
		return start.Add(off)
	}

	go func() {
		var timer *time.Timer
		defer func() {
			if timer != nil {
				timer.Stop()
			}
		}()

		for _, ev := range events {
			at := stamp(ev.At)
			if speed > 0 {
				if d := time.Until(at); d > 0 {
					if timer == nil {
						timer = time.NewTimer(d)
					} else {
						timer.Reset(d)
					}
					select {
					case <-ctx.Done():
						r.Finish()
						return
					case <-timer.C:
					}
				}
			}
			if ctx.Err() != nil {
				r.Finish()
				return
			}
			ev.At = at
			ev.Seq = 0
			r.Handle(ev)
		}

		end := stamp(events[len(events)-1].At)
		if speed > 0 {
			// The last event was delivered at its stamp or a little after;
			// ending the run before it would make Elapsed shorter than the
			// last device's own duration.
			if now := time.Now(); now.After(end) {
				end = now
			}
		}
		r.finishAt(end)
	}()
}
