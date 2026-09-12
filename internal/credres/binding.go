// internal/credres/binding.go
//
// Persisted last-known-good bindings, keyed on an alias set.
//
// A binding is "this device last authenticated with credential ID Y". It holds
// no secret material, so it lives in a plain JSON file next to the map data
// rather than inside the encrypted vault. Keeping it out of the vault matters:
// the binding store has to be readable to plan a crawl before anyone has typed
// a master password, and it has to survive a vault re-key untouched.
//
// A binding is a hint, never an authority. If the pinned credential fails to
// authenticate, the resolver drops the pin and walks the full ladder.
//
// # Why an alias set rather than a key
//
// The same device answers to several strings. LLDP, CDP, DNS and the device's
// own prompt each report it differently, and the address is a fourth shape on
// top of those. A store keyed on one string records the device once per shape
// it was addressed by: an address-seeded run and a name-seeded run warm two
// separate entries and neither helps the other.
//
// Nothing about that failure is loud. A miss is indistinguishable from first
// contact — the crawl just walks the ladder again, which spends failed
// authentication attempts against live AAA on every run. That re-spend is the
// cost this file exists to remove; the in-memory negative cache and breaker in
// credres.go reset with the process, so this store is the only thing that
// learns across runs.
//
// So a record carries a set of aliases, any one of which finds it. Canonical
// is a display label, not the key — the device's own reported name is not
// known until after authentication, so it can never be what you look up by on
// first contact, only the thing everything else aliases to.
//
// # The three shapes
//
// An identity is admitted in three forms, and no others:
//
//	short hostname     eng-spine-1
//	fully qualified    eng-spine-1.lab.local
//	full address       172.16.1.2
//
// The short form of an address is not one of them. Splitting 172.16.128.2 at
// the first dot yields "172", which every other 172.x device in the estate
// also yields — one key for an entire prefix. In the crawl claim set that
// collision costs a device that is silently never visited; here it would hand
// one device's credential to another. normalize.ShortName carries a
// netip.ParseAddr guard for exactly this reason, and this file relies on it.
//
// # Strong and weak aliases
//
// Aliases differ in how much they prove, and the difference decides only one
// thing: whether an alias may MERGE two existing records.
//
//	strong  an address, or a name reported with a dot in it
//	weak    a bare label, and anything this package derived by shortening
//
// Two records sharing a strong alias are one device and are merged. A weak
// alias will resolve a lookup, but a weak alias already owned by a different
// record is refused rather than merged, and the refusal is logged. The costs
// are asymmetric: refusing loses a cache hit and falls back to the ladder,
// while merging wrongly offers device A's credential to device B, which is a
// failed authentication this package generated itself.
//
// The one case where a weak alias may still select a record is a bind that
// finds no strong match at all and exactly one weak match — the bare-prompt
// device, where the pre-auth Record and the post-crawl Bind share only the
// short name. That assumes first labels are unique across domains, which is
// the assumption tracked in Deferred.md: the symptom to watch for is a record
// whose alias set holds two fully qualified names under different domains.
package credres

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/secfile"
)

// bindingFormatVersion is the on-disk format. Version 1 was keyed on a single
// "identity" string; it is migrated on read and never written again.
const bindingFormatVersion = 2

// cgnat is the shared address space from RFC 6598.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// Binding is one device's record: every string it is known by, and the
// credential that last authenticated to it.
type Binding struct {
	// Canonical is a display label — the device's own reported name once
	// something has learned it, otherwise whatever it was first reached by.
	// Never used as a lookup key.
	Canonical string `json:"canonical"`

	// Aliases is every string that resolves to this record, sorted for a
	// stable file. Canonical is always among them.
	Aliases []string `json:"aliases"`

	CredID string    `json:"cred_id,omitempty"`
	LastOK time.Time `json:"last_ok,omitempty"`
	Hits   int       `json:"hits"`
}

// BindingStore is the persistence interface. Implementations must be safe for
// concurrent use; a crawl resolves from many goroutines.
//
// The identity arguments are variadic because a caller rarely holds one shape.
// Passing every shape it has is correct and cheap: aliases only ever widen a
// lookup, and on a write they are what keeps two shapes of one device from
// becoming two records.
type BindingStore interface {
	// Lookup returns the pinned credential for the device any of ids
	// resolves to. A record whose pin has been forgotten reports false: the
	// question this answers is "is there a credential to try first", and a
	// record that exists only to hold aliases is not an answer to it.
	Lookup(ids ...string) (Binding, bool)

	// Resolve returns the record any of ids belongs to, credential or not.
	//
	// Lookup deliberately hides an alias-only record, because "is there a
	// credential to try first" has no answer there. Resolve asks a
	// different question — "which device is this" — and for that an
	// alias-only record is the whole point. Capture keys its storage on
	// the canonical name, so without this a device that was folded but
	// never had a credential recorded, or one whose pin was forgotten,
	// would file its config history under whatever string it happened to
	// be dialed by.
	Resolve(ids ...string) (Binding, bool)

	// Record asserts that credID authenticated to the device known by ids.
	// Note the argument order: credID first, then the identities.
	Record(credID string, ids ...string) error

	// Forget clears the credential pin without discarding the alias set. The
	// pin being stale is not evidence that the identities are wrong.
	Forget(ids ...string) error

	// Bind attaches aliases to a record without asserting anything about
	// credentials. This is the post-crawl fold: the device's reported name
	// and answering address, learned after authentication already happened.
	Bind(canonical string, aliases ...string) error
}

// shape is one admitted form of an identity.
type shape struct {
	name   string
	strong bool
}

// shapesFor expands one identity string into the forms it should be indexed
// under, or nil if it is not usable as an identity at all.
//
// Parsing debris from a mis-anchored template and a chassis MAC handed back in
// place of a sysname are both rejected here rather than downstream: a neighbor
// table yields both, and either one attached to a record is a wrong alias.
func shapesFor(id string) []shape {
	s := normalize.Identifier(strings.TrimSpace(id))
	if s == "" || normalize.IsArtifactName(s) {
		return nil
	}
	// An address is indexed whole and never shortened.
	if a, err := netip.ParseAddr(s); err == nil {
		// Except a CGNAT address, which is not admitted at all. It is
		// carrier-assigned and may point at a different device tomorrow, so
		// it names a route rather than a box — and normalize.Canonical
		// already replaces one with its PTR name for that reason. Admitting
		// the raw address here would put the credential pin back on the
		// unstable thing. Refusing costs a cache miss; accepting wrongly
		// costs a credential offered to whatever holds the address next.
		if cgnat.Contains(a) {
			return nil
		}
		return []shape{{name: s, strong: true}}
	}
	if normalize.IsMACAddress(s) {
		return nil
	}
	// As reported: strong when qualified, weak when it is a bare label.
	out := []shape{{name: s, strong: strings.Contains(s, ".")}}
	// Derived, therefore weak even though it may contain a dot.
	if short := normalize.ShortName(s); short != "" && short != s {
		out = append(out, shape{name: short, strong: false})
	}
	return out
}

// shapesForAll expands several identities, de-duplicated. An alias seen both
// strong and weak is strong.
func shapesForAll(ids []string) []shape {
	seen := make(map[string]int, len(ids)*2)
	var out []shape
	for _, id := range ids {
		for _, sh := range shapesFor(id) {
			if i, ok := seen[sh.name]; ok {
				if sh.strong {
					out[i].strong = true
				}
				continue
			}
			seen[sh.name] = len(out)
			out = append(out, sh)
		}
	}
	return out
}

// record is the in-memory form. Pointers, because several aliases index the
// same record and a merge has to be visible through all of them.
type record struct {
	canonical string
	aliases   map[string]bool // alias -> strong
	credID    string
	lastOK    time.Time
	hits      int
}

// hasStrong reports whether anything has ever identified this record by an
// address or a qualified name. A record without one is a stub — something was
// reached by a bare prompt name and nothing since has said what it is. Joining
// a stub is safe; joining an identified device on a shared label is not.
func (r *record) hasStrong() bool {
	for _, strong := range r.aliases {
		if strong {
			return true
		}
	}
	return false
}

func (r *record) snapshot() Binding {
	names := make([]string, 0, len(r.aliases))
	for a := range r.aliases {
		names = append(names, a)
	}
	sort.Strings(names)
	return Binding{
		Canonical: r.canonical,
		Aliases:   names,
		CredID:    r.credID,
		LastOK:    r.lastOK,
		Hits:      r.hits,
	}
}

// betterCanonical picks the more useful display label. A name beats an
// address, and a qualified name beats a bare one; a map node labelled
// 172.16.1.2 when the box calls itself wan-core-1 is the case this avoids.
func betterCanonical(a, b string) string {
	if a == "" {
		return b
	}
	if b == "" {
		return a
	}
	rank := func(s string) int {
		if _, err := netip.ParseAddr(s); err == nil {
			return 0
		}
		if strings.Contains(s, ".") {
			return 2
		}
		return 1
	}
	ra, rb := rank(a), rank(b)
	if rb > ra {
		return b
	}
	if rb < ra {
		return a
	}
	// Equal rank: pick deterministically, since the candidates arrive from a
	// map and the winner would otherwise vary between runs.
	if b < a {
		return b
	}
	return a
}

// aliasSet is the shared core of both stores. It is not safe for concurrent
// use on its own; the wrappers hold the lock.
type aliasSet struct {
	records []*record
	index   map[string]*record
	logf    func(string, ...any)
}

func newAliasSet() *aliasSet {
	return &aliasSet{index: make(map[string]*record)}
}

func (s *aliasSet) log(format string, args ...any) {
	if s.logf != nil {
		s.logf(format, args...)
	}
}

func (s *aliasSet) lookup(ids []string) (*record, bool) {
	for _, sh := range shapesForAll(ids) {
		if r, ok := s.index[sh.name]; ok {
			return r, true
		}
	}
	return nil, false
}

// merge folds src into dst: hits sum, the more recent success supplies the
// credential, and the better label wins.
func (s *aliasSet) merge(dst, src *record) {
	if src == dst {
		return
	}
	for a, strong := range src.aliases {
		if dst.aliases[a] || strong {
			dst.aliases[a] = dst.aliases[a] || strong
		} else if _, ok := dst.aliases[a]; !ok {
			dst.aliases[a] = strong
		}
		s.index[a] = dst
	}
	dst.hits += src.hits
	if src.lastOK.After(dst.lastOK) {
		dst.lastOK, dst.credID = src.lastOK, src.credID
	} else if dst.credID == "" {
		dst.credID = src.credID
	}
	dst.canonical = betterCanonical(dst.canonical, src.canonical)

	for i, r := range s.records {
		if r == src {
			s.records = append(s.records[:i], s.records[i+1:]...)
			break
		}
	}
}

// bind resolves ids to a single record, creating or merging as needed, and
// attaches every admitted alias. Returns nil if ids held nothing usable.
func (s *aliasSet) bind(canonical string, ids []string) *record {
	shapes := shapesForAll(ids)
	if len(shapes) == 0 {
		return nil
	}

	// Strong matches decide the record, and two of them mean one device.
	var target *record
	var haveStrong bool
	for _, sh := range shapes {
		if !sh.strong {
			continue
		}
		haveStrong = true
		r, ok := s.index[sh.name]
		if !ok {
			continue
		}
		if target == nil {
			target = r
			continue
		}
		s.merge(target, r)
	}

	// No strong match. A single unambiguous weak match still selects — the
	// bare-prompt device, whose pre-auth and post-crawl writes share only a
	// short name. Ambiguous, or none, and this is a new record.
	if target == nil {
		var weakHit *record
		ambiguous := false
		for _, sh := range shapes {
			if sh.strong {
				continue
			}
			if r, ok := s.index[sh.name]; ok {
				if weakHit != nil && weakHit != r {
					ambiguous = true
					break
				}
				weakHit = r
			}
		}
		switch {
		case ambiguous:
			s.log("credres: %q matches more than one binding by short name; starting a new record",
				shapes[0].name)
		case weakHit == nil:
			// Nothing known. New record.
		case !weakHit.hasStrong():
			// The match is a stub: reached by a bare name and never since
			// identified. This bind is what identifies it.
			target = weakHit
		case !haveStrong:
			// We have nothing but a bare name either, so the label is all
			// there is to go on and it is the only lead available.
			target = weakHit
		default:
			// Both sides are identified and they disagree: two devices that
			// happen to share a first label. Merging them here is how one
			// device's credential would be offered to the other.
			s.log("credres: %q is already identified; not merging it with new %q on a shared short name",
				weakHit.canonical, shapes[0].name)
		}
	}

	if target == nil {
		target = &record{aliases: make(map[string]bool)}
		s.records = append(s.records, target)
	}

	for _, sh := range shapes {
		if sh.strong {
			target.aliases[sh.name] = true
			s.index[sh.name] = target
			continue
		}
		// Weak: attach only if unowned or already ours.
		owner, ok := s.index[sh.name]
		if !ok {
			if _, have := target.aliases[sh.name]; !have {
				target.aliases[sh.name] = false
			}
			s.index[sh.name] = target
			continue
		}
		if owner != target {
			s.log("credres: refusing short name %q for %q; it already belongs to %q",
				sh.name, betterCanonical(canonical, target.canonical), owner.canonical)
		}
	}

	// Canonical is derived from the alias set rather than from whichever
	// call happened to supply it. Order-independence matters more than it
	// sounds: this label is written to the file, so a canonical that moved
	// with call order would churn the store on every run and make a real
	// change indistinguishable from noise in a diff.
	target.canonical = betterCanonical(target.canonical, canonical)
	for a := range target.aliases {
		target.canonical = betterCanonical(target.canonical, a)
	}
	if target.canonical == "" {
		target.canonical = shapes[0].name
	}
	return target
}

func (s *aliasSet) record(credID string, ids []string) {
	r := s.bind("", ids)
	if r == nil {
		return
	}
	r.credID = credID
	r.lastOK = time.Now()
	r.hits++
}

// forget clears the pin and leaves the alias set intact.
//
// A credential that stopped working says nothing about which strings name the
// device, and throwing the aliases away would make the next run re-learn the
// identity from scratch and re-split the record. The record stops answering
// Lookup, so callers see the pin as gone, but a later Bind still merges into
// it rather than starting over.
func (s *aliasSet) forget(ids []string) bool {
	r, ok := s.lookup(ids)
	if !ok || r.credID == "" {
		return false
	}
	r.credID = ""
	return true
}

func (s *aliasSet) snapshotAll() []Binding {
	out := make([]Binding, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r.snapshot())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Canonical < out[j].Canonical })
	return out
}

// load rebuilds the set from decoded records. Strongness is recomputed from
// each alias rather than persisted, so the rule can change without a format
// bump invalidating every stored file.
func (s *aliasSet) load(bs []Binding) {
	for _, b := range bs {
		ids := b.Aliases
		if b.Canonical != "" {
			ids = append([]string{b.Canonical}, ids...)
		}
		r := s.bind(b.Canonical, ids)
		if r == nil {
			continue
		}
		r.hits += b.Hits
		if b.LastOK.After(r.lastOK) {
			r.lastOK, r.credID = b.LastOK, b.CredID
		} else if r.credID == "" {
			r.credID = b.CredID
		}
	}
}

// MemoryBindings is an in-memory store. Useful for tests and for a run that
// should not leave state behind.
type MemoryBindings struct {
	mu sync.RWMutex
	s  *aliasSet
}

// NewMemoryBindings returns an empty in-memory binding store.
func NewMemoryBindings() *MemoryBindings {
	return &MemoryBindings{s: newAliasSet()}
}

// SetLogger installs a log sink for refused aliases and merges.
func (m *MemoryBindings) SetLogger(f func(string, ...any)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.logf = f
}

func (m *MemoryBindings) Lookup(ids ...string) (Binding, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.s.lookup(ids)
	if !ok || r.credID == "" {
		return Binding{}, false
	}
	return r.snapshot(), true
}

func (m *MemoryBindings) Resolve(ids ...string) (Binding, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.s.lookup(ids)
	if !ok {
		return Binding{}, false
	}
	return r.snapshot(), true
}

func (m *MemoryBindings) Record(credID string, ids ...string) error {
	if credID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.record(credID, ids)
	return nil
}

func (m *MemoryBindings) Forget(ids ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.forget(ids)
	return nil
}

func (m *MemoryBindings) Bind(canonical string, aliases ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.s.bind(canonical, append([]string{canonical}, aliases...))
	return nil
}

// Len reports how many devices are held. Note this counts records, not
// aliases: the whole point is that it falls when two shapes turn out to be one
// device.
func (m *MemoryBindings) Len() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.s.records)
}

// FileBindings persists bindings to a JSON file. Every write rewrites the file
// atomically, which is fine at the scale a single crawl produces.
type FileBindings struct {
	path string

	mu       sync.RWMutex
	s        *aliasSet
	suffixes []string
}

type bindingFile struct {
	Version int `json:"version"`

	// Suffixes is the union of every domain-suffix context this file has been
	// written under. Identity does not depend on it — aliases already span the
	// stripped and unstripped forms — so this is a check, not a fix: a context
	// the file has not seen before is worth one log line, because it is the
	// condition under which records used to split silently.
	Suffixes []string `json:"suffixes,omitempty"`

	Bindings []storedBinding `json:"bindings"`
}

// storedBinding decodes both formats. Version 1 carried a single "identity";
// it becomes the first alias and the initial label.
type storedBinding struct {
	Identity  string    `json:"identity,omitempty"`
	Canonical string    `json:"canonical,omitempty"`
	Aliases   []string  `json:"aliases,omitempty"`
	CredID    string    `json:"cred_id"`
	LastOK    time.Time `json:"last_ok"`
	Hits      int       `json:"hits"`
}

func (sb storedBinding) toBinding() Binding {
	b := Binding{
		Canonical: sb.Canonical,
		Aliases:   sb.Aliases,
		CredID:    sb.CredID,
		LastOK:    sb.LastOK,
		Hits:      sb.Hits,
	}
	if b.Canonical == "" {
		b.Canonical = sb.Identity
	}
	if len(b.Aliases) == 0 && sb.Identity != "" {
		b.Aliases = []string{sb.Identity}
	}
	return b
}

// OpenFileBindings loads (or creates) a binding file. A missing file is not an
// error; a corrupt one is, so a truncated write is noticed rather than
// silently discarding a warm cache and looking like first contact.
//
// A version 1 file is migrated in memory on read and rewritten in the new
// format on the first write. Migration is not a merge: each old identity
// becomes its own record, and records only fold together when a later bind
// supplies an alias they share — which is what the post-crawl fold does.
func OpenFileBindings(path string) (*FileBindings, error) {
	s := &FileBindings{path: path, s: newAliasSet()}

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("failed to read binding store: %w", err)
	}
	if len(raw) == 0 {
		return s, nil
	}
	var f bindingFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("binding store is corrupt: %w", err)
	}
	s.suffixes = f.Suffixes

	bs := make([]Binding, 0, len(f.Bindings))
	for _, sb := range f.Bindings {
		bs = append(bs, sb.toBinding())
	}
	s.s.load(bs)
	return s, nil
}

// SetLogger installs a log sink for refused aliases and merges.
func (s *FileBindings) SetLogger(f func(string, ...any)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s.logf = f
}

// NoteContext records the domain-suffix context of this run and reports what
// the file had seen before and whether this run introduces something new.
func (s *FileBindings) NoteContext(suffixes []string) (prior []string, isNew bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	prior = append([]string(nil), s.suffixes...)
	have := make(map[string]bool, len(s.suffixes))
	for _, x := range s.suffixes {
		have[normalize.Identifier(x)] = true
	}
	for _, x := range suffixes {
		x = normalize.Identifier(strings.TrimPrefix(strings.TrimSpace(x), "."))
		if x == "" || have[x] {
			continue
		}
		have[x] = true
		s.suffixes = append(s.suffixes, x)
		isNew = true
	}
	if isNew {
		sort.Strings(s.suffixes)
		_ = s.saveLocked()
	}
	return prior, isNew
}

func (s *FileBindings) Lookup(ids ...string) (Binding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.s.lookup(ids)
	if !ok || r.credID == "" {
		return Binding{}, false
	}
	return r.snapshot(), true
}

func (s *FileBindings) Resolve(ids ...string) (Binding, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.s.lookup(ids)
	if !ok {
		return Binding{}, false
	}
	return r.snapshot(), true
}

func (s *FileBindings) Record(credID string, ids ...string) error {
	if credID == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.s.record(credID, ids)
	return s.saveLocked()
}

func (s *FileBindings) Forget(ids ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.s.forget(ids) {
		return nil
	}
	return s.saveLocked()
}

func (s *FileBindings) Bind(canonical string, aliases ...string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.s.bind(canonical, append([]string{canonical}, aliases...)) == nil {
		return nil
	}
	return s.saveLocked()
}

// Len reports how many devices are held.
func (s *FileBindings) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.s.records)
}

// saveLocked writes the store atomically. Caller holds s.mu.
func (s *FileBindings) saveLocked() error {
	f := bindingFile{Version: bindingFormatVersion, Suffixes: s.suffixes}
	for _, b := range s.s.snapshotAll() {
		f.Bindings = append(f.Bindings, storedBinding{
			Canonical: b.Canonical,
			Aliases:   b.Aliases,
			CredID:    b.CredID,
			LastOK:    b.LastOK,
			Hits:      b.Hits,
		})
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal binding store: %w", err)
	}
	if err := secfile.WriteAtomic(s.path, out); err != nil {
		return fmt.Errorf("failed to write binding store: %w", err)
	}
	return nil
}
