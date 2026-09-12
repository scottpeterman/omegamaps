// internal/crawler/plan.go
// Per-platform neighbor-collection plans: which CLI commands to run and
// which template key parses each. Encodes the validated field findings:
//   - EOS: lldp detail alone carries everything.
//   - IOS/IOS-XE: some builds omit Local Intf from lldp detail, so edges
//     come from the plain lldp table (+ cdp detail, which does carry the
//     local interface and a mgmt IP for crawl targets).
//   - NX-OS: cdp detail + lldp detail.
//   - Junos: newer builds take `show lldp neighbors detail`; older ones
//     reject it outright, and the terse table is then the only thing that
//     runs. Detail is best-effort for exactly that reason. What the terse
//     table cannot supply — a management address — is completed from DNS
//     after collection; see neighboraddr.go.
package crawler

import (
	"strings"

	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// step is one command in a platform's plan.
type step struct {
	Command    string
	Key        string // tfsm command key
	Protocol   string // "cdp" | "lldp"
	BestEffort bool   // command may be rejected on some builds; not fatal
	EdgeSource bool   // records from this step create edges (vs enrich only)

	// ScrubToRows drops lines this step's output cannot legally contain
	// before the template sees them, rather than letting one unrecognized
	// line fail the parse for the whole device. Only for steps whose
	// template has a strict Error rule AND whose row shape is distinctive
	// enough to recognize positively — see scrubToRows, which encodes the
	// Junos terse shape and would scrub a Cisco table to nothing.
	ScrubToRows bool

	// PerInterfaceFallback is the command to retry this step with, one
	// interface at a time, for edges the bulk form left without a system
	// description. Empty disables it.
	//
	// It is a FORMAT, not a prefix: %s is where the interface goes, and the
	// platforms disagree about where that is. Junos puts it last after a
	// literal "interface"; IOS, EOS and NX-OS put it in the middle with
	// "detail" after. A prefix-and-append scheme silently produced
	// "show lldp neighbors detail Ethernet1" on three of the four.
	//
	// The step's template parses the result, so it has to be one whose
	// detail format this command actually returns — which is the same
	// format in every case, since these are all the per-interface form of
	// the same bulk command.
	PerInterfaceFallback string
}

var plans = map[string][]step{
	"arista_eos": {
		// EOS answers detail in bulk reliably, so the per-interface form is
		// only ever reached for ports the bulk output skipped. Costs nothing
		// when there are none.
		{Command: "show lldp neighbors detail", Key: "lldp_detail", Protocol: "lldp", EdgeSource: true,
			PerInterfaceFallback: "show lldp neighbors %s detail"},
	},
	"cisco_ios": {
		// Detail BEFORE summary, and an edge source in its own right.
		// Both forms describe the same links, and the first record to
		// claim a {local, remote, remote-port} key wins — so whichever
		// runs first decides whether the edge carries a management
		// address. The summary form has no address column at all and
		// truncates the neighbor name to 20 characters, so letting it
		// win meant a Cisco->Arista link (no CDP on the far end) mapped
		// as a bare, address-less name with nothing to fall back to.
		// The summary stays as the fallback for boxes where detail is
		// unsupported or errors out. nxos and junos were already
		// ordered this way; ios and iosxe were the outliers.
		{Command: "show lldp neighbors detail", Key: "lldp_detail", Protocol: "lldp",
			BestEffort: true, EdgeSource: true,
			// The reason this matters here as much as on Junos: a server
			// hanging off an IOS ToR runs LLDP and not CDP, so when lldp
			// detail is unsupported the only record of it is the plain
			// table, which has no description column. Nothing then matches
			// an exclude pattern and every server gets dialed.
			PerInterfaceFallback: "show lldp neighbors %s detail"},
		{Command: "show lldp neighbors", Key: "lldp", Protocol: "lldp", EdgeSource: true},
		{Command: "show cdp neighbors detail", Key: "cdp_detail", Protocol: "cdp", BestEffort: true, EdgeSource: true},
	},
	"cisco_iosxe": {
		// Same ordering as cisco_ios, same reason.
		{Command: "show lldp neighbors detail", Key: "lldp_detail", Protocol: "lldp",
			BestEffort: true, EdgeSource: true,
			// The reason this matters here as much as on Junos: a server
			// hanging off an IOS ToR runs LLDP and not CDP, so when lldp
			// detail is unsupported the only record of it is the plain
			// table, which has no description column. Nothing then matches
			// an exclude pattern and every server gets dialed.
			PerInterfaceFallback: "show lldp neighbors %s detail"},
		{Command: "show lldp neighbors", Key: "lldp", Protocol: "lldp", EdgeSource: true},
		{Command: "show cdp neighbors detail", Key: "cdp_detail", Protocol: "cdp", BestEffort: true, EdgeSource: true},
	},
	"cisco_nxos": {
		{Command: "show cdp neighbors detail", Key: "cdp_detail", Protocol: "cdp", EdgeSource: true},
		// Same gap as IOS: CDP carries a platform but a server does not
		// speak CDP, so a host behind an NX-OS leaf is described by LLDP or
		// not at all.
		{Command: "show lldp neighbors detail", Key: "lldp_detail", Protocol: "lldp",
			BestEffort: true, EdgeSource: true,
			PerInterfaceFallback: "show lldp neighbors interface %s detail"},
	},
	"juniper_junos": {
		// detail first: it carries System Description (pre-dial exclusion
		// depends on it) and in-device dedup keeps the first record per
		// edge. Old Junos rejects detail (best-effort); the terse table
		// then supplies the edges on its own — local interface, chassis
		// MAC, port and system name, with no description, no neighbor
		// platform and no management address.
		//
		// Three things make that table usable rather than merely present.
		// The descriptions come back via PerInterfaceFallback: the same
		// builds that reject detail in bulk answer it one interface at a
		// time, and the description is the field exclusion runs on, so
		// without it "-exclude linux" matches nothing here.
		// The address gap is closed by forward-resolving the reported name
		// (neighboraddr.go), which is what keeps the dial-by-address retry
		// available for these devices. And the table is hard-wrapped at
		// the device's screen width, which cuts long rows mid-token and
		// takes the whole parse down with it; unwrap.go repairs that
		// before the template runs. The real fix for the wrapping is a
		// wider `set cli screen-width` at session setup, which belongs in
		// the netexec probe rather than here.
		{Command: "show lldp neighbors detail", Key: "lldp_detail", Protocol: "lldp",
			BestEffort: true, EdgeSource: true,
			PerInterfaceFallback: "show lldp neighbors interface %s"},
		{Command: "show lldp neighbors", Key: "lldp", Protocol: "lldp", EdgeSource: true,
			ScrubToRows: true},
	},
	// The three plans below are new and, unlike the ones above, have not
	// been run against real gear through this codebase's own test harness
	// (no Go toolchain in the authoring environment) -- only against the
	// TextFSM templates by hand. BestEffort is set unconditionally on all
	// three so a template gap degrades to "no neighbors found" rather than
	// aborting the device's crawl. See internal/tfsm/templates for the
	// per-template confidence notes.
	"aruba_procurve": {
		{Command: "show lldp info remote-device detail", Key: "lldp_detail", Protocol: "lldp", BestEffort: true, EdgeSource: true},
	},
	"aruba_cx": {
		{Command: "show lldp neighbor-info detail", Key: "lldp_detail", Protocol: "lldp", BestEffort: true, EdgeSource: true},
	},
	"extreme_exos": {
		{Command: "show lldp neighbors detailed", Key: "lldp_detail", Protocol: "lldp", BestEffort: true, EdgeSource: true},
	},
	"hp_comware": {
		// Confirmed live (2026-08-21) against two different Comware
		// devices: Comware 5's plain form already gives full per-field
		// detail, but the SAME command on Comware 7 gives a terse
		// summary (combined "ChassisID/subtype" and "PortID/subtype"
		// lines, one "Capabilities" line) the template below does not
		// parse -- Comware 7 needs the "verbose" form for the matching
		// shape. Both are sent, in the order most likely to match
		// first: a version that doesn't understand one command either
		// rejects it cleanly or produces non-matching output, and the
		// template's permissive catch-all means either failure mode
		// contributes zero records rather than corrupting the ones the
		// other command found.
		{Command: "display lldp neighbor-information verbose", Key: "lldp_detail", Protocol: "lldp", BestEffort: true, EdgeSource: true},
		{Command: "display lldp neighbor-information", Key: "lldp_detail", Protocol: "lldp", BestEffort: true, EdgeSource: true},
	},
}

// planFor returns the collection plan for a fingerprinted platform.
func planFor(platform string) ([]step, bool) {
	p, ok := plans[platform]
	return p, ok
}

// firstNonEmpty is a small helper for record-field fallbacks.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// recordToNeighbor maps a parsed template record (field names vary a little
// per template family) onto the topology model.
func recordToNeighbor(rec map[string]string, protocol string) topo.Neighbor {
	return topo.Neighbor{
		LocalInterface:  firstNonEmpty(rec["LOCAL_INTERFACE"]),
		RemoteDevice:    firstNonEmpty(rec["NEIGHBOR_NAME"], rec["SYSTEM_NAME"], rec["CHASSIS_ID"]),
		RemoteInterface: firstNonEmpty(rec["NEIGHBOR_INTERFACE"], rec["NEIGHBOR_PORT_ID"], rec["PORT_ID"]),
		RemoteIP:        firstNonEmpty(rec["MGMT_ADDRESS"], rec["REMOTE_IP"], rec["MANAGEMENT_IP"]),
		// CDP reports a platform directly; LLDP does not, and carries it as
		// prose inside the system description instead. Without the fallback
		// every LLDP-only device — which is every Arista and every Junos
		// here — reports no neighbor platform at all.
		RemotePlatform: firstNonEmpty(
			rec["PLATFORM"],
			normalize.PlatformFromDescription(
				firstNonEmpty(rec["NEIGHBOR_DESCRIPTION"], rec["SYSTEM_DESCRIPTION"]),
			),
		),
		RemoteDescr:  firstNonEmpty(rec["NEIGHBOR_DESCRIPTION"], rec["SYSTEM_DESCRIPTION"]),
		Capabilities: firstNonEmpty(rec["CAPABILITIES"]),
		Protocol:     protocol,
	}
}
