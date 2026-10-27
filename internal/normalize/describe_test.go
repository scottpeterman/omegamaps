package normalize

import "testing"

func TestDescribe(t *testing.T) {
	cases := []struct {
		name, token, text, want string
	}{
		// SSH: show version
		{"eos hw", "arista_eos", "Arista DCS-7280SR3-48YC8\nHardware version: 11.02\nSoftware image version: 4.30.1F", "Arista DCS-7280SR3-48YC8 EOS"},
		{"eos veos", "arista_eos", "Arista vEOS-lab\nHardware version:\nSoftware image version: 4.33.1F", "Arista vEOS-lab EOS"},
		{"eos ceos", "arista_eos", "Arista cEOSLab\nHardware version:", "Arista cEOSLab EOS"},
		{"nxos chassis", "cisco_nxos", "Cisco Nexus Operating System (NX-OS) Software\nHardware\n  cisco Nexus9000 C9396PX Chassis\n  Intel(R) Core(TM)", "Cisco Nexus9000 C9396PX NX-OS"},
		{"nxos spaced", "cisco_nxos", "Cisco Nexus Operating System (NX-OS) Software\n  cisco Nexus 3048 Chassis (\"48x1GE + 4x10G Supervisor\")", "Cisco Nexus 3048 NX-OS"},
		{"nxos bare", "cisco_nxos", "Cisco Nexus Operating System (NX-OS) Software\n  system:    version 9.3(10)", "Cisco Nexus NX-OS"},
		{"iosxe cat9k", "cisco_iosxe", "Cisco IOS XE Software, Version 17.03.04\ncisco C9300-48P (X86) processor with 1419044K/6147K bytes of memory.", "Cisco C9300-48P IOS-XE"},
		{"iosxe isr", "cisco_iosxe", "Cisco IOS XE Software, Version 16.09.04\ncisco ISR4451-X/K9 (2RU) processor with 1795979K/6147K bytes", "Cisco ISR4451-X/K9 IOS-XE"},
		{"ios 3750", "cisco_ios", "Cisco IOS Software, C3750E Software (C3750E-UNIVERSALK9-M), Version 15.2(4)E7\ncisco WS-C3750X-48P (PowerPC405) processor (revision W0) with 262144K bytes", "Cisco WS-C3750X-48P IOS"},
		{"iosv", "cisco_ios", "Cisco IOS Software, IOSv Software (VIOS-ADVENTERPRISEK9-M), Version 15.6(2)T\nSystem image file is \"flash0:/vios-adventerprisek9-m\"", "Cisco IOSv"},
		{"iosvl2", "cisco_ios", "Cisco IOS Software, vios_l2 Software (vios_l2-ADVENTERPRISEK9-M), Version 15.2\ncisco IOSv (revision 1.0) with 657346K/24576K bytes", "Cisco IOSvL2"},
		{"asa", "cisco_asa", "Cisco Adaptive Security Appliance Software Version 9.12(4)\nHardware:   ASA5516, 8192 MB RAM, CPU Atom C2000", "Cisco ASA5516 ASA"},
		{"junos mx", "juniper_junos", "Hostname: r1\nModel: mx960\nJunos: 21.4R3-S5.4", "Juniper MX960 JUNOS"},
		{"junos vmx", "juniper_junos", "Hostname: r1\nModel: vmx\nJunos: 21.4R3", "Juniper VMX JUNOS"},
		{"palo", "paloalto_panos", "hostname: fw1\nmodel: PA-3220\nsw-version: 10.1.6", "Palo Alto PA-3220 PAN-OS"},
		{"forti", "fortinet", "Version: FortiGate-100F v7.2.5,build1517,230606 (GA.F)", "Fortinet FortiGate-100F FortiOS"},

		// SNMP: sysDescr
		{"snmp eos", "arista_eos", "Arista Networks EOS version 4.33.1F running on an Arista vEOS-lab", "Arista vEOS-lab EOS"},
		{"snmp eos hw", "", "Arista Networks EOS version 4.28.3M running on an Arista Networks DCS-7050SX3-48YC8", "Arista DCS-7050SX3-48YC8 EOS"},
		{"snmp ios", "cisco_ios", "Cisco IOS Software, C3750E Software (C3750E-UNIVERSALK9-M), Version 15.2(4)E7, RELEASE SOFTWARE (fc2)", "Cisco C3750E IOS"},
		{"snmp nxos", "cisco_nxos", "Cisco NX-OS(tm) n9000, Software (n9000-dk9), Version 9.3(10), RELEASE SOFTWARE", "Cisco Nexus9000 NX-OS"},
		{"snmp palo", "", "Palo Alto Networks PA-3200 series firewall", "Palo Alto PA-3200 PAN-OS"},
		{"snmp asr", "cisco_ios", "Cisco IOS Software [Cupertino], ASR1000 Software (X86_64_LINUX_IOSD-UNIVERSALK9-M), Version 17.9.4a, RELEASE SOFTWARE (fc3)", "Cisco ASR1000 IOS"},
		{"snmp junos", "juniper_junos", "Juniper Networks, Inc. qfx5100-48s-6q Ethernet Switch, kernel JUNOS 18.4R2-S3", "Juniper QFX5100-48S-6Q JUNOS"},

		// Nothing to add
		{"linux", "linux", "Linux host 6.1.0", ""},
		{"empty", "", "", ""},
	}
	for _, c := range cases {
		if got := Describe(c.token, c.text); got != c.want {
			t.Errorf("%s: Describe(%q) = %q, want %q", c.name, c.token, got, c.want)
		}
	}
}

func TestDescribeOr(t *testing.T) {
	if got := DescribeOr("linux", "Linux host"); got != "linux" {
		t.Errorf("fallback = %q, want the token", got)
	}
	if got := DescribeOr("juniper_junos", "Model: ex4300-48t"); got != "Juniper EX4300-48T JUNOS" {
		t.Errorf("got %q", got)
	}
}
