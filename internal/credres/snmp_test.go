package credres

import (
	"errors"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/crawlrun"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

func snmpCred(id, name string, prio int) vault.Credential {
	return vault.Credential{ID: id, Name: name, AuthType: vault.AuthTypeSNMPv2c, Password: "c-" + id, Priority: prio}
}

// An SSH resolver offering a community as a password, or an SNMP resolver
// offering a login as a community, is the failure the kinds exist to stop --
// pinned or not.
func TestResolverOffersOnlyItsKind(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{
		cred("s-pw", "lab-pw"), cred("s-key", "lab-key", withKeyAuth()),
		snmpCred("n-ro", "lab-ro", 0),
		{ID: "n-v3", Name: "lab-v3", AuthType: vault.AuthTypeSNMPv3, Username: "mon", Password: "k"},
	}}
	ssh := New(store, nil, Config{MaxPerHost: -1})
	got, err := ssh.Resolve(Target{Identity: "sw1", Pin: "lab-ro"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if c.Cred.IsSNMP() {
			t.Fatalf("SSH resolver offered %s", c.Cred.Name)
		}
	}
	snmp := New(store, nil, Config{Method: crawlrun.MethodSNMP, MaxPerHost: -1})
	got, err = snmp.Resolve(Target{Identity: "sw1", Pin: "lab-pw"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("SNMP resolver offered %v, want the two SNMP credentials", ids(got))
	}
	for _, c := range got {
		if !c.Cred.IsSNMP() {
			t.Fatalf("SNMP resolver offered %s", c.Cred.Name)
		}
	}
}

var (
	errNoAnswer = errors.New("no response")
	errRefused  = errors.New("not authentic")
)

func snmpClassify(err error) Outcome {
	switch {
	case err == nil:
		return OutcomeSuccess
	case errors.Is(err, errNoAnswer):
		return OutcomeNoAnswer
	case errors.Is(err, errRefused):
		return OutcomeAuthRejected
	}
	return OutcomeOther
}

// Dead hosts answer nothing to every community. If that counted, a breaker of
// one would park the community that works everywhere else; a genuine
// rejection, on the other hand, must still trip it.
func TestNoAnswerIsRetryableButCountsNothing(t *testing.T) {
	store := fakeStore{creds: []vault.Credential{snmpCred("good", "lab-ro", 10), snmpCred("other", "lab-alt", 20)}}
	r := New(store, NewMemoryBindings(), Config{Method: crawlrun.MethodSNMP, Classify: snmpClassify,
		BreakerThreshold: 1, MaxPerHost: -1})

	for _, dead := range []string{"dead1", "dead2", "dead3"} {
		tried := 0
		if _, err := r.Walk(Target{Identity: dead}, func(vault.Credential) error { tried++; return errNoAnswer }); err == nil {
			t.Fatal("dead host succeeded")
		}
		if tried != 2 {
			t.Fatalf("%s: tried %d credentials, want both (no answer is retryable)", dead, tried)
		}
	}
	if len(r.Stats().ParkedCreds) != 0 {
		t.Fatalf("no-answers parked %v", r.Stats().ParkedCreds)
	}
	got, err := r.Walk(Target{Identity: "sw1"}, func(c vault.Credential) error { return nil })
	if err != nil || got.ID != "good" {
		t.Fatalf("after dead hosts the good community must still lead: %v %v", got.ID, err)
	}

	if _, err := r.Walk(Target{Identity: "sw2"}, func(c vault.Credential) error {
		if c.ID == "other" {
			return errRefused
		}
		return errNoAnswer
	}); err == nil {
		t.Fatal("expected failure")
	}
	if _, parked := r.Stats().ParkedCreds["other"]; !parked {
		t.Fatalf("a real rejection with breaker 1 should park: %v", r.Stats().ParkedCreds)
	}
}
