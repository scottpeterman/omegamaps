// internal/credres/classify.go
//
// Dial outcome classification.
//
// The resolver only reacts to authentication outcomes. A handshake that failed
// on algorithm negotiation, a host-key mismatch, or an unreachable target says
// nothing about whether the credential was correct, and must not burn a
// negative-cache slot, trip the circuit breaker, or drop a pin. Getting this
// wrong is how a legacy-crypto device convinces the resolver that every
// credential in the vault is bad.
//
// Classification is by string matching on the error, because golang.org/x/crypto/ssh
// does not export typed errors for most of these. Callers that already know
// the outcome (a dial layer that distinguishes them structurally) should pass
// it directly to Report and skip Classify entirely.
//
// Two vocabularies arrive here, and both have to be covered. x/crypto/ssh
// produces the handshake and authentication text; sshcore wraps that but also
// emits its own for failures it detects locally — an unreadable key file, a
// rejected host key, a jump host with nothing to authenticate with. The second
// set was missing at first, so those errors fell through to OutcomeOther and
// quietly stopped the credential ladder without saying why.
// sshcore_errors_test.go pins both.
package credres

import (
	"errors"
	"io"
	"net"
	"os"
	"strings"
)

// Outcome is the result of one dial attempt with one credential.
type Outcome int

const (
	// OutcomeSuccess: authenticated. Pins and promotes.
	OutcomeSuccess Outcome = iota

	// OutcomeAuthRejected: the server refused these credentials. This is the
	// only failure that counts against a credential.
	OutcomeAuthRejected

	// OutcomeAlgoMismatch: no common key exchange, cipher, MAC, or host-key
	// algorithm. Retry axis is the algorithm set, not the credential.
	OutcomeAlgoMismatch

	// OutcomeHostKey: host key unknown under a strict policy, or mismatched.
	// Always fails closed; never a credential problem.
	OutcomeHostKey

	// OutcomeUnreachable: refused, timed out, or DNS failure.
	OutcomeUnreachable

	// OutcomeKeyMaterial: the private key could not be loaded or decrypted
	// locally. The credential is misconfigured rather than rejected, so it is
	// worth disabling for the run but is not a lockout signal.
	OutcomeKeyMaterial

	// OutcomeOther: unclassified. Treated as inconclusive.
	OutcomeOther

	// OutcomeNoAnswer: nothing came back. SNMPv2c's answer to a wrong
	// community, and also a host that is down -- the two cannot be told
	// apart, so it is worth trying the next credential but never counts
	// against this one. Counting it would let a handful of dead hosts park
	// the community that works everywhere else.
	OutcomeNoAnswer
)

func (o Outcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "success"
	case OutcomeAuthRejected:
		return "auth-rejected"
	case OutcomeAlgoMismatch:
		return "algo-mismatch"
	case OutcomeHostKey:
		return "host-key"
	case OutcomeUnreachable:
		return "unreachable"
	case OutcomeKeyMaterial:
		return "key-material"
	case OutcomeNoAnswer:
		return "no-answer"
	default:
		return "other"
	}
}

// CountsAgainstCredential reports whether the outcome is evidence that the
// credential itself is wrong. Only auth rejection qualifies.
func (o Outcome) CountsAgainstCredential() bool { return o == OutcomeAuthRejected }

// Retryable reports whether trying the next credential could plausibly help.
// A host-key or unreachable failure will fail identically for every credential,
// so the ladder should stop rather than walk itself for nothing.
func (o Outcome) Retryable() bool {
	switch o {
	case OutcomeAuthRejected, OutcomeKeyMaterial, OutcomeNoAnswer:
		return true
	default:
		return false
	}
}

// Classify maps a dial error onto an Outcome.
func Classify(err error) Outcome {
	if err == nil {
		return OutcomeSuccess
	}

	// Structural checks first; they are reliable where they apply.
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return OutcomeUnreachable
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return OutcomeUnreachable
	}
	if errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, io.EOF) {
		return OutcomeUnreachable
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, os.ErrPermission) {
		return OutcomeKeyMaterial
	}

	s := strings.ToLower(err.Error())

	switch {
	case containsAny(s,
		"no common algorithm",
		"no common algorithms",
		"unable to negotiate",
		"no mutual signature algorithm",
		"ssh: handshake failed: ssh: no common algorithm"):
		return OutcomeAlgoMismatch

	case containsAny(s,
		"host key mismatch",
		"knownhosts: key mismatch",
		"knownhosts: key is unknown",
		"host key verification failed",
		"unknown host key",
		"remote host identification has changed",
		"jump host key:",
		"known_hosts") ||
		// sshcore's TOFU-declined path: "host key for <host> rejected".
		containsAll(s, "host key for", "rejected"):
		return OutcomeHostKey

	case containsAny(s,
		"unable to authenticate",
		"permission denied",
		"no supported methods remain",
		"auth failed",
		"authentication failed",
		// sshcore's own message when the server asks a
		// keyboard-interactive question this credential cannot answer:
		// "no handler for keyboard-interactive question: \"Password: \"".
		//
		// It reads like a client defect and is not one. The server
		// refused what was offered and asked for something else, which
		// is exactly what a rejection is — and it is specific to THIS
		// credential, because one carrying a password would have
		// answered. Left unclassified it fell to OutcomeOther, which is
		// not Retryable, so Walk BROKE OUT of the ladder after the
		// first candidate and the password credential two rows down was
		// never tried. On an estate where the modern gear takes a key
		// and the older gear only offers a password prompt, that is a
		// column of authentication failures on exactly the devices a
		// vault was set up to reach.
		"no handler for keyboard-interactive"):
		return OutcomeAuthRejected

	case containsAny(s,
		"cannot decode encrypted private key",
		"passphrase",
		"failed to parse private key",
		"ssh: no key found",
		"invalid private key",
		// sshcore's own wrapping of local credential-material failures.
		"parse private key",
		"read private key",
		"ssh agent:",
		"no authentication methods available",
		"no usable credentials"):
		return OutcomeKeyMaterial

	case containsAny(s,
		"connection refused",
		"i/o timeout",
		"no route to host",
		"network is unreachable",
		"connection reset",
		"no such host",
		"context deadline exceeded"):
		return OutcomeUnreachable
	}

	return OutcomeOther
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// containsAll is for markers that are only unambiguous together — "rejected"
// alone is far too broad, "host key for" alone catches the unknown-key case
// that is already handled above.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
