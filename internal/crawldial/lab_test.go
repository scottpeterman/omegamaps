package crawldial

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/fakedev"
	"github.com/scottpeterman/omegamaps/internal/topo"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

// The whole stack against the fake lab: vault-resolved SSH, platform
// detection, LLDP collection, names that do not resolve retried at the
// addresses neighbors report, an excluded host left undialed, a rejected
// credential, and a map. Needs the lab's addresses on this host and the right
// to bind port 22 (see fakedev.LabAddrCommands); skipped otherwise.
func TestCrawlTheFakeLab(t *testing.T) {
	lab, err := fakedev.StartLab(0)
	if err != nil {
		t.Skipf("fake lab unavailable (%v); give the host its addresses with:\n  %s",
			err, strings.Join(fakedev.LabAddrCommands(), "\n  "))
	}
	defer lab.Close()

	dir := t.TempDir()
	v := vault.New(filepath.Join(dir, "vault.json"))
	if err := v.Create("lab-master-pass"); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Add(vault.Credential{Name: "lab", Username: fakedev.LabUser,
		AuthType: "password", Password: fakedev.LabPassword, Tags: []string{"lab"}}); err != nil {
		t.Fatal(err)
	}

	p := crawlrun.Defaults()
	p.Seeds = []string{fakedev.LabSeed}
	p.Depth = 3
	p.Domains = []string{"lab.local"}
	p.Exclude = []string{"linux"}
	p.Methods = []crawlrun.Method{crawlrun.MethodSSH}
	p.VaultPath = v.Path()
	p.KnownHostsPath = filepath.Join(dir, "known_hosts")
	p.Timeout = 5 * time.Second

	run := crawlrun.New()
	run.KeepNotes(0)
	b, err := Build(p, Options{Vault: v, Emit: run.Emit()})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	devices := b.Crawler.CrawlContext(ctx, p.Seeds)
	run.Finish()

	c := run.Counts()
	if c.Reached != 5 || c.Failed != 1 || c.NotDialed != 1 {
		for _, r := range run.Rows() {
			t.Logf("%-14s %-10s %s", r.Display(), r.State, r.Detail)
		}
		t.Fatalf("counts = %+v; want 5 reached, 1 failed (eng-leaf-1), 1 not dialed (eng-host-9)", c)
	}
	for _, r := range run.Rows() {
		switch r.Display() {
		case "eng-leaf-1":
			if r.State != crawlrun.StateFailed {
				t.Errorf("eng-leaf-1 = %s, want failed on the rejected credential", r.State)
			}
		case "eng-host-9":
			if r.State != crawlrun.StateNotDialed {
				t.Errorf("eng-host-9 = %s, want not dialed by the exclude", r.State)
			}
		}
	}

	m := topo.Generate(devices, MapOptions(p))
	for _, want := range []string{"wan-core-1", "usa-rtr-1", "eng-rtr-1", "eng-spine-1", "eng-spine-2"} {
		if _, ok := m[want]; !ok {
			t.Errorf("map has no %s; nodes: %v", want, keys(m))
		}
	}
	if peers := m["eng-rtr-1"].Peers; len(peers) < 4 {
		t.Errorf("eng-rtr-1 has %d peers in the map, want its 4 links", len(peers))
	}
	// The device that failed stays on the map as a leaf of the one that
	// reported it, not dropped for failing to confirm a link it never could.
	if _, ok := m["eng-spine-1"].Peers["eng-leaf-1"]; !ok {
		t.Errorf("eng-spine-1 -> eng-leaf-1 missing from the map; peers: %v", m["eng-spine-1"].Peers)
	}
	// And the excluded host is a leaf too: reported, never dialed, still drawn.
	if _, ok := m["eng-spine-2"].Peers["eng-host-9"]; !ok {
		t.Errorf("eng-spine-2 -> eng-host-9 missing from the map; peers: %v", m["eng-spine-2"].Peers)
	}

	// Trust on first use wrote to the file it was given, not the user's own.
	kh, err := os.ReadFile(p.KnownHostsPath)
	if err != nil || strings.Count(string(kh), "\n") < 5 {
		t.Errorf("known_hosts: %v; %d lines, want one per reached device", err, strings.Count(string(kh), "\n"))
	}
}

func keys(m map[string]topo.MapNode) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
