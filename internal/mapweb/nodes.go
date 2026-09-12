// internal/mapweb/nodes.go
// The connectable-node table behind the opaque IDs.
//
// A map has two kinds of node in it. Devices that were crawled are top-level
// keys and carry their own details. Devices that were only ever *named* by a
// neighbour — the leaves — appear as peer entries, and the only thing known
// about them is what the neighbour reported. Both are worth clicking: a leaf
// that was excluded from the crawl by a filter is still a host somebody wants
// a terminal on.
//
// A leaf is marked Discovered=false so the page can render it as the
// known-unknown it is, rather than as a device the crawl vouched for.
package mapweb

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/scottpeterman/omegamaps/internal/topo"
)

// parseNodes turns map.json bytes into the node list. It returns an error
// rather than an empty list on malformed input: "no nodes" and "not a map"
// look identical on screen, and only one of them is worth a message.
func parseNodes(data []byte) ([]NodeRef, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("map is empty")
	}

	var m map[string]topo.MapNode
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse map: %w", err)
	}
	if len(m) == 0 {
		return nil, fmt.Errorf("map contains no devices")
	}

	nodes := make(map[string]NodeRef, len(m))

	for name, node := range m {
		nodes[name] = NodeRef{
			Name:       name,
			IP:         node.NodeDetails.IP,
			Platform:   node.NodeDetails.Platform,
			Discovered: true,
		}
	}

	// Leaves: named by a peer, never crawled themselves. A device that is
	// both (crawled, and also claimed by someone else) keeps its crawled
	// record — the peer entry is second-hand.
	for _, node := range m {
		for peer, entry := range node.Peers {
			if _, ok := nodes[peer]; ok {
				continue
			}
			nodes[peer] = NodeRef{
				Name:       peer,
				IP:         entry.IP,
				Platform:   entry.Platform,
				Discovered: false,
			}
		}
	}

	out := make([]NodeRef, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n)
	}
	// Sorted so a failure is reproducible; map iteration order is not.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
