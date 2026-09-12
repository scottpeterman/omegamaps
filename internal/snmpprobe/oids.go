package snmpprobe

// OIDs, ported from SecureCartography 2.5's oids.py and the collectors that
// use it. Only what the collectors walk is here; the rest of oids.py describes
// data nothing in this package reads.
//
// Every root carries a leading dot because gosnmp returns PDU names in that
// form, so a root and the names under it compare without normalizing either.

// SNMPv2-MIB system group. Scalars, fetched in one GET.
const (
	oidSysDescr = ".1.3.6.1.2.1.1.1.0"
	oidSysName  = ".1.3.6.1.2.1.1.5.0"
)

// IF-MIB. ifName is the primary source; ifDescr is walked only when a device
// has no ifXTable at all.
const (
	oidIfDescr = ".1.3.6.1.2.1.2.2.1.2"
	oidIfName  = ".1.3.6.1.2.1.31.1.1.1.1"
)

// LLDP-MIB.
const (
	// lldpLocPortEntry, index lldpLocPortNum. Columns: 2 lldpLocPortIdSubtype,
	// 3 lldpLocPortId, 4 lldpLocPortDesc. lldpLocPortNum is what the remote
	// table's index names, and it is NOT necessarily ifIndex.
	oidLldpLocPortEntry = ".1.0.8802.1.1.2.1.3.7.1"

	// lldpRemEntry, index timeMark.lldpRemLocalPortNum.lldpRemIndex. Walked
	// as one table rather than column by column: SC2.5 found column walks
	// time out on older Junos where the single walk does not.
	oidLldpRemEntry = ".1.0.8802.1.1.2.1.4.1.1"

	// lldpRemManAddrEntry. The address is in the index, not in any column:
	// timeMark.localPortNum.remIndex.addrSubtype.[len.]addr...
	oidLldpRemManAddrEntry = ".1.0.8802.1.1.2.1.4.2.1"
)

const (
	lldpLocColPortIDSubtype = 2
	lldpLocColPortID        = 3
	lldpLocColPortDesc      = 4

	lldpRemColChassisIDSubtype = 4
	lldpRemColChassisID        = 5
	lldpRemColPortIDSubtype    = 6
	lldpRemColPortID           = 7
	lldpRemColPortDesc         = 8
	lldpRemColSysName          = 9
	lldpRemColSysDesc          = 10
	lldpRemColSysCapEnabled    = 12
)

// LldpChassisIdSubtype / LldpPortIdSubtype values the decoders branch on.
const (
	chassisSubtypeMAC     = 4
	chassisSubtypeNetwork = 5
	portSubtypeMAC        = 3
	portSubtypeNetwork    = 4
	portSubtypeIfName     = 5
)

// CISCO-CDP-MIB cdpCacheEntry, index cdpCacheIfIndex.cdpCacheDeviceIndex. The
// first index component is a real ifIndex, unlike LLDP's local port number.
const oidCdpCacheEntry = ".1.3.6.1.4.1.9.9.23.1.2.1.1"

// Column numbers per CISCO-CDP-MIB. oids.py's CACHE_PRIMARY_MGMT_ADDR is 16,
// which is cdpCacheMTU; the primary management address is 20. Nothing in
// SC2.5 walked it, which is why device validation never caught it.
const (
	cdpColAddress         = 4
	cdpColVersion         = 5
	cdpColDeviceID        = 6
	cdpColDevicePort      = 7
	cdpColPlatform        = 8
	cdpColCapabilities    = 9
	cdpColPrimaryMgmtAddr = 20
)
