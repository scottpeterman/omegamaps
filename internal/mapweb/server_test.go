// internal/mapweb/server_test.go
// The tests that matter here are the refusals. A viewer that renders is
// obvious the first time it is opened; a guard that does not bite is invisible
// until somebody else's browser tab is the one asking.
package mapweb

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const labMap = `{
  "eng-rtr-1": {
    "node_details": { "ip": "172.16.11.1", "platform": "cisco_ios" },
    "peers": {
      "eng-spine-1": {
        "ip": "172.16.2.2",
        "platform": "arista_eos",
        "connections": [["Gi0/0", "Eth3"]]
      },
      "eng-host-9": {
        "ip": "172.16.9.9",
        "platform": "linux",
        "connections": [["Gi0/2", "eth0"]]
      }
    }
  },
  "eng-spine-1": {
    "node_details": { "ip": "172.16.2.2", "platform": "arista_eos" },
    "peers": {
      "eng-rtr-1": {
        "ip": "172.16.11.1",
        "platform": "cisco_ios",
        "connections": [["Eth3", "Gi0/0"]]
      }
    }
  }
}`

func newTestServer(t *testing.T, opts Options) *Server {
	t.Helper()
	s, err := Serve(opts)
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.SetMap("lab-map.json", []byte(labMap)); err != nil {
		t.Fatalf("SetMap: %v", err)
	}
	return s
}

// do issues an API request with the token and Origin a real page would send,
// unless the caller overrides them.
func (s *Server) do(t *testing.T, method, path, body string, mutate func(*http.Request)) *http.Response {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, "http://"+s.addr+path, rdr)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set(tokenHeader, s.token)
	req.Header.Set("Origin", "http://"+s.addr)
	req.Header.Set("Content-Type", "application/json")
	if mutate != nil {
		mutate(req)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func (s *Server) idFor(t *testing.T, name string) string {
	t.Helper()
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.ids[name]
	if !ok {
		t.Fatalf("no id for %q", name)
	}
	return id
}

func TestMapIsServedToAPageWithTheToken(t *testing.T) {
	s := newTestServer(t, Options{OnConnect: func(NodeRef) {}})

	resp := s.do(t, http.MethodGet, "/api/map", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got mapPayload
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "lab-map.json" {
		t.Errorf("name = %q", got.Name)
	}
	if !got.CanConnect {
		t.Error("can_connect = false with an OnConnect set")
	}
	// Two crawled devices plus the one leaf.
	if len(got.IDs) != 3 {
		t.Errorf("ids = %d, want 3: %v", len(got.IDs), got.IDs)
	}
	if !bytes.Contains(got.Map, []byte("eng-spine-1")) {
		t.Error("served map does not contain the map body")
	}
}

func TestAnAPIRequestWithoutTheTokenIsRefused(t *testing.T) {
	s := newTestServer(t, Options{OnConnect: func(NodeRef) {}})

	resp := s.do(t, http.MethodGet, "/api/map", "", func(r *http.Request) {
		r.Header.Del(tokenHeader)
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("no token: status = %d, want 403", resp.StatusCode)
	}

	resp = s.do(t, http.MethodGet, "/api/map", "", func(r *http.Request) {
		r.Header.Set(tokenHeader, "0000000000000000")
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong token: status = %d, want 403", resp.StatusCode)
	}
}

func TestAnAPIRequestFromAnotherOriginIsRefused(t *testing.T) {
	s := newTestServer(t, Options{OnConnect: func(NodeRef) {}})

	resp := s.do(t, http.MethodGet, "/api/map", "", func(r *http.Request) {
		r.Header.Set("Origin", "http://example.invalid")
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// A rebinding attack arrives with a correct-looking request and the wrong Host.
func TestARequestUnderAnotherNameIsRefused(t *testing.T) {
	s := newTestServer(t, Options{OnConnect: func(NodeRef) {}})

	resp := s.do(t, http.MethodGet, "/api/map", "", func(r *http.Request) {
		r.Host = "map.example.invalid:1234"
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestConnectOpensTheNodeBehindTheID(t *testing.T) {
	got := make(chan NodeRef, 1)
	s := newTestServer(t, Options{OnConnect: func(n NodeRef) { got <- n }})

	id := s.idFor(t, "eng-spine-1")
	resp := s.do(t, http.MethodPost, "/api/connect", `{"id":"`+id+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	n := <-got
	if n.Name != "eng-spine-1" || n.IP != "172.16.2.2" {
		t.Errorf("connected to %+v", n)
	}
	if !n.Discovered {
		t.Error("a crawled device came through as undiscovered")
	}
}

func TestALeafIsConnectableAndMarkedUndiscovered(t *testing.T) {
	got := make(chan NodeRef, 1)
	s := newTestServer(t, Options{OnConnect: func(n NodeRef) { got <- n }})

	resp := s.do(t, http.MethodPost, "/api/connect", `{"id":"`+s.idFor(t, "eng-host-9")+`"}`, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	n := <-got
	if n.IP != "172.16.9.9" {
		t.Errorf("ip = %q, want the address its neighbour reported", n.IP)
	}
	if n.Discovered {
		t.Error("a leaf came through as discovered")
	}
}

func TestAnUnknownIDConnectsToNothing(t *testing.T) {
	fired := make(chan NodeRef, 1)
	s := newTestServer(t, Options{OnConnect: func(n NodeRef) { fired <- n }})

	resp := s.do(t, http.MethodPost, "/api/connect", `{"id":"deadbeefdeadbeef"}`, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	select {
	case n := <-fired:
		t.Fatalf("OnConnect fired for an unknown id: %+v", n)
	default:
	}
}

// The point of the opaque id: the request body a page can send never names a
// host, so the set of reachable hosts is exactly the set in the loaded map.
func TestConnectRefusesAHostnameInPlaceOfAnID(t *testing.T) {
	fired := make(chan NodeRef, 1)
	s := newTestServer(t, Options{OnConnect: func(n NodeRef) { fired <- n }})

	for _, body := range []string{
		`{"id":"eng-spine-1"}`,
		`{"id":"172.16.2.2"}`,
	} {
		resp := s.do(t, http.MethodPost, "/api/connect", body, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", body, resp.StatusCode)
		}
	}
	select {
	case n := <-fired:
		t.Fatalf("OnConnect fired for a named host: %+v", n)
	default:
	}
}

func TestConnectIsPOSTOnly(t *testing.T) {
	s := newTestServer(t, Options{OnConnect: func(NodeRef) {}})

	resp := s.do(t, http.MethodGet, "/api/connect", "", nil)
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
}

// A page left open on the previous map must not still be able to connect.
func TestLoadingANewMapInvalidatesTheOldIDs(t *testing.T) {
	s := newTestServer(t, Options{OnConnect: func(NodeRef) {}})
	old := s.idFor(t, "eng-rtr-1")

	if err := s.SetMap("lab-map.json", []byte(labMap)); err != nil {
		t.Fatalf("SetMap: %v", err)
	}

	resp := s.do(t, http.MethodPost, "/api/connect", `{"id":"`+old+`"}`, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if now := s.idFor(t, "eng-rtr-1"); now == old {
		t.Error("ids survived a reload")
	}
}

func TestAViewerWithNoConnectCallbackSaysSo(t *testing.T) {
	s := newTestServer(t, Options{})

	// The page asks before it draws the button, so this flag is what keeps
	// a Connect control off a viewer that has nothing behind it.
	resp := s.do(t, http.MethodGet, "/api/map", "", nil)
	var payload mapPayload
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.CanConnect {
		t.Error("can_connect = true with no OnConnect")
	}

	resp = s.do(t, http.MethodPost, "/api/connect", `{"id":"`+s.idFor(t, "eng-rtr-1")+`"}`, nil)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestThePageIsServedWithoutATokenAndCarriesNoData(t *testing.T) {
	s := newTestServer(t, Options{OnConnect: func(NodeRef) {}})

	resp, err := http.Get("http://" + s.addr + "/")
	if err != nil {
		t.Fatalf("get page: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if bytes.Contains(body, []byte("eng-rtr-1")) || bytes.Contains(body, []byte(s.token)) {
		t.Error("the page carries map data or the token in its body")
	}
}

func TestSetMapRejectsSomethingThatIsNotAMap(t *testing.T) {
	s, err := Serve(Options{})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer s.Close()

	for name, data := range map[string]string{
		"empty":     ``,
		"not json":  `<html>`,
		"an array":  `[1,2,3]`,
		"no device": `{}`,
	} {
		if err := s.SetMap("x.json", []byte(data)); err == nil {
			t.Errorf("%s: SetMap accepted it", name)
		}
	}
}

func TestNoMapLoadedIsNotFoundRatherThanAnEmptyPage(t *testing.T) {
	s, err := Serve(Options{})
	if err != nil {
		t.Fatalf("Serve: %v", err)
	}
	defer s.Close()

	resp := s.do(t, http.MethodGet, "/api/map", "", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

// A browser with strict MIME checking will not execute a script served as
// text/plain, and the error it prints names the symbol the script would have
// defined rather than the header. Pin the type here so the page cannot break
// because of what is or is not in /etc/mime.types on the machine it runs on.
func TestScriptsAreServedAsJavaScript(t *testing.T) {
	s := newTestServer(t, Options{})

	for _, asset := range []string{
		"/assets/viewer.js",
		"/assets/app.js",
		"/assets/vendor/cytoscape.min.js",
	} {
		resp, err := http.Get("http://" + s.addr + asset)
		if err != nil {
			t.Fatalf("get %s: %v", asset, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status = %d", asset, resp.StatusCode)
			continue
		}
		if len(body) == 0 {
			t.Errorf("%s: served empty", asset)
		}
		if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("%s: Content-Type = %q", asset, ct)
		}
		if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: missing nosniff", asset)
		}
	}
}

func TestAssetsCannotBeWalkedOutOf(t *testing.T) {
	s := newTestServer(t, Options{})

	for _, p := range []string{
		"/assets/../server.go",
		"/assets/../../etc/passwd",
		"/assets/nope.js",
	} {
		resp, err := http.Get("http://" + s.addr + p)
		if err != nil {
			t.Fatalf("get %s: %v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", p, resp.StatusCode)
		}
	}
}

func TestFaviconIsAnsweredRatherThanLoggedAsAMiss(t *testing.T) {
	s := newTestServer(t, Options{})

	resp, err := http.Get("http://" + s.addr + "/favicon.ico")
	if err != nil {
		t.Fatalf("get favicon: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

func TestThePlatformMapIsServedAsJSON(t *testing.T) {
	s := newTestServer(t, Options{})

	resp, err := http.Get("http://" + s.addr + "/assets/platform_map.json")
	if err != nil {
		t.Fatalf("get platform map: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q", ct)
	}

	var m struct {
		PlatformPatterns map[string]string `json:"platform_patterns"`
		FallbackPatterns map[string]any    `json:"fallback_patterns"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(m.PlatformPatterns) == 0 || len(m.FallbackPatterns) == 0 {
		t.Errorf("platform map looks empty: %d patterns, %d fallbacks",
			len(m.PlatformPatterns), len(m.FallbackPatterns))
	}
}
