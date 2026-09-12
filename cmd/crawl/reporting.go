// cmd/crawl/reporting.go
//
// End-of-run credential reporting for the crawl CLI, and nothing else.
//
// The dial layer that used to live here moved to internal/crawldial so both
// front ends assemble the crawler through one Build. What stayed behind is
// the part that is genuinely CLI-shaped: prose written to stderr after the
// run, which a window has no use for because it shows the same facts as
// columns.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/scottpeterman/omegamaps/internal/credres"
)

func reportCredentialStats(res *credres.Resolver, names map[string]string) {
	s := res.Stats()
	if s.Promoted != "" {
		fmt.Fprintf(os.Stderr, "crawl: credential most recently successful: %s\n",
			credName(names, s.Promoted))
	}
	if s.NegativeHosts > 0 {
		fmt.Fprintf(os.Stderr, "crawl: %d device(s) rejected at least one credential\n",
			s.NegativeHosts)
	}
	for id, why := range s.ParkedCreds {
		fmt.Fprintf(os.Stderr, "crawl: credential %s parked for this run: %s\n",
			credName(names, id), why)
	}
}

func credName(names map[string]string, id string) string {
	if n, ok := names[id]; ok && n != "" {
		return fmt.Sprintf("%q", n)
	}
	return id
}

// flagWasSet reports whether a flag appeared on the command line, as opposed
// to carrying its default. Used to warn only when a credential flag was
// actually passed alongside -vault, since -user defaults to $USER and would
// otherwise warn on every vault-mode run.
func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}
