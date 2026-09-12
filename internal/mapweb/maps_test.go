// internal/mapweb/maps_test.go
package mapweb

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

func TestListMapsCountsDevicesAndLeaves(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "map.json", labMap)

	got, err := ListMaps(dir)
	if err != nil {
		t.Fatalf("ListMaps: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d files, want 1", len(got))
	}

	f := got[0]
	if !f.OK() {
		t.Fatalf("problem = %q", f.Problem)
	}
	if f.Devices != 2 || f.Leaves != 1 || f.Nodes() != 3 {
		t.Errorf("devices=%d leaves=%d nodes=%d, want 2/1/3", f.Devices, f.Leaves, f.Nodes())
	}
}

// A half-written map is exactly the file somebody goes looking for, so it is
// listed with the reason rather than quietly dropped.
func TestAFileThatIsNotAMapIsListedWithTheReason(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "good.json", labMap)
	writeFile(t, dir, "truncated.json", `{"eng-rtr-1": {"node_details":`)
	writeFile(t, dir, "empty.json", ``)

	got, err := ListMaps(dir)
	if err != nil {
		t.Fatalf("ListMaps: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("listed %d files, want 3", len(got))
	}

	byName := map[string]MapFile{}
	for _, f := range got {
		byName[f.Name] = f
	}
	if byName["truncated.json"].OK() {
		t.Error("a truncated map listed as openable")
	}
	if byName["empty.json"].Problem != "empty file" {
		t.Errorf("empty file problem = %q", byName["empty.json"].Problem)
	}
	if !byName["good.json"].OK() {
		t.Errorf("good map: %q", byName["good.json"].Problem)
	}
}

func TestListMapsIgnoresEverythingElse(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "map.json", labMap)
	writeFile(t, dir, "notes.txt", "not a map")
	writeFile(t, dir, "run.yaml", "seeds: []")
	if err := os.Mkdir(filepath.Join(dir, "archive"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	got, err := ListMaps(dir)
	if err != nil {
		t.Fatalf("ListMaps: %v", err)
	}
	if len(got) != 1 || got[0].Name != "map.json" {
		t.Fatalf("listed %v", got)
	}
}

// Newest first: after a crawl, the map just written is the one wanted.
func TestNewestMapIsListedFirst(t *testing.T) {
	dir := t.TempDir()
	old := writeFile(t, dir, "old.json", labMap)
	writeFile(t, dir, "new.json", labMap)

	then := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, then, then); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	got, err := ListMaps(dir)
	if err != nil {
		t.Fatalf("ListMaps: %v", err)
	}
	if got[0].Name != "new.json" {
		t.Errorf("first = %q, want new.json", got[0].Name)
	}
}

func TestAnEmptyFolderIsNotAnError(t *testing.T) {
	got, err := ListMaps(t.TempDir())
	if err != nil {
		t.Fatalf("ListMaps: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("listed %d files in an empty folder", len(got))
	}
}

func TestAMissingFolderIsAnError(t *testing.T) {
	if _, err := ListMaps(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Error("ListMaps accepted a folder that is not there")
	}
	if _, err := ListMaps("  "); err == nil {
		t.Error("ListMaps accepted a blank folder")
	}
}

func TestSummaryNamesTheProblemWhenThereIsOne(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "bad.json", `[]`)

	got, _ := ListMaps(dir)
	if len(got) != 1 {
		t.Fatalf("listed %d", len(got))
	}
	if got[0].Summary() != got[0].Problem || got[0].Problem == "" {
		t.Errorf("summary = %q, problem = %q", got[0].Summary(), got[0].Problem)
	}
}
