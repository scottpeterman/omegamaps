// internal/crawler/exclusion.go
//
// Whether a target is excluded is a decision about a DEVICE. The evidence
// arrives per EDGE, and the edges disagree.
//
// A host with two links to the same leaf appears twice in its neighbor table.
// If detail came back for one port and not the other, one claim carries the
// system description that matches "linux" and the other carries nothing at
// all. Judged edge by edge, the first claim excludes the host and the second
// admits it — so it is dialed anyway, and the run table shows it twice: once
// not-dialed and once running. That reads as a contradiction and is really
// two half-informed answers to the same question.
//
// So the verdict is settled once per batch, against everything the batch
// knows, before anything is admitted. And it is remembered: a target excluded
// at depth 3 stays excluded when a different parent reports it at depth 5 with
// a barer claim.
package crawler

import (
	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// markExcluded walks every neighbor claim in a finished batch and records the
// targets an exclude pattern matches, before the admission pass runs.
func (c *Crawler) markExcluded(results []*topo.Device) {
	if len(c.cfg.ExcludePatterns) == 0 {
		return
	}
	for _, d := range results {
		if d == nil {
			continue
		}
		for _, n := range d.Neighbors {
			t, _, ok := nextTarget(n, func(string, ...any) {})
			if !ok {
				continue
			}
			excl, pat := normalize.ShouldExclude(
				[]string{n.RemoteDescr, n.RemotePlatform, n.RemoteDevice, n.RemoteInterface},
				c.cfg.ExcludePatterns)
			if !excl {
				continue
			}
			id := c.identity(t)
			c.mu.Lock()
			if _, seen := c.excluded[id]; !seen {
				c.excluded[id] = pat
			}
			c.mu.Unlock()
		}
	}
}

// exclusionFor reports whether a target has been excluded, and by which
// pattern. Keyed on the claim identity so a host named "x.site.example.net"
// by one neighbor and "x.site" by another is one verdict, not two.
func (c *Crawler) exclusionFor(target string) (string, bool) {
	if len(c.cfg.ExcludePatterns) == 0 {
		return "", false
	}
	id := c.identity(target)
	c.mu.Lock()
	defer c.mu.Unlock()
	pat, ok := c.excluded[id]
	return pat, ok
}
