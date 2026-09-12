package snmpprobe

import (
	"fmt"

	"github.com/scottpeterman/omegamaps/internal/vault"
)

// FromVault converts a vault credential of an SNMP kind, validated. The
// secrets live in the SSH fields -- see vault.Credential for why -- and this
// is the one place that knows which is which.
func FromVault(c vault.Credential) (Credential, error) {
	var out Credential
	switch c.Method() {
	case vault.AuthSNMPv2c:
		out = Credential{Community: c.Password}
	case vault.AuthSNMPv3:
		out = Credential{
			User:         c.Username,
			AuthKey:      c.Password,
			AuthProtocol: c.SNMPAuthProtocol,
			PrivKey:      c.KeyPassphrase,
			PrivProtocol: c.SNMPPrivProtocol,
		}
	default:
		return Credential{}, fmt.Errorf("credential %q is %s, not SNMP", c.Name, c.Method())
	}
	if err := out.Validate(); err != nil {
		return Credential{}, fmt.Errorf("credential %q: %w", c.Name, err)
	}
	return out, nil
}
