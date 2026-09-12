// capi/mapview.go
//go:build cgo

// The map viewer's server, for the Qt viewer executable. The viewer is a
// QWebEngineView on internal/mapweb's loopback page: the same page, the same
// guards and the same exports as `mapview` in a browser, so nothing about the
// map is implemented twice. What the Qt side adds is the window. The contract
// is include/omegamaps/mapview.h.
//
// A viewer is a handle of its own, separate from runs: it has no notifier
// because nothing in it changes unless the caller changes it.
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/scottpeterman/omegamaps/internal/mapweb"
	"github.com/scottpeterman/omegamaps/internal/vaultcli"
)

type mapView struct {
	srv       *mapweb.Server
	layoutDir string

	mu   sync.Mutex
	path string // absolute path of the loaded map
}

var (
	viewsMu  sync.Mutex
	views    = map[int64]*mapView{}
	nextView int64
)

func lookupView(h C.longlong) *mapView {
	viewsMu.Lock()
	defer viewsMu.Unlock()
	return views[int64(h)]
}

// DefaultLayoutDir is ~/.omegamaps/layouts; "" (no layout store) when there is
// no home directory.
func defaultLayoutDir() string {
	if d := vaultcli.AppDir(); d != "" {
		return filepath.Join(d, "layouts")
	}
	return ""
}

// load reads a map and hands it to the server. The server refuses a file
// that is not a map, and the previous map stays loaded when it does.
func (v *mapView) load(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return err
	}
	if err := v.srv.SetMapLayout(filepath.Base(abs), data, mapweb.LayoutPath(v.layoutDir, abs)); err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(abs), err)
	}
	v.mu.Lock()
	v.path = abs
	v.mu.Unlock()
	return nil
}

//export omegamaps_mapview_open
func omegamaps_mapview_open(mapPath *C.char, layoutDir *C.char) C.longlong {
	if mapPath == nil {
		setErr("mapview: no map path")
		return -1
	}
	dir := defaultLayoutDir()
	if layoutDir != nil {
		if d := C.GoString(layoutDir); d != "" {
			dir = d
		}
	}

	srv, err := mapweb.Serve(mapweb.Options{})
	if err != nil {
		setErr("mapview: %v", err)
		return -1
	}
	v := &mapView{srv: srv, layoutDir: dir}
	if err := v.load(C.GoString(mapPath)); err != nil {
		srv.Close()
		setErr("mapview: %v", err)
		return -1
	}

	viewsMu.Lock()
	nextView++
	h := nextView
	views[h] = v
	viewsMu.Unlock()
	clearErr()
	return C.longlong(h)
}

//export omegamaps_mapview_load
func omegamaps_mapview_load(h C.longlong, mapPath *C.char) C.int {
	v := lookupView(h)
	if v == nil {
		setErr("mapview_load: no viewer %d", int64(h))
		return -1
	}
	if mapPath == nil {
		setErr("mapview_load: no map path")
		return -1
	}
	if err := v.load(C.GoString(mapPath)); err != nil {
		setErr("mapview_load: %v", err)
		return -1
	}
	clearErr()
	return 0
}

type mapViewInfo struct {
	URL        string `json:"url"`
	Addr       string `json:"addr"`
	Path       string `json:"path"`
	Name       string `json:"name"`
	Nodes      int    `json:"nodes"`
	LayoutPath string `json:"layout_path"`
}

//export omegamaps_mapview_info
func omegamaps_mapview_info(h C.longlong) *C.char {
	v := lookupView(h)
	if v == nil {
		setErr("mapview_info: no viewer %d", int64(h))
		return nil
	}
	v.mu.Lock()
	p := v.path
	v.mu.Unlock()
	return jsonOut("mapview_info", mapViewInfo{
		URL:        v.srv.URL(),
		Addr:       v.srv.Addr(),
		Path:       p,
		Name:       filepath.Base(p),
		Nodes:      v.srv.NodeCount(),
		LayoutPath: mapweb.LayoutPath(v.layoutDir, p),
	})
}

//export omegamaps_mapview_close
func omegamaps_mapview_close(h C.longlong) C.int {
	viewsMu.Lock()
	v := views[int64(h)]
	delete(views, int64(h))
	viewsMu.Unlock()
	if v == nil {
		setErr("mapview_close: no viewer %d", int64(h))
		return -1
	}
	if err := v.srv.Close(); err != nil {
		setErr("mapview_close: %v", err)
		return -1
	}
	clearErr()
	return 0
}
