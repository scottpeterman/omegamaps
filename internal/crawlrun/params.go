// internal/crawlrun/params.go
//
// Crawl parameters, validated, and the profiles that keep you from typing them
// twice.
//
// This is the modal's model. Keeping it here rather than in the view means the
// rules about what a valid crawl looks like are testable without a toolkit,
// and a second front end — a wizard, a saved-job runner, the CLI itself — gets
// the same answers without restating them.
//
// # Profiles are not a nicety
//
// A dialog that opens empty every time is slower than the command line it
// replaces, and the person using it will go back to the command line. Named
// profiles are what make a form worth filling in once. This is the same idea
// as everything else in this package: state that survives the run is the
// difference between an application and a launcher.
//
// # Host keys
//
// Discovery meets devices it has never seen; unknown keys are the normal case,
// not an exception, so TOFU is the default and Strict is the opt-in for an
// estate whose keys are already pinned. There is deliberately no third option
// here. sshcore also has an Insecure mode, and Insecure is the only one that
// stops checking for a key that CHANGED — which is the case that means
// something. Skipping that is a decision someone should have to type on a
// command line, not tick in a dialog next to the concurrency box.
package crawlrun

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/scottpeterman/omegamaps/internal/secfile"
)

// HostKeyMode is the subset of sshcore's policy a UI may offer.
type HostKeyMode string

const (
	// HostKeyTOFU trusts an unknown key on first contact and records it. A
	// key that changes afterwards still fails closed.
	HostKeyTOFU HostKeyMode = "tofu"

	// HostKeyStrict requires the key to already be in known_hosts.
	HostKeyStrict HostKeyMode = "strict"
)

// Method is one way of collecting a device's identity and neighbors.
type Method string

const (
	// MethodSSH logs in, fingerprints the CLI, and parses the platform's
	// neighbor commands with its templates.
	MethodSSH Method = "ssh"

	// MethodSNMP reads the system group, LLDP-MIB and CISCO-CDP-MIB.
	MethodSNMP Method = "snmp"
)

// ParseMethods reads a method order from text: "snmp,ssh", "ssh snmp", "SSH".
// It applies the same checks as Validate, so a front end that assembles its
// own crawl from flags refuses exactly what a form would.
func ParseMethods(text string) ([]Method, error) {
	fields := strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
	ms := make([]Method, 0, len(fields))
	for _, f := range fields {
		ms = append(ms, Method(f))
	}
	ms = normalizeMethods(ms)
	if errs := methodErrors(ms); len(errs) > 0 {
		return nil, errs[0]
	}
	return ms, nil
}

func normalizeMethods(ms []Method) []Method {
	out := make([]Method, 0, len(ms))
	for _, m := range ms {
		if m = Method(strings.ToLower(strings.TrimSpace(string(m)))); m != "" {
			out = append(out, m)
		}
	}
	return out
}

func methodErrors(ms []Method) []ValidationError {
	var errs []ValidationError
	seen := map[Method]bool{}
	for _, m := range ms {
		switch m {
		case MethodSSH, MethodSNMP:
		default:
			errs = append(errs, ValidationError{"methods",
				fmt.Sprintf("unknown method %q; use ssh, snmp, or both in the order to try them", m)})
			continue
		}
		if seen[m] {
			errs = append(errs, ValidationError{"methods", fmt.Sprintf("%q is listed twice", m)})
		}
		seen[m] = true
	}
	return errs
}

// Params is everything a crawl needs to start.
type Params struct {
	Seeds []string `json:"seeds"`

	Depth       int           `json:"depth"`
	Concurrency int           `json:"concurrency"`
	Timeout     time.Duration `json:"timeout"`

	// Domains are appended when a bare neighbor name does not resolve, and
	// stripped from names in the map.
	Domains []string `json:"domains,omitempty"`

	// AllowDomains restricts which neighbors are dialed at all. Everything
	// else stays in the map as a leaf. Essential when a seed faces an
	// exchange or any shared fabric.
	AllowDomains []string `json:"allow_domains,omitempty"`

	// Exclude are substrings matched against platform, hostname and sysname.
	Exclude []string `json:"exclude,omitempty"`

	VaultPath   string   `json:"vault_path,omitempty"`
	CredTags    []string `json:"cred_tags,omitempty"`
	MaxCreds    int      `json:"max_creds,omitempty"`
	CredBreaker int      `json:"cred_breaker,omitempty"`

	HostKeys       HostKeyMode `json:"host_keys"`
	KnownHostsPath string      `json:"known_hosts_path,omitempty"`

	// Legacy enables old KEX and ciphers. Real gear needs it; it is an
	// algorithm policy, not a verification bypass.
	Legacy bool `json:"legacy,omitempty"`

	// TrustUnidirectional accepts one-sided link claims between discovered
	// devices, matching the legacy engine.
	TrustUnidirectional bool `json:"trust_unidirectional,omitempty"`

	// Methods is the order collection methods are tried on each device.
	// Empty means SSH only, which is what every profile saved before SNMP
	// existed means. A device moves to the next method when one fails, and
	// also when one reaches it but finds no neighbors -- a monitoring
	// community whose view stops at the system group answers sysName and
	// returns empty LLDP tables, and that must not become a silent leaf.
	//
	// SNMP credentials are secrets and are not here; see crawldial.Options.
	Methods []Method `json:"methods,omitempty"`

	// SNMPTimeout is per SNMP request, not per device. Zero uses the
	// collector's default (5s). It is also the price of every miss on a
	// v2c ladder, so a fabric that answers quickly is worth a lower value.
	SNMPTimeout time.Duration `json:"snmp_timeout,omitempty"`
}

// CollectionMethods is Methods with the default applied.
func (p Params) CollectionMethods() []Method {
	if len(p.Methods) == 0 {
		return []Method{MethodSSH}
	}
	return p.Methods
}

// Uses reports whether m is one of the methods a crawl with these parameters
// will try.
func (p Params) Uses(m Method) bool {
	for _, x := range p.CollectionMethods() {
		if x == m {
			return true
		}
	}
	return false
}

// Defaults matches the CLI's flag defaults, so the two front ends cannot drift
// into producing different crawls from the same intent.
func Defaults() Params {
	return Params{
		Depth:       3,
		Concurrency: 5,
		Timeout:     30 * time.Second,
		HostKeys:    HostKeyTOFU,
	}
}

// ParseSeeds splits a free-text field into seeds. Accepts newlines, commas,
// and spaces interchangeably, because a paste out of a spreadsheet, a ticket,
// or another terminal will use any of them.
func ParseSeeds(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t' || r == ';'
	})
	out := make([]string, 0, len(fields))
	seen := make(map[string]bool, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[strings.ToLower(f)] {
			continue
		}
		seen[strings.ToLower(f)] = true
		out = append(out, f)
	}
	return out
}

// ValidationError names the field so a form can highlight it rather than
// showing one message over the whole dialog.
type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) Error() string { return e.Field + ": " + e.Message }

// Normalize cleans up whitespace and casing in place. Called by Validate, and
// safe to call on its own as the user types.
func (p *Params) Normalize() {
	p.Seeds = cleanList(p.Seeds, false)
	p.Domains = cleanList(p.Domains, true)
	p.AllowDomains = cleanList(p.AllowDomains, true)
	p.Exclude = cleanPatterns(p.Exclude)
	p.CredTags = cleanList(p.CredTags, false)
	p.VaultPath = strings.TrimSpace(p.VaultPath)
	p.Methods = normalizeMethods(p.Methods)
	p.KnownHostsPath = strings.TrimSpace(p.KnownHostsPath)
	if p.HostKeys == "" {
		p.HostKeys = HostKeyTOFU
	}
}

// cleanPatterns is cleanList for exclude substrings: trimmed, lowercased and
// deduped, but WITHOUT the leading-dot strip. That strip exists because a
// domain suffix written ".lab.example" and one written "lab.example" are the
// same intent. An exclude pattern is not a suffix — ".net" and "net" are
// different substrings, and silently turning the first into the second makes
// a narrow pattern match far more than the person wrote.
func cleanPatterns(in []string) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanList(in []string, lower bool) []string {
	out := make([]string, 0, len(in))
	seen := make(map[string]bool, len(in))
	for _, v := range in {
		v = strings.TrimSpace(v)
		// A suffix written ".lab.example" and one written "lab.example" are
		// the same intent; the crawler wants the second form.
		v = strings.TrimPrefix(v, ".")
		if lower {
			v = strings.ToLower(v)
		}
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Validate reports every problem, not just the first, so a form can mark all
// the offending fields in one pass.
func (p *Params) Validate() []ValidationError {
	p.Normalize()
	var errs []ValidationError

	if len(p.Seeds) == 0 {
		errs = append(errs, ValidationError{"seeds", "at least one seed is required"})
	}
	for _, s := range p.Seeds {
		if !plausibleTarget(s) {
			errs = append(errs, ValidationError{"seeds",
				fmt.Sprintf("%q is not an address or a hostname", s)})
		}
	}
	if p.Depth < 0 {
		errs = append(errs, ValidationError{"depth", "must be 0 or more (0 crawls the seeds only)"})
	}
	if p.Concurrency < 1 {
		errs = append(errs, ValidationError{"concurrency", "must be at least 1"})
	}
	if p.Timeout <= 0 {
		errs = append(errs, ValidationError{"timeout", "must be greater than zero"})
	}
	switch p.HostKeys {
	case HostKeyTOFU, HostKeyStrict:
	case "insecure":
		errs = append(errs, ValidationError{"host_keys",
			"skipping host-key verification is not offered here; it also stops " +
				"detecting a key that changed, which is the case worth catching"})
	default:
		errs = append(errs, ValidationError{"host_keys",
			fmt.Sprintf("unknown mode %q; use tofu or strict", p.HostKeys)})
	}
	if p.HostKeys == HostKeyStrict && p.KnownHostsPath == "" {
		// Not fatal — sshcore falls back to ~/.ssh/known_hosts — but strict
		// discovery against the personal file is almost never what is meant.
		errs = append(errs, ValidationError{"known_hosts_path",
			"strict mode with no known_hosts path will use ~/.ssh/known_hosts; " +
				"point at a discovery-specific file instead"})
	}
	errs = append(errs, methodErrors(p.Methods)...)
	if p.SNMPTimeout < 0 {
		errs = append(errs, ValidationError{"snmp_timeout", "must be zero (the default) or more"})
	}
	for _, tag := range p.CredTags {
		if p.VaultPath == "" {
			errs = append(errs, ValidationError{"cred_tags",
				fmt.Sprintf("tag %q has no effect without a vault", tag)})
			break
		}
	}
	return errs
}

// plausibleTarget is a shape check, not a resolution attempt. A name that does
// not resolve today is still a legitimate seed — this lab's names have no DNS
// behind them at all, and the address fallback is what handles that.
func plausibleTarget(s string) bool {
	// A target may carry a port. dial.SplitTarget is what actually reads
	// it; this only has to stop rejecting the shape, and it checks the
	// host half so a bad name with a good port is still refused.
	if host, port, ok := splitTargetPort(s); ok {
		s = host
		_ = port
	}
	if _, err := netip.ParseAddr(s); err == nil {
		return true
	}
	if s == "" || len(s) > 253 || strings.ContainsAny(s, " \t/\\@") {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
				(r >= '0' && r <= '9') || r == '-' || r == '_'
			if !ok {
				return false
			}
		}
	}
	return true
}

// splitTargetPort reports the host half of a target that carries a port.
//
// It is a copy of the rule in internal/dial rather than a call to it: these
// two packages are the parameter layer and the dial layer, and a run model
// importing a dialer to validate a text field would be a dependency nobody
// reading the import list would predict. The rule is small and pinned by
// tests on both sides.
func splitTargetPort(s string) (string, int, bool) {
	host, portStr, err := net.SplitHostPort(strings.TrimSpace(s))
	if err != nil {
		return s, 0, false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return s, 0, false
	}
	return host, port, true
}

// Profile is a named set of parameters.
type Profile struct {
	Name     string    `json:"name"`
	Params   Params    `json:"params"`
	LastUsed time.Time `json:"last_used,omitempty"`
}

type profileFile struct {
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
}

// Profiles is the saved-parameters store.
type Profiles struct {
	path string
	m    map[string]Profile
}

// OpenProfiles loads (or creates) the profile store. A missing file is not an
// error; a corrupt one is, so a bad write is noticed rather than quietly
// presenting an empty list that looks like a first run.
func OpenProfiles(path string) (*Profiles, error) {
	p := &Profiles{path: path, m: map[string]Profile{}}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return p, nil
		}
		return nil, fmt.Errorf("failed to read crawl profiles: %w", err)
	}
	if len(raw) == 0 {
		return p, nil
	}
	var f profileFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("crawl profiles file is corrupt: %w", err)
	}
	for _, pr := range f.Profiles {
		if pr.Name != "" {
			p.m[pr.Name] = pr
		}
	}
	return p, nil
}

// Names returns the profiles most-recently-used first, which is the order a
// dropdown wants.
func (p *Profiles) Names() []string {
	out := make([]string, 0, len(p.m))
	for n := range p.m {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := p.m[out[i]], p.m[out[j]]
		if !a.LastUsed.Equal(b.LastUsed) {
			return a.LastUsed.After(b.LastUsed)
		}
		return out[i] < out[j]
	})
	return out
}

// Get returns a profile by name.
func (p *Profiles) Get(name string) (Profile, bool) {
	pr, ok := p.m[name]
	return pr, ok
}

// Save stores a profile under name, replacing any existing one. The params are
// normalized first so two profiles cannot differ only by whitespace.
func (p *Profiles) Save(name string, params Params) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ValidationError{"name", "a profile needs a name"}
	}
	params.Normalize()
	p.m[name] = Profile{Name: name, Params: params, LastUsed: time.Now()}
	return p.flush()
}

// Touch records that a profile was used, so it sorts to the top next time.
func (p *Profiles) Touch(name string) error {
	pr, ok := p.m[name]
	if !ok {
		return nil
	}
	pr.LastUsed = time.Now()
	p.m[name] = pr
	return p.flush()
}

// Delete removes a profile.
func (p *Profiles) Delete(name string) error {
	if _, ok := p.m[name]; !ok {
		return nil
	}
	delete(p.m, name)
	return p.flush()
}

func (p *Profiles) flush() error {
	f := profileFile{Version: 1}
	for _, n := range p.Names() {
		f.Profiles = append(f.Profiles, p.m[n])
	}
	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal crawl profiles: %w", err)
	}
	if dir := filepath.Dir(p.path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("failed to create profile directory: %w", err)
		}
	}
	if err := secfile.WriteAtomic(p.path, out); err != nil {
		return fmt.Errorf("failed to write crawl profiles: %w", err)
	}
	return nil
}
