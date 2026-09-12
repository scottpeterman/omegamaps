package crawldial

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/scottpeterman/omegamaps/internal/crawler"
	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/credres"
	"github.com/scottpeterman/omegamaps/internal/snmpprobe"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

func TestSNMPOutcome(t *testing.T) {
	probe := func(stage string, err error) error { return &snmpprobe.Error{Target: "sw1", Stage: stage, Err: err} }
	cases := []struct {
		name string
		err  error
		want credres.Outcome
	}{
		{"answered", nil, credres.OutcomeSuccess},
		{"no answer", probe(snmpprobe.StageSystem, fmt.Errorf("%w: request timeout", snmpprobe.ErrNoResponse)), credres.OutcomeNoAnswer},
		{"v3 refusal", probe(snmpprobe.StageSystem, errors.New("incoming packet is not authentic")), credres.OutcomeAuthRejected},
		{"misconfigured", probe(snmpprobe.StageCredential, errors.New("unknown v3 auth protocol")), credres.OutcomeKeyMaterial},
		{"nothing listening", probe(snmpprobe.StageSystem, &net.OpError{Op: "read", Err: errors.New("refused")}), credres.OutcomeUnreachable},
		{"walk after acceptance", probe("lldpRemTable walk", errors.New("request timeout")), credres.OutcomeUnreachable},
		{"not a probe error", errors.New("?"), credres.OutcomeOther},
	}
	for _, c := range cases {
		if got := SNMPOutcome(c.err); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSNMPBindingsPath(t *testing.T) {
	for in, want := range map[string]string{
		"/v/vault.bindings.json": "/v/vault.snmp.bindings.json",
		"/v/custom.json":         "/v/custom.snmp.json",
	} {
		if got := SNMPBindingsPath(in); got != want {
			t.Errorf("SNMPBindingsPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func labVault(t *testing.T, creds ...vault.Credential) (*vault.Vault, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "vault.json")
	v := vault.New(path)
	if err := v.Create("lab-master-pw"); err != nil {
		t.Fatal(err)
	}
	for _, c := range creds {
		if _, err := v.Add(c); err != nil {
			t.Fatal(err)
		}
	}
	return v, path
}

func TestBuildResolvesSNMPFromTheVault(t *testing.T) {
	v, path := labVault(t, vault.Credential{Name: "lab-ro", AuthType: vault.AuthTypeSNMPv2c, Password: "public"})
	p := snmpParams(crawlrun.MethodSNMP)
	p.VaultPath = path
	b, err := Build(p, Options{Vault: v})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	defer b.Close()
	if b.SNMPResolver == nil || b.SNMPBindings == nil {
		t.Fatal("no SNMP resolver from a vault holding an SNMP credential")
	}
	if b.Resolver != nil || b.Bindings != nil {
		t.Fatal("an SNMP-only run set up SSH resolution")
	}
}

func TestBuildRefusesAVaultWithoutSNMPCredentials(t *testing.T) {
	v, path := labVault(t, vault.Credential{Name: "lab-pw", Username: "admin", AuthType: "password", Password: "x"})
	p := snmpParams(crawlrun.MethodSNMP)
	p.VaultPath = path
	if _, err := Build(p, Options{Vault: v}); err == nil || !strings.Contains(err.Error(), "no enabled SNMP credential") {
		t.Fatalf("want a refusal naming the empty vault, got %v", err)
	}
}

// The point of routing SNMP through the resolver. A wrong community sits ahead
// of the right one; the first probe of a device pays a timeout to learn which
// answers, and the second goes straight to it because the binding says so.
//
//	PFSNMP_TEST_TARGET=127.0.0.1 PFSNMP_TEST_PORT=16161 PFSNMP_TEST_COMMUNITY=... go test ./internal/crawldial -run Learns -v
func TestVaultSNMPFuncLearnsTheCommunity(t *testing.T) {
	target, community := os.Getenv("PFSNMP_TEST_TARGET"), os.Getenv("PFSNMP_TEST_COMMUNITY")
	if target == "" || community == "" {
		t.Skip("PFSNMP_TEST_TARGET and PFSNMP_TEST_COMMUNITY not set")
	}
	v, _ := labVault(t,
		vault.Credential{Name: "wrong", AuthType: vault.AuthTypeSNMPv2c, Password: "not-" + community, Priority: 10},
		vault.Credential{Name: "right", AuthType: vault.AuthTypeSNMPv2c, Password: community, Priority: 20},
	)
	opt := snmpprobe.Options{Timeout: time.Second, Retries: -1}
	if s := os.Getenv("PFSNMP_TEST_PORT"); s != "" {
		n, _ := strconv.Atoi(s)
		opt.Port = uint16(n)
	}
	res := credres.New(v, credres.NewMemoryBindings(), credres.Config{
		Method: crawlrun.MethodSNMP, Classify: SNMPOutcome, MaxPerHost: -1, Log: t.Logf})
	fn := NewVaultSNMPFunc(res, nil, opt)
	dt := crawler.DialTarget{Target: target, Identity: "lab-snmpd"}

	for i, want := range []time.Duration{time.Second, 0} {
		start := time.Now()
		d, err := fn(context.Background(), dt)
		el := time.Since(start)
		if err != nil || d.Failed {
			t.Fatalf("probe %d: %v %+v", i+1, err, d)
		}
		t.Logf("probe %d: %v", i+1, el.Round(time.Millisecond))
		if want > 0 && el < want {
			t.Fatalf("probe %d took %v: the wrong community should have cost a timeout first", i+1, el)
		}
		if want == 0 && el > 500*time.Millisecond {
			t.Fatalf("probe %d took %v: the binding should have put the right community first", i+1, el)
		}
	}
}
