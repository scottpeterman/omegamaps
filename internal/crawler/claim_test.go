// internal/crawler/claim_test.go
//
// A batch routinely contains a device that another device in the same batch
// also reports as a neighbor. Whether that device is crawled once or twice
// comes down to the order of two operations, so it gets its own test.
package crawler

import (
	"testing"

	"github.com/scottpeterman/omegamaps/internal/topo"
)

// The invariant: once a batch is finished, nothing any of those devices
// answers to may be admitted again — no matter which device in the batch
// reported it, or in what order.
func TestBatchClaimsEveryNameBeforeAnyNeighborIsAdmitted(t *testing.T) {
	c := New(Config{Log: func(string, ...any) {}})

	// Two seeds crawled together. The second was reached by address and only
	// named itself once connected, which is the case that used to slip: the
	// first device's neighbor list is walked before the second has claimed
	// the name it answers to.
	results := []*topo.Device{
		{Hostname: "wan-core-1", SysName: "wan-core-1", IPAddress: "172.16.1.2"},
		{Hostname: "172.16.1.3", SysName: "eng-rtr-1", IPAddress: "172.16.1.3"},
	}
	c.claimAll(results)

	for _, name := range []string{
		"eng-rtr-1",  // what it called itself
		"172.16.1.3", // what it was dialed as
		"wan-core-1", // the other device in the batch
		"172.16.1.2", // and its address
	} {
		if _, ok := c.admit(name, "", 1, ""); ok {
			t.Errorf("%q was admitted again after its batch completed; "+
				"it will be dialed twice and spend a second set of credential attempts",
				name)
		}
	}
}

// A device nobody in the batch reported is still admitted; the fix must not
// turn into a blanket refusal.
func TestUnrelatedNeighborsAreStillAdmitted(t *testing.T) {
	c := New(Config{Log: func(string, ...any) {}})
	c.claimAll([]*topo.Device{
		{Hostname: "wan-core-1", SysName: "wan-core-1", IPAddress: "172.16.1.2"},
	})
	if _, ok := c.admit("eng-leaf-1", "", 1, ""); !ok {
		t.Error("a genuinely new neighbor was refused")
	}
	// ...and only once.
	if _, ok := c.admit("eng-leaf-1", "", 1, ""); ok {
		t.Error("the same new neighbor was admitted twice")
	}
}

func TestClaimAllToleratesNilDevices(t *testing.T) {
	c := New(Config{Log: func(string, ...any) {}})
	c.claimAll([]*topo.Device{nil, {Hostname: "wan-core-1"}, nil})
	if got := len(c.devices); got != 1 {
		t.Errorf("filed %d devices, want 1", got)
	}
}
