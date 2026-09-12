// internal/credres/credres.go
//
// Credential resolution: turning "I am about to dial this device" into an
// ordered list of credentials to try.
//
// This is the layer the terminal and the crawler share. The vault knows about
// secrets and nothing about hosts; this package knows about hosts and treats
// secrets as opaque. A session takes the first candidate. A crawl walks the
// list until one authenticates.
//
// Four mechanisms shape the order and the length of that list:
//
//   - Pin. The persisted last-known-good credential for this identity goes
//     first. Survives across runs. A hint, not an authority: if it fails to
//     authenticate the pin is dropped and the ladder is walked.
//   - Promotion. Within one run, the credential that most recently
//     authenticated anywhere is tried first on the next *unknown* identity.
//     This is what makes a cold crawl fast, since the persisted pins are empty
//     on a first pass and most devices in a fabric share a credential set.
//   - Negative cache. A credential that was rejected by this identity during
//     this run is not offered to it again.
//   - Circuit breaker. A credential rejected by N distinct identities in a row
//     is parked for the rest of the run. This is the lockout guard: without
//     it, one stale credential in the vault means one failed authentication
//     per device against the same account, and the account is what breaks.
//
// Only OutcomeAuthRejected feeds any of these. See classify.go.
package credres

import (
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

// Defaults applied when Config leaves a field at zero.
const (
	DefaultBreakerThreshold = 3
	DefaultMaxPerHost       = 4
)

// ErrNoCandidates is returned by Walk when nothing in the vault is eligible for
// the target, which is a configuration problem rather than a dial failure.
var ErrNoCandidates = errors.New("no eligible credentials for target")

// Store is the resolver's view of the vault. Narrow on purpose: the resolver
// never creates, updates, or unlocks anything.
type Store interface {
	All() ([]vault.Credential, error)
}

// Target describes the device about to be dialed.
type Target struct {
	// Identity is the canonical cache key. Build it with Identity(); passing
	// a raw dial string here is the bug that warms two cache entries for one
	// device.
	Identity string

	// Addr is the literal address, when known, for CIDR scope matching. May
	// be empty if the target is only known by name.
	Addr string

	// Platform is the fingerprint result, e.g. "arista_eos". Empty before
	// fingerprinting; platform-scoped credentials are then skipped rather
	// than guessed at.
	Platform string

	// Tags the caller requires. A credential must carry all of them.
	Tags []string

	// Aliases is every other string this device is known by — the reported
	// name, the dialed name, the address. They only ever widen a binding
	// lookup; nothing here is treated as evidence about a credential.
	Aliases []string

	// Pin names a credential — by id or by name — that the CALLER already
	// knows belongs to this device, and it goes first.
	//
	// This is the same slot the binding store fills, from a different
	// source. A binding is something the resolver LEARNED; a pin is
	// something a person WROTE DOWN, on a session in the inventory, and it
	// outranks a learned pin for that reason.
	//
	// First rather than exclusive, matching how a binding pin behaves: a
	// credential that was right last month and is not right today should
	// cost one attempt, not the device. A pin naming a credential that is
	// not in the vault, or that the tags and scope filtered out, is
	// LOGGED and then ignored — silently falling through is how somebody
	// spends an afternoon wondering why their session's credential is not
	// being used.
	Pin string
}

// Reason records why a candidate landed where it did. Surfaced for logging so
// a slow crawl can be explained without re-deriving the ordering.
type Reason int

const (
	ReasonPinned Reason = iota
	ReasonPromoted
	ReasonRanked
)

func (r Reason) String() string {
	switch r {
	case ReasonPinned:
		return "pinned"
	case ReasonPromoted:
		return "promoted"
	default:
		return "ranked"
	}
}

// Candidate is one credential to try, in order.
type Candidate struct {
	Cred   vault.Credential
	Reason Reason
}

// Config tunes the resolver. The zero value is usable and safe.
type Config struct {
	// Method is which kind of credential this resolver offers. Empty means
	// SSH, so every resolver built before SNMP credentials existed keeps
	// offering exactly what it did. An SSH resolver never offers an SNMP
	// credential and an SNMP resolver never offers anything else.
	Method crawlrun.Method

	// Classify maps an attempt's error onto an Outcome. Nil uses Classify,
	// which reads SSH errors. A dial layer that knows its own error shapes
	// -- SNMP's -- supplies one instead of teaching this package them.
	Classify func(error) Outcome

	// BreakerThreshold is how many distinct identities may reject a
	// credential before it is parked for the run. Zero uses the default;
	// negative disables the breaker.
	BreakerThreshold int

	// MaxPerHost caps how many credentials are offered for one target. Zero
	// uses the default; negative means unlimited. This is the per-host
	// attempt cap; it is a second, independent lockout guard.
	MaxPerHost int

	// DisablePromotion turns off per-run promotion. Ordering then comes only
	// from pins and static ranking.
	DisablePromotion bool

	// Log, if set, receives ordering and state-change notes. Never called
	// with secret material: credentials are identified by name and ID.
	Log func(format string, args ...any)

	// Emit receives credential outcomes as structured events, beside the
	// Log lines they mirror. Reason ("pinned", "promoted", "ranked") is the
	// part worth carrying: it is the difference between a binding that hit
	// and a ladder that was walked, which is what makes an attempt count
	// mean something rather than just being a number.
	//
	// Deprecated in place: this is a crawlrun event type, so a capture got
	// NOTHING from it and its run model's KindAuthOK / KindAuthReject /
	// KindCredParked were declared and never fed. Set Observer instead;
	// both are called, so an existing crawl caller needs no edit.
	Emit crawlrun.Emit

	// Observer receives the same three outcomes as plain callbacks.
	//
	// Plain callbacks rather than an event type, for the reason
	// dial.BaseConfig already learned when it dropped crawlrun.Emit for
	// OnNewHostKey + Announce: a package this low importing one run model
	// hands that run model to every caller, and the other one then gets
	// silence. Each front end adapts these to whatever it calls an event.
	//
	// The consequence of not having this was not cosmetic. A capture that
	// failed on every device could not say WHICH credential it offered or
	// why the vault's other entries were not tried, so the only way to
	// find out was to re-run the CLI with -v and read stderr.
	Observer Observer
}

// Observer is told what the resolver decided about a credential.
//
// All fields are optional; a zero Observer is a working no-op. Nothing here is
// ever called with secret material — credentials are identified by name and
// ID.
type Observer struct {
	// AuthOK: cred authenticated to identity. reason is "pinned",
	// "promoted" or "ranked".
	AuthOK func(identity, cred, reason string)

	// AuthReject: the device refused cred. This is the one that answers
	// "why did my run fail" — without it a failed device reports a
	// handshake error and says nothing about which of the vault's
	// credentials produced it.
	AuthReject func(identity, cred, reason string)

	// CredParked: cred has been taken out of the run after too many
	// distinct devices rejected it. Worth surfacing on its own, because
	// every device AFTER this point never sees that credential at all,
	// and nothing else in the output would explain why.
	CredParked func(cred, detail string)
}

func (o Observer) authOK(identity, cred, reason string) {
	if o.AuthOK != nil {
		o.AuthOK(identity, cred, reason)
	}
}

func (o Observer) authReject(identity, cred, reason string) {
	if o.AuthReject != nil {
		o.AuthReject(identity, cred, reason)
	}
}

func (o Observer) credParked(cred, detail string) {
	if o.CredParked != nil {
		o.CredParked(cred, detail)
	}
}

func (c Config) breakerThreshold() int {
	if c.BreakerThreshold == 0 {
		return DefaultBreakerThreshold
	}
	return c.BreakerThreshold
}

func (c Config) maxPerHost() int {
	if c.MaxPerHost == 0 {
		return DefaultMaxPerHost
	}
	return c.MaxPerHost
}

// Resolver orders credentials for a target and learns from the outcomes.
// Safe for concurrent use.
type Resolver struct {
	store    Store
	bindings BindingStore
	cfg      Config

	mu sync.Mutex
	// promoted is the credential ID that most recently authenticated.
	promoted string
	// negative[identity][credID] means "rejected here, this run".
	negative map[string]map[string]struct{}
	// rejects[credID][identity] counts distinct identities that rejected the
	// credential since its last success.
	rejects map[string]map[string]struct{}
	// parked credentials are out for the rest of the run.
	parked map[string]string // credID -> why
	// names is credID -> display name, so an outcome reported with only an
	// id can still be described in the words a person filed it under.
	names map[string]string
}

// New returns a resolver over store. bindings may be nil, in which case
// pinning is disabled and only per-run state applies.
func New(store Store, bindings BindingStore, cfg Config) *Resolver {
	return &Resolver{
		store:    store,
		bindings: bindings,
		cfg:      cfg,
		negative: make(map[string]map[string]struct{}),
		rejects:  make(map[string]map[string]struct{}),
		parked:   make(map[string]string),
	}
}

func (r *Resolver) logf(format string, args ...any) {
	if r.cfg.Log != nil {
		r.cfg.Log(format, args...)
	}
}

// Resolve returns the ordered credentials to try for target. An empty result
// means nothing in the vault is eligible, which is a configuration problem,
// not a transient one.
func (r *Resolver) Resolve(target Target) ([]Candidate, error) {
	all, err := r.store.All()
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	eligible := make([]vault.Credential, 0, len(all))
	for _, c := range all {
		// Remember the display name for every credential seen, not just
		// the eligible ones. Parking happens in Report, which is handed
		// an ID and nothing else, and a line saying a credential is out
		// for the rest of the run has to say WHICH — a UUID names it to
		// the code and to nobody else.
		if r.names == nil {
			r.names = make(map[string]string, len(all))
		}
		r.names[c.ID] = c.Name

		if c.Disabled {
			continue
		}
		if c.IsSNMP() != (r.cfg.Method == crawlrun.MethodSNMP) {
			continue
		}
		if _, isParked := r.parked[c.ID]; isParked {
			continue
		}
		if _, rejected := r.negative[target.Identity][c.ID]; rejected {
			continue
		}
		if !hasAllTags(c, target.Tags) {
			continue
		}
		if !scopeMatches(c.Scope, target) {
			continue
		}
		eligible = append(eligible, c)
	}

	rank(eligible)

	var pinnedID string
	if r.bindings != nil && target.Identity != "" {
		if b, ok := r.bindings.Lookup(target.bindingIDs()...); ok {
			pinnedID = b.CredID
		}
	}
	// A caller's pin outranks a learned binding: somebody wrote it on the
	// session, and a stale binding is exactly the thing they are
	// correcting.
	if want := strings.TrimSpace(target.Pin); want != "" {
		if id, ok := matchCredential(eligible, want); ok {
			pinnedID = id
		} else {
			r.logf("credres: %s asks for credential %q, which is not eligible here "+
				"(missing, disabled, or filtered out by tags or scope); using the ladder instead",
				target.Identity, want)
		}
	}
	promotedID := r.promoted
	if r.cfg.DisablePromotion {
		promotedID = ""
	}

	out := make([]Candidate, 0, len(eligible))
	// Pin first, then promotion, then everything else in ranked order.
	for _, want := range []struct {
		id     string
		reason Reason
	}{
		{pinnedID, ReasonPinned},
		{promotedID, ReasonPromoted},
	} {
		if want.id == "" {
			continue
		}
		if containsCandidate(out, want.id) {
			continue
		}
		for _, c := range eligible {
			if c.ID == want.id {
				out = append(out, Candidate{Cred: c, Reason: want.reason})
				break
			}
		}
	}
	for _, c := range eligible {
		if containsCandidate(out, c.ID) {
			continue
		}
		out = append(out, Candidate{Cred: c, Reason: ReasonRanked})
	}

	if max := r.cfg.maxPerHost(); max > 0 && len(out) > max {
		out = out[:max]
	}
	return out, nil
}

// credName is the display name for a credential id, falling back to the id
// when the resolver has not seen it — which only happens if something reports
// an outcome for a credential that was never resolved.
func (r *Resolver) credName(id string) string {
	if n, ok := r.names[id]; ok && n != "" {
		return n
	}
	return id
}

// matchCredential finds a credential by id or by name, case-insensitively.
//
// Both, because a session file written by a person names a credential the way
// a person does and a machine-written one carries the id. Searching the
// ELIGIBLE list rather than the whole vault is deliberate: a pin must not
// resurrect a credential that is disabled, parked, already rejected by this
// device, or excluded by the caller's tags — a pin says which one to try
// first, never that a filter does not apply.
func matchCredential(eligible []vault.Credential, want string) (string, bool) {
	for _, c := range eligible {
		if c.ID == want {
			return c.ID, true
		}
	}
	for _, c := range eligible {
		if strings.EqualFold(c.Name, want) {
			return c.ID, true
		}
	}
	return "", false
}

// Report records the outcome of one attempt. Everything the resolver learns,
// it learns here.
//
// Only OutcomeAuthRejected updates the negative cache and the breaker. An
// algorithm mismatch, a host-key failure, or an unreachable target leaves all
// credential state untouched, because none of them is evidence about the
// credential.
func (r *Resolver) Report(target Target, credID string, outcome Outcome) {
	if credID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	switch outcome {
	case OutcomeSuccess:
		delete(r.rejects, credID)
		delete(r.negative, target.Identity)
		if !r.cfg.DisablePromotion {
			r.promoted = credID
		}
		if r.bindings != nil && target.Identity != "" {
			if err := r.bindings.Record(credID, target.bindingIDs()...); err != nil {
				// Best effort: a cold cache next run is a slow crawl, not a
				// broken one.
				r.logf("credres: could not persist binding for %s: %v", target.Identity, err)
			}
		}

	case OutcomeAuthRejected:
		if target.Identity != "" {
			if r.negative[target.Identity] == nil {
				r.negative[target.Identity] = make(map[string]struct{})
			}
			r.negative[target.Identity][credID] = struct{}{}

			if r.rejects[credID] == nil {
				r.rejects[credID] = make(map[string]struct{})
			}
			r.rejects[credID][target.Identity] = struct{}{}

			if threshold := r.cfg.breakerThreshold(); threshold > 0 &&
				len(r.rejects[credID]) >= threshold {
				r.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindCredParked,
					Credential: credID, Detail: "consecutive rejections"})
				r.cfg.Observer.credParked(r.credName(credID), "consecutive rejections")
				r.parked[credID] = fmt.Sprintf(
					"rejected by %d distinct devices", len(r.rejects[credID]))
				r.logf("credres: parking credential %s for this run: %s",
					credID, r.parked[credID])
			}
		}
		// A pin that no longer authenticates is stale, not authoritative.
		if r.bindings != nil && target.Identity != "" {
			if b, ok := r.bindings.Lookup(target.bindingIDs()...); ok && b.CredID == credID {
				_ = r.bindings.Forget(target.bindingIDs()...)
			}
		}

	case OutcomeKeyMaterial:
		// Locally broken, not remotely rejected. Park it so the rest of the
		// crawl does not repeat a failure that will never resolve, but do not
		// count it toward the lockout breaker.
		r.parked[credID] = "key material could not be loaded"
		r.logf("credres: parking credential %s for this run: %s", credID, r.parked[credID])

	default:
		// Inconclusive. Deliberately no state change.
	}
}

// Walk resolves candidates for target and calls dial with each in turn until
// one authenticates. It stops early on any non-retryable outcome, since a
// host-key or reachability failure will reproduce identically for every
// remaining credential and trying them only spends attempts.
//
// The credential that succeeded is returned. The last error is returned when
// none did.
func (r *Resolver) Walk(target Target, dial func(vault.Credential) error) (vault.Credential, error) {
	candidates, err := r.Resolve(target)
	if err != nil {
		return vault.Credential{}, err
	}
	if len(candidates) == 0 {
		return vault.Credential{}, ErrNoCandidates
	}

	var lastErr error
	for _, cand := range candidates {
		derr := dial(cand.Cred)
		classify := r.cfg.Classify
		if classify == nil {
			classify = Classify
		}
		outcome := classify(derr)
		r.Report(target, cand.Cred.ID, outcome)

		if outcome == OutcomeSuccess {
			r.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindAuthOK,
				Identity: target.Identity, Credential: cand.Cred.Name,
				CredReason: cand.Reason.String()})
			r.cfg.Observer.authOK(target.Identity, cand.Cred.Name, cand.Reason.String())
			r.logf("credres: %s authenticated with %q (%s)",
				target.Identity, cand.Cred.Name, cand.Reason)
			return cand.Cred, nil
		}
		lastErr = derr
		if outcome == OutcomeAuthRejected {
			// Only a rejection counts as a spent attempt. A host-key or
			// reachability failure would reproduce for every credential and
			// says nothing about this one.
			r.cfg.Emit.Send(crawlrun.Event{Kind: crawlrun.KindAuthReject,
				Identity: target.Identity, Credential: cand.Cred.Name,
				CredReason: cand.Reason.String()})
			r.cfg.Observer.authReject(target.Identity, cand.Cred.Name, cand.Reason.String())
		}
		if outcome == OutcomeNoAnswer {
			// Not "rejected": nothing answered, which a host that is down
			// also produces, and the log should not claim more than that.
			r.logf("credres: %s gave no answer to %q (%s)",
				target.Identity, cand.Cred.Name, cand.Reason)
		} else {
			r.logf("credres: %s rejected %q (%s): %s",
				target.Identity, cand.Cred.Name, cand.Reason, outcome)
		}

		if !outcome.Retryable() {
			break
		}
	}
	if lastErr == nil {
		lastErr = ErrNoCandidates
	}
	return vault.Credential{}, lastErr
}

// Stats is a snapshot of what the resolver has learned this run.
type Stats struct {
	Promoted      string
	ParkedCreds   map[string]string
	NegativeHosts int
}

// Stats returns the current run state, for end-of-crawl reporting.
func (r *Resolver) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	parked := make(map[string]string, len(r.parked))
	for k, v := range r.parked {
		parked[k] = v
	}
	return Stats{
		Promoted:      r.promoted,
		ParkedCreds:   parked,
		NegativeHosts: len(r.negative),
	}
}

// ResetRun clears per-run state (promotion, negative cache, breaker, parking)
// while leaving persisted bindings alone.
func (r *Resolver) ResetRun() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.promoted = ""
	r.negative = make(map[string]map[string]struct{})
	r.rejects = make(map[string]map[string]struct{})
	r.parked = make(map[string]string)
}

// rank sorts credentials into static try order: most specific scope first,
// then explicit priority, then key auth ahead of password auth, then name.
//
// Key-before-password is deliberate. A rejected public key generally does not
// increment a remote lockout counter the way a rejected password does, so
// spending the cheap attempt first is the safer ladder.
func rank(creds []vault.Credential) {
	sort.SliceStable(creds, func(i, j int) bool {
		a, b := creds[i], creds[j]
		if sa, sb := a.Scope.Specificity(), b.Scope.Specificity(); sa != sb {
			return sa > sb
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if ka, kb := isKeyAuth(a), isKeyAuth(b); ka != kb {
			return ka
		}
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

func isKeyAuth(c vault.Credential) bool {
	return c.Method() == vault.AuthPublicKey || c.Method() == vault.AuthAgent
}

func containsCandidate(list []Candidate, id string) bool {
	for _, c := range list {
		if c.Cred.ID == id {
			return true
		}
	}
	return false
}

func hasAllTags(c vault.Credential, want []string) bool {
	for _, t := range want {
		if strings.TrimSpace(t) == "" {
			continue
		}
		if !c.HasTag(t) {
			return false
		}
	}
	return true
}

// scopeMatches reports whether a credential's scope admits the target. All
// populated scope fields must match. A platform-scoped credential is skipped
// for an unfingerprinted target rather than assumed to fit.
func scopeMatches(s vault.Scope, t Target) bool {
	if s.IsZero() {
		return true
	}
	if s.DomainSuffix != "" && !matchesSuffix(t.Identity, s.DomainSuffix) {
		return false
	}
	if len(s.Platforms) > 0 {
		if t.Platform == "" {
			return false
		}
		found := false
		for _, p := range s.Platforms {
			if strings.EqualFold(strings.TrimSpace(p), t.Platform) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if len(s.CIDRs) > 0 {
		if t.Addr == "" {
			return false
		}
		addr, err := netip.ParseAddr(t.Addr)
		if err != nil {
			return false
		}
		found := false
		for _, c := range s.CIDRs {
			pfx, err := netip.ParsePrefix(strings.TrimSpace(c))
			if err != nil {
				continue
			}
			if pfx.Contains(addr) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// bindingIDs is every shape of this target, for the binding store. Passing all
// of them is what stops one device warming two records.
func (t Target) bindingIDs() []string {
	out := make([]string, 0, len(t.Aliases)+2)
	if t.Identity != "" {
		out = append(out, t.Identity)
	}
	if t.Addr != "" {
		out = append(out, t.Addr)
	}
	return append(out, t.Aliases...)
}
