# Cancellation, params, and the host-key event

All of this is compiled and tested against the real tree — `go build ./...`,
`go vet`, and the whole non-Fyne suite green under `-race`. The one failure is
pre-existing and not mine: `internal/tfsm/concurrency_test.go` has a stray
`package tfsm` on line 1 in this zip, so that package's tests do not build.

## Breaking change: `DialFunc` takes a context

```go
type DialFunc func(ctx context.Context, t DialTarget) (*sshcore.Client, error)
```

A context in `DialTarget` would have been less disruptive and wrong — it is a
per-call signal, not a property of the device. Updated call sites:
`cmd/crawl/dialer.go` (both dialers), `cmd/crawl/dialer_test.go`, and the
crawler's own test dial funcs. `cmd/reach` needed no change.

`Crawl(seeds)` still exists and behaves exactly as before, so the CLI is
untouched. `CrawlContext(ctx, seeds)` is the new entry point.

## How the stop actually behaves

Cancellation is checked between depth batches and inside each worker once it
holds a semaphore slot. That second check is what makes it feel immediate:
every device in a batch spawns its goroutine straight away and then blocks on
the semaphore, so on cancel the queued ones fall through without dialing and
only the handful genuinely in flight have to drain.

Both dialers also check before connecting, so a cancel that lands between
scheduling and dialing costs nothing.

What is **not** covered: `sshcore.Dial` does not take a context, so a
connection already inside a TCP connect or a key exchange runs to its own
timeout. Worst-case stop latency is therefore one device's dial timeout, not
the whole batch. Good enough to ship behind a button that says "Stopping…";
fixing it properly means `net.Dialer.DialContext` inside sshcore.

Abandoned devices are **returned, marked failed, with a reason** rather than
dropped. A device that silently vanishes from a stopped run looks identical to
one that was never discovered, and those are not the same thing.

Tests are in `internal/crawler/cancel_test.go`: a crawl stopped mid-flight
returns every seed accounted for, never-attempted devices carry a reason, a
pre-cancelled context opens zero connections, and plain `Crawl` is unchanged.

## `crawlrun.Params`

The modal's model. `Defaults()` matches the CLI flag defaults exactly, so the
two front ends cannot drift into producing different crawls from the same
intent.

- `ParseSeeds(text)` takes a textarea and splits on newlines, commas, spaces,
  or semicolons, de-duplicating — a paste out of a spreadsheet or a ticket uses
  any of them.
- `Validate()` returns **every** problem with a field name attached, so the
  form marks all the offending inputs in one pass instead of one at a time.
- `Normalize()` is safe to call as the user types; it makes `.lab.local` and
  `lab.local` the same intent and drops duplicates.
- A seed that does not resolve is still valid. This lab's names have no DNS
  behind them at all, and the address fallback is what covers that — so the
  check is shape only, never resolution.

Host keys are `tofu` (default) or `strict`. Passing `"insecure"` is a
validation error with an explanation, since that mode is the only one that also
stops detecting a key that **changed**. It stays available on the command line.
Strict with no `KnownHostsPath` is flagged too — it would fall back to
`~/.ssh/known_hosts`, which is almost never what a discovery run means.

`Profiles` is a named-parameter store with atomic writes, sorted
most-recently-used first. Without it the dialog is slower than the CLI it
replaces and nobody uses it twice.

## `KindHostKeyNew`

Added to the event taxonomy, notable by default, and counted as
`Counts.NewHostKeys`. `cmd/crawl/dialer.go` already auto-accepts unknown keys
on first contact and prints to stderr — that line is exactly what scrolls away.
Emit alongside it:

```go
	HostKeyPrompt: func(hostname string, remote net.Addr, key ssh.PublicKey) (bool, error) {
		emit.Send(crawlrun.Event{
			Kind:     crawlrun.KindHostKeyNew,
			Identity: identity,
			Detail:   fmt.Sprintf("%s %s", key.Type(), ssh.FingerprintSHA256(key)),
		})
		return true, nil
	},
```

The counter is the useful part over time: large on a first crawl of an estate,
near zero afterwards, and a later run that jumps is worth a look. That is what
makes trust-on-first-use auditable rather than blind — you are not claiming you
knew the keys, you are recording what you saw, and a mismatch on any later run
still fails closed in sshcore before any of this runs.

## Left for you

The modal itself. It binds widgets to `Params`, calls `Validate()` on OK, and
marks fields from the returned errors — the rules are all on this side now, so
the view has no judgement in it.
