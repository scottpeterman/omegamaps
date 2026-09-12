// internal/crawler/unwrap.go
//
// Pre-parse repair of terminal-damaged command output: rejoin what the CLI
// hard-wrapped, then drop what still cannot be a table row.
//
// A Junos box with a screen width narrower than its widest LLDP row cuts that
// row mid-token and continues on the next line. The tail is often the system
// name, so the neighbor that gets damaged is exactly the one the crawler was
// going to dial. Two shapes it takes, wrapped at column 148:
//
//	ge-4/0/3   -     02:00:00:00:10:01  <long port description> usa-spine-
//	2.lab.local
//
//	xe-4/0/4   ae40  02:00:00:00:10:02  <long port description> eng-rtr-1.lab.lo
//	cal
//
// The fragment on the second line is not a table row, so a template with a
// strict `-> Error` rule fails the whole parse and the device reports zero
// neighbors. Loosening the template to skip the fragment would be worse, not
// better: it turns a loud failure into a silently truncated neighbor name that
// then fails to resolve. The fragment has to be put back.
//
// The right fix is upstream — ask the device for a wide screen so it never
// wraps. That is worth doing (see the note in plan.go) and it is not enough on
// its own: some builds cap the width, some ignore the request, and captures
// replayed from a file were wrapped by whatever produced them. So the parse
// path repairs what it can and stays loud about what it cannot.
package crawler

import (
	"regexp"
	"strings"
)

var (
	// rowRe is what a neighbor row looks like from the left: two columns and
	// then a chassis MAC. Used only to recognize that a line starts a NEW
	// record and therefore is not a continuation of the previous one.
	rowRe = regexp.MustCompile(`^\S+\s+\S+\s+(?:[0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}\s`)

	// trailerRe covers the lines Junos prints around a table: the routing
	// engine context marker and an echoed CLI prompt.
	trailerRe = regexp.MustCompile(`^(\{\S*\}|\S+@\S+>.*)$`)
)

// minWrapWidth is the narrowest column count worth believing. Nothing sane
// wraps a neighbor table below this, and the check keeps a handful of short
// lines from being read as evidence of a wrap.
const minWrapWidth = 80

// unwrapWrapped rejoins hard-wrapped continuation lines and reports how many
// joins it made. Text with no detectable wrap is returned unchanged.
func unwrapWrapped(text string) (string, int) {
	lines := strings.Split(text, "\n")
	w := wrapWidth(lines)
	if w == 0 {
		return text, 0
	}
	out := make([]string, 0, len(lines))
	joins := 0
	for _, l := range lines {
		if len(out) > 0 && len(out[len(out)-1]) >= w && isContinuation(l) {
			// Concatenated with no separator: a hard wrap cuts a token in
			// half, it does not insert whitespace.
			out[len(out)-1] += l
			joins++
			continue
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n"), joins
}

// wrapWidth finds the column the output was cut at, or 0 when there is no
// good evidence of wrapping.
//
// Two independent signals, because there are two shapes of cut. A cut that
// lands mid-token leaves a bare fragment — a single word with no internal
// whitespace — which nothing else in this output ever looks like, so one of
// those confirms the width on its own. A cut that lands mid-FIELD leaves a
// continuation with spaces in it, indistinguishable line-by-line from a
// trailing warning, so it is confirmed structurally instead: the width has to
// repeat, EVERY line sitting at it has to be followed by a continuation, and
// none of them may be the last line. A warning after two coincidentally
// equal-length rows fails that immediately — the first of those rows is
// followed by the second, which is a row, not a continuation.
//
// An earlier version required the repeat unconditionally, which was wrong in
// the other direction: a table with exactly one over-long row wraps exactly
// once and would never have been repaired.
//
// What is left undetectable is a single mid-field cut: one continuation, with
// spaces, and no repetition to corroborate it. Nothing distinguishes that from
// a message the device appended, so it is not guessed at. The fragment reaches
// the template and the parse fails loudly, which is the right outcome — this
// repairs damage it can prove and hands the rest to the error rule.
func wrapWidth(lines []string) int {
	max, count := 0, 0
	for _, l := range lines {
		switch {
		case len(l) > max:
			max, count = len(l), 1
		case len(l) == max:
			count++
		}
	}
	if max < minWrapWidth {
		return 0
	}

	// Signal 1: a bare fragment anywhere.
	for i := 0; i < len(lines)-1; i++ {
		next := lines[i+1]
		if len(lines[i]) == max && isContinuation(next) && len(strings.Fields(next)) == 1 {
			return max
		}
	}

	// Signal 2: a repeated width where every occurrence is continued.
	if count < 2 || len(lines[len(lines)-1]) == max {
		return 0
	}
	for i := 0; i < len(lines)-1; i++ {
		if len(lines[i]) == max && !isContinuation(lines[i+1]) {
			return 0
		}
	}
	return max
}

// isContinuation reports whether a line could be the tail of a wrapped row:
// non-blank, not the start of a new record, and not one of the trailers the
// CLI prints around the table.
func isContinuation(l string) bool {
	s := strings.TrimSpace(l)
	return s != "" && !rowRe.MatchString(l) && !trailerRe.MatchString(s)
}

// headerRe is the terse table's column header.
var headerRe = regexp.MustCompile(`^Local\s+Interface\s`)

// scrubToRows drops lines that cannot be part of a Junos terse LLDP table and
// returns them alongside the cleaned text.
//
// This is the last resort, and it exists because the failure mode without it
// is disproportionate: one line the template does not recognize takes down the
// parse for the whole device, and a table with ninety good rows in it reports
// zero neighbors. Losing one row to keep eighty-nine is the right trade. The
// reverse is not.
//
// It runs AFTER unwrapWrapped, never before. A wrapped continuation is by
// definition a line that cannot parse; scrubbing first would delete exactly
// the fragment the unwrap needs, and quietly truncate a system name instead of
// repairing it.
//
// What survives: blank lines, the column header, the CLI trailers, and
// anything shaped like a row — two columns and then a chassis MAC. Everything
// else is returned to the caller, which is the part that makes this safe to
// have. A silent scrub is how a device ends up with a plausible-looking
// neighbor list that is missing links nobody knows about; a scrub that reports
// what it dropped is a diagnostic. The template keeps its `-> Error` rule as a
// tripwire behind this, and it should now never fire — if it does, the format
// is one nothing here has seen.
//
// Deliberately not applied to every platform. The row shape below is the Junos
// terse one; a Cisco lldp table has no MAC column and would be scrubbed to
// nothing. Steps opt in via ScrubToRows in the plan.
// The third return is how many of the dropped lines sat directly after a line
// at the output's maximum width — the shape of a hard cut. Those are the
// dangerous ones: the unwrap declined to repair them, so deleting one
// silently truncates a neighbor name rather than removing noise. Dropping is
// still the better trade — a truncated name costs one edge, a failed parse
// costs the whole table — but it is a different event and the caller says so.
//
// Maximum width rather than merely "long": most rows in a real table clear
// any fixed threshold, so a threshold would flag every ordinary warning that
// happened to follow a row and the count would stop meaning anything.
func scrubToRows(text string) (string, []string, int) {
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	var dropped []string
	max := 0
	for _, l := range lines {
		if len(l) > max {
			max = len(l)
		}
	}
	suspect := 0
	for i, l := range lines {
		s := strings.TrimSpace(l)
		switch {
		case s == "", rowRe.MatchString(l), headerRe.MatchString(l), trailerRe.MatchString(s):
			kept = append(kept, l)
		default:
			dropped = append(dropped, s)
			if i > 0 && max >= minWrapWidth && len(lines[i-1]) == max {
				suspect++
			}
		}
	}
	return strings.Join(kept, "\n"), dropped, suspect
}

// truncate shortens a line for a log or event message. Dropped lines are
// arbitrary device output and can be very long; the point of showing one is
// to say what KIND of thing was dropped, not to reproduce it.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
