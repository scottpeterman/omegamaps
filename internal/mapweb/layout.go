// internal/mapweb/layout.go
// Where a map's saved layout is kept.
//
// Not beside the map. A maps directory is read by other tools that take every
// *.json in it for a map, and a layout file there is one they would try to
// load. So layouts go in a directory of their own (the caller's choice;
// ~/.omegamaps/layouts for the application), one file per map, named for the
// map's absolute path: the same map opened again finds its layout, and two
// maps called map.json in different directories do not share one.
//
// The cost is that moving or renaming a map loses its layout. A re-crawl that
// writes the same path keeps it, which is the case that matters: positions
// are by node name, new nodes are placed by the layout, gone ones are ignored.
package mapweb

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
)

// LayoutPath is the layout file for mapPath under dir. The name leads with the
// map's own base name so the directory can be read by a person, and ends with
// a hash of the absolute path so it is unique. Returns "" when dir is empty.
func LayoutPath(dir, mapPath string) string {
	if dir == "" || mapPath == "" {
		return ""
	}
	abs, err := filepath.Abs(mapPath)
	if err != nil {
		abs = mapPath
	}
	abs = filepath.Clean(abs)
	sum := sha256.Sum256([]byte(abs))

	base := strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	return filepath.Join(dir, safeName(base)+"-"+hex.EncodeToString(sum[:6])+".json")
}

// safeName keeps a base name to characters every file system accepts, and
// short enough that the hash suffix always fits.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		if b.Len() >= 64 {
			break
		}
	}
	if b.Len() == 0 {
		return "map"
	}
	return b.String()
}
