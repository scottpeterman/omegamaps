package vault

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestSNMPAuthTypes(t *testing.T) {
	for in, want := range map[string]AuthMethod{
		"snmp-v2c": AuthSNMPv2c, "SNMPv2c": AuthSNMPv2c, "snmp-v3": AuthSNMPv3, "snmp_v3": AuthSNMPv3,
		"password": AuthPassword, "publickey": AuthPublicKey,
	} {
		if got := StringToAuthType(in); got != want {
			t.Errorf("StringToAuthType(%q) = %v, want %v", in, got, want)
		}
	}
	if !(Credential{AuthType: AuthTypeSNMPv2c}).IsSNMP() || (Credential{AuthType: "password"}).IsSNMP() {
		t.Error("IsSNMP disagrees with the auth type")
	}
	if AuthSNMPv3.String() != "SNMP v3" {
		t.Errorf("label = %q", AuthSNMPv3.String())
	}
	c := Credential{AuthType: AuthTypeSNMPv3, Username: "monitor", Password: "authkey", KeyPassphrase: "privkey"}
	if r := c.Redact(); r.Password != "" || r.KeyPassphrase != "" {
		t.Errorf("Redact left SNMP secrets: %+v", r)
	}
}

// legacyCredential is the record as a build without SNMP support knows it --
// Pathfinder or Omega as they stand. Either one rewrites the whole vault from
// this shape when it saves.
type legacyCredential struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Username      string   `json:"username"`
	AuthType      string   `json:"auth_type"`
	Password      string   `json:"password,omitempty"`
	KeyPath       string   `json:"key_path,omitempty"`
	KeyPassphrase string   `json:"key_passphrase,omitempty"`
	Priority      int      `json:"priority,omitempty"`
	Tags          []string `json:"tags,omitempty"`
}

// The reason the secrets live in the SSH fields: a round trip through an
// older build keeps every one of them and the kind, losing only the two
// protocol names, which fall back to SHA and AES.
func TestSNMPCredentialSurvivesAnOlderBuildsSave(t *testing.T) {
	in := Credential{ID: "c1", Name: "lab-v3", AuthType: AuthTypeSNMPv3, Username: "monitor",
		Password: "authkey", KeyPassphrase: "privkey", SNMPAuthProtocol: "SHA256",
		SNMPPrivProtocol: "AES256", Tags: []string{"lab"}}
	b, _ := json.Marshal(in)
	var old legacyCredential
	if err := json.Unmarshal(b, &old); err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(old)
	var back Credential
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !back.IsSNMP() || back.Username != "monitor" || back.Password != "authkey" || back.KeyPassphrase != "privkey" {
		t.Fatalf("secrets or kind lost: %+v", back)
	}
	if back.SNMPAuthProtocol != "" || back.SNMPPrivProtocol != "" {
		t.Fatalf("expected the protocol names to be the only loss, got %+v", back)
	}
}

func TestSNMPCredentialCannotBeTheDefault(t *testing.T) {
	v := New(filepath.Join(t.TempDir(), "vault.json"))
	if err := v.Create("lab-master-pw"); err != nil {
		t.Fatal(err)
	}
	ssh, err := v.Add(Credential{Name: "lab-pw", Username: "admin", AuthType: "password", Password: "x", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Add(Credential{Name: "ro", AuthType: AuthTypeSNMPv2c, Password: "public", IsDefault: true}); !errors.Is(err, ErrSNMPDefault) {
		t.Fatalf("Add as default: %v", err)
	}
	snmp, err := v.Add(Credential{Name: "ro", AuthType: AuthTypeSNMPv2c, Password: "public"})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.SetDefault(snmp.ID); !errors.Is(err, ErrSNMPDefault) {
		t.Fatalf("SetDefault: %v", err)
	}
	if d, ok := v.Default(); !ok || d.ID != ssh.ID {
		t.Fatalf("a refused SetDefault disturbed the existing default: %+v %v", d, ok)
	}
	snmp.IsDefault = true
	if err := v.Update(snmp); !errors.Is(err, ErrSNMPDefault) {
		t.Fatalf("Update as default: %v", err)
	}
}
