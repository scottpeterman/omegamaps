// internal/sshcore/config.go
// Connection configuration for the headless SSH core.
//
// Extracted from the tetherssh SSH backend (cli/ssh_backend.go) with all
// GUI/terminal coupling removed. Differences from the baseline are
// deliberate:
//   - Host-key handling is a first-class policy enum (strict / TOFU /
//     insecure opt-in) instead of a pair of loosely related fields.
//   - Legacy KEX/cipher/MAC support is opt-in per connection, not the
//     global default.
package sshcore

import (
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// HostKeyPolicy selects how server host keys are verified.
type HostKeyPolicy int

const (
	// HostKeyStrict verifies against known_hosts only. Unknown hosts fail.
	HostKeyStrict HostKeyPolicy = iota
	// HostKeyTOFU verifies against known_hosts; on first contact with an
	// unknown host the HostKeyPrompt callback decides, and an accepted key
	// is persisted. A key MISMATCH against a pinned host always fails
	// closed regardless of the callback (possible MITM).
	HostKeyTOFU
	// HostKeyInsecure skips verification entirely. Explicit opt-in for
	// disposable lab gear only.
	HostKeyInsecure
)

// HostKeyPromptFunc is consulted on first contact with an unknown host under
// HostKeyTOFU. Return true to accept-and-persist the key. It is never called
// for a mismatch against an already-pinned host.
type HostKeyPromptFunc func(hostname string, remote net.Addr, key ssh.PublicKey) (bool, error)

// AuthPromptFunc supplies answers for keyboard-interactive prompts and
// encrypted-key passphrases. echo=false means the input is a secret.
type AuthPromptFunc func(prompt string, echo bool) (string, error)

// JumpConfig describes an optional bastion the target is tunneled through.
type JumpConfig struct {
	Host           string
	Port           int // 0 => 22
	Username       string
	Password       string
	PrivateKeyPath string
	KeyPassphrase  string
}

// Config holds everything needed to dial one target.
type Config struct {
	Host    string
	Port    int           // 0 => 22
	Timeout time.Duration // 0 => 30s

	Username       string
	Password       string
	PrivateKeyPath string // path to key file; "~/" expands
	PrivateKey     []byte // in-memory key; takes precedence over path
	KeyPassphrase  string
	UseAgent       bool // try SSH agent first (SSH_AUTH_SOCK)

	Jump *JumpConfig // nil => direct connection

	HostKeys       HostKeyPolicy
	KnownHostsPath string // "" => ~/.ssh/known_hosts
	HostKeyPrompt  HostKeyPromptFunc
	AuthPrompt     AuthPromptFunc

	// LegacyAlgorithms appends the legacy KEX/cipher/MAC/host-key tail
	// (group1-sha1, CBC modes, hmac-sha1/md5, ssh-rsa/ssh-dss) after the
	// modern set. Required for old routers/switches; off by default.
	LegacyAlgorithms bool
}

func (c *Config) withDefaults() Config {
	out := *c
	if out.Port == 0 {
		out.Port = 22
	}
	if out.Timeout == 0 {
		out.Timeout = 30 * time.Second
	}
	if out.KnownHostsPath == "" {
		out.KnownHostsPath = defaultKnownHostsPath()
	}
	return out
}
