// capi/capi.go
//go:build cgo

// The C surface over the run model, built as a c-archive for the Qt
// application. The contract is include/omegamaps/omegamaps.h; the header cgo
// writes beside the archive is an artifact.
//
// A run is a handle. Everything the Qt side shows is pulled, never pushed:
// progress in one read, rows and decisions since a sequence number. What
// tells the Qt side to pull is a notifier -- the read end of a self-pipe (a
// socket pair on Windows) that QSocketNotifier watches -- for the reason
// omegassh gives in its own header: a callback into C fires on a Go-created
// thread, and forgetting to marshal once is a rare crash rather than a
// compile error. A readable notifier means "come and look"; one wake covers
// any number of changes, so the pipe can never back up behind a fast crawl.
//
// Results cross as JSON. The run is small by any measure -- a thousand rows
// is a large crawl -- and a pull is one allocation on each side, against a
// struct layout mirrored in two languages that drifts the first time a field
// is added on one side only.
//
// The only source of a run today is a recorded stream played back with
// crawlrun.Play. A live crawl will be a second constructor returning the same
// kind of handle; nothing after the open depends on which one made it.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
	"unsafe"

	"github.com/scottpeterman/omegamaps/internal/buildinfo"
	"github.com/scottpeterman/omegamaps/internal/crawlrun"
)

func main() {}

// runState is one open run.
type runState struct {
	run    *crawlrun.Run
	cancel context.CancelFunc
	notify *notifier

	// scale turns this process's durations back into the run's own. A
	// replay at 50x stamps a 7s collection as 140ms; every duration that
	// crosses the boundary is multiplied back, so a view shows the recorded
	// durations whatever the speed. 1 for anything that is not a replay.
	scale float64

	// notifyMu guards a wake against the notifier being closed underneath
	// it: Run.OnChange hooks captured before close can still fire after it.
	notifyMu   sync.Mutex
	notifyGone bool

	// res is what the run produced, for omegamaps_run_result. Written by the
	// run's own goroutine as it finishes, so it has its own lock.
	resMu sync.Mutex
	res   runResult
}

// runResult is where a run's output went and how it ended.
type runResult struct {
	Kind       string `json:"kind"`  // "replay" or "crawl"
	State      string `json:"state"` // running, done, cancelled, failed
	Error      string `json:"error,omitempty"`
	MapPath    string `json:"map_path,omitempty"`
	EventsPath string `json:"events_path,omitempty"`
	LogPath    string `json:"log_path,omitempty"`
	Devices    int    `json:"devices,omitempty"` // devices the crawl returned
	Nodes      int    `json:"nodes,omitempty"`   // nodes written to the map
}

func (st *runState) setResult(f func(*runResult)) {
	st.resMu.Lock()
	f(&st.res)
	st.resMu.Unlock()
}

func (st *runState) result() runResult {
	st.resMu.Lock()
	defer st.resMu.Unlock()
	r := st.res
	// A replay has no output of its own to wait for: it is done when the
	// run is.
	if r.Kind == "replay" && r.State == "running" && st.run.Progress().Finished {
		r.State = "done"
	}
	return r
}

// register files a run and returns its handle.
func register(st *runState) C.longlong {
	handlesMu.Lock()
	nextHandle++
	h := nextHandle
	handles[h] = st
	handlesMu.Unlock()
	return C.longlong(h)
}

func (st *runState) poke() {
	st.notifyMu.Lock()
	defer st.notifyMu.Unlock()
	if st.notifyGone || st.notify == nil {
		return
	}
	st.notify.wake()
}

var (
	handlesMu  sync.Mutex
	handles    = map[int64]*runState{}
	nextHandle int64
)

func lookup(h C.longlong) *runState {
	handlesMu.Lock()
	defer handlesMu.Unlock()
	return handles[int64(h)]
}

// cstr hands a Go string to C. The caller frees it with omegamaps_free.
func cstr(s string) *C.char { return C.CString(s) }

//export omegamaps_version
func omegamaps_version() *C.char {
	return cstr(buildinfo.String())
}

//export omegamaps_free
func omegamaps_free(p unsafe.Pointer) {
	C.free(p)
}

//export omegamaps_replay_open
func omegamaps_replay_open(path *C.char, speed C.double) C.longlong {
	if path == nil {
		setErr("replay: no path")
		return -1
	}
	p := C.GoString(path)
	f, err := os.Open(p)
	if err != nil {
		setErr("replay: %v", err)
		return -1
	}
	events, err := crawlrun.ReadEvents(f)
	f.Close()
	if err != nil {
		setErr("replay %s: %v", p, err)
		return -1
	}

	n, err := newNotifier()
	if err != nil {
		setErr("replay: notifier: %v", err)
		return -1
	}

	ctx, cancel := context.WithCancel(context.Background())
	st := &runState{cancel: cancel, notify: n, scale: 1,
		res: runResult{Kind: "replay", State: "running"}}
	if speed > 0 {
		st.scale = float64(speed)
	}
	st.run = crawlrun.New()
	// The Qt log keeps its own history and pulls by sequence; the run's
	// default bound would drop the oldest decisions in any burst larger
	// than it between two pulls.
	st.run.KeepNotes(0)
	st.run.OnChange(st.poke)
	crawlrun.Play(ctx, st.run, events, float64(speed))

	h := register(st)
	clearErr()
	return h
}

//export omegamaps_run_result
func omegamaps_run_result(h C.longlong) *C.char {
	st := lookup(h)
	if st == nil {
		setErr("run_result: no run %d", int64(h))
		return nil
	}
	return jsonOut("run_result", st.result())
}

//export omegamaps_notify_handle
func omegamaps_notify_handle(h C.longlong) C.longlong {
	st := lookup(h)
	if st == nil {
		setErr("notify_handle: no run %d", int64(h))
		return -1
	}
	clearErr()
	return C.longlong(st.notify.handle())
}

//export omegamaps_cancel
func omegamaps_cancel(h C.longlong) C.int {
	st := lookup(h)
	if st == nil {
		setErr("cancel: no run %d", int64(h))
		return -1
	}
	st.cancel()
	clearErr()
	return 0
}

//export omegamaps_close
func omegamaps_close(h C.longlong) C.int {
	handlesMu.Lock()
	st := handles[int64(h)]
	delete(handles, int64(h))
	handlesMu.Unlock()
	if st == nil {
		setErr("close: no run %d", int64(h))
		return -1
	}
	st.cancel()
	st.run.OnChange(nil)
	st.notifyMu.Lock()
	st.notifyGone = true
	st.notify.close()
	st.notifyMu.Unlock()
	clearErr()
	return 0
}

// ---------------------------------------------------------------------------
// Wire shapes. snake_case, stable, additive: fields may be added and a reader
// ignores what it does not know -- the same rule as the recorded stream.

type wireCounts struct {
	Queued      int `json:"queued"`
	Running     int `json:"running"`
	Reached     int `json:"reached"`
	Failed      int `json:"failed"`
	NotDialed   int `json:"not_dialed"`
	NewHostKeys int `json:"new_host_keys"`
	Attempts    int `json:"attempts"`
	Rejections  int `json:"rejections"`
}

type wireDepth struct {
	Depth     int `json:"depth"`
	Total     int `json:"total"`
	Queued    int `json:"queued"`
	Running   int `json:"running"`
	Reached   int `json:"reached"`
	Failed    int `json:"failed"`
	NotDialed int `json:"not_dialed"`
}

// wireRow is a DeviceRow. Times the view needs as durations are converted
// here, against this process's clock, so the C++ side never has to agree
// with Go about what "now" is.
type wireRow struct {
	Seq        uint64 `json:"seq"`
	Identity   string `json:"identity"`
	Name       string `json:"name,omitempty"`
	Display    string `json:"display"`
	Depth      int    `json:"depth"`
	Platform   string `json:"platform,omitempty"`
	State      string `json:"state"`
	Via        string `json:"via,omitempty"`
	Detail     string `json:"detail,omitempty"`
	Descr      string `json:"descr,omitempty"`
	Caps       string `json:"caps,omitempty"`
	Credential string `json:"credential,omitempty"`
	CredReason string `json:"cred_reason,omitempty"`
	Attempts   int    `json:"attempts"`
	Neighbors  int    `json:"neighbors"`
	New        int    `json:"new"`
	Method     string `json:"method,omitempty"`
	Phase      string `json:"phase,omitempty"`
	PhaseMS    int64  `json:"phase_ms,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

type wireProgress struct {
	Seq       uint64      `json:"seq"`
	Depth     int         `json:"depth"`
	ElapsedMS int64       `json:"elapsed_ms"`
	Finished  bool        `json:"finished"`
	Counts    wireCounts  `json:"counts"`
	Depths    []wireDepth `json:"depths"`
	Running   []wireRow   `json:"running"`
}

type wireDecision struct {
	Seq      uint64 `json:"seq"`
	AtMS     int64  `json:"at_ms"`
	Kind     string `json:"kind"`
	Identity string `json:"identity,omitempty"`
	Name     string `json:"name,omitempty"`
	Via      string `json:"via,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Text     string `json:"text"`
}

func ms(d time.Duration) int64 {
	if d < 0 {
		return 0
	}
	return d.Milliseconds()
}

func (st *runState) ms(d time.Duration) int64 {
	return ms(time.Duration(float64(d) * st.scale))
}

func (st *runState) toWireRow(d crawlrun.DeviceRow, now time.Time) wireRow {
	w := wireRow{
		Seq: d.Seq, Identity: d.Identity, Name: d.Name, Display: d.Display(),
		Depth: d.Depth, Platform: d.Platform, State: d.State.String(), Via: d.Via,
		Detail: d.Detail, Descr: d.Descr, Caps: d.Caps,
		Credential: d.Credential, CredReason: d.CredReason, Attempts: d.Attempts,
		Neighbors: d.Neighbors, New: d.New, Method: string(d.Method),
		Phase: d.Phase, DurationMS: st.ms(d.Duration()),
	}
	if d.Phase != "" && !d.PhaseSince.IsZero() {
		w.PhaseMS = st.ms(now.Sub(d.PhaseSince))
	}
	return w
}

// jsonOut marshals v for C, or records why it could not.
func jsonOut(what string, v any) *C.char {
	b, err := json.Marshal(v)
	if err != nil {
		setErr("%s: %v", what, err)
		return nil
	}
	clearErr()
	return cstr(string(b))
}

//export omegamaps_progress
func omegamaps_progress(h C.longlong) *C.char {
	st := lookup(h)
	if st == nil {
		setErr("progress: no run %d", int64(h))
		return nil
	}
	p := st.run.Progress()
	now := time.Now()
	w := wireProgress{
		Seq: p.Seq, Depth: p.Depth, ElapsedMS: st.ms(p.Elapsed), Finished: p.Finished,
		Counts: wireCounts{
			Queued: p.Counts.Queued, Running: p.Counts.Running, Reached: p.Counts.Reached,
			Failed: p.Counts.Failed, NotDialed: p.Counts.NotDialed,
			NewHostKeys: p.Counts.NewHostKeys, Attempts: p.Counts.Attempts,
			Rejections: p.Counts.Rejections,
		},
		Depths:  make([]wireDepth, 0, len(p.Depths)),
		Running: make([]wireRow, 0, len(p.Running)),
	}
	for _, d := range p.Depths {
		w.Depths = append(w.Depths, wireDepth{
			Depth: d.Depth, Total: d.Total, Queued: d.Queued, Running: d.Running,
			Reached: d.Reached, Failed: d.Failed, NotDialed: d.NotDialed,
		})
	}
	for _, d := range p.Running {
		w.Running = append(w.Running, st.toWireRow(d, now))
	}
	return jsonOut("progress", w)
}

//export omegamaps_rows_since
func omegamaps_rows_since(h C.longlong, seq C.ulonglong) *C.char {
	st := lookup(h)
	if st == nil {
		setErr("rows_since: no run %d", int64(h))
		return nil
	}
	rows, next := st.run.RowsSince(uint64(seq))
	now := time.Now()
	out := struct {
		Seq  uint64    `json:"seq"`
		Rows []wireRow `json:"rows"`
	}{Seq: next, Rows: make([]wireRow, 0, len(rows))}
	for _, d := range rows {
		out.Rows = append(out.Rows, st.toWireRow(d, now))
	}
	return jsonOut("rows_since", out)
}

//export omegamaps_decisions_since
func omegamaps_decisions_since(h C.longlong, seq C.ulonglong) *C.char {
	st := lookup(h)
	if st == nil {
		setErr("decisions_since: no run %d", int64(h))
		return nil
	}
	evs, next := st.run.DecisionsSince(uint64(seq))
	out := struct {
		Seq       uint64         `json:"seq"`
		Decisions []wireDecision `json:"decisions"`
	}{Seq: next, Decisions: make([]wireDecision, 0, len(evs))}
	for _, ev := range evs {
		out.Decisions = append(out.Decisions, wireDecision{
			Seq: ev.Seq, AtMS: ev.At.UnixMilli(), Kind: ev.Kind.String(),
			Identity: ev.Identity, Name: ev.Name, Via: ev.Via, Detail: ev.Detail,
			Text: ev.Describe(),
		})
	}
	return jsonOut("decisions_since", out)
}
