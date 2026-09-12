// internal/credres/sshcore_errors_test.go
//
// Classify has to read sshcore's errors, not just x/crypto's.
//
// The resolver only reacts to authentication outcomes, and it decides whether
// to keep walking the credential ladder from what Classify returns. An error
// it does not recognize becomes OutcomeOther, which is non-retryable and
// counts against nothing — so the ladder stops on the first device with an
// unreadable key file and never says why. That is a quiet wrong answer rather
// than a loud one, which is what this file is for.
//
// Two kinds of case below. The first group builds real errors by calling
// sshcore.Dial with configurations that fail before any network I/O, so the
// strings cannot drift out from under the classifier unnoticed. The second is
// a literal table for errors that need a live server or a declined host-key
// prompt to produce; those are annotated with the source line they come from
// and have to be re-checked by hand if sshcore's wording changes.
package credres

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/scottpeterman/omegamaps/internal/sshcore"
)

// TestClassifyRealSSHCoreErrors runs sshcore.Dial for real. Every case here
// fails while building auth methods or the host-key callback, both of which
// happen before the TCP connect, so nothing touches the network.
func TestClassifyRealSSHCoreErrors(t *testing.T) {
	dir := t.TempDir()

	garbageKey := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbageKey, []byte("not a key at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	knownHosts := filepath.Join(dir, "known_hosts")
	if err := os.WriteFile(knownHosts, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		cfg  sshcore.Config
		want Outcome
		why  string
	}{
		{
			name: "key file does not exist",
			cfg: sshcore.Config{
				Host: "192.0.2.1", Username: "u",
				PrivateKeyPath: filepath.Join(dir, "absent.pem"),
				KnownHostsPath: knownHosts,
			},
			want: OutcomeKeyMaterial,
			why:  "a missing key file is a local misconfiguration, not a rejection",
		},
		{
			name: "key file is not a key",
			cfg: sshcore.Config{
				Host: "192.0.2.1", Username: "u",
				PrivateKeyPath: garbageKey,
				KnownHostsPath: knownHosts,
			},
			want: OutcomeKeyMaterial,
			why:  "an unparseable key must not burn a negative-cache slot",
		},
		{
			name: "known_hosts is unreadable",
			cfg: sshcore.Config{
				Host: "192.0.2.1", Username: "u", Password: "x",
				KnownHostsPath: dir, // a directory, not a file
			},
			want: OutcomeHostKey,
			why:  "host-key trouble fails closed and is never a credential problem",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sshcore.Dial(tc.cfg)
			if err == nil {
				t.Fatal("expected a dial error, got nil")
			}
			got := Classify(err)
			if got != tc.want {
				t.Errorf("Classify(%v) = %s, want %s\n%s", err, got, tc.want, tc.why)
			}
			if got == OutcomeOther {
				t.Errorf("error fell through to %s; the ladder would stop "+
					"without explanation: %v", got, err)
			}
		})
	}
}

// TestClassifySSHCoreErrorText covers the strings that need a live server or a
// declined prompt to produce. Each is quoted from the source line named beside
// it; if sshcore's wording changes, these have to change with it, which is the
// cost of not being able to generate them here.
func TestClassifySSHCoreErrorText(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want Outcome
	}{
		// --- sshcore/hostkey.go ---
		{
			name: "pinned key mismatch (hostkey.go, key differs)",
			err: "host key verification failed for lab-r1.lab.example: offered key " +
				"(ssh-ed25519 SHA256:abc) does not match the pinned key in " +
				"/home/u/.ssh/known_hosts; if this change is expected, remove the " +
				"old entry and reconnect",
			want: OutcomeHostKey,
		},
		{
			name: "unknown key under a strict policy (hostkey.go, first contact)",
			err:  "unknown host key for lab-r1.lab.example (ssh-ed25519 SHA256:abc); not in /home/u/.ssh/known_hosts",
			want: OutcomeHostKey,
		},
		{
			name: "TOFU prompt declined (hostkey.go, accept=false)",
			err:  "host key for lab-r1.lab.example rejected",
			want: OutcomeHostKey,
		},

		// --- sshcore/dial.go, jump path ---
		{
			name: "jump host key failure (dial.go, jump wrapping)",
			err:  "jump host key: unknown host key for lab-jump1.lab.example (ssh-ed25519 SHA256:abc); not in /home/u/.ssh/known_hosts",
			want: OutcomeHostKey,
		},
		{
			name: "jump host has nothing to authenticate with (dial.go)",
			err:  "jump host lab-jump1.lab.example:22: no usable credentials (set a key or password)",
			want: OutcomeKeyMaterial,
		},
		{
			name: "auth failure through a jump host keeps its inner meaning (dial.go wrapping)",
			err: "reach lab-r1.lab.example:22 through jump host: ssh: handshake failed: " +
				"ssh: unable to authenticate, attempted methods [none password], no supported methods remain",
			want: OutcomeAuthRejected,
		},

		// --- sshcore/auth.go ---
		{
			name: "passphrase prompt failed (auth.go)",
			err:  "key passphrase: operation cancelled",
			want: OutcomeKeyMaterial,
		},
		{
			name: "wrong passphrase (auth.go)",
			err:  "parse private key with passphrase: x509: decryption password incorrect",
			want: OutcomeKeyMaterial,
		},
		{
			name: "agent unreachable (auth.go)",
			err:  "ssh agent: dial unix /run/user/1000/keyring/ssh: connect: no such file or directory",
			want: OutcomeKeyMaterial,
		},

		// --- x/crypto, wrapped by sshcore/dial.go ---
		{
			name: "credential rejected",
			err: "SSH handshake with lab-r1.lab.example:22: ssh: handshake failed: " +
				"ssh: unable to authenticate, attempted methods [none password], no supported methods remain",
			want: OutcomeAuthRejected,
		},
		{
			name: "legacy device, no common KEX",
			err: "SSH handshake with lab-old1.lab.example:22: ssh: handshake failed: " +
				"ssh: no common algorithm for key exchange; client offered: [curve25519-sha256], " +
				"server offered: [diffie-hellman-group1-sha1]",
			want: OutcomeAlgoMismatch,
		},
		{
			name: "device is down",
			err:  "connect to lab-r1.lab.example:22: dial tcp 192.0.2.10:22: connect: connection refused",
			want: OutcomeUnreachable,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(stringError(tc.err)); got != tc.want {
				t.Errorf("Classify = %s, want %s\nerror: %s", got, tc.want, tc.err)
			}
		})
	}
}

// TestClassifyDoesNotOverMatch guards the broad markers. "rejected" and
// "known_hosts" are common enough words that matching them alone would
// misfile ordinary auth failures as host-key problems, which would stop the
// ladder on a device where the next credential would have worked.
func TestClassifyDoesNotOverMatch(t *testing.T) {
	tests := []struct {
		err  string
		want Outcome
	}{
		{
			err: "SSH handshake with lab-r1.lab.example:22: ssh: handshake failed: " +
				"ssh: unable to authenticate, attempted methods [none publickey], no supported methods remain",
			want: OutcomeAuthRejected,
		},
		{err: "connect to lab-r1.lab.example:22: dial tcp 192.0.2.10:22: i/o timeout", want: OutcomeUnreachable},
	}
	for _, tc := range tests {
		if got := Classify(stringError(tc.err)); got != tc.want {
			t.Errorf("Classify = %s, want %s\nerror: %s", got, tc.want, tc.err)
		}
	}
}

// Note on a marker that cannot currently fire: sshcore's
// "no authentication methods available" is unreachable, because
// buildAuthMethods always appends keyboard-interactive before the length
// check. Classify recognizes it anyway — it is cheap, and the alternative is
// that restoring the guard silently reintroduces an OutcomeOther.

type stringError string

func (e stringError) Error() string { return string(e) }
