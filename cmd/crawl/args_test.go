package main

import (
	"flag"
	"strings"
	"testing"
	"time"
)

func parseCrawlLike(args ...string) (*flag.FlagSet, error) {
	fs := flag.NewFlagSet("crawl", flag.ContinueOnError)
	fs.String("events", "", "")
	fs.String("exclude", "", "")
	fs.String("o", "map.json", "")
	fs.Int("cred-breaker", 0, "")
	fs.Bool("v", false, "")
	fs.Duration("snmp-timeout", 5*time.Second, "")
	return fs, fs.Parse(args)
}

func TestCheckArgs(t *testing.T) {
	cases := []struct {
		args []string
		want string // substring of the error; empty means accepted
	}{
		{[]string{"-events", "run.jsonl", "-exclude", "linux,debian", "-v"}, ""},
		{[]string{"-cred-breaker", "-1", "-v"}, ""}, // a negative number is a value
		// The command that prompted this: -events swallowed -exclude, and
		// parsing stopped at the pattern list, so -v was never seen.
		{[]string{"-events", "-exclude", "linux,debian,broadcom", "-v"}, `-events "-exclude"`},
		{[]string{"-o", "-v"}, `-o "-v"`},
		{[]string{"-exclude", "linux", "debian"}, `unexpected argument "debian"`},
	}
	for _, c := range cases {
		fs, err := parseCrawlLike(c.args...)
		if err != nil {
			t.Fatalf("%v: parse: %v", c.args, err)
		}
		err = checkArgs(fs)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%v: refused: %v", c.args, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%v: err = %v, want it to mention %s", c.args, err, c.want)
		}
	}
}
