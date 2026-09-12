package crawlrun

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSeqRowsSinceAndPhases(t *testing.T) {
	r := New()
	r.Handle(Event{Kind: KindQueued, Identity: "lab-r1", Depth: 0})
	r.Handle(Event{Kind: KindQueued, Identity: "lab-r2", Depth: 1})
	if r.Seq() != 2 {
		t.Fatalf("seq = %d, want 2", r.Seq())
	}
	rows, seq := r.RowsSince(0)
	if len(rows) != 2 || seq != 2 {
		t.Fatalf("RowsSince(0) = %d rows, seq %d", len(rows), seq)
	}
	if rows, _ := r.RowsSince(seq); len(rows) != 0 {
		t.Fatalf("nothing changed, got %d rows", len(rows))
	}

	r.Handle(Event{Kind: KindPhase, Identity: "lab-r1", Method: MethodSSH, Detail: "ssh dial"})
	rows, seq2 := r.RowsSince(seq)
	if len(rows) != 1 || rows[0].Identity != "lab-r1" || rows[0].Phase != "ssh dial" ||
		rows[0].State != StateRunning || rows[0].PhaseSince.IsZero() {
		t.Fatalf("after a phase: %+v", rows)
	}
	if d, _ := r.DecisionsSince(0); len(d) != 0 {
		t.Fatalf("a phase is progress, not a decision: %+v", d)
	}

	r.Handle(Event{Kind: KindFallback, Identity: "lab-r1", Method: MethodSNMP, Detail: "snmp failed; trying ssh"})
	d, _ := r.DecisionsSince(seq2)
	if len(d) != 1 || d[0].Kind != KindFallback || d[0].Seq != seq2+1 {
		t.Fatalf("DecisionsSince: %+v", d)
	}
	if row := r.Rows()[0]; row.Phase != "ssh dial" {
		t.Fatalf("a note must not replace the phase: %q", row.Phase)
	}

	r.Handle(Event{Kind: KindReached, Identity: "lab-r1", Method: MethodSSH})
	if row := r.Rows()[0]; row.Phase != "" || !row.PhaseSince.IsZero() || row.State != StateReached {
		t.Fatalf("a finished device keeps no phase: %+v", row)
	}
}

func TestProgressPerDepthAndWhatItWaitsOn(t *testing.T) {
	r := New()
	t0 := time.Now()
	r.Handle(Event{Kind: KindQueued, Identity: "core", Depth: 0, At: t0})
	r.Handle(Event{Kind: KindReached, Identity: "core", At: t0})
	for _, id := range []string{"leaf1", "leaf2", "leaf3"} {
		r.Handle(Event{Kind: KindQueued, Identity: id, Depth: 1, At: t0})
	}
	r.Handle(Event{Kind: KindPhase, Identity: "leaf2", Detail: "ssh: show lldp neighbors detail", At: t0.Add(1 * time.Second)})
	r.Handle(Event{Kind: KindPhase, Identity: "leaf1", Detail: "snmp probe", At: t0.Add(5 * time.Second)})
	r.Handle(Event{Kind: KindQueued, Identity: "server9", Depth: 2, At: t0}) // the next depth, filling

	p := r.Progress()
	want := []DepthProgress{
		{Depth: 0, Total: 1, Reached: 1},
		{Depth: 1, Total: 3, Running: 2, Queued: 1},
		{Depth: 2, Total: 1, Queued: 1},
	}
	if !reflect.DeepEqual(p.Depths, want) {
		t.Fatalf("depths:\n got %+v\nwant %+v", p.Depths, want)
	}
	if len(p.Running) != 2 || p.Running[0].Identity != "leaf2" {
		t.Fatalf("the longest-running phase must lead: %+v", p.Running)
	}
	if p.Finished || p.Counts.Running != 2 || p.Seq != r.Seq() {
		t.Fatalf("progress: %+v", p)
	}

	before := r.Seq()
	r.Finish()
	rows, _ := r.RowsSince(before)
	if len(rows) != 4 { // leaf1, leaf2, leaf3, server9 all ended by Finish
		t.Fatalf("Finish must stamp the rows it ends; RowsSince returned %d", len(rows))
	}
	for _, row := range rows {
		if row.State != StateFailed || row.Phase != "" {
			t.Fatalf("after Finish: %+v", row)
		}
	}
	if !r.Progress().Finished {
		t.Fatal("not finished")
	}
}

func sampleRun(tap func(Event)) *Run {
	r := New()
	r.Tap(tap)
	t0 := time.Date(2026, 9, 10, 20, 0, 0, 0, time.UTC)
	for i, ev := range []Event{
		{Kind: KindDepth, Depth: 0},
		{Kind: KindQueued, Identity: "eng-rtr-1", Depth: 0},
		{Kind: KindPhase, Identity: "eng-rtr-1", Method: MethodSNMP, Detail: "snmp probe"},
		{Kind: KindAuthOK, Identity: "eng-rtr-1", Detail: "legacy"},
		{Kind: KindCollect, Identity: "eng-rtr-1", Detail: "snmp", Parsed: 4, New: 4},
		{Kind: KindReached, Identity: "eng-rtr-1", Name: "eng-rtr-1.lab.local", Platform: "cisco_ios", Method: MethodSNMP},
		{Kind: KindQueued, Identity: "eng-spine-1", Depth: 1, Via: "eng-rtr-1", Descr: "Cisco IOS Software, vios_l2"},
		{Kind: KindFallback, Identity: "eng-spine-1", Method: MethodSNMP, Detail: `snmp failed: "x" & <y>; trying ssh`},
		{Kind: KindFailed, Identity: "eng-spine-1", Detail: "ssh dial: refused"},
	} {
		ev.At = t0.Add(time.Duration(i) * time.Second)
		r.Handle(ev)
	}
	return r
}

func TestEventStreamRoundTripsAndReplays(t *testing.T) {
	var buf bytes.Buffer
	ew := NewEventWriter(&buf)
	orig := sampleRun(ew.Write)
	if err := ew.Flush(); err != nil {
		t.Fatal(err)
	}
	if first := strings.SplitN(buf.String(), "\n", 2)[0]; first != `{"schema":"omegamaps.events","version":1}` {
		t.Fatalf("header = %s", first)
	}
	if !strings.Contains(buf.String(), `"x" & <y>`) && !strings.Contains(buf.String(), `\"x\" & <y>`) {
		t.Fatalf("detail not written as-is (HTML escaping must be off):\n%s", buf.String())
	}

	evs, err := ReadEvents(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 9 {
		t.Fatalf("read %d events, want 9", len(evs))
	}
	for i, ev := range evs {
		if ev.Seq != uint64(i+1) {
			t.Fatalf("event %d has seq %d", i, ev.Seq)
		}
	}
	if evs[5].Kind != KindReached || evs[5].Method != MethodSNMP || evs[4].Parsed != 4 {
		t.Fatalf("fields lost: %+v %+v", evs[4], evs[5])
	}

	orig.Finish()
	if !reflect.DeepEqual(Replay(evs).Rows(), orig.Rows()) {
		t.Fatalf("replay differs:\n got %+v\nwant %+v", Replay(evs).Rows(), orig.Rows())
	}
}

func TestReadEventsFormatChecks(t *testing.T) {
	if _, err := ReadEvents(strings.NewReader(`{"schema":"omegamaps.events","version":2}` + "\n")); err == nil {
		t.Error("a newer version was accepted")
	}
	if _, err := ReadEvents(strings.NewReader(`{"nodes":[]}` + "\n")); err == nil {
		t.Error("a non-stream was accepted")
	}
	if _, err := ReadEvents(strings.NewReader("")); err == nil {
		t.Error("an empty file was accepted")
	}
	evs, err := ReadEvents(strings.NewReader(`{"schema":"omegamaps.events","version":1}` + "\n" +
		`{"seq":1,"kind":"teleport","identity":"x","future_field":true}` + "\n"))
	if err != nil || len(evs) != 1 || evs[0].Kind != KindUnknown {
		t.Errorf("a newer build's unknown kind should read as unknown: %+v %v", evs, err)
	}

	var buf bytes.Buffer
	if err := NewEventWriter(&buf).Flush(); err != nil || !strings.HasPrefix(buf.String(), `{"schema"`) {
		t.Errorf("an empty run should still write its header: %q %v", buf.String(), err)
	}
}
