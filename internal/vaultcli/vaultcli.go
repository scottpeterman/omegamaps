// internal/vaultcli/vaultcli.go
//
// Getting the master password from a human, from an OS keyring, or from an
// environment that has neither.
//
// This is deliberately a separate package from vault. The vault port's whole
// point was removing the terminal coupling — it takes a string and knows
// nothing about where the string came from. Every command needs the same
// answer to "where does the string come from", though, and two copies of that
// question would drift.
//
// It is also where the OS-keyring decision landed. Master() is the only
// function that knows the preference order; vault itself is unaffected,
// because the keyring supplies the same string a human would have typed and
// Argon2id still derives the key from it. See keyring.go.
package vaultcli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"github.com/scottpeterman/omegamaps/internal/vault"
)

// MasterEnvVar supplies the master password to a run with no terminal and no
// keyring. It is the weakest of the supported options and exists so a
// scheduled crawl on a headless box can run at all.
const MasterEnvVar = "OMEGAMAPS_VAULT_PASSWORD"

// MasterSource says where a master password came from. Callers use it to
// decide how much to trust a failed unlock: a human who mistypes wants
// another prompt, a stale keyring entry wants to be reported and worked
// around rather than silently retried.
type MasterSource int

const (
	SourcePrompt MasterSource = iota
	SourceKeyring
	SourceEnv
)

func (s MasterSource) String() string {
	switch s {
	case SourceKeyring:
		return "keyring"
	case SourceEnv:
		return "environment"
	default:
		return "prompt"
	}
}

// Master returns the master password for the vault at vaultPath, and where it
// came from.
//
// Preference order is strongest-available-first: OS keyring, then
// MasterEnvVar, then an interactive prompt. The keyring wins over the
// environment deliberately — an entry the operator filed on purpose should
// not be shadowed by a variable inherited from a parent process. To stop
// using a stored entry, clear it (`omvault keyring clear`) or set
// NoKeyringEnvVar for the run; both are explicit, which is the point.
//
// A keyring that is present but unreachable is not fatal. The fallback chain
// continues, because the common cause is a headless session with no D-Bus
// and that is precisely when MasterEnvVar earns its keep.
func Master(vaultPath string) (string, MasterSource, error) {
	if vaultPath != "" {
		switch m, err := KeyringGet(vaultPath); {
		case err == nil:
			return m, SourceKeyring, nil
		case errors.Is(err, ErrNoKeyringEntry):
			// Nothing filed, or deliberately disabled. Fall through.
		default:
			fmt.Fprintf(os.Stderr, "keyring unavailable (%v); falling back\n", err)
		}
	}
	if m, ok := os.LookupEnv(MasterEnvVar); ok && m != "" {
		return m, SourceEnv, nil
	}
	m, err := Prompt("vault master password")
	return m, SourcePrompt, err
}

// ErrNeedsPassword reports that no master password could be obtained without
// asking a human.
//
// It exists for callers that have nowhere to prompt. A CLI reaches Prompt and
// blocks on a terminal read, which is correct; a GUI doing the same blocks on
// a terminal nobody is watching, forever, with no error and no window. Such a
// caller uses MasterQuiet/OpenQuiet, gets this back, and puts up its own
// dialog.
var ErrNeedsPassword = errors.New("vault master password required")

// MasterQuiet is Master without the prompt: keyring, then MasterEnvVar, then
// ErrNeedsPassword. Same preference order and the same tolerance of an
// unreachable keyring.
func MasterQuiet(vaultPath string) (string, MasterSource, error) {
	if vaultPath != "" {
		switch m, err := KeyringGet(vaultPath); {
		case err == nil:
			return m, SourceKeyring, nil
		case errors.Is(err, ErrNoKeyringEntry):
			// Nothing filed, or deliberately disabled. Fall through.
		default:
			fmt.Fprintf(os.Stderr, "keyring unavailable (%v); falling back\n", err)
		}
	}
	if m, ok := os.LookupEnv(MasterEnvVar); ok && m != "" {
		return m, SourceEnv, nil
	}
	return "", SourcePrompt, ErrNeedsPassword
}

// OpenWith unlocks the vault at path with a master password the caller already
// has. It never prompts and never consults the keyring.
//
// A wrong password comes back wrapping vault.ErrWrongPassword, so a GUI can
// tell "you typed it wrong" from "there is no vault there" and say so.
func OpenWith(path, master string) (*vault.Vault, error) {
	v := vault.New(path)
	if !v.Exists() {
		return nil, fmt.Errorf("no vault at %s", path)
	}
	if err := v.Unlock(master); err != nil {
		return nil, fmt.Errorf("unlock %s: %w", path, err)
	}
	return v, nil
}

// OpenQuiet unlocks from the keyring or the environment only, returning
// ErrNeedsPassword when neither has it.
//
// This is the startup path for a GUI: try to unlock without bothering anyone,
// and leave the vault locked if that is not possible rather than blocking.
func OpenQuiet(path string) (*vault.Vault, error) {
	master, src, err := MasterQuiet(path)
	if err != nil {
		return nil, err
	}
	v, err := OpenWith(path, master)
	if err != nil && src == SourceKeyring && errors.Is(err, vault.ErrWrongPassword) {
		// A stale keyring entry must not read as "wrong password" to a
		// caller that never supplied one. Report it as needing a
		// password, and name the reason.
		return nil, fmt.Errorf("%w: the stored keyring entry no longer unlocks %s "+
			"(re-file it with `omvault -vault %s keyring set`)", ErrNeedsPassword, path, path)
	}
	return v, err
}

// MasterNew reads a master password for a vault being created, twice, and
// checks that the two agree. The environment variable short-circuits both
// prompts, since a scripted create has nothing to typo against. The keyring
// is deliberately NOT consulted here: an entry left over from a deleted vault
// must never become the master password of a new one.
func MasterNew() (string, error) {
	if m, ok := os.LookupEnv(MasterEnvVar); ok && m != "" {
		return m, nil
	}
	first, err := Prompt("new vault master password")
	if err != nil {
		return "", err
	}
	second, err := Prompt("confirm master password")
	if err != nil {
		return "", err
	}
	if first != second {
		return "", errors.New("passwords do not match")
	}
	return first, nil
}

// Prompt reads one secret. On a terminal it prompts without echoing. When
// stdin is a pipe it reads a single line, which is what makes these commands
// scriptable without putting a secret on argv — argv is visible in the process
// table and in shell history, a pipe is not.
func Prompt(label string) (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		out, done := promptTo()
		defer done()
		fmt.Fprintf(out, "%s: ", label)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(out)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", label, err)
		}
		return string(b), nil
	}
	return readLine(label)
}

// stdin is buffered across calls so a command reading two secrets from one
// pipe gets two lines rather than losing the second to the first read's
// buffering.
var stdin = bufio.NewReader(os.Stdin)

// promptTo is where an interactive prompt is written: the controlling
// terminal, as ssh and sudo do, so the prompt is seen however stderr is
// redirected. On a long crawl stderr goes to a log file or is discarded, and
// a prompt written there leaves the program sitting silently, waiting for a
// password nobody was asked for. Stderr where there is no /dev/tty (Windows).
func promptTo() (io.Writer, func()) {
	if f, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		return f, func() { f.Close() }
	}
	return os.Stderr, func() {}
}

func readLine(label string) (string, error) {
	line, err := stdin.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil && line == "" {
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("no %s on stdin and no terminal to prompt on; "+
				"pipe it in, set %s, or store one with `omvault keyring set`",
				label, MasterEnvVar)
		}
		return "", fmt.Errorf("read %s: %w", label, err)
	}
	return line, nil
}

// Open unlocks an existing vault at path.
//
// A stale keyring entry must not lock an operator out of their own vault, so
// a wrong-password failure on a keyring-sourced master is reported and
// retried interactively rather than returned. Every other failure is
// returned as-is: a mistyped prompt is the human's to repeat, and an
// environment variable that is wrong is a scripting bug that should surface.
func Open(path string) (*vault.Vault, error) {
	v := vault.New(path)
	if !v.Exists() {
		return nil, fmt.Errorf("no vault at %s", path)
	}
	master, src, err := Master(path)
	if err != nil {
		return nil, err
	}
	err = v.Unlock(master)
	if err == nil {
		return v, nil
	}
	if src == SourceKeyring && errors.Is(err, vault.ErrWrongPassword) {
		fmt.Fprintf(os.Stderr,
			"the stored keyring entry did not unlock %s; "+
				"re-file it with `omvault -vault %s keyring set`\n", path, path)
		master, err = Prompt("vault master password")
		if err != nil {
			return nil, err
		}
		if err := v.Unlock(master); err != nil {
			return nil, fmt.Errorf("unlock %s: %w", path, err)
		}
		return v, nil
	}
	return nil, fmt.Errorf("unlock %s: %w", path, err)
}

// appHomeDir is omegamaps' configuration directory under the user's home.
//
// A clean break from Pathfinder's ~/.pathfinderssh, with no fallback to it.
// The file format is shared, so -vault can still open a Pathfinder or Omega
// vault on purpose; but a default that quietly adopted Pathfinder's file
// would put SNMP credentials where a Pathfinder build that does not know the
// SNMP kinds reads them as SSH passwords.
const appHomeDir = ".omegamaps"

// DefaultPath is where both commands look when no path is given.
func DefaultPath() string {
	dir := AppDir()
	if dir == "" {
		return "vault.json"
	}
	return filepath.Join(dir, "vault.json")
}

// AppDir is ~/.omegamaps, or "" when there is no home directory to put it in.
// Everything the application keeps between runs lives under it.
func AppDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, appHomeDir)
}

// BindingsPath puts the binding store beside the vault, since the two are only
// meaningful together: the bindings are credential IDs from that vault and
// nothing else.
func BindingsPath(vaultPath string) string {
	if vaultPath == "" {
		return ""
	}
	return strings.TrimSuffix(vaultPath, ".json") + ".bindings.json"
}
