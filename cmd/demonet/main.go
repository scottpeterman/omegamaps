// cmd/demonet: crawl the company-free demo network and write what a real
// crawl writes -- the recording, the map and the log -- for screenshots and
// demos that must show nobody's production network.
//
//	demonet -out demo                 # demo/demo.events.jsonl, demo/demo.json, demo/demo.log
//	demonet -out demo -speed 0.2      # five times quicker; the recording keeps the pace it had
//	demonet -list                     # the network's devices, and exit
//
// The crawl is the real crawler with only per-device SNMP collection
// simulated; see internal/demonet. Open the recording in omegamaps with Open
// recording..., or run scripts/demo-screenshots.sh for the full set of grabs.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/demonet"
)

func main() {
	out := flag.String("out", "demo", "directory to write into")
	name := flag.String("name", "demo", "file name stem: <name>.events.jsonl, <name>.json, <name>.log")
	speed := flag.Float64("speed", 1, "collection time per device: 1 is real-world pacing, 0 is instant")
	list := flag.Bool("list", false, "print the network's devices and exit")
	flag.Parse()

	n := demonet.Build()
	if *list {
		for _, d := range n.Names() {
			dev := n.Devices[d]
			fmt.Printf("%-22s %-15s %s\n", d, dev.IP, demonet.Kinds[dev.Kind].Platform)
		}
		dial, ends := n.Count()
		fmt.Fprintf(os.Stderr, "%d dialable devices, %d endpoints\n", dial, ends)
		return
	}

	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	stem := filepath.Join(*out, *name)

	ef, err := os.Create(stem + ".events.jsonl")
	if err != nil {
		fail(err)
	}
	lf, err := os.Create(stem + ".log")
	if err != nil {
		fail(err)
	}
	events := crawlrun.NewEventWriter(ef)

	devices, run := n.Crawl(context.Background(), demonet.CrawlOptions{
		Options: demonet.Options{Speed: *speed},
		Tap:     events.Write,
		Log: func(format string, args ...any) {
			fmt.Fprintf(lf, format+"\n", args...)
		},
	})
	if err := events.Flush(); err != nil {
		fail(err)
	}
	if err := ef.Close(); err != nil {
		fail(err)
	}
	if err := lf.Close(); err != nil {
		fail(err)
	}

	data, err := n.Map(devices)
	if err != nil {
		fail(err)
	}
	if err := os.WriteFile(stem+".json", data, 0o644); err != nil {
		fail(err)
	}

	c := run.Counts()
	fmt.Printf("demonet: %d reached, %d failed, %d not dialed in %s; %d events\n",
		c.Reached, c.Failed, c.NotDialed, run.Elapsed().Round(100e6), run.Seq())
	fmt.Printf("  %s.events.jsonl\n  %s.json\n  %s.log\n", stem, stem, stem)
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "demonet: %v\n", err)
	os.Exit(1)
}
