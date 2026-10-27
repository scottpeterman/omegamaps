package demonet

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/topo"
)

// The demo crawl is the real crawler over the synthetic network. These pin
// what the screenshots depend on: its size, its deliberate failures, the
// exclusions, and model-bearing platforms on every crawled node.
func TestDemoCrawl(t *testing.T) {
	n := Build()
	devices, run := n.Crawl(context.Background(), CrawlOptions{})

	c := run.Counts()
	if c.Reached != 74 || c.Failed != 3 {
		t.Errorf("reached %d, failed %d; want 74 and 3", c.Reached, c.Failed)
	}
	if c.NotDialed == 0 {
		t.Errorf("nothing was left undialed; the exclusions did not apply")
	}

	data, err := n.Map(devices)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]topo.MapNode
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]bool{}
	for name, node := range m {
		nodes[name] = true
		for peer := range node.Peers {
			nodes[peer] = true
		}
		if strings.Contains(name, "example.net") {
			t.Errorf("%s: the domain should be stripped from map names", name)
		}
		p := node.NodeDetails.Platform
		if p == "" || strings.Contains(p, "_") {
			t.Errorf("%s: platform %q is a token, not a described model", name, p)
		}
	}
	if len(nodes) != 150 {
		t.Errorf("%d nodes on the map; the screenshots are sized for 150", len(nodes))
	}
	for _, want := range []string{"core1.dc1", "spine1.dc1", "leaf1.dc2", "rtr1.br10"} {
		if _, ok := m[want]; !ok {
			t.Errorf("%s missing from the map", want)
		}
	}
	if got := m["spine1.dc1"].NodeDetails.Platform; got != "Arista DCS-7280SR3-48YC8 EOS" {
		t.Errorf("spine1.dc1 platform %q", got)
	}
}

// Two runs of the same network produce the same map: screenshots taken on
// different days show the same network.
func TestDemoDeterministic(t *testing.T) {
	a, _ := Build().Crawl(context.Background(), CrawlOptions{})
	b, _ := Build().Crawl(context.Background(), CrawlOptions{})
	n := Build()
	ma, _ := n.Map(a)
	mb, _ := n.Map(b)
	if string(ma) != string(mb) {
		t.Errorf("two crawls of the demo network produced different maps")
	}
}
