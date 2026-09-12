// internal/sshcore/algorithms.go
// Algorithm policy shared by BOTH the target and jump-host dial paths.
//
// Ported from the tetherssh backend, which learned this the hard way: when
// the two dial paths carried different HostKeyAlgorithms preference orders,
// the SAME server could offer an ed25519 key on one path and an ecdsa key on
// the other, and known_hosts read the difference as a host-key MISMATCH for
// a box whose keys never changed. Every dial in this package therefore goes
// through algorithmPolicy()/hostKeyAlgos() — never inline lists.
//
// Restructured from the baseline: the modern set is the default and the
// legacy tail (old KEX groups, CBC ciphers, sha1/md5 MACs, ssh-rsa/ssh-dss)
// is appended only when Config.LegacyAlgorithms is set.
package sshcore

import "golang.org/x/crypto/ssh"

var modernKex = []string{
	"curve25519-sha256",
	"curve25519-sha256@libssh.org",
	"ecdh-sha2-nistp256",
	"ecdh-sha2-nistp384",
	"ecdh-sha2-nistp521",
	"diffie-hellman-group14-sha256",
	"diffie-hellman-group16-sha512",
	"diffie-hellman-group18-sha512",
}

var legacyKex = []string{
	"diffie-hellman-group14-sha1",
	"diffie-hellman-group1-sha1",
	"diffie-hellman-group-exchange-sha256",
	"diffie-hellman-group-exchange-sha1",
}

var modernCiphers = []string{
	"chacha20-poly1305@openssh.com",
	"aes128-gcm@openssh.com",
	"aes256-gcm@openssh.com",
	"aes128-ctr",
	"aes192-ctr",
	"aes256-ctr",
}

var legacyCiphers = []string{
	"aes128-cbc",
	"aes192-cbc",
	"aes256-cbc",
	"3des-cbc",
}

var modernMACs = []string{
	"hmac-sha2-256-etm@openssh.com",
	"hmac-sha2-512-etm@openssh.com",
	"hmac-sha2-256",
	"hmac-sha2-512",
}

var legacyMACs = []string{
	"hmac-sha1",
	"hmac-sha1-96",
	"hmac-md5",
	"hmac-md5-96",
}

var modernHostKeys = []string{
	"ssh-ed25519",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
	"rsa-sha2-512",
	"rsa-sha2-256",
}

var legacyHostKeys = []string{
	"ssh-rsa",
	"ssh-dss",
}

func join(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	return append(out, b...)
}

// algorithmPolicy returns the KEX/cipher/MAC set for a connection.
func algorithmPolicy(legacy bool) ssh.Config {
	if !legacy {
		return ssh.Config{
			KeyExchanges: modernKex,
			Ciphers:      modernCiphers,
			MACs:         modernMACs,
		}
	}
	return ssh.Config{
		KeyExchanges: join(modernKex, legacyKex),
		Ciphers:      join(modernCiphers, legacyCiphers),
		MACs:         join(modernMACs, legacyMACs),
	}
}

// hostKeyAlgos returns the host-key preference order. Order matters: the
// first type the server also holds is the key that gets offered, and thus
// the key known_hosts records — so this MUST be identical on every dial
// path that can reach the same box.
func hostKeyAlgos(legacy bool) []string {
	if !legacy {
		return modernHostKeys
	}
	return join(modernHostKeys, legacyHostKeys)
}
