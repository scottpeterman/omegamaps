package snmpprobe

import (
	"strconv"

	"github.com/gosnmp/gosnmp"

	"github.com/scottpeterman/omegamaps/internal/topo"
)

// cdpColumns are walked in this order. cdpCacheDeviceId goes first and alone
// establishes the rows, as in SC2.5: the other columns only fill rows it
// created, and when it returns nothing none of the rest are walked -- which on
// an Arista or Junos box saves six walks per device.
var cdpColumns = []int{
	cdpColDeviceID,
	cdpColDevicePort,
	cdpColAddress,
	cdpColPlatform,
	cdpColVersion,
	cdpColCapabilities,
	cdpColPrimaryMgmtAddr,
}

func cdpColumnRoot(col int) string { return oidCdpCacheEntry + "." + strconv.Itoa(col) }

type cdpRow struct {
	ifIndex  int
	deviceID string
	port     string
	platform string
	version  string
	addr     string // cdpCacheAddress: the first address the neighbor advertised
	primary  string // cdpCachePrimaryMgmtAddr: the one it says to manage it by
	caps     []byte
}

type cdpTable struct {
	rows  map[string]*cdpRow
	order []string
}

// parseCDPDeviceIDs reads the cdpCacheDeviceId walk and creates the rows.
// The skip list is SC2.5's; "(\x00" is covered by "(" because decodeString
// strips NULs.
func parseCDPDeviceIDs(pdus []gosnmp.SnmpPDU) *cdpTable {
	t := &cdpTable{rows: map[string]*cdpRow{}}
	for _, p := range pdus {
		sfx := oidSuffix(p.Name, cdpColumnRoot(cdpColDeviceID))
		if len(sfx) != 2 { // ifIndex.deviceIndex
			continue
		}
		id := decodeString(pduBytes(p))
		switch id {
		case "", "(", "CW_":
			continue
		}
		k := indexKey(sfx)
		if _, dup := t.rows[k]; !dup {
			t.order = append(t.order, k)
		}
		t.rows[k] = &cdpRow{ifIndex: sfx[0], deviceID: id}
	}
	return t
}

// applyCDPColumn fills one non-DeviceId column into existing rows.
func applyCDPColumn(t *cdpTable, col int, pdus []gosnmp.SnmpPDU) {
	root := cdpColumnRoot(col)
	for _, p := range pdus {
		sfx := oidSuffix(p.Name, root)
		if len(sfx) != 2 {
			continue
		}
		r := t.rows[indexKey(sfx)]
		if r == nil {
			continue
		}
		b := pduBytes(p)
		switch col {
		case cdpColDevicePort:
			r.port = decodeString(b)
		case cdpColAddress:
			r.addr = decodeIPv4(b)
		case cdpColPlatform:
			r.platform = decodeString(b)
		case cdpColVersion:
			r.version = decodeString(b)
		case cdpColCapabilities:
			r.caps = b
		case cdpColPrimaryMgmtAddr:
			r.primary = decodeIPv4(b)
		}
	}
}

// cdpNeighbors converts the table into neighbor records. RemoteIP prefers the
// primary management address and falls back to cdpCacheAddress, which is only
// the first address in the neighbor's address TLV -- on a routed link that is
// usually the far end of the /31, not where the device is managed. Devices
// too old to populate column 20 return nothing for it, so they fall back to
// exactly SC2.5's behavior.
func cdpNeighbors(t *cdpTable, ifn ifNames) []topo.Neighbor {
	var out []topo.Neighbor
	for _, k := range t.order {
		r := t.rows[k]
		ip := firstNonEmpty(r.primary, r.addr)
		device := r.deviceID
		if device == "N/A" || device == "n/a" {
			if ip == "" {
				continue
			}
			device = ip
		}
		out = append(out, topo.Neighbor{
			LocalInterface:  ifn.name(r.ifIndex),
			RemoteDevice:    device,
			RemoteInterface: r.port,
			RemoteIP:        ip,
			RemotePlatform:  r.platform,
			RemoteDescr:     r.version,
			Capabilities:    cdpCapabilities(r.caps),
			Protocol:        "cdp",
		})
	}
	return out
}
