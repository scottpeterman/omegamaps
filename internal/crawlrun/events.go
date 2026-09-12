package crawlrun

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// The recorded event stream: JSON Lines, one header line and then one event
// per line, in Seq order. It is its own format, deliberately separate from
// map.json -- the map is the result and other tools read it; this is how the
// result was reached. Field names are snake_case and stable; new fields may
// be added, and a reader ignores what it does not know.
const (
	EventsSchema  = "omegamaps.events"
	EventsVersion = 1
)

type eventsHeader struct {
	Schema  string `json:"schema"`
	Version int    `json:"version"`
}

type wireEvent struct {
	Seq      uint64    `json:"seq"`
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	Identity string    `json:"identity,omitempty"`
	Name     string    `json:"name,omitempty"`
	Platform string    `json:"platform,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	Depth    int       `json:"depth"`
	Via      string    `json:"via,omitempty"`
	Descr    string    `json:"descr,omitempty"`
	Caps     string    `json:"caps,omitempty"`
	Parsed   int       `json:"parsed,omitempty"`
	New      int       `json:"new,omitempty"`
	Enriched int       `json:"enriched,omitempty"`
	Skipped  int       `json:"skipped,omitempty"`
	Method   Method    `json:"method,omitempty"`

	Credential string `json:"credential,omitempty"`
	CredReason string `json:"cred_reason,omitempty"`
}

// toWire and fromWire are the only two places an Event and its recorded form
// meet. TestWireCarriesEveryEventField fails the build if an Event field has
// no counterpart here -- the first version of this mapping dropped the
// credential from every auth event without a sound.
func toWire(ev Event) wireEvent {
	return wireEvent{
		Seq: ev.Seq, At: ev.At, Kind: ev.Kind.String(), Identity: ev.Identity, Name: ev.Name,
		Platform: ev.Platform, Detail: ev.Detail, Depth: ev.Depth, Via: ev.Via, Descr: ev.Descr,
		Caps: ev.Caps, Parsed: ev.Parsed, New: ev.New, Enriched: ev.Enriched, Skipped: ev.Skipped,
		Method: ev.Method, Credential: ev.Credential, CredReason: ev.CredReason,
	}
}

func fromWire(w wireEvent) Event {
	k, _ := ParseKind(w.Kind)
	return Event{
		Seq: w.Seq, At: w.At, Kind: k, Identity: w.Identity, Name: w.Name,
		Platform: w.Platform, Detail: w.Detail, Depth: w.Depth, Via: w.Via, Descr: w.Descr,
		Caps: w.Caps, Parsed: w.Parsed, New: w.New, Enriched: w.Enriched, Skipped: w.Skipped,
		Method: w.Method, Credential: w.Credential, CredReason: w.CredReason,
	}
}

// EventWriter records a run as JSON Lines. Hand its Write to Run.Tap.
// Errors are sticky: after the first, Write does nothing and Flush reports it.
type EventWriter struct {
	w       *bufio.Writer
	enc     *json.Encoder
	started bool
	err     error
}

// NewEventWriter records to w. Call Flush when the run is over.
func NewEventWriter(w io.Writer) *EventWriter {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	enc.SetEscapeHTML(false)
	return &EventWriter{w: bw, enc: enc}
}

func (ew *EventWriter) header() {
	if !ew.started && ew.err == nil {
		ew.started = true
		ew.err = ew.enc.Encode(eventsHeader{Schema: EventsSchema, Version: EventsVersion})
	}
}

// Write records one event. Its signature fits Run.Tap.
func (ew *EventWriter) Write(ev Event) {
	ew.header()
	if ew.err != nil {
		return
	}
	ew.err = ew.enc.Encode(toWire(ev))
}

// Flush writes anything buffered -- and the header, so even a run with no
// events leaves a file that says what it is -- and returns the first error.
func (ew *EventWriter) Flush() error {
	ew.header()
	if ew.err != nil {
		return ew.err
	}
	return ew.w.Flush()
}

// ReadEvents reads a recorded stream. An event of a kind this build does not
// know -- written by a newer one -- is kept as KindUnknown rather than
// failing the read; a newer format version is refused.
func ReadEvents(r io.Reader) ([]Event, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var (
		out  []Event
		line int
	)
	for sc.Scan() {
		line++
		b := sc.Bytes()
		if len(b) == 0 {
			continue
		}
		if line == 1 {
			var h eventsHeader
			if err := json.Unmarshal(b, &h); err != nil || h.Schema != EventsSchema {
				return nil, fmt.Errorf("not an omegamaps event stream (line 1)")
			}
			if h.Version > EventsVersion {
				return nil, fmt.Errorf("event stream version %d is newer than this build reads (%d)", h.Version, EventsVersion)
			}
			continue
		}
		var w wireEvent
		if err := json.Unmarshal(b, &w); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		out = append(out, fromWire(w))
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if line == 0 {
		return nil, fmt.Errorf("empty event stream")
	}
	return out, nil
}

// Replay rebuilds a Run from a recorded stream, finished at its last event:
// a view can be built and tested against a real crawl without running one.
func Replay(events []Event) *Run {
	r := New()
	if len(events) > 0 {
		r.begun = events[0].At
	}
	for _, ev := range events {
		r.Handle(ev)
	}
	end := time.Now()
	if len(events) > 0 {
		end = events[len(events)-1].At
	}
	r.finishAt(end)
	return r
}
