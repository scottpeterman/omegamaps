// internal/credres/identity.go
//
// Cache keys.
//
// Every cache in this package is keyed on a canonical identity rather than on
// whatever string happened to be dialed. The canonicalization itself lives in
// normalize, because the crawler needs the identical answer — if the crawler's
// claim set and this package's binding cache disagree about what counts as one
// device, the cache warms twice and helps neither path, silently.
//
// This file used to carry its own copy of the rule, and that copy was subtly
// wrong: it adopted the first PTR record unconditionally, where the crawler
// forward-confirmed it first. See normalize/identity.go for why the
// forward-confirm is the correct behavior.
package credres

import (
	"strings"

	"github.com/scottpeterman/omegamaps/internal/normalize"
)

// Identity canonicalizes host into a cache key.
//
// domainSuffixes is a list rather than a single suffix because a crawl can
// span several: a device claimed short by one neighbor and fully qualified by
// another has to key the same regardless of which suffix it carries.
//
// Callers inside a crawl should not use this directly — the crawler already
// computed the identity it claimed the device under and passes it through as
// crawler.DialTarget.Identity. Recomputing it here risks a second DNS answer
// and a second key. This is for callers that only have a hostname.
func Identity(host string, domainSuffixes []string) string {
	return normalize.Canonical(host, domainSuffixes)
}

// matchesSuffix reports whether name ends in suffix on a label boundary. Used
// for credential scope matching, which is a policy question about where a
// credential may be offered rather than a question about identity.
func matchesSuffix(name, suffix string) bool {
	name = strings.ToLower(strings.Trim(strings.TrimSpace(name), "."))
	suffix = strings.ToLower(strings.Trim(strings.TrimSpace(suffix), "."))
	if suffix == "" {
		return true
	}
	return name == suffix || strings.HasSuffix(name, "."+suffix)
}
