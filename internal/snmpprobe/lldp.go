package snmpprobe

import (
	"strconv"

	"github.com/gosnmp/gosnmp"

	"github.com/scottpeterman/omegamaps/internal/normalize"
	"github.com/scottpeterman/omegamaps/internal/topo"
)

// ifNames maps ifIndex to a name: ifName, or ifDescr on a device with no
// ifXTable.
type ifNames map[int]string

// name is resolve_interface_name, including its "ifIndex_N" last resort. The
// placeholder is deliberate: an edge with an empty local interface is dropped
// by topo, and a visible placeholder says which index failed to resolve.
func (m ifNames) name(ifIndex int) string {
	if n := m[ifIndex]; n != "" {
		return n
	}
	return "ifIndex_" + strconv.Itoa(ifIndex)
}

func parseIfNames(pdus []gosnmp.SnmpPDU, root string) ifNames {
	out := ifNames{}
	for _, p := range pdus {
		sfx := oidSuffix(p.Name, root)
		if len(sfx) != 1 {
			continue
		}
		if s := decodeString(pduBytes(p)); s != "" {
			out[sfx[0]] = s
		}
	}
	return out
}

// locPort is one lldpLocPortTable row.
type locPort struct {
	subtype int
	id      string
	desc    string
}

// parseLocPorts is get_lldp_local_port_map, extended to read the subtype and
// description columns so the local side can be resolved the way the remote
// side is. The ID is decoded by its subtype rather than as a string, so a
// MAC-subtype local port ID reads as a MAC and is recognized as unusable.
func parseLocPorts(pdus []gosnmp.SnmpPDU) map[int]*locPort {
	type raw struct {
		subtype    int
		hasSubtype bool
		id         []byte
		desc       string
	}
	rows := map[int]*raw{}
	get := func(n int) *raw {
		r := rows[n]
		if r == nil {
			r = &raw{}
			rows[n] = r
		}
		return r
	}
	for _, p := range pdus {
		sfx := oidSuffix(p.Name, oidLldpLocPortEntry)
		if len(sfx) != 2 {
			continue
		}
		col, num := sfx[0], sfx[1]
		switch col {
		case lldpLocColPortIDSubtype:
			if v, ok := pduInt(p); ok {
				r := get(num)
				r.subtype, r.hasSubtype = v, true
			}
		case lldpLocColPortID:
			get(num).id = pduBytes(p)
		case lldpLocColPortDesc:
			get(num).desc = decodeString(pduBytes(p))
		}
	}
	out := make(map[int]*locPort, len(rows))
	for num, r := range rows {
		st := portSubtypeIfName
		if r.hasSubtype {
			st = r.subtype
		}
		out[num] = &locPort{subtype: st, id: decodePortID(st, r.id), desc: r.desc}
	}
	return out
}

// lldpRow is one lldpRemTable entry, held raw until the walk is complete.
// SC2.5 decoded the chassis and port IDs as their values arrived, which is
// correct only because a lexicographic walk returns each subtype column
// before the value it describes. Decoding after the walk removes the
// dependency on that ordering.
type lldpRow struct {
	localPort     int
	chassisSub    int
	hasChassisSub bool
	chassisRaw    []byte
	portSub       int
	hasPortSub    bool
	portRaw       []byte
	portDesc      string
	sysName       string
	sysDesc       string
	caps          []byte
	mgmt4, mgmt6  string
}

type lldpTable struct {
	rows  map[string]*lldpRow
	order []string // first-seen order, so output is deterministic
}

func newLLDPTable() *lldpTable { return &lldpTable{rows: map[string]*lldpRow{}} }

// row returns the entry for index timeMark.localPort.remIndex, creating it.
func (t *lldpTable) row(idx []int) *lldpRow {
	k := indexKey(idx)
	r := t.rows[k]
	if r == nil {
		r = &lldpRow{localPort: idx[1]}
		t.rows[k] = r
		t.order = append(t.order, k)
	}
	return r
}

// parseRemTable reads a walk of lldpRemEntry.
func parseRemTable(pdus []gosnmp.SnmpPDU) *lldpTable {
	t := newLLDPTable()
	for _, p := range pdus {
		sfx := oidSuffix(p.Name, oidLldpRemEntry)
		if len(sfx) != 4 { // column + timeMark.localPort.remIndex
			continue
		}
		col, idx := sfx[0], sfx[1:]
		switch col {
		case lldpRemColChassisIDSubtype:
			if v, ok := pduInt(p); ok {
				r := t.row(idx)
				r.chassisSub, r.hasChassisSub = v, true
			}
		case lldpRemColChassisID:
			t.row(idx).chassisRaw = pduBytes(p)
		case lldpRemColPortIDSubtype:
			if v, ok := pduInt(p); ok {
				r := t.row(idx)
				r.portSub, r.hasPortSub = v, true
			}
		case lldpRemColPortID:
			t.row(idx).portRaw = pduBytes(p)
		case lldpRemColPortDesc:
			t.row(idx).portDesc = decodeString(pduBytes(p))
		case lldpRemColSysName:
			t.row(idx).sysName = decodeString(pduBytes(p))
		case lldpRemColSysDesc:
			t.row(idx).sysDesc = decodeString(pduBytes(p))
		case lldpRemColSysCapEnabled:
			t.row(idx).caps = pduBytes(p)
		}
	}
	return t
}

// applyManAddrs is _fetch_management_addresses. The address lives in the
// index: column.timeMark.localPort.remIndex.addrSubtype.[len.]octets. Taking
// the trailing octets, as SC2.5 does, accepts agents that omit the length
// prefix as well as ones that include it. Like SC2.5, a management address
// with no lldpRemTable row creates one. When a neighbor advertises several
// addresses of one family the last in walk order wins, also as in SC2.5.
func applyManAddrs(t *lldpTable, pdus []gosnmp.SnmpPDU) {
	for _, p := range pdus {
		sfx := oidSuffix(p.Name, oidLldpRemManAddrEntry)
		if len(sfx) < 9 { // column + 3-part index + subtype + at least 4 octets
			continue
		}
		idx, subtype, addr := sfx[1:4], sfx[4], sfx[5:]
		var ip string
		switch {
		case subtype == 1 && len(addr) >= 4:
			ip = octetsToAddr(addr[len(addr)-4:])
		case subtype == 2 && len(addr) >= 16:
			ip = octetsToAddr(addr[len(addr)-16:])
		}
		if ip == "" {
			continue
		}
		r := t.row(idx)
		if subtype == 1 {
			r.mgmt4 = ip
		} else {
			r.mgmt6 = ip
		}
	}
}

// octetsToAddr turns 4 or 16 OID components into an address, rejecting any
// component that is not an octet.
func octetsToAddr(c []int) string {
	b := make([]byte, len(c))
	for i, v := range c {
		if v < 0 || v > 255 {
			return ""
		}
		b[i] = byte(v)
	}
	return decodeNetAddr(b)
}

// cleanName blanks the placeholder values SC2.5 filters out.
func cleanName(s string) string {
	if s == "(" {
		return ""
	}
	return s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}

// lldpNeighbors converts the collected tables into neighbor records shaped
// like the SSH path's recordToNeighbor output: raw names, normalization left
// to the dedup key and to topo.
func lldpNeighbors(t *lldpTable, loc map[int]*locPort, ifn ifNames, logf func(string, ...any)) []topo.Neighbor {
	var out []topo.Neighbor
	for _, k := range t.order {
		r := t.rows[k]

		chassisSub := chassisSubtypeMAC
		if r.hasChassisSub {
			chassisSub = r.chassisSub
		}
		portSub := portSubtypeIfName
		if r.hasPortSub {
			portSub = r.portSub
		}
		sysName := cleanName(r.sysName)
		chassis := cleanName(decodeChassisID(chassisSub, r.chassisRaw))
		mgmt := firstNonEmpty(r.mgmt4, r.mgmt6)
		if sysName == "" && chassis == "" && mgmt == "" {
			continue
		}

		// Local side: lldpLocPortTable first, through the same resolution
		// as the remote side (see resolvePort); ifIndex only when the
		// device has no local-port row for this number.
		var local string
		if lp := loc[r.localPort]; lp != nil {
			local = resolvePort(lp.id, lp.subtype, lp.desc)
			if local != "" && local != lp.id {
				logf("lldp: local port %d id %q (subtype %d) unusable; named %q from its description",
					r.localPort, lp.id, lp.subtype, local)
			}
		}
		if local == "" {
			local = ifn.name(r.localPort)
		}

		port := decodePortID(portSub, r.portRaw)
		remote := resolvePort(port, portSub, r.portDesc)
		if remote != port {
			logf("lldp: remote port id %q (subtype %d) unusable; using description %q",
				port, portSub, remote)
		}

		out = append(out, topo.Neighbor{
			LocalInterface:  local,
			RemoteDevice:    firstNonEmpty(sysName, chassis, mgmt),
			RemoteInterface: remote,
			RemoteIP:        mgmt,
			RemotePlatform:  normalize.PlatformFromDescription(r.sysDesc),
			RemoteDescr:     r.sysDesc,
			Capabilities:    lldpCapabilities(r.caps),
			Protocol:        "lldp",
		})
	}
	return out
}
