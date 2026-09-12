// internal/mapweb/maps.go
// Listing a directory of maps, for a picker to render.
//
// This is deliberately not a file browser. A map is a JSON file whose shape
// this package already knows, so the listing parses each candidate and reports
// what is in it: a picker that says "13 devices, 1 leaf, an hour ago" tells you
// which crawl you are looking at, where "map.json, 41 KB" does not.
//
// Parsing also decides openability. A file that cannot be read as a map is
// listed with the reason rather than hidden, because a map that was written by
// a run that failed halfway is exactly the file somebody goes looking for.
package mapweb

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// maxMapBytes bounds what the listing will parse. A crawl of a large estate
// produces a map in the low megabytes; anything past this is not one, and
// reading it to find that out would stall the picker.
const maxMapBytes = 64 << 20

// MapFile is one candidate in a maps directory.
type MapFile struct {
	Path    string
	Name    string
	ModTime time.Time
	Size    int64

	// Devices were crawled; Leaves were only named by a neighbour. The two
	// together are the node count the viewer will draw.
	Devices int
	Leaves  int

	// Problem is empty when the file is a map that can be opened. When it
	// is set, it says why not, in words worth putting in front of someone.
	Problem string
}

// Nodes is what the viewer will render: everything, filters aside.
func (f MapFile) Nodes() int { return f.Devices + f.Leaves }

// OK reports whether this file can be opened.
func (f MapFile) OK() bool { return f.Problem == "" }

// Summary is the one-line description a picker puts beside the name.
func (f MapFile) Summary() string {
	if !f.OK() {
		return f.Problem
	}
	s := fmt.Sprintf("%d device%s", f.Devices, plural(f.Devices))
	if f.Leaves > 0 {
		s += fmt.Sprintf(", %d leaf%s", f.Leaves, leafPlural(f.Leaves))
	}
	return s + " · " + age(time.Since(f.ModTime))
}

// ListMaps reads dir and describes every .json file in it, newest first —
// which is nearly always the crawl that was just run.
//
// A directory that does not exist is an error; a directory with nothing in it
// is not. Those are different situations and only one of them is a mistake.
func ListMaps(dir string) ([]MapFile, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, fmt.Errorf("no maps folder set")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	out := make([]MapFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".json") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}

		f := MapFile{
			Path:    filepath.Join(dir, e.Name()),
			Name:    e.Name(),
			ModTime: info.ModTime(),
			Size:    info.Size(),
		}
		describe(&f)
		out = append(out, f)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].ModTime.Equal(out[j].ModTime) {
			return out[i].Name < out[j].Name
		}
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// describe fills in the counts, or the reason there are none.
func describe(f *MapFile) {
	if f.Size == 0 {
		f.Problem = "empty file"
		return
	}
	if f.Size > maxMapBytes {
		f.Problem = "too large to be a map"
		return
	}

	data, err := os.ReadFile(f.Path)
	if err != nil {
		f.Problem = "cannot read: " + err.Error()
		return
	}

	nodes, err := parseNodes(data)
	if err != nil {
		f.Problem = "not a topology map"
		return
	}

	for _, n := range nodes {
		if n.Discovered {
			f.Devices++
		} else {
			f.Leaves++
		}
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func leafPlural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// age is coarse on purpose. Which of two crawls is the recent one is the
// question; the exact minute is not.
func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return "over 2 weeks ago"
	}
}
