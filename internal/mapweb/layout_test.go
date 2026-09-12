// internal/mapweb/layout_test.go
package mapweb

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/secfile"
)

func newLayoutServer(t *testing.T) (*Server, string) {
	t.Helper()
	s, err := Serve(Options{})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	lp := LayoutPath(filepath.Join(t.TempDir(), "layouts"), "/maps/lab-map.json")
	if err := s.SetMapLayout("lab-map.json", []byte(labMap), lp); err != nil {
		t.Fatalf("SetMapLayout: %v", err)
	}
	return s, lp
}

const aLayout = `{"positions":{"eng-rtr-1":{"x":10,"y":20}},"layout":"dagre","savedAt":"2026-09-11T00:00:00Z"}`

func TestTheMapSaysWhetherItHasALayoutStore(t *testing.T) {
	for _, tc := range []struct {
		layout string
		want   bool
	}{{"", false}, {"/tmp/x.json", true}} {
		s, err := Serve(Options{})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SetMapLayout("m.json", []byte(labMap), tc.layout); err != nil {
			t.Fatal(err)
		}
		var got mapPayload
		resp := s.do(t, http.MethodGet, "/api/map", "", nil)
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.LayoutStore != tc.want {
			t.Errorf("layout %q: layout_store = %v, want %v", tc.layout, got.LayoutStore, tc.want)
		}
		s.Close()
	}
}

// The reason the store exists: a layout saved through one server comes back
// through the next, which listens on a different port.
func TestALayoutOutlivesTheServerThatSavedIt(t *testing.T) {
	s1, lp := newLayoutServer(t)
	if resp := s1.do(t, http.MethodPut, "/api/layout", aLayout, nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("PUT = %d, want 204", resp.StatusCode)
	}
	s1.Close()

	s2, err := Serve(Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if s2.Addr() == s1.Addr() {
		t.Skip("the OS handed out the same port twice; nothing to prove")
	}
	if err := s2.SetMapLayout("lab-map.json", []byte(labMap), lp); err != nil {
		t.Fatal(err)
	}
	resp := s2.do(t, http.MethodGet, "/api/layout", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != aLayout {
		t.Errorf("layout came back as %s", body)
	}
}

func TestNoSavedLayoutIsNoContent(t *testing.T) {
	s, _ := newLayoutServer(t)
	if resp := s.do(t, http.MethodGet, "/api/layout", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("GET with nothing saved = %d, want 204", resp.StatusCode)
	}
}

func TestClearingALayoutRemovesItAndIsIdempotent(t *testing.T) {
	s, lp := newLayoutServer(t)
	s.do(t, http.MethodPut, "/api/layout", aLayout, nil)
	for i := 0; i < 2; i++ {
		if resp := s.do(t, http.MethodDelete, "/api/layout", "", nil); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("DELETE #%d = %d, want 204", i+1, resp.StatusCode)
		}
	}
	if _, err := os.Stat(lp); !os.IsNotExist(err) {
		t.Errorf("layout file still there after clear: %v", err)
	}
}

func TestALayoutMustBeAJSONObject(t *testing.T) {
	s, lp := newLayoutServer(t)
	for _, body := range []string{`[1,2]`, `"x"`, `{"positions":`, `null`} {
		if resp := s.do(t, http.MethodPut, "/api/layout", body, nil); resp.StatusCode != http.StatusBadRequest {
			t.Errorf("PUT %s = %d, want 400", body, resp.StatusCode)
		}
	}
	if _, err := os.Stat(lp); !os.IsNotExist(err) {
		t.Error("a refused layout was written anyway")
	}
}

func TestAnOversizedLayoutIsRefused(t *testing.T) {
	s, lp := newLayoutServer(t)
	big := `{"pad":"` + strings.Repeat("x", maxLayoutBytes) + `"}`
	if resp := s.do(t, http.MethodPut, "/api/layout", big, nil); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("PUT %d bytes = %d, want 413", len(big), resp.StatusCode)
	}
	if _, err := os.Stat(lp); !os.IsNotExist(err) {
		t.Error("an oversized layout was written anyway")
	}
}

// The layout endpoint writes to disk, so it gets the same guard as connect.
func TestTheLayoutEndpointIsGuarded(t *testing.T) {
	s, lp := newLayoutServer(t)
	resp := s.do(t, http.MethodPut, "/api/layout", aLayout, func(r *http.Request) {
		r.Header.Set("Origin", "http://evil.example")
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin PUT = %d, want 403", resp.StatusCode)
	}
	resp = s.do(t, http.MethodPut, "/api/layout", aLayout, func(r *http.Request) {
		r.Header.Del(tokenHeader)
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("tokenless PUT = %d, want 403", resp.StatusCode)
	}
	if _, err := os.Stat(lp); !os.IsNotExist(err) {
		t.Error("a refused PUT wrote the layout")
	}
}

func TestAMapWithoutALayoutStoreSaysSo(t *testing.T) {
	s := newTestServer(t, Options{})
	if resp := s.do(t, http.MethodGet, "/api/layout", "", nil); resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("GET = %d, want 501", resp.StatusCode)
	}
}

// Layouts name devices; they are not left readable by other users.
func TestTheLayoutFileIsPrivate(t *testing.T) {
	s, lp := newLayoutServer(t)
	s.do(t, http.MethodPut, "/api/layout", aLayout, nil)

	// The file is checked on every platform now; secfile.Verify reads the DACL
	// on Windows and the mode word elsewhere.
	if err := secfile.Verify(lp); err != nil {
		t.Errorf("layout file is not private: %v", err)
	}

	// The DIRECTORY is still POSIX-only. MkdirAll's mode is ignored on
	// Windows and secfile does not ACL directories -- the file's own protected
	// DACL is what keeps the layout out of other users' hands there.
	if runtime.GOOS != "windows" {
		di, err := os.Stat(filepath.Dir(lp))
		if err != nil {
			t.Fatal(err)
		}
		if di.Mode().Perm() != 0o700 {
			t.Errorf("layout dir mode %v, want 0700", di.Mode().Perm())
		}
	}

	left, _ := filepath.Glob(filepath.Join(filepath.Dir(lp), ".*.tmp"))
	if len(left) != 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

func TestLayoutPathIsPerMapAndReadable(t *testing.T) {
	dir := "/layouts"
	a := LayoutPath(dir, "/maps/site-a/map.json")
	b := LayoutPath(dir, "/maps/site-b/map.json")
	if a == b {
		t.Fatalf("two maps called map.json share a layout: %s", a)
	}
	if a != LayoutPath(dir, "/maps/site-a/../site-a/map.json") {
		t.Error("the same map by another spelling gets a different layout")
	}
	if !strings.HasPrefix(filepath.Base(a), "map-") {
		t.Errorf("layout name %q does not lead with the map's name", filepath.Base(a))
	}
	if got := filepath.Base(LayoutPath(dir, "/maps/lab map (copy)?.json")); strings.ContainsAny(got, " ()?") {
		t.Errorf("unsafe characters survived: %q", got)
	}
	if LayoutPath("", "/maps/m.json") != "" {
		t.Error("no directory should mean no layout store")
	}
}
