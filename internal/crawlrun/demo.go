// internal/crawlrun/demo.go
//
// A scripted run, for bringing the view up without a lab.
//
// The first thing you do with a new view is fix toolkit mistakes, and doing
// that against a real crawl means a five-minute round trip and a lab that has
// to be reachable. This plays a realistic run into a Run instead: every state
// the table can show, a device that is deliberately not dialed, a ladder walk,
// a first-contact host key, and a stop that leaves something mid-flight.
//
// It lives here rather than in the view package on purpose — it is data, it is
// testable, and keeping it out of the UI means the only unverified code in the
// preview path is the widget layer itself.
package crawlrun

import "time"

// DemoOptions controls playback.
type DemoOptions struct {
	// Step is the pause between events. Zero plays instantly, which is what
	// tests want; something like 40ms looks like a crawl in progress.
	Step time.Duration

	// Stop aborts playback early, leaving devices mid-flight so the view can
	// be checked against a cancelled run.
	Stop <-chan struct{}
}

// Demo plays a scripted lab crawl into run. It returns when the script ends or
// Stop fires; call it on a goroutine when Step is non-zero.
func Demo(run *Run, opts DemoOptions) {
	e := run.Emit()
	send := func(ev Event) bool {
		e.Send(ev)
		if opts.Step > 0 {
			select {
			case <-opts.Stop:
				return false
			case <-time.After(opts.Step):
			}
		} else {
			select {
			case <-opts.Stop:
				return false
			default:
			}
		}
		return true
	}

	type dev struct {
		id, name, platform string
		addr               string
		rungs              int
		cred, reason       string
		neighbors, fresh   int
		newKey             bool
	}

	seeds := []dev{
		{id: "172.16.1.2", name: "wan-core-1.lab.local", platform: "cisco_ios",
			addr: "172.16.1.2", rungs: 2, cred: "lab-admin", reason: "ranked",
			neighbors: 8, fresh: 6, newKey: true},
	}
	depth1 := []dev{
		{id: "eng-rtr-1.lab.local", name: "eng-rtr-1.lab.local", platform: "cisco_ios",
			addr: "172.16.1.3", rungs: 1, cred: "lab-admin", reason: "promoted",
			neighbors: 5, fresh: 4, newKey: true},
		{id: "usa-rtr-1.lab.local", name: "usa-rtr-1.lab.local", platform: "cisco_ios",
			addr: "172.16.128.2", rungs: 1, cred: "lab-admin", reason: "pinned",
			neighbors: 5, fresh: 3, newKey: true},
		{id: "eng-spine-1", name: "eng-spine-1", platform: "arista_eos",
			addr: "172.16.1.21", rungs: 2, cred: "lab-arista", reason: "ranked",
			neighbors: 4, fresh: 3, newKey: true},
		{id: "eng-spine-2", name: "eng-spine-2", platform: "arista_eos",
			addr: "172.16.1.22", rungs: 1, cred: "lab-arista", reason: "promoted",
			neighbors: 4, fresh: 1, newKey: true},
	}
	depth2 := []dev{
		{id: "eng-leaf-1.lab.local", name: "eng-leaf-1.lab.local", platform: "cisco_nxos",
			addr: "172.16.1.31", rungs: 1, cred: "lab-admin", reason: "pinned",
			neighbors: 2, fresh: 0},
		{id: "eng-leaf-2.lab.local", name: "eng-leaf-2.lab.local", platform: "cisco_nxos",
			addr: "172.16.1.32", rungs: 1, cred: "lab-admin", reason: "pinned",
			neighbors: 2, fresh: 0},
		{id: "usa-leaf-1.lab.local", name: "usa-leaf-1.lab.local", platform: "cisco_nxos",
			addr: "172.16.128.31", rungs: 1, cred: "lab-admin", reason: "pinned",
			neighbors: 2, fresh: 0},
	}

	play := func(depth int, devs []dev) bool {
		if !send(Event{Kind: KindDepth, Depth: depth}) {
			return false
		}
		for _, d := range devs {
			if !send(Event{Kind: KindQueued, Identity: d.id, Depth: depth}) {
				return false
			}
		}
		for _, d := range devs {
			if d.newKey {
				if !send(Event{Kind: KindHostKeyNew, Identity: d.id,
					Detail: "ssh-ed25519 SHA256:2mZ9x" + d.addr}) {
					return false
				}
			}
			for i := 1; i < d.rungs; i++ {
				if !send(Event{Kind: KindAuthReject, Identity: d.id, Credential: "lab-admin"}) {
					return false
				}
			}
			if !send(Event{Kind: KindAuthOK, Identity: d.id,
				Credential: d.cred, CredReason: d.reason}) {
				return false
			}
			if !send(Event{Kind: KindPlatform, Identity: d.id, Platform: d.platform}) {
				return false
			}
			if d.name != d.id {
				if !send(Event{Kind: KindRenamed, Identity: d.id, Name: d.name}) {
					return false
				}
			}
			if !send(Event{Kind: KindCollect, Identity: d.id, Detail: "show lldp neighbors detail",
				Parsed: d.neighbors, New: d.fresh}) {
				return false
			}
			if !send(Event{Kind: KindReached, Identity: d.id, Name: d.name}) {
				return false
			}
		}
		return true
	}

	if !play(0, seeds) {
		return
	}
	if !play(1, depth1) {
		return
	}

	// The two outcomes a progress bar cannot express, and the one that is
	// currently invisible without this view.
	if !send(Event{Kind: KindQueued, Identity: "ix-peer-1.example.net", Depth: 2}) {
		return
	}
	if !send(Event{Kind: KindNotDialed, Identity: "ix-peer-1.example.net",
		Detail: "outside allowed domains; mapped as leaf"}) {
		return
	}
	if !send(Event{Kind: KindQueued, Identity: "eng-leaf-9.lab.local", Depth: 2}) {
		return
	}
	if !send(Event{Kind: KindRetryAddr, Identity: "eng-leaf-9.lab.local",
		Detail: "172.16.1.39"}) {
		return
	}
	if !send(Event{Kind: KindFailed, Identity: "eng-leaf-9.lab.local",
		Detail: "dial: i/o timeout"}) {
		return
	}
	if !send(Event{Kind: KindCredParked, Credential: "lab-legacy",
		Detail: "3 consecutive rejections"}) {
		return
	}

	play(2, depth2)
}

// DemoPrevious is a snapshot of an earlier run of the same lab, so the
// comparison tab has something to show. It differs from Demo deliberately: one
// device has since been excluded, one has changed platform, one is gone, and
// one used to authenticate on the first try.
func DemoPrevious() Snapshot {
	prev := New()
	e := prev.Emit()
	for _, d := range []struct {
		id, name, platform string
		state              Kind
		rungs              int
	}{
		{"172.16.1.2", "wan-core-1.lab.local", "cisco_ios", KindReached, 1},
		{"eng-rtr-1.lab.local", "eng-rtr-1.lab.local", "cisco_ios", KindReached, 1},
		{"usa-rtr-1.lab.local", "usa-rtr-1.lab.local", "cisco_ios", KindReached, 1},
		{"eng-spine-1", "eng-spine-1", "cisco_nxos", KindReached, 1},
		{"eng-spine-2", "eng-spine-2", "arista_eos", KindReached, 1},
		{"eng-leaf-9.lab.local", "eng-leaf-9.lab.local", "cisco_nxos", KindReached, 1},
		{"usa-leaf-7.lab.local", "usa-leaf-7.lab.local", "cisco_nxos", KindReached, 1},
	} {
		e.Send(Event{Kind: KindQueued, Identity: d.id})
		for i := 0; i < d.rungs; i++ {
			e.Send(Event{Kind: KindAuthOK, Identity: d.id,
				Credential: "lab-admin", CredReason: "pinned"})
		}
		e.Send(Event{Kind: KindPlatform, Identity: d.id, Platform: d.platform})
		e.Send(Event{Kind: d.state, Identity: d.id, Name: d.name})
	}
	prev.Finish()
	return prev.Snapshot([]string{"172.16.1.2"}, []string{"lab.local"})
}
