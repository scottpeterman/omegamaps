// capi/crawl.go
//go:build cgo

// A live crawl behind the same handle a replay returns.
//
// The request is JSON: the crawl's parameters -- crawlrun.Params, with
// durations in milliseconds because C++ has no time.Duration -- and where the
// output goes. Credentials are NOT in it. They come from a vault handle opened
// and unlocked through the vault surface, and are resolved inside Go per
// device, so no password or community string ever sits in the request or on
// the C++ side.
//
// Assembly goes through crawldial.Build, the same path every front end takes,
// so the window cannot crawl differently from anything else built on it.
//
// Every crawl records its event stream beside the map. That was a flag to
// remember on the command line; here it is simply what a crawl produces,
// because a run you cannot replay is a run you cannot look at again.
//
// The run is not marked finished until the map is on disk: a view that loads
// map.json when it sees "finished" must never find it missing or half
// written.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/scottpeterman/omegamaps/internal/crawldial"
	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/topo"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

// crawlRequest is the wire form. Pointers where zero is a real value that
// differs from "use the default": depth 0 means seeds only.
type crawlRequest struct {
	Seeds         []string `json:"seeds"`
	Depth         *int     `json:"depth"`
	Concurrency   *int     `json:"concurrency"`
	TimeoutMS     int64    `json:"timeout_ms"`
	SNMPTimeoutMS int64    `json:"snmp_timeout_ms"`

	Domains      []string `json:"domains"`
	AllowDomains []string `json:"allow_domains"`
	Exclude      []string `json:"exclude"`

	Methods     []string `json:"methods"`
	CredTags    []string `json:"cred_tags"`
	MaxCreds    int      `json:"max_creds"`
	CredBreaker int      `json:"cred_breaker"`

	HostKeys            string `json:"host_keys"`
	KnownHostsPath      string `json:"known_hosts_path"`
	Legacy              bool   `json:"legacy"`
	TrustUnidirectional bool   `json:"trust_unidirectional"`

	// Output. MapPath is required; the events and log paths default to
	// siblings of the map: lab-map.json -> lab-map.events.jsonl, lab-map.log.
	MapPath    string `json:"map_path"`
	EventsPath string `json:"events_path"`
	LogPath    string `json:"log_path"`
}

// params turns the request into crawlrun.Params over the defaults.
func (r crawlRequest) params(vaultPath string) crawlrun.Params {
	p := crawlrun.Defaults()
	// Seeds pasted as one blob -- a spreadsheet column, a ticket -- split
	// the same way the form's free-text field does.
	p.Seeds = crawlrun.ParseSeeds(strings.Join(r.Seeds, "\n"))
	if r.Depth != nil {
		p.Depth = *r.Depth
	}
	if r.Concurrency != nil {
		p.Concurrency = *r.Concurrency
	}
	if r.TimeoutMS > 0 {
		p.Timeout = time.Duration(r.TimeoutMS) * time.Millisecond
	}
	if r.SNMPTimeoutMS > 0 {
		p.SNMPTimeout = time.Duration(r.SNMPTimeoutMS) * time.Millisecond
	}
	p.Domains = r.Domains
	p.AllowDomains = r.AllowDomains
	p.Exclude = r.Exclude
	for _, m := range r.Methods {
		p.Methods = append(p.Methods, crawlrun.Method(m))
	}
	p.CredTags = r.CredTags
	p.MaxCreds = r.MaxCreds
	p.CredBreaker = r.CredBreaker
	if r.HostKeys != "" {
		p.HostKeys = crawlrun.HostKeyMode(r.HostKeys)
	}
	p.KnownHostsPath = r.KnownHostsPath
	p.Legacy = r.Legacy
	p.TrustUnidirectional = r.TrustUnidirectional
	p.VaultPath = vaultPath
	p.Normalize()
	return p
}

// outputPaths fills the defaults for the event stream and the log.
func (r crawlRequest) outputPaths() (mapPath, eventsPath, logPath string) {
	mapPath = r.MapPath
	stem := strings.TrimSuffix(mapPath, filepath.Ext(mapPath))
	eventsPath = r.EventsPath
	if eventsPath == "" {
		eventsPath = stem + ".events.jsonl"
	}
	logPath = r.LogPath
	if logPath == "" {
		logPath = stem + ".log"
	}
	return mapPath, eventsPath, logPath
}

//export omegamaps_crawl_defaults
func omegamaps_crawl_defaults() *C.char {
	// The request a form starts from: the same defaults the command line
	// uses, so the window and crawl cannot drift apart on what "default"
	// means.
	d := crawlrun.Defaults()
	depth, conc := d.Depth, d.Concurrency
	methods := []string{}
	for _, m := range d.CollectionMethods() {
		methods = append(methods, string(m))
	}
	return jsonOut("crawl_defaults", crawlRequest{
		Seeds:         []string{},
		Depth:         &depth,
		Concurrency:   &conc,
		TimeoutMS:     d.Timeout.Milliseconds(),
		SNMPTimeoutMS: d.SNMPTimeout.Milliseconds(),
		Methods:       methods,
		HostKeys:      string(d.HostKeys),
	})
}

type wireValidation struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

//export omegamaps_crawl_validate
func omegamaps_crawl_validate(reqJSON *C.char) *C.char {
	var req crawlRequest
	if err := json.Unmarshal([]byte(C.GoString(reqJSON)), &req); err != nil {
		setErr("crawl request: %v", err)
		return nil
	}
	p := req.params("")
	out := []wireValidation{}
	for _, e := range p.Validate() {
		// Field names are the request's, so a form can find the widget.
		// Params calls the durations timeout and snmp_timeout; the request
		// carries them in milliseconds under _ms names.
		field := e.Field
		switch field {
		case "timeout", "snmp_timeout":
			field += "_ms"
		}
		out = append(out, wireValidation{Field: field, Message: e.Message})
	}
	if strings.TrimSpace(req.MapPath) == "" {
		out = append(out, wireValidation{Field: "map_path", Message: "where to write the map is required"})
	}
	return jsonOut("crawl_validate", out)
}

// crawlLog writes the crawler's text log, timestamped, to a file beside the
// map. The window shows events; this is what to read when a device did
// something the events do not explain.
type crawlLog struct {
	mu sync.Mutex
	f  *os.File
}

func (l *crawlLog) printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return
	}
	fmt.Fprintf(l.f, "%s %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func (l *crawlLog) close() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}

//export omegamaps_crawl_open
func omegamaps_crawl_open(vaultHandle C.longlong, reqJSON *C.char) C.longlong {
	var req crawlRequest
	if err := json.Unmarshal([]byte(C.GoString(reqJSON)), &req); err != nil {
		setErr("crawl request: %v", err)
		return -1
	}
	if strings.TrimSpace(req.MapPath) == "" {
		setErr("crawl request: map_path is required")
		return -1
	}

	var v *vault.Vault
	vaultPath := ""
	if vaultHandle > 0 {
		v = vlookup(vaultHandle)
		if v == nil {
			setErr("crawl: no vault handle %d", int64(vaultHandle))
			return -1
		}
		if v.IsLocked() {
			setErr("crawl: the vault is locked; unlock it first")
			return -1
		}
		vaultPath = v.Path()
	}
	p := req.params(vaultPath)
	mapPath, eventsPath, logPath := req.outputPaths()

	// Output files first: a crawl that cannot record itself should not run.
	if err := os.MkdirAll(filepath.Dir(mapPath), 0o755); err != nil {
		setErr("crawl: output directory: %v", err)
		return -1
	}
	ef, err := os.Create(eventsPath)
	if err != nil {
		setErr("crawl: events file: %v", err)
		return -1
	}
	lf, err := os.Create(logPath)
	if err != nil {
		ef.Close()
		setErr("crawl: log file: %v", err)
		return -1
	}
	logw := &crawlLog{f: lf}

	run := crawlrun.New()
	run.KeepNotes(0)
	events := crawlrun.NewEventWriter(ef)
	run.Tap(events.Write)

	built, err := crawldial.Build(p, crawldial.Options{
		Vault:   v,
		Log:     logw.printf,
		CredLog: logw.printf,
		Emit:    run.Emit(),
	})
	if err != nil {
		// Nothing ran; leave no empty artifacts claiming otherwise.
		ef.Close()
		logw.close()
		os.Remove(eventsPath)
		os.Remove(logPath)
		setErr("%v", err)
		return -1
	}

	n, err := newNotifier()
	if err != nil {
		built.Close()
		ef.Close()
		logw.close()
		setErr("crawl: notifier: %v", err)
		return -1
	}

	ctx, cancel := context.WithCancel(context.Background())
	st := &runState{cancel: cancel, notify: n, scale: 1, run: run,
		res: runResult{Kind: "crawl", State: "running",
			MapPath: mapPath, EventsPath: eventsPath, LogPath: logPath}}
	run.OnChange(st.poke)

	logw.printf("crawl: seeds %v, depth %d, methods %v, vault %q",
		p.Seeds, p.Depth, p.CollectionMethods(), vaultPath)

	go func() {
		devices := built.Crawler.CrawlContext(ctx, p.Seeds)
		cancelled := ctx.Err() != nil

		crawldial.Fold(built.Bindings, devices, p.Domains, logw.printf)
		crawldial.Fold(built.SNMPBindings, devices, p.Domains, logw.printf)
		built.Close()

		nodes, werr := writeMap(mapPath, devices, crawldial.MapOptions(p))
		if werr != nil {
			logw.printf("crawl: write %s: %v", mapPath, werr)
		} else {
			logw.printf("crawl: %d devices, %d nodes in %s", len(devices), nodes, mapPath)
		}

		// Finish before flushing: Finish stamps anything still in flight,
		// and those rows belong in the recording too.
		st.setResult(func(r *runResult) {
			r.Devices, r.Nodes = len(devices), nodes
			switch {
			case werr != nil:
				r.State, r.Error = "failed", werr.Error()
			case cancelled:
				r.State = "cancelled"
			default:
				r.State = "done"
			}
		})
		run.Finish()
		if err := events.Flush(); err != nil {
			logw.printf("crawl: events: %v", err)
		}
		ef.Close()
		logw.close()
	}()

	h := register(st)
	clearErr()
	return h
}

// writeMap generates the map and writes it through a temporary file, so a
// reader never sees a partial one.
func writeMap(path string, devices []*topo.Device, opt topo.Options) (int, error) {
	m := topo.Generate(devices, opt)
	data, err := topo.MarshalMap(m)
	if err != nil {
		return 0, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return 0, err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	return len(m), nil
}
