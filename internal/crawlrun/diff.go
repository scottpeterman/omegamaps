// internal/crawlrun/diff.go
//
// Saving a run, and comparing it to the last one.
//
// This is the capability a script structurally cannot offer. "Which devices
// stopped answering since yesterday", "which one changed platform underneath
// me", "why did this run spend three times the authentication attempts" are
// all questions about two runs, and you can only ask them if the first one
// still exists in a form you can read.
//
// The comparison keys on Identity, not on the display name. That is the same
// reason the binding store derives its canonical label deterministically: a
// label that moves between runs turns every diff into noise, and a real change
// becomes indistinguishable from the labelling wobbling.
package crawlrun

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/scottpeterman/omegamaps/internal/secfile"
)

const snapshotVersion = 1

// Snapshot is a finished run, on disk.
type Snapshot struct {
	Version  int         `json:"version"`
	Started  time.Time   `json:"started"`
	Finished time.Time   `json:"finished"`
	Seeds    []string    `json:"seeds,omitempty"`
	Suffixes []string    `json:"suffixes,omitempty"`
	Devices  []DeviceRow `json:"devices"`
	Counts   Counts      `json:"counts"`
}

// Snapshot captures the run for later comparison.
func (r *Run) Snapshot(seeds, suffixes []string) Snapshot {
	r.mu.RLock()
	started, finished := r.begun, r.closed
	r.mu.RUnlock()
	if finished.IsZero() {
		finished = time.Now()
	}
	return Snapshot{
		Version:  snapshotVersion,
		Started:  started,
		Finished: finished,
		Seeds:    seeds,
		Suffixes: suffixes,
		Devices:  r.Rows(),
		Counts:   r.Counts(),
	}
}

// Save writes a snapshot atomically.
func (s Snapshot) Save(path string) error {
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal run snapshot: %w", err)
	}
	if err := secfile.WriteAtomic(path, out); err != nil {
		return fmt.Errorf("failed to write run snapshot: %w", err)
	}
	return nil
}

// LoadSnapshot reads a previously saved run.
func LoadSnapshot(path string) (Snapshot, error) {
	var s Snapshot
	raw, err := os.ReadFile(path)
	if err != nil {
		return s, fmt.Errorf("failed to read run snapshot: %w", err)
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("run snapshot is corrupt: %w", err)
	}
	return s, nil
}

// ChangeKind classifies one difference between runs.
type ChangeKind int

const (
	// Appeared is a device this run found and the previous one did not.
	Appeared ChangeKind = iota

	// Vanished is a device the previous run knew and this one never saw at
	// all — distinct from one that was seen and failed, which is StateMoved.
	Vanished

	// StateMoved is the same device ending in a different outcome. A device
	// that went from reached to not-dialed did not break; a policy changed.
	StateMoved

	// PlatformMoved is a fingerprint that changed underneath you.
	PlatformMoved

	// LadderCost is a device that started spending more authentication
	// attempts than it used to, which usually means its binding stopped
	// matching rather than that anything about the device changed.
	LadderCost
)

func (c ChangeKind) String() string {
	switch c {
	case Appeared:
		return "appeared"
	case Vanished:
		return "vanished"
	case StateMoved:
		return "state"
	case PlatformMoved:
		return "platform"
	case LadderCost:
		return "ladder"
	}
	return "?"
}

// Change is one difference, phrased so it can be rendered directly.
type Change struct {
	Kind     ChangeKind
	Identity string
	Display  string
	Was      string
	Now      string
}

// Describe renders a change for the comparison view.
func (c Change) Describe() string {
	switch c.Kind {
	case Appeared:
		return fmt.Sprintf("%s is new (%s)", c.Display, c.Now)
	case Vanished:
		return fmt.Sprintf("%s was not seen this run (was %s)", c.Display, c.Was)
	case StateMoved:
		return fmt.Sprintf("%s went from %s to %s", c.Display, c.Was, c.Now)
	case PlatformMoved:
		return fmt.Sprintf("%s changed platform: %s -> %s", c.Display, c.Was, c.Now)
	case LadderCost:
		return fmt.Sprintf("%s spent %s credential attempts, was %s", c.Display, c.Now, c.Was)
	}
	return c.Display
}

// Compare diffs a previous run against the current one. Changes come back in
// a stable order so the view does not reshuffle between redraws.
func Compare(prev Snapshot, cur []DeviceRow) []Change {
	before := make(map[string]DeviceRow, len(prev.Devices))
	for _, d := range prev.Devices {
		before[d.Identity] = d
	}
	seen := make(map[string]bool, len(cur))

	var out []Change
	for _, now := range cur {
		seen[now.Identity] = true
		was, ok := before[now.Identity]
		if !ok {
			out = append(out, Change{
				Kind: Appeared, Identity: now.Identity,
				Display: now.Display(), Now: now.State.String(),
			})
			continue
		}
		if was.State != now.State {
			out = append(out, Change{
				Kind: StateMoved, Identity: now.Identity, Display: now.Display(),
				Was: was.State.String(), Now: now.State.String(),
			})
		}
		if was.Platform != now.Platform && was.Platform != "" && now.Platform != "" {
			out = append(out, Change{
				Kind: PlatformMoved, Identity: now.Identity, Display: now.Display(),
				Was: was.Platform, Now: now.Platform,
			})
		}
		// Only a rise is worth reporting: falling back to one attempt is the
		// binding store doing its job.
		if now.Attempts > was.Attempts && was.Attempts > 0 {
			out = append(out, Change{
				Kind: LadderCost, Identity: now.Identity, Display: now.Display(),
				Was: fmt.Sprint(was.Attempts), Now: fmt.Sprint(now.Attempts),
			})
		}
	}
	for _, was := range prev.Devices {
		if !seen[was.Identity] {
			out = append(out, Change{
				Kind: Vanished, Identity: was.Identity,
				Display: was.Display(), Was: was.State.String(),
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Identity < out[j].Identity
	})
	return out
}
