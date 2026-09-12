// internal/crawlrun/run.go
//
// The run model: what a crawl looks like as state rather than as output.
//
// This is the whole difference between an application and a script runner. A
// log is only useful while it is scrolling; a run you can still interrogate
// after it finishes — which devices were never dialed, which one needed the
// address fallback, which credential won where — is a different kind of
// object. Everything in this file is deliberately free of any UI dependency
// so that the answer to "what happened" is testable without a toolkit.
//
// Safe for concurrent use: a crawl emits from every worker goroutine while the
// UI reads on the main thread.
package crawlrun

import (
	"sort"
	"sync"
	"time"
)

// State is where a device ended up. The third outcome is the point: a device
// that was deliberately not connected to is neither a success nor a failure,
// and collapsing it into either one is how it becomes invisible.
type State int

const (
	StateQueued State = iota
	StateRunning
	StateReached
	StateFailed
	StateNotDialed
)

func (s State) String() string {
	switch s {
	case StateQueued:
		return "queued"
	case StateRunning:
		return "running"
	case StateReached:
		return "reached"
	case StateFailed:
		return "failed"
	case StateNotDialed:
		return "not dialed"
	}
	return "?"
}

// DeviceRow is one line of the results table.
type DeviceRow struct {
	Identity string
	Name     string
	Depth    int
	Platform string
	State    State

	// Via is the device that reported this one, empty for a seed.
	Via string

	// Detail is why, for the states that have a why.
	Detail string

	// Descr and Caps are what the neighbor advertised about this device
	// before anything dialed it: the LLDP or CDP system description and
	// capability string. Empty for a seed, and for any device discovered on
	// a run with the per-interface detail fallback disabled.
	//
	// This is the pre-dial evidence. Platform is the post-dial answer, and
	// by the time it exists the credentials have already been offered — so
	// on a run that is walking into a rack of servers, this column is the
	// one that says so while there is still a run to cancel.
	Descr string
	Caps  string

	Credential string
	CredReason string

	// Attempts is how many credentials were offered before one worked. This
	// is the lockout-exposure number: every attempt past the first is a
	// failed authentication against a real account, and a run whose average
	// climbs is spending them somewhere new.
	Attempts int

	// Neighbors is what the collection commands parsed, and New is how many
	// of those the crawl had not already claimed.
	Neighbors int
	New       int

	// Seq is the sequence number of this row's last change; RowsSince
	// returns rows whose Seq is past the caller's.
	Seq uint64

	// Phase is what the device is doing now ("ssh dial", "ssh: show lldp
	// neighbors detail", "snmp probe") and PhaseSince when it started.
	// Empty once the device is done. A depth batch ends when its slowest
	// device does, and these are what say which one that is and why.
	Phase      string
	PhaseSince time.Time

	// Method is how the device was collected, once it has been reached.
	// Empty for a device that was never reached.
	Method Method

	FirstSeen time.Time
	Ended     time.Time
}

// Duration is how long the device took, or zero if it has not finished.
func (d DeviceRow) Duration() time.Duration {
	if d.Ended.IsZero() || d.FirstSeen.IsZero() {
		return 0
	}
	return d.Ended.Sub(d.FirstSeen)
}

// Display is the best label available: what the device calls itself once it
// has said, and the string it was claimed under before that.
func (d DeviceRow) Display() string {
	if d.Name != "" {
		return d.Name
	}
	return d.Identity
}

// rowNote is the short per-device annotation for the Detail column: the
// out-of-the-ordinary thing that happened on the way to reaching this device.
// Empty for the routine cases, which is most of them.
func rowNote(ev Event) string {
	switch ev.Kind {
	case KindRetryAddr:
		return "unreachable by name; reached at " + ev.Detail
	case KindResolved:
		return ev.Detail
	case KindPlatform:
		return ev.Detail // "no neighbor plan; leaf", or empty
	case KindFallback:
		return ev.Detail // "snmp failed: ...; trying ssh"
	}
	return ""
}

// Counts is the header summary.
type Counts struct {
	Queued    int
	Running   int
	Reached   int
	Failed    int
	NotDialed int

	// NewHostKeys is how many devices were trusted on first contact this
	// run. Expected to be large on a first crawl and near zero afterwards;
	// a later run that jumps is worth a look.
	NewHostKeys int

	// Attempts is the total credentials offered across the run, and
	// Rejections is how many of those were refused. Rejections is the number
	// worth watching — it is the run's cost in failed authentications.
	Attempts   int
	Rejections int
}

// Total is every device the crawl knows about.
func (c Counts) Total() int {
	return c.Queued + c.Running + c.Reached + c.Failed + c.NotDialed
}

// AttemptsPerReached is the ladder cost per device that answered. A warm
// binding store holds this near 1.0; a cold or split one pushes it up, and the
// difference is paid in failed authentications.
func (c Counts) AttemptsPerReached() float64 {
	if c.Reached == 0 {
		return 0
	}
	return float64(c.Attempts) / float64(c.Reached)
}

// Run accumulates events into the state a view renders.
type Run struct {
	mu     sync.RWMutex
	rows   map[string]*DeviceRow
	order  []string
	notes  []Event
	depth  int
	begun  time.Time
	closed time.Time

	// maxNotes bounds the decisions list; zero or less keeps everything.
	// The default suits a terminal view. A view that pulls DecisionsSince
	// and keeps its own history raises it with KeepNotes: trimming drops
	// the OLDEST notes, so a burst larger than the bound between two pulls
	// -- one depth claiming hundreds of excluded hosts at once, which a crawl
	// through switches that report their servers does -- loses decisions the
	// view never saw.
	maxNotes int

	// Run totals kept as they happen. They used to be counted from the notes
	// list, which is bounded and drops its oldest entries: a run with more
	// decisions than the bound reported fewer rejections than it caused, and
	// rejections are the number an AAA team asks about.
	rejections  int
	newHostKeys int

	// changed is signalled on every mutation so a view can redraw without
	// polling on a timer.
	changed func()

	// seq numbers every change: events as Handle receives them, and the
	// rows Finish ends without one. Rows carry the seq of their last change.
	seq uint64

	// tap sees every event, stamped, in seq order. See Tap.
	tap func(Event)
}

// New returns an empty run.
func New() *Run {
	return &Run{rows: map[string]*DeviceRow{}, maxNotes: 500, begun: time.Now()}
}

// KeepNotes sets how many decisions the run holds for Decisions and
// DecisionsSince; zero or less keeps all of them. Set it before events flow.
func (r *Run) KeepNotes(n int) {
	r.mu.Lock()
	r.maxNotes = n
	r.mu.Unlock()
}

// OnChange installs a redraw hook. It is called from crawl goroutines, so a
// toolkit view must marshal to its own thread inside the callback.
func (r *Run) OnChange(f func()) {
	r.mu.Lock()
	r.changed = f
	r.mu.Unlock()
}

// Emit returns the hook to hand the crawler.
func (r *Run) Emit() Emit { return func(ev Event) { r.Handle(ev) } }

func (r *Run) rowLocked(id string, at time.Time) *DeviceRow {
	if d, ok := r.rows[id]; ok {
		return d
	}
	d := &DeviceRow{Identity: id, State: StateQueued, FirstSeen: at}
	r.rows[id] = d
	r.order = append(r.order, id)
	return d
}

// Handle folds one event into the run.
func (r *Run) Handle(ev Event) {
	if ev.At.IsZero() {
		ev.At = time.Now()
	}

	r.mu.Lock()
	r.seq++
	ev.Seq = r.seq
	switch ev.Kind {
	case KindDepth:
		r.depth = ev.Depth
	case KindAuthReject:
		r.rejections++
	case KindHostKeyNew:
		r.newHostKeys++
	}
	if ev.Identity != "" {
		d := r.rowLocked(ev.Identity, ev.At)
		d.Seq = ev.Seq

		// Phase is progress, not a note: it never touches Detail, which
		// holds the reasons a view shows beside the device.
		switch ev.Kind {
		case KindPhase:
			if d.State == StateQueued {
				d.State = StateRunning
			}
			if d.State == StateRunning {
				d.Phase, d.PhaseSince = ev.Detail, ev.At
			}
		case KindReached, KindFailed, KindNotDialed:
			d.Phase, d.PhaseSince = "", time.Time{}
		}

		if ev.Name != "" {
			d.Name = ev.Name
		}
		if ev.Platform != "" {
			d.Platform = ev.Platform
		}
		// Never overwrite with empty. Most events carry no advertisement,
		// and letting them blank the field would erase the description a
		// moment after KindQueued set it — the column would flicker full
		// on discovery and empty for the rest of the run.
		if ev.Descr != "" {
			d.Descr = ev.Descr
		}
		if ev.Caps != "" {
			d.Caps = ev.Caps
		}
		if ev.Via != "" && d.Via == "" {
			// First reporter wins. A device several neighbors see would
			// otherwise flip between them on every run and turn the
			// comparison tab into noise.
			d.Via = ev.Via
		}

		switch ev.Kind {
		case KindQueued:
			d.Depth = ev.Depth

		case KindNotDialed:
			// Terminal, and deliberately not a failure.
			d.State, d.Detail, d.Ended = StateNotDialed, ev.Detail, ev.At
			// Depth too. A not-dialed device is never queued, so
			// KindQueued never runs for it and nothing else would
			// ever set this -- leaving every excluded device
			// reading as depth 0, which on a run where most
			// devices are excluded makes the column meaningless
			// and the Depth sort useless.
			if ev.Depth > 0 {
				d.Depth = ev.Depth
			}

		case KindResolved, KindRetryAddr, KindRenamed, KindPlatform, KindHostKeyNew, KindFallback:
			if d.State == StateQueued {
				d.State = StateRunning
			}
			// Detail is blank for most rows, because most devices simply
			// answer. When something out of the ordinary happened to THIS
			// device, that belongs on its row and not only in the decisions
			// list — the list says what happened during the run, the row
			// says what happened to the device you are looking at.
			if note := rowNote(ev); note != "" {
				d.Detail = note
			}

		case KindAuthOK:
			d.State = StateRunning
			d.Attempts++
			d.Credential, d.CredReason = ev.Credential, ev.CredReason

		case KindAuthReject:
			d.State = StateRunning
			d.Attempts++

		case KindCollect:
			d.State = StateRunning
			d.Neighbors += ev.Parsed
			d.New += ev.New

		case KindReached:
			d.State, d.Ended = StateReached, ev.At
			if ev.Method != "" {
				d.Method = ev.Method
			}

		case KindFailed:
			d.State, d.Detail, d.Ended = StateFailed, ev.Detail, ev.At
		}
	}

	if ev.Notable() {
		r.notes = append(r.notes, ev)
		if r.maxNotes > 0 && len(r.notes) > r.maxNotes {
			r.notes = r.notes[len(r.notes)-r.maxNotes:]
		}
	}
	changed := r.changed
	if r.tap != nil {
		r.tap(ev)
	}
	r.mu.Unlock()

	if changed != nil {
		changed()
	}
}

// Finish marks the run complete. Anything still running is recorded as failed
// rather than left mid-flight, because a row that stays "running" forever is
// the same silent gap this package exists to close.
func (r *Run) Finish() { r.finishAt(time.Now()) }

// finishAt is Finish at a given time, so Replay can end a recorded run when
// it actually ended. The rows it ends change without an event, so they are
// stamped here -- a view reading RowsSince would otherwise never see them end.
func (r *Run) finishAt(at time.Time) {
	r.mu.Lock()
	r.closed = at
	for _, d := range r.rows {
		if d.State == StateQueued || d.State == StateRunning {
			d.State = StateFailed
			if d.Detail == "" {
				d.Detail = "run ended before this device completed"
			}
			d.Ended = r.closed
			d.Phase, d.PhaseSince = "", time.Time{}
			r.seq++
			d.Seq = r.seq
		}
	}
	changed := r.changed
	r.mu.Unlock()
	if changed != nil {
		changed()
	}
}

// Rows returns a snapshot in discovery order.
func (r *Run) Rows() []DeviceRow {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]DeviceRow, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, *r.rows[id])
	}
	return out
}

// RowsByState returns a snapshot filtered to one state — the click-through
// behind each counter.
func (r *Run) RowsByState(s State) []DeviceRow {
	out := make([]DeviceRow, 0, 8)
	for _, d := range r.Rows() {
		if d.State == s {
			out = append(out, d)
		}
	}
	return out
}

// Sorted returns rows ordered by a named column, for table header clicks.
func (r *Run) Sorted(column string, asc bool) []DeviceRow {
	rows := r.Rows()
	less := func(i, j int) bool { return rows[i].Display() < rows[j].Display() }
	switch column {
	case "depth":
		less = func(i, j int) bool { return rows[i].Depth < rows[j].Depth }
	case "state":
		less = func(i, j int) bool { return rows[i].State < rows[j].State }
	case "platform":
		less = func(i, j int) bool { return rows[i].Platform < rows[j].Platform }
	case "attempts":
		less = func(i, j int) bool { return rows[i].Attempts < rows[j].Attempts }
	case "neighbors":
		less = func(i, j int) bool { return rows[i].Neighbors < rows[j].Neighbors }
	case "via":
		less = func(i, j int) bool { return rows[i].Via < rows[j].Via }
	case "descr":
		// The tuning sort: identical advertisements cluster, so a rack of
		// servers arrives as one block rather than scattered through the
		// discovery order.
		less = func(i, j int) bool { return rows[i].Descr < rows[j].Descr }
	case "duration":
		less = func(i, j int) bool { return rows[i].Duration() < rows[j].Duration() }
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if asc {
			return less(i, j)
		}
		return less(j, i)
	})
	return rows
}

// Counts summarizes the run.
func (r *Run) Counts() Counts {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.countsLocked()
}

func (r *Run) countsLocked() Counts {
	var c Counts
	for _, d := range r.rows {
		switch d.State {
		case StateQueued:
			c.Queued++
		case StateRunning:
			c.Running++
		case StateReached:
			c.Reached++
		case StateFailed:
			c.Failed++
		case StateNotDialed:
			c.NotDialed++
		}
		c.Attempts += d.Attempts
	}
	c.Rejections = r.rejections
	c.NewHostKeys = r.newHostKeys
	return c
}

// Decisions returns the notable events, most recent last.
func (r *Run) Decisions() []Event {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]Event(nil), r.notes...)
}

// Depth is the deepest batch started so far.
func (r *Run) Depth() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.depth
}

// Elapsed is how long the run has been going, or how long it took.
func (r *Run) Elapsed() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if !r.closed.IsZero() {
		return r.closed.Sub(r.begun)
	}
	return time.Since(r.begun)
}

// Seq is the sequence number of the most recent change.
func (r *Run) Seq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.seq
}

// Tap registers f to see every event as Handle stamps it, in Seq order: the
// hook for recording a run (EventWriter). It runs with the run's lock held so
// the order is exact, which means f must be quick and must not call back into
// the Run. A second call replaces the first; nil removes it.
func (r *Run) Tap(f func(Event)) {
	r.mu.Lock()
	r.tap = f
	r.mu.Unlock()
}

// RowsSince returns the rows changed after seq, in first-seen order, and the
// sequence number to pass next time. A view that holds thousands of rows asks
// for these instead of all of them on every redraw.
func (r *Run) RowsSince(seq uint64) ([]DeviceRow, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []DeviceRow
	for _, id := range r.order {
		if d := r.rows[id]; d.Seq > seq {
			out = append(out, *d)
		}
	}
	return out, r.seq
}

// DecisionsSince returns the notable events handled after seq, oldest first,
// and the sequence number to pass next time.
func (r *Run) DecisionsSince(seq uint64) ([]Event, uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Event
	for _, ev := range r.notes {
		if ev.Seq > seq {
			out = append(out, ev)
		}
	}
	return out, r.seq
}

// DepthProgress is one depth level's share of the run.
type DepthProgress struct {
	Depth     int
	Total     int
	Queued    int
	Running   int
	Reached   int
	Failed    int
	NotDialed int
}

// Progress is what a progress view needs, taken in one consistent read.
//
// There is no overall percentage. A breadth-first crawl does not know how
// many devices it will find, so any such number would be invented. What is
// known is each depth: how many are done out of how many admitted -- and the
// next depth's count growing as neighbors are claimed.
type Progress struct {
	Seq      uint64
	Depth    int // the batch being collected
	Elapsed  time.Duration
	Finished bool
	Counts   Counts

	// Depths is every depth with devices, shallowest first.
	Depths []DepthProgress

	// Running is the devices in flight, longest in their current phase
	// first: the head of this list is what the current depth is waiting on.
	Running []DeviceRow
}

// Progress returns the run's progress.
func (r *Run) Progress() Progress {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p := Progress{Seq: r.seq, Depth: r.depth, Counts: r.countsLocked(), Finished: !r.closed.IsZero()}
	if p.Finished {
		p.Elapsed = r.closed.Sub(r.begun)
	} else {
		p.Elapsed = time.Since(r.begun)
	}
	byDepth := map[int]*DepthProgress{}
	for _, id := range r.order {
		d := r.rows[id]
		dp := byDepth[d.Depth]
		if dp == nil {
			dp = &DepthProgress{Depth: d.Depth}
			byDepth[d.Depth] = dp
		}
		dp.Total++
		switch d.State {
		case StateQueued:
			dp.Queued++
		case StateRunning:
			dp.Running++
			p.Running = append(p.Running, *d)
		case StateReached:
			dp.Reached++
		case StateFailed:
			dp.Failed++
		case StateNotDialed:
			dp.NotDialed++
		}
	}
	for _, dp := range byDepth {
		p.Depths = append(p.Depths, *dp)
	}
	sort.Slice(p.Depths, func(i, j int) bool { return p.Depths[i].Depth < p.Depths[j].Depth })
	since := func(d DeviceRow) time.Time {
		if !d.PhaseSince.IsZero() {
			return d.PhaseSince
		}
		return d.FirstSeen
	}
	sort.SliceStable(p.Running, func(i, j int) bool { return since(p.Running[i]).Before(since(p.Running[j])) })
	return p
}
