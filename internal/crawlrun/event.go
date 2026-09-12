// internal/crawlrun/event.go
//
// Structured crawl events.
//
// The crawler's only observation surface used to be a printf logger. That is
// enough to watch a run scroll past and nothing else: a log answers "what is
// happening", never "what happened to device X", and the moment a UI tries to
// answer the second question by parsing the first, every log message becomes
// an API that can no longer be reworded.
//
// So the crawler emits these alongside its log lines rather than instead of
// them. The CLI keeps the prose; anything that needs to accumulate state gets
// the struct. Emitting both from the same call site is mild duplication and
// worth it — it means adding the UI cannot change what the CLI prints.
//
// # Why these kinds
//
// They are not invented. Each one already existed as a distinct log line in
// crawler.go or credres.go; this file only gives them a shape. The grouping
// that matters is the outcome triple at the bottom: Reached, Failed, and
// NotDialed. That third one has no representation in a progress bar and no
// trace in a finished map — a device excluded by pattern or sitting outside
// AllowDomains is drawn as a leaf, exactly like a real edge device, and the
// only evidence of the difference is one log line that has already scrolled
// away.
package crawlrun

import (
	"fmt"
	"time"
)

// Kind is what happened. Ordering is not meaningful.
type Kind int

const (
	KindUnknown Kind = iota

	// Lifecycle.
	KindQueued    // admitted to the frontier
	KindDepth     // a new depth batch started
	KindPlatform  // fingerprinted
	KindReached   // connected, collected, done
	KindFailed    // dial, session, or fingerprint failed
	KindNotDialed // deliberately not connected to; mapped as a leaf

	// Identity decisions.
	KindResolved   // CGNAT substitution or domain-suffix completion
	KindRetryAddr  // unreachable by name, retried at the reported address
	KindRenamed    // node named from the device's own prompt
	KindHostKeyNew // an unknown host key was accepted on first contact and recorded

	// Credentials.
	KindAuthOK     // authenticated
	KindAuthReject // a credential was rejected
	KindCredParked // a credential was parked for the rest of the run

	// Collection.
	KindCollect    // a command ran and parsed
	KindCollectErr // a command or its parse failed

	// Methods. Appended rather than grouped above so the existing values do
	// not renumber.
	KindFallback // one collection method did not produce the device; the next is tried

	// Progress. What a device is doing right now -- dialing, a command, a
	// probe -- in Detail. Not a decision: never notable, never a note.
	KindPhase
)

func (k Kind) String() string {
	switch k {
	case KindQueued:
		return "queued"
	case KindDepth:
		return "depth"
	case KindPlatform:
		return "platform"
	case KindReached:
		return "reached"
	case KindFailed:
		return "failed"
	case KindNotDialed:
		return "not-dialed"
	case KindResolved:
		return "resolved"
	case KindRetryAddr:
		return "retry-addr"
	case KindRenamed:
		return "renamed"
	case KindHostKeyNew:
		return "host-key-new"
	case KindAuthOK:
		return "auth-ok"
	case KindAuthReject:
		return "auth-reject"
	case KindCredParked:
		return "cred-parked"
	case KindCollect:
		return "collect"
	case KindCollectErr:
		return "collect-err"
	case KindFallback:
		return "fallback"
	case KindPhase:
		return "phase"
	}
	return "unknown"
}

// ParseKind is the inverse of Kind.String, for reading a recorded stream.
func ParseKind(s string) (Kind, bool) {
	for k := KindUnknown; k <= KindPhase; k++ {
		if k.String() == s {
			return k, true
		}
	}
	return KindUnknown, false
}

// Event is one thing the crawler decided or observed.
//
// Identity is the crawler's own claim key, not a display name — it is the
// string that ties events to a row, so it has to be the same value the crawl
// claimed the device under. Everything human-facing derives from Name, which
// arrives later when the device says what it calls itself.
type Event struct {
	// Seq is stamped by Run.Handle: strictly increasing within a run, and
	// zero on an event no Run has handled. A view asks for what changed
	// after the last Seq it saw.
	Seq uint64

	At       time.Time
	Kind     Kind
	Identity string

	// Name is the device's reported name once known. Empty until then.
	Name string

	// Depth in the crawl, for KindQueued, KindDepth and KindNotDialed.
	//
	// KindNotDialed carries it because such a device is never queued, so
	// KindQueued never fires for it and nothing else would set it.
	Depth int

	// Via is the device whose neighbor table produced this one. Empty for a
	// seed. It answers the first question an unexpected row provokes —
	// which box reported this — which otherwise means correlating log lines
	// by hand.
	Via string

	// Detail is the human-readable specifics: the failure reason, the
	// exclusion pattern, the command that ran.
	Detail string

	// Platform from the fingerprint.
	Platform string

	// Descr and Caps are the neighbor's own advertisement — the LLDP or CDP
	// system description and capability string — carried from the claim that
	// produced this target. Set on KindQueued and KindNotDialed; empty for a
	// seed, which nothing advertised.
	//
	// These are the fields ShouldExclude actually matches on, which is the
	// reason they travel. A run that dials a rack of servers says so in the
	// description long before it says so in the platform: the fingerprint
	// arrives after the credential ladder has already been walked, and the
	// description arrives before anything is dialed at all.
	//
	// Caps is here as evidence, not as a filter. Servers routinely advertise
	// Bridge and Router capability, so a capability bit is not a safe basis
	// for deciding what to skip — but seeing it next to the description is
	// what tells you why filtering on it would not have worked.
	Descr string
	Caps  string

	// Credential and CredReason describe an auth outcome. CredReason is
	// credres's own ranking word — pinned, promoted, or ranked — which is
	// the difference between a warm cache and a ladder walk.
	Credential string
	CredReason string

	// Parsed, New, Enriched, Skipped are the neighbor counts from one
	// collection command.
	Parsed, New, Enriched, Skipped int
	// Method is the collection method the event concerns: on KindReached
	// the one whose result the device kept, on KindFallback the one being
	// abandoned.
	Method Method
}

// Notable reports whether this event belongs in the decisions view.
//
// A healthy crawl should produce very few. The filter is deliberately not
// "errors": a device silently not dialed is not an error and is the single
// most important thing to surface, while a successful authentication is only
// interesting when the pin missed and the ladder had to be walked.
func (e Event) Notable() bool {
	switch e.Kind {
	case KindNotDialed, KindFailed, KindResolved, KindRetryAddr,
		KindRenamed, KindHostKeyNew, KindAuthReject, KindCredParked, KindCollectErr,
		KindFallback:
		return true
	case KindAuthOK:
		return e.CredReason != "" && e.CredReason != "pinned"
	}
	return false
}

// Describe renders an event for the decisions list.
func (e Event) Describe() string {
	who := e.Name
	if who == "" {
		who = e.Identity
	}
	switch e.Kind {
	case KindNotDialed:
		if e.Via != "" {
			return fmt.Sprintf("%s was not dialed: %s (reported by %s)", who, e.Detail, e.Via)
		}
		return fmt.Sprintf("%s was not dialed: %s", who, e.Detail)
	case KindFailed:
		return fmt.Sprintf("%s failed: %s", who, e.Detail)
	case KindResolved:
		return fmt.Sprintf("%s resolved: %s", who, e.Detail)
	case KindRetryAddr:
		return fmt.Sprintf("%s unreachable by name; retried at %s", who, e.Detail)
	case KindRenamed:
		return fmt.Sprintf("%s identifies itself as %s", who, e.Name)
	case KindHostKeyNew:
		// First contact with an unknown key is expected in discovery; a
		// MISMATCH is not, and never reaches here — sshcore fails that closed
		// before any of this runs. Recording what was seen is what makes
		// trust-on-first-use auditable instead of blind.
		return fmt.Sprintf("%s: recorded new host key %s", who, e.Detail)
	case KindAuthReject:
		return fmt.Sprintf("%s rejected credential %s", who, e.Credential)
	case KindCredParked:
		return fmt.Sprintf("credential %s parked: %s", e.Credential, e.Detail)
	case KindCollectErr, KindFallback, KindPhase:
		return fmt.Sprintf("%s: %s", who, e.Detail)
	case KindAuthOK:
		return fmt.Sprintf("%s authenticated with %s (%s)", who, e.Credential, e.CredReason)
	}
	return fmt.Sprintf("%s: %s", who, e.Detail)
}

// Emit is the crawler's hook. A nil Emit is valid and costs nothing, so the
// CLI is unchanged by any of this.
type Emit func(Event)

// Send is a nil-safe call, so call sites do not each need the guard.
func (e Emit) Send(ev Event) {
	if e == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	e(ev)
}
