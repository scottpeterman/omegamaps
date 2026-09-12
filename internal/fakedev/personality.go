// internal/fakedev/personality.go
//
// Per-platform device presets.
//
// Each one carries the three things platform detection actually turns on:
// the prompt shape, which paging-disable command the device accepts, and
// what its version output says. The rest is scenery.
//
// The rejection strings matter as much as the acceptances. netexec decides
// a probe missed by reading the device's complaint, so a personality whose
// unknown-command reply is too polite will fingerprint as the wrong
// platform — which is a bug this package exists to be able to reproduce.
//
// Names here are lab names by convention (lab-r1, lab-spine-1,
// site1.lab.example). Nothing in this package should ever carry a name
// from a real network.
package fakedev

import "strings"

// IOS is a Cisco IOS device. Accepts "terminal length 0"; rejects
// everything else with the % marker line real IOS emits.
func IOS(name string) Config {
	return Config{
		Prompt:            name + "#",
		Banner:            "\r\nUnauthorized access prohibited.\r\n",
		AcceptAnyPassword: true,
		Unknown:           "% Invalid input detected at '^' marker.",
		Commands: map[string]string{
			"terminal length 0": "",
			"show version": strings.Join([]string{
				"Cisco IOS Software, IOSv Software (VIOS-ADVENTERPRISEK9-M), Version 15.6(2)T",
				"Technical Support: http://www.cisco.com/techsupport",
				"",
				name + " uptime is 4 days, 2 hours, 11 minutes",
				"System image file is \"flash0:/vios-adventerprisek9-m\"",
			}, "\n"),
			"show running-config": iosRunningConfig(name),
			"show lldp neighbors detail": strings.Join([]string{
				"------------------------------------------------",
				"Local Intf: Gi0/1",
				"Chassis id: 0c1d.5e2f.0001",
				"Port id: Ethernet1",
				"System Name: lab-spine-1",
				"",
				"System Description:",
				"Arista Networks EOS version 4.33.1F",
				"",
				"Management Addresses:",
				"    IP: 172.16.2.2",
				"",
				"Total entries displayed: 1",
			}, "\n"),
		},
	}
}

// EOS is an Arista device. Same paging command as IOS, different version
// output — which is the whole reason the probe classifies on the output
// rather than on which paging command stuck.
func EOS(name string) Config {
	return Config{
		Prompt:            name + "#",
		AcceptAnyPassword: true,
		Unknown:           "% Invalid input",
		Commands: map[string]string{
			"terminal length 0": "",
			"show version": strings.Join([]string{
				"Arista vEOS-lab",
				"Hardware version:",
				"Software image version: 4.33.1F",
				"Architecture: i686",
				"Uptime: 4 days, 2 hours and 11 minutes",
			}, "\n"),
			"show running-config": "! device: " + name + " (vEOS-lab, EOS-4.33.1F)\n!\nhostname " + name + "\n!\nend",
		},
	}
}

// NXOS is a Cisco Nexus device.
func NXOS(name string) Config {
	return Config{
		Prompt:            name + "#",
		AcceptAnyPassword: true,
		Unknown:           "% Invalid command at '^' marker.",
		Commands: map[string]string{
			"terminal length 0": "",
			"show version": strings.Join([]string{
				"Cisco Nexus Operating System (NX-OS) Software",
				"TAC support: http://www.cisco.com/tac",
				"",
				"  system:    version 9.3(10)",
				"  Device name: " + name,
			}, "\n"),
			"show running-config": "!Command: show running-config\nversion 9.3(10)\nhostname " + name,
		},
	}
}

// Junos is a Juniper device. Rejects "terminal length 0" the way Junos
// does — with a caret line and "unknown command." — so the first probe
// misses and the second one lands.
func Junos(name string) Config {
	return Config{
		Prompt:            "lab@" + name + ">",
		AcceptAnyPassword: true,
		Unknown:           "                     ^\nunknown command.",
		Commands: map[string]string{
			"set cli screen-length 0": "Screen length set to 0",
			"show version": strings.Join([]string{
				"Hostname: " + name,
				"Model: vmx",
				"Junos: 21.4R3-S5.4",
				"JUNOS OS Kernel 64-bit  [20230307.043029_builder_stable_11]",
			}, "\n"),
			"show configuration": "## Last commit: 2026-07-30\nsystem {\n    host-name " + name + ";\n}",
		},
	}
}

// Linux is a plain server. Present because a crawl finds them and a
// capture has to decide what to do about them, and because it is the one
// personality whose rejection text ("command not found") takes a different
// branch of the CLI-error match.
func Linux(name string) Config {
	return Config{
		Prompt:            "lab@" + name + ":~$",
		AcceptAnyPassword: true,
		Unknown:           "bash: command not found",
		Commands: map[string]string{
			"uname -a": "Linux " + name + " 6.8.0-45-generic #45-Ubuntu SMP x86_64 GNU/Linux",
		},
	}
}

// iosRunningConfig is long enough to be worth capturing and short enough
// to read in a failure message.
func iosRunningConfig(name string) string {
	return strings.Join([]string{
		"Building configuration...",
		"",
		"Current configuration : 1387 bytes",
		"!",
		"version 15.6",
		"service timestamps debug datetime msec",
		"!",
		"hostname " + name,
		"!",
		"interface GigabitEthernet0/0",
		" description uplink to lab-spine-1",
		" ip address 172.16.11.41 255.255.255.0",
		"!",
		"interface GigabitEthernet0/1",
		" description lab access",
		" ip address 172.16.12.41 255.255.255.0",
		"!",
		"router bgp 65001",
		" neighbor 172.16.2.2 remote-as 65000",
		"!",
		"end",
	}, "\n")
}
