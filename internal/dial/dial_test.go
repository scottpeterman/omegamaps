// internal/dial/dial_test.go
//
// The credential ladder as the crawler and capture both drive it.
//
// This is the code that decides how many authentication attempts each device
// in a fabric sees, so the interesting assertions are about restraint: that a
// non-credential failure stops the walk instead of offering every remaining
// credential to a device that was never going to accept any of them, and that
// the caches key on the crawler's identity rather than on whatever string was
// dialed.
//
// These moved here with the dial primitives. They test the ladder, not the
// crawl, and leaving them behind in crawldial would have meant the package
// that owns the code has no tests while the package that merely calls it does.
//
// No server and no vault file here — credres.Store is an interface, so a slice
// of credentials stands in and the tests stay fast enough to run on every save.
package dial

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"golang.org/x/crypto/ssh"
	"path/filepath"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/credres"
	"github.com/scottpeterman/omegamaps/internal/sshcore"
	"github.com/scottpeterman/omegamaps/internal/vault"
)

type fakeStore []vault.Credential

func (f fakeStore) All() ([]vault.Credential, error) { return []vault.Credential(f), nil }

func cred(id, name, user, pass string) vault.Credential {
	return vault.Credential{ID: id, Name: name, Username: user, AuthType: "password", Password: pass}
}

// authRejected is the shape x/crypto produces, wrapped the way sshcore wraps
// it, so Classify sees what it would see in production.
func authRejected(host string) error {
	return errors.New("SSH handshake with " + host + ":22: ssh: handshake failed: " +
		"ssh: unable to authenticate, attempted methods [none password], no supported methods remain")
}

func TestVaultDialerWalksToTheWorkingCredential(t *testing.T) {
	// Priority is explicit so the ladder order is the test's, not the
	// resolver's tie-break rule. Lower runs first.
	store := fakeStore{
		{ID: "id-a", Name: "wrong", Username: "admin", AuthType: "password", Password: "nope", Priority: 1},
		{ID: "id-b", Name: "right", Username: "admin", AuthType: "password", Password: "correct", Priority: 2},
	}
	var tried []sshcore.Config
	d := &Vault{
		res: credres.New(store, nil, credres.Config{}),
		dial: func(cfg sshcore.Config) (*sshcore.Client, error) {
			tried = append(tried, cfg)
			if cfg.Password != "correct" {
				return nil, authRejected(cfg.Host)
			}
			return &sshcore.Client{}, nil
		},
	}

	client, err := d.Dial(context.Background(), Target{Target: "lab-r1.lab.example", Identity: "lab-r1"})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if client == nil {
		t.Fatal("nil client on success")
	}
	if len(tried) != 2 {
		t.Fatalf("%d attempt(s), want 2", len(tried))
	}
	if tried[len(tried)-1].Password != "correct" {
		t.Error("the successful attempt did not use the working credential")
	}
	for _, cfg := range tried {
		if cfg.Host != "lab-r1.lab.example" {
			t.Errorf("dialed %q, want the resolved target", cfg.Host)
		}
	}
}

// TestVaultDialerStopsOnNonCredentialFailure is the restraint case. A host-key
// problem will reproduce identically for every credential in the vault, so
// offering the rest is pure lockout exposure for no chance of success.
func TestVaultDialerStopsOnNonCredentialFailure(t *testing.T) {
	store := fakeStore{
		cred("id-a", "one", "admin", "a"),
		cred("id-b", "two", "admin", "b"),
		cred("id-c", "three", "admin", "c"),
	}
	attempts := 0
	d := &Vault{
		res: credres.New(store, nil, credres.Config{}),
		dial: func(cfg sshcore.Config) (*sshcore.Client, error) {
			attempts++
			return nil, errors.New("unknown host key for lab-r1.lab.example (ssh-ed25519 SHA256:abc); not in /home/u/.ssh/known_hosts")
		},
	}

	if _, err := d.Dial(context.Background(), Target{Target: "lab-r1.lab.example", Identity: "lab-r1"}); err == nil {
		t.Fatal("expected an error")
	}
	if attempts != 1 {
		t.Errorf("%d attempt(s) after a host-key failure, want 1", attempts)
	}
}

// TestVaultDialerBindsOnIdentityNotTarget is the wiring invariant. The crawler
// resolved the device and claimed it under Identity; the binding cache has to
// agree, or a device reached by address on one hop and by name on the next
// warms two entries and neither helps.
func TestVaultDialerBindsOnIdentityNotTarget(t *testing.T) {
	store := fakeStore{cred("id-a", "only", "admin", "ok")}
	bindings, err := credres.OpenFileBindings(filepath.Join(t.TempDir(), "bindings.json"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Vault{
		res: credres.New(store, bindings, credres.Config{}),
		dial: func(cfg sshcore.Config) (*sshcore.Client, error) {
			return &sshcore.Client{}, nil
		},
	}

	// Reached by its CGNAT address; the crawler resolved it to a name.
	target := Target{
		Target:   "lab-r1.lab.example",
		Reported: "100.64.0.9",
		Identity: "lab-r1",
	}
	if _, err := d.Dial(context.Background(), target); err != nil {
		t.Fatalf("dial: %v", err)
	}

	if _, ok := bindings.Lookup("lab-r1"); !ok {
		t.Error("no binding under the crawler's identity")
	}
	// The double-warm bug was one device warming two separate ENTRIES, so
	// neither helped the other. The alias set fixes that by making the other
	// shapes resolve to the same record — so the invariant is no longer
	// "nothing else resolves", it is "nothing else is a second record".
	if got := bindings.Len(); got != 1 {
		t.Errorf("one device produced %d binding records; that is the double-warm bug", got)
	}
	if b, ok := bindings.Lookup("lab-r1.lab.example"); ok {
		if b.CredID == "" {
			t.Error("the dialed name resolves to a record with no credential")
		}
	}
}

func TestVaultDialerTagFilter(t *testing.T) {
	store := fakeStore{
		{ID: "id-a", Name: "prod", Username: "admin", AuthType: "password", Password: "a", Tags: []string{"prod"}},
		{ID: "id-b", Name: "lab", Username: "admin", AuthType: "password", Password: "b", Tags: []string{"lab"}},
	}
	var tried []sshcore.Config
	d := &Vault{
		res:  credres.New(store, nil, credres.Config{}),
		tags: []string{"lab"},
		dial: func(cfg sshcore.Config) (*sshcore.Client, error) {
			tried = append(tried, cfg)
			return &sshcore.Client{}, nil
		},
	}
	if _, err := d.Dial(context.Background(), Target{Target: "lab-r1.lab.example", Identity: "lab-r1"}); err != nil {
		t.Fatalf("dial: %v", err)
	}
	if len(tried) != 1 || tried[0].Password != "b" {
		t.Errorf("tag filter did not restrict the ladder: %+v", tried)
	}
}

func TestVaultDialerNoEligibleCredentials(t *testing.T) {
	d := &Vault{
		res:  credres.New(fakeStore{cred("id-a", "one", "admin", "a")}, nil, credres.Config{}),
		tags: []string{"nothing-carries-this"},
		dial: func(cfg sshcore.Config) (*sshcore.Client, error) {
			t.Error("dialed despite no eligible credentials")
			return &sshcore.Client{}, nil
		},
	}
	if _, err := d.Dial(context.Background(), Target{Target: "lab-r1.lab.example", Identity: "lab-r1"}); err == nil {
		t.Fatal("expected an error when nothing is eligible")
	}
}

func TestApplyCredentialByAuthType(t *testing.T) {
	tests := []struct {
		name string
		cred vault.Credential
		want sshcore.Config
	}{
		{
			name: "password",
			cred: vault.Credential{Username: "admin", AuthType: "password", Password: "pw", KeyPath: "/ignored"},
			want: sshcore.Config{Username: "admin", Password: "pw"},
		},
		{
			name: "public key",
			cred: vault.Credential{Username: "admin", AuthType: "publickey", KeyPath: "/k", KeyPassphrase: "pp", Password: "ignored"},
			want: sshcore.Config{Username: "admin", PrivateKeyPath: "/k", KeyPassphrase: "pp"},
		},
		{
			name: "agent",
			cred: vault.Credential{Username: "admin", AuthType: "agent", Password: "ignored", KeyPath: "/ignored"},
			want: sshcore.Config{Username: "admin", UseAgent: true},
		},
		{
			name: "keyboard-interactive answers with the password",
			cred: vault.Credential{Username: "admin", AuthType: "keyboard-interactive", Password: "pw"},
			want: sshcore.Config{Username: "admin", Password: "pw"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got sshcore.Config
			applyCredential(&got, tc.cred)
			if got.Username != tc.want.Username ||
				got.Password != tc.want.Password ||
				got.PrivateKeyPath != tc.want.PrivateKeyPath ||
				got.KeyPassphrase != tc.want.KeyPassphrase ||
				got.UseAgent != tc.want.UseAgent {
				t.Errorf("applyCredential\n got: user=%q pass=%q key=%q pp=%q agent=%v\nwant: user=%q pass=%q key=%q pp=%q agent=%v",
					got.Username, got.Password, got.PrivateKeyPath, got.KeyPassphrase, got.UseAgent,
					tc.want.Username, tc.want.Password, tc.want.PrivateKeyPath, tc.want.KeyPassphrase, tc.want.UseAgent)
			}
		})
	}
}

// A key trusted on first contact is reported against the device, not against
// the string that was dialed. A device reached by address after its name
// failed to resolve used to be reported under the address, which the run
// model took for a second device that never finished.
func TestFirstContactHostKeyIsReportedAgainstTheIdentity(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	base := BaseConfig{OnNewHostKey: func(host, _, _ string) { got = append(got, host) }}

	cfg := base.ApplyFor("172.16.100.2", "usa-rtr-1.lab.local")
	if ok, err := cfg.HostKeyPrompt("172.16.100.2:22", nil, signer.PublicKey()); !ok || err != nil {
		t.Fatalf("prompt = %v, %v", ok, err)
	}
	cfg = base.Apply("172.16.1.2:2222")
	if ok, err := cfg.HostKeyPrompt("172.16.1.2:2222", nil, signer.PublicKey()); !ok || err != nil {
		t.Fatalf("prompt = %v, %v", ok, err)
	}
	want := []string{"usa-rtr-1.lab.local", "172.16.1.2"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("reported %q, want %q", got, want)
	}
}
