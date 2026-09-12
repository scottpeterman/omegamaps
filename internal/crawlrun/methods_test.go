package crawlrun

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestParseMethods(t *testing.T) {
	good := []struct {
		in   string
		want []Method
	}{
		{"ssh", []Method{MethodSSH}},
		{"snmp,ssh", []Method{MethodSNMP, MethodSSH}},
		{" SNMP ; ssh ", []Method{MethodSNMP, MethodSSH}},
		{"ssh snmp", []Method{MethodSSH, MethodSNMP}},
	}
	for _, c := range good {
		got, err := ParseMethods(c.in)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseMethods(%q) = %v, %v; want %v", c.in, got, err, c.want)
		}
	}
	for _, in := range []string{"telnet", "ssh,ssh", "snmp,netconf"} {
		if _, err := ParseMethods(in); err == nil {
			t.Errorf("ParseMethods(%q) accepted", in)
		}
	}
}

func TestMethodsDefaultAndValidate(t *testing.T) {
	p := Defaults()
	p.Seeds = []string{"lab-r1.lab.example"}
	if got := p.CollectionMethods(); !reflect.DeepEqual(got, []Method{MethodSSH}) {
		t.Fatalf("default methods = %v, want [ssh]", got)
	}
	if !p.Uses(MethodSSH) || p.Uses(MethodSNMP) {
		t.Fatal("default Params should use SSH only")
	}

	p.Methods = []Method{" SNMP ", "ssh"}
	if errs := p.Validate(); len(errs) != 0 {
		t.Fatalf("valid methods refused: %v", errs)
	}
	if !reflect.DeepEqual(p.Methods, []Method{MethodSNMP, MethodSSH}) {
		t.Fatalf("Validate did not normalize methods: %v", p.Methods)
	}

	p.Methods = []Method{"snmp", "snmp", "telnet"}
	errs := p.Validate()
	if len(errs) != 2 {
		t.Fatalf("want a duplicate and an unknown method reported, got %v", errs)
	}
	for _, e := range errs {
		if e.Field != "methods" {
			t.Errorf("error on field %q, want methods: %v", e.Field, e)
		}
	}
}

// A profile saved before SNMP existed has no "methods" key and must load as
// SSH only; one saved with methods must round-trip them.
func TestMethodsProfileCompatibility(t *testing.T) {
	var old Params
	if err := json.Unmarshal([]byte(`{"seeds":["lab-r1"],"depth":3}`), &old); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(old.CollectionMethods(), []Method{MethodSSH}) {
		t.Fatalf("pre-SNMP profile loads as %v", old.CollectionMethods())
	}

	p := Params{Seeds: []string{"lab-r1"}, Methods: []Method{MethodSNMP, MethodSSH}}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"methods":["snmp","ssh"]`) {
		t.Fatalf("methods not saved: %s", b)
	}
	var back Params
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Methods, p.Methods) {
		t.Fatalf("round trip: %v", back.Methods)
	}
}

// A fallback is a decision worth surfacing, it is not a failure, and the row
// records how the device was finally reached.
func TestRunFallbackThenReached(t *testing.T) {
	r := New()
	r.Handle(Event{Kind: KindQueued, Identity: "lab-r1"})
	fb := Event{Kind: KindFallback, Identity: "lab-r1", Method: MethodSNMP,
		Detail: "snmp failed: snmp system: request timeout; trying ssh"}
	r.Handle(fb)

	row := r.Rows()[0]
	if row.State != StateRunning || row.Detail != fb.Detail || row.Method != "" {
		t.Fatalf("after fallback: state=%s detail=%q method=%q", row.State, row.Detail, row.Method)
	}
	if !fb.Notable() || !strings.Contains(fb.Describe(), "trying ssh") || fb.Kind.String() != "fallback" {
		t.Fatalf("fallback event: notable=%v describe=%q kind=%s", fb.Notable(), fb.Describe(), fb.Kind)
	}

	r.Handle(Event{Kind: KindReached, Identity: "lab-r1", Method: MethodSSH})
	row = r.Rows()[0]
	if row.State != StateReached || row.Method != MethodSSH {
		t.Fatalf("after reached: state=%s method=%q", row.State, row.Method)
	}
	if len(r.Decisions()) != 1 {
		t.Fatalf("decisions = %d, want the one fallback", len(r.Decisions()))
	}
}
