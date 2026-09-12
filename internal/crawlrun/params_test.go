// internal/crawlrun/params_test.go
package crawlrun

import (
	"path/filepath"
	"testing"
	"time"
)

func fieldsOf(errs []ValidationError) map[string]string {
	out := map[string]string{}
	for _, e := range errs {
		out[e.Field] = e.Message
	}
	return out
}

func TestDefaultsAreValid(t *testing.T) {
	p := Defaults()
	p.Seeds = []string{"172.16.1.2"}
	if errs := p.Validate(); len(errs) != 0 {
		t.Fatalf("defaults did not validate: %+v", errs)
	}
}

func TestParseSeedsAcceptsWhateverGotPasted(t *testing.T) {
	got := ParseSeeds("172.16.1.2, wan-core-1.lab.local\n eng-rtr-1.lab.local;172.16.1.2\n\n")
	want := []string{"172.16.1.2", "wan-core-1.lab.local", "eng-rtr-1.lab.local"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("seed %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestValidateReportsEveryProblemAtOnce(t *testing.T) {
	p := Params{
		Depth:       -1,
		Concurrency: 0,
		Timeout:     0,
		HostKeys:    HostKeyTOFU,
	}
	f := fieldsOf(p.Validate())
	for _, want := range []string{"seeds", "depth", "concurrency", "timeout"} {
		if _, ok := f[want]; !ok {
			t.Errorf("validation missed %s: %+v", want, f)
		}
	}
}

// Discovery meets devices it has never seen, so an unknown key is the normal
// case and TOFU must be the default.
func TestTOFUIsTheDefaultAndInsecureIsRefused(t *testing.T) {
	if Defaults().HostKeys != HostKeyTOFU {
		t.Errorf("default host key mode is %q, want tofu", Defaults().HostKeys)
	}

	p := Defaults()
	p.Seeds = []string{"172.16.1.2"}
	p.HostKeys = "insecure"
	f := fieldsOf(p.Validate())
	if _, ok := f["host_keys"]; !ok {
		t.Error("insecure mode was accepted from a form")
	}

	// Strict remains available for an estate whose keys are already pinned.
	p.HostKeys = HostKeyStrict
	p.KnownHostsPath = "/tmp/discovery_known_hosts"
	if errs := p.Validate(); len(errs) != 0 {
		t.Errorf("strict mode rejected: %+v", errs)
	}
}

func TestStrictWithoutADedicatedKnownHostsIsFlagged(t *testing.T) {
	p := Defaults()
	p.Seeds = []string{"172.16.1.2"}
	p.HostKeys = HostKeyStrict
	if _, ok := fieldsOf(p.Validate())["known_hosts_path"]; !ok {
		t.Error("strict mode against the personal known_hosts was not flagged")
	}
}

func TestNormalizeMakesSuffixFormsAgree(t *testing.T) {
	p := Params{
		Seeds:        []string{" 172.16.1.2 ", "172.16.1.2"},
		Domains:      []string{".Lab.Local", "lab.local", "  "},
		AllowDomains: []string{".LAB.LOCAL"},
	}
	p.Normalize()

	if len(p.Seeds) != 1 {
		t.Errorf("duplicate seed survived: %v", p.Seeds)
	}
	if len(p.Domains) != 1 || p.Domains[0] != "lab.local" {
		t.Errorf("domains = %v, want [lab.local]", p.Domains)
	}
	if len(p.AllowDomains) != 1 || p.AllowDomains[0] != "lab.local" {
		t.Errorf("allow domains = %v", p.AllowDomains)
	}
}

// A name with no DNS behind it is still a legitimate seed — this lab's names
// do not resolve at all, and the address fallback is what covers that.
func TestUnresolvableNamesAreStillValidSeeds(t *testing.T) {
	p := Defaults()
	p.Seeds = []string{"wan-core-1.lab.local", "eng-spine-1", "172.16.128.2"}
	if errs := p.Validate(); len(errs) != 0 {
		t.Errorf("valid seeds rejected: %+v", errs)
	}

	p.Seeds = []string{"user@host", "has space", "//nope"}
	if got := len(p.Validate()); got != 3 {
		t.Errorf("got %d seed errors, want 3", got)
	}
}

func TestCredTagsWithoutAVaultAreFlagged(t *testing.T) {
	p := Defaults()
	p.Seeds = []string{"172.16.1.2"}
	p.CredTags = []string{"lab"}
	if _, ok := fieldsOf(p.Validate())["cred_tags"]; !ok {
		t.Error("cred tags without a vault were silently accepted")
	}
}

func TestProfilesSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "crawl-profiles.json")
	store, err := OpenProfiles(path)
	if err != nil {
		t.Fatal(err)
	}

	lab := Defaults()
	lab.Seeds = []string{"172.16.1.2", "172.16.128.2"}
	lab.Domains = []string{"lab.local"}
	if err := store.Save("lab", lab); err != nil {
		t.Fatal(err)
	}
	edge := Defaults()
	edge.Seeds = []string{"wan-core-1.lab.local"}
	edge.Depth = 1
	if err := store.Save("edge only", edge); err != nil {
		t.Fatal(err)
	}

	again, err := OpenProfiles(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := again.Get("lab")
	if !ok {
		t.Fatal("profile did not survive the reopen")
	}
	if len(got.Params.Seeds) != 2 || got.Params.Domains[0] != "lab.local" {
		t.Errorf("profile came back wrong: %+v", got.Params)
	}
	if got.Params.Timeout != 30*time.Second {
		t.Errorf("duration round trip lost: %v", got.Params.Timeout)
	}

	// Most recently used sorts first, so the dropdown opens on the one you
	// are actually iterating on.
	if err := again.Touch("lab"); err != nil {
		t.Fatal(err)
	}
	if names := again.Names(); names[0] != "lab" {
		t.Errorf("names = %v, want lab first", names)
	}

	if err := again.Delete("edge only"); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Get("edge only"); ok {
		t.Error("delete did not take")
	}
}

func TestUnnamedProfileIsRefused(t *testing.T) {
	store, err := OpenProfiles(filepath.Join(t.TempDir(), "p.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save("  ", Defaults()); err == nil {
		t.Error("an unnamed profile was saved")
	}
}

func TestHostKeyEventIsNotableAndCounted(t *testing.T) {
	r := New()
	e := r.Emit()
	e.Send(Event{Kind: KindQueued, Identity: "wan-core-1"})
	e.Send(Event{Kind: KindHostKeyNew, Identity: "wan-core-1",
		Detail: "ssh-rsa SHA256:abc123"})
	e.Send(Event{Kind: KindReached, Identity: "wan-core-1"})
	r.Finish()

	if got := r.Counts().NewHostKeys; got != 1 {
		t.Errorf("new host keys = %d, want 1", got)
	}
	var found bool
	for _, ev := range r.Decisions() {
		if ev.Kind == KindHostKeyNew {
			found = true
			if ev.Describe() == "" {
				t.Error("host key event has no description")
			}
		}
	}
	if !found {
		t.Error("first-contact host key did not reach the decisions view")
	}
	// Accepting a key must not stop the device counting as reached.
	if got := r.Counts().Reached; got != 1 {
		t.Errorf("reached = %d, want 1", got)
	}
}
