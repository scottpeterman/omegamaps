package snmpprobe

import (
	"encoding/binary"
	"encoding/hex"
	"net/netip"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gosnmp/gosnmp"
)

// The decoders in SC2.5's parsers.py spend most of their length getting bytes
// out of pysnmp objects: asOctets, prettyPrint, a "0x" prefix to undo. gosnmp
// hands over an OctetString as []byte, so what remains here is the part that
// was ever about SNMP.

// pduBytes returns the raw value of an OctetString (or anything gosnmp
// represents as bytes or a string). Anything else, including the
// NoSuchObject/NoSuchInstance exceptions, is nil.
func pduBytes(p gosnmp.SnmpPDU) []byte {
	switch v := p.Value.(type) {
	case []byte:
		return v
	case string:
		return []byte(v)
	}
	return nil
}

// pduInt returns an integer-typed value.
func pduInt(p gosnmp.SnmpPDU) (int, bool) {
	switch p.Type {
	case gosnmp.Integer, gosnmp.Counter32, gosnmp.Gauge32, gosnmp.Uinteger32,
		gosnmp.Counter64, gosnmp.TimeTicks:
		return int(gosnmp.ToBigInt(p.Value).Int64()), true
	}
	return 0, false
}

// decodeString is decode_string: UTF-8 when it is valid, Latin-1 when it is
// not, NULs removed, whitespace trimmed. The "0x" handling in the Python is
// dropped deliberately -- it undid pysnmp's hex rendering of binary values,
// and applied to raw bytes it would rewrite a device that genuinely reports a
// string starting "0x".
func decodeString(b []byte) string {
	var s string
	if utf8.Valid(b) {
		s = string(b)
	} else {
		r := make([]rune, len(b))
		for i, c := range b {
			r[i] = rune(c)
		}
		s = string(r)
	}
	return strings.TrimSpace(strings.ReplaceAll(s, "\x00", ""))
}

// decodeMAC renders bytes as lowercase colon-separated hex, as decode_mac
// does. It does not insist on six bytes: a chassis ID that claims the MAC
// subtype and carries something else is still best shown as its bytes.
func decodeMAC(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	h := hex.EncodeToString(b)
	var sb strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			sb.WriteByte(':')
		}
		sb.WriteString(h[i : i+2])
	}
	return sb.String()
}

// decodeNetAddr decodes an address that may or may not lead with an IANA
// address-family byte: 4 bytes is IPv4, 5 is family 1 plus IPv4 (the CDP and
// LLDP network-address form), 16 is IPv6, 17 is family 2 plus IPv6. Anything
// else is "" -- decode_ip returns repr() there, which is not an address.
func decodeNetAddr(b []byte) string {
	switch len(b) {
	case 4:
		return netip.AddrFrom4([4]byte(b)).String()
	case 5:
		if b[0] == 1 {
			return netip.AddrFrom4([4]byte(b[1:])).String()
		}
	case 16:
		return netip.AddrFrom16([16]byte(b)).String()
	case 17:
		if b[0] == 2 {
			return netip.AddrFrom16([16]byte(b[1:])).String()
		}
	}
	return ""
}

// decodeIPv4 is decodeNetAddr limited to IPv4, for the CDP columns, where
// SC2.5 accepts nothing else.
func decodeIPv4(b []byte) string {
	a := decodeNetAddr(b)
	if ip, err := netip.ParseAddr(a); err == nil && ip.Is4() {
		return a
	}
	return ""
}

// decodeChassisID is decode_chassis_id. The unknown-subtype branch tries a
// string as the Python does.
func decodeChassisID(subtype int, b []byte) string {
	switch subtype {
	case chassisSubtypeMAC:
		return decodeMAC(b)
	case chassisSubtypeNetwork:
		if a := decodeNetAddr(b); a != "" {
			return a
		}
		return decodeMAC(b)
	}
	return decodeString(b)
}

// decodePortID is decode_port_id.
func decodePortID(subtype int, b []byte) string {
	switch subtype {
	case portSubtypeMAC:
		return decodeMAC(b)
	case portSubtypeNetwork:
		if a := decodeNetAddr(b); a != "" {
			return a
		}
		return decodeMAC(b)
	}
	return decodeString(b)
}

// LLDP capability names in LldpSystemCapabilitiesMap bit order, with the
// names parse_lldp_capabilities uses.
var lldpCapNames = [...]string{
	"other", "repeater", "bridge", "wlan-ap", "router", "telephone", "docsis", "station",
}

// lldpCapabilities decodes lldpRemSysCapEnabled. It is an SMIv2 BITS value,
// so bit 0 is the most significant bit of the first octet -- the reverse of
// the TLV's on-wire bit order, which is what the CAP_* masks in oids.py
// describe. SC2.5 read this column with decode_string, so there is no
// validated Python behavior to match here.
func lldpCapabilities(b []byte) string {
	var names []string
	for bit, name := range lldpCapNames {
		octet := bit / 8
		if octet < len(b) && b[octet]&(0x80>>(bit%8)) != 0 {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// CDP capability bits, with the masks and names from oids.py and
// parse_cdp_capabilities.
var cdpCaps = []struct {
	mask uint32
	name string
}{
	{0x01, "router"},
	{0x02, "bridge"},
	{0x04, "source-route-bridge"},
	{0x08, "switch"},
	{0x10, "host"},
	{0x20, "igmp"},
	{0x40, "repeater"},
}

// cdpCapabilities decodes cdpCacheCapabilities: OCTET STRING (SIZE (0..4))
// holding the capability word in network byte order. SC2.5 read it with
// decode_string, which yields control characters, so as with LLDP there is no
// validated behavior to preserve.
func cdpCapabilities(b []byte) string {
	if len(b) == 0 || len(b) > 4 {
		return ""
	}
	var buf [4]byte
	copy(buf[4-len(b):], b)
	v := binary.BigEndian.Uint32(buf[:])
	var names []string
	for _, c := range cdpCaps {
		if v&c.mask != 0 {
			names = append(names, c.name)
		}
	}
	return strings.Join(names, ", ")
}

// oidSuffix returns the numeric components of name below root, or nil when
// name is not under root or a component is not a number.
func oidSuffix(name, root string) []int {
	rest, ok := strings.CutPrefix(name, root+".")
	if !ok || rest == "" {
		return nil
	}
	parts := strings.Split(rest, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out[i] = n
	}
	return out
}

// indexKey joins index components back into the dotted form used as a map
// key, so a row from one table can be found from another.
func indexKey(parts []int) string {
	s := make([]string, len(parts))
	for i, p := range parts {
		s[i] = strconv.Itoa(p)
	}
	return strings.Join(s, ".")
}
