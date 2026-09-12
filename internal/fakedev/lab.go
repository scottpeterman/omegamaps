// internal/fakedev/lab.go
//
// A small fake network with the shape of the home lab: a WAN core, two
// routers, a spine pair, and what hangs below them. Each device is a fakedev
// IOS box listening on port 22 at its lab address and answering LLDP with its
// neighbors, so a crawl seeded at the core follows links, retries names at the
// addresses neighbors report, authenticates through the vault, and writes a
// map -- the whole stack, with no gear.
//
// The addresses have to exist on the host. The crawler refuses loopback
// addresses as neighbors on purpose, so the lab cannot live on 127.0.0.1; it
// lives on 172.16.x aliases instead (see LabAddrCommands), and binding port 22
// needs root. That makes this a sandbox and CI tool, not something a unit
// test can assume.
//
// Two devices exist to exercise the other outcomes:
//
//   - eng-leaf-1 accepts a different password from the rest, so the lab
//     credential is rejected there and the device ends failed.
//   - eng-host-9 is not a device at all: eng-spine-2 reports it over LLDP as a
//     Linux server, which an exclude pattern of "linux" leaves undialed.
package fakedev

import (
	"fmt"
	"strings"
	"time"
)

// LabUser and LabPassword are what every lab device accepts, except the one
// that is there to reject them.
const (
	LabUser     = "cisco"
	LabPassword = "lab-pass"

	// labLeafPassword is eng-leaf-1's, and nothing is told it.
	labLeafPassword = "leaf-only-pass"
)

// LabDevice describes one device of the fake lab.
type LabDevice struct {
	Name     string // hostname and prompt
	SysName  string // what it advertises over LLDP
	Addr     string // management address; the device listens on Addr:22
	Descr    string // LLDP system description
	Caps     string // LLDP enabled capabilities
	Password string // "" means LabPassword
	Links    []LabLink
}

// LabLink is one LLDP neighbor as the device sees it.
type LabLink struct {
	Local      string // local interface
	Remote     string // neighbor's device name (key into the lab, or a host)
	RemotePort string
}

const (
	iosvDescr  = "Cisco IOS Software, IOSv Software (VIOS-ADVENTERPRISEK9-M), Version 15.6(2)T, RELEASE SOFTWARE (fc2)"
	iosl2Descr = "Cisco IOS Software, vios_l2 Software (vios_l2-ADVENTERPRISEK9-M), Version 15.2(20170321:233949) [sweickge 101]"
)

// labHost is a neighbor that is not a fake device: reported, never dialed.
type labHost struct {
	sysName, addr, descr string
}

var labHosts = map[string]labHost{
	"eng-host-9": {"eng-host-9", "172.16.3.9",
		"Linux eng-host-9 5.15.0-91-generic #101-Ubuntu SMP x86_64"},
}

// HomeLab is the topology. Routers advertise fully qualified names and the
// L2 images bare ones, as the IOSv lab does.
func HomeLab() []LabDevice {
	return []LabDevice{
		{Name: "wan-core-1", SysName: "wan-core-1.lab.local", Addr: "172.16.1.2", Descr: iosvDescr, Caps: "R",
			Links: []LabLink{
				{"Gi0/1", "usa-rtr-1", "Gi0/0"},
				{"Gi0/2", "eng-rtr-1", "Gi0/0"},
			}},
		{Name: "usa-rtr-1", SysName: "usa-rtr-1.lab.local", Addr: "172.16.100.2", Descr: iosvDescr, Caps: "R",
			Links: []LabLink{
				{"Gi0/0", "wan-core-1", "Gi0/1"},
				{"Gi0/2", "eng-rtr-1", "Gi0/3"},
			}},
		{Name: "eng-rtr-1", SysName: "eng-rtr-1.lab.local", Addr: "172.16.128.2", Descr: iosvDescr, Caps: "R",
			Links: []LabLink{
				{"Gi0/0", "wan-core-1", "Gi0/2"},
				{"Gi0/3", "usa-rtr-1", "Gi0/2"},
				{"Gi0/1", "eng-spine-1", "Gi0/0"},
				{"Gi0/2", "eng-spine-2", "Gi0/0"},
			}},
		{Name: "eng-spine-1", SysName: "eng-spine-1", Addr: "172.16.2.2", Descr: iosl2Descr, Caps: "B,R",
			Links: []LabLink{
				{"Gi0/0", "eng-rtr-1", "Gi0/1"},
				{"Gi0/1", "eng-spine-2", "Gi0/1"},
				{"Gi0/2", "eng-leaf-1", "Gi0/0"},
			}},
		{Name: "eng-spine-2", SysName: "eng-spine-2", Addr: "172.16.2.6", Descr: iosl2Descr, Caps: "B,R",
			Links: []LabLink{
				{"Gi0/0", "eng-rtr-1", "Gi0/2"},
				{"Gi0/1", "eng-spine-1", "Gi0/1"},
				{"Gi0/3", "eng-host-9", "eth0"},
			}},
		{Name: "eng-leaf-1", SysName: "eng-leaf-1", Addr: "172.16.3.2", Descr: iosl2Descr, Caps: "B",
			Password: labLeafPassword,
			Links: []LabLink{
				{"Gi0/0", "eng-spine-1", "Gi0/2"},
			}},
	}
}

// LabSeed is where a crawl of the lab starts.
const LabSeed = "172.16.1.2"

// LabAddrCommands are the commands that give this host the lab's addresses,
// for a caller to print when a bind fails.
func LabAddrCommands() []string {
	var out []string
	for _, d := range HomeLab() {
		out = append(out, fmt.Sprintf("ip addr add %s/32 dev lo", d.Addr))
	}
	return out
}

// Lab is a running fake lab.
type Lab struct {
	Devices []LabDevice
	Servers []*Server
}

// StartLab brings up every device of the lab at its address. latency delays
// every command's output, which turns a crawl that finishes in under a second
// into one a test can catch in flight. On any failure the devices already
// started are closed and the error names the address.
func StartLab(latency time.Duration) (*Lab, error) {
	lab := &Lab{Devices: HomeLab()}
	byName := map[string]LabDevice{}
	for _, d := range lab.Devices {
		byName[d.Name] = d
	}
	for _, d := range lab.Devices {
		cfg := IOS(d.Name)
		cfg.Listen = d.Addr + ":22"
		cfg.Latency = latency
		cfg.AcceptAnyPassword = false
		cfg.Username = LabUser
		cfg.Password = LabPassword
		if d.Password != "" {
			cfg.Password = d.Password
		}
		cfg.Commands["show lldp neighbors detail"] = lldpDetail(d, byName)
		cfg.Commands["show lldp neighbors"] = lldpSummary(d, byName)
		// Best effort in the IOS plan; the lab runs LLDP only, and this is
		// what IOS says when CDP is off.
		cfg.Commands["show cdp neighbors detail"] = "% CDP is not enabled"
		srv, err := Start(cfg)
		if err != nil {
			lab.Close()
			return nil, fmt.Errorf("%s at %s: %w", d.Name, cfg.Listen, err)
		}
		lab.Servers = append(lab.Servers, srv)
	}
	return lab, nil
}

// Close stops every device.
func (l *Lab) Close() {
	for _, s := range l.Servers {
		s.Close()
	}
	l.Servers = nil
}

// neighbor returns what a link's far end advertises.
func neighbor(name string, byName map[string]LabDevice) (sysName, addr, descr, caps string) {
	if d, ok := byName[name]; ok {
		return d.SysName, d.Addr, d.Descr, d.Caps
	}
	if h, ok := labHosts[name]; ok {
		return h.sysName, h.addr, h.descr, "S"
	}
	return name, "", "", ""
}

// lldpDetail renders "show lldp neighbors detail" as IOS prints it.
func lldpDetail(d LabDevice, byName map[string]LabDevice) string {
	var b strings.Builder
	for i, l := range d.Links {
		sys, addr, descr, caps := neighbor(l.Remote, byName)
		b.WriteString("------------------------------------------------\n")
		fmt.Fprintf(&b, "Local Intf: %s\n", l.Local)
		fmt.Fprintf(&b, "Chassis id: 5254.00%02x.%04x\n", i+1, len(d.Name)*97+i)
		fmt.Fprintf(&b, "Port id: %s\n", l.RemotePort)
		fmt.Fprintf(&b, "Port Description: %s\n", longPort(l.RemotePort))
		fmt.Fprintf(&b, "System Name: %s\n\n", sys)
		b.WriteString("System Description: \n")
		fmt.Fprintf(&b, "%s\n\n", descr)
		b.WriteString("Time remaining: 101 seconds\n")
		fmt.Fprintf(&b, "System Capabilities: %s\n", caps)
		fmt.Fprintf(&b, "Enabled Capabilities: %s\n", caps)
		b.WriteString("Management Addresses:\n")
		if addr != "" {
			fmt.Fprintf(&b, "    IP: %s\n", addr)
		}
		b.WriteString("Auto Negotiation - not supported\n")
		b.WriteString("Physical media capabilities - not advertised\n")
		b.WriteString("Media Attachment Unit type - not advertised\n")
		b.WriteString("Vlan ID: - not advertised\n\n")
	}
	fmt.Fprintf(&b, "\nTotal entries displayed: %d", len(d.Links))
	return b.String()
}

// lldpSummary renders "show lldp neighbors" as IOS prints it: the name
// truncated to 20 characters, no address column.
func lldpSummary(d LabDevice, byName map[string]LabDevice) string {
	var b strings.Builder
	b.WriteString("Capability codes:\n")
	b.WriteString("    (R) Router, (B) Bridge, (T) Telephone, (C) DOCSIS Cable Device\n")
	b.WriteString("    (W) WLAN Access Point, (P) Repeater, (S) Station, (O) Other\n\n")
	b.WriteString("Device ID           Local Intf     Hold-time  Capability      Port ID\n")
	for _, l := range d.Links {
		sys, _, _, caps := neighbor(l.Remote, byName)
		if len(sys) > 20 {
			sys = sys[:20]
		}
		fmt.Fprintf(&b, "%-20s%-15s%-11d%-16s%s\n", sys, l.Local, 120, caps, l.RemotePort)
	}
	fmt.Fprintf(&b, "\nTotal entries displayed: %d", len(d.Links))
	return b.String()
}

func longPort(p string) string {
	if strings.HasPrefix(p, "Gi") {
		return "GigabitEthernet" + strings.TrimPrefix(p, "Gi")
	}
	return p
}
