//go:build windows

package secfile

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// restrict replaces the file's DACL with a single ACE granting the calling
// user full control, and marks the DACL protected so nothing is inherited from
// the parent directory.
//
// PROTECTED_DACL_SECURITY_INFORMATION is the part that matters and the part
// that is easy to leave out. Without it the explicit ACE is added ahead of the
// inherited ones and the inherited ones still apply, so a file in a directory
// that grants Users:(RX) stays readable by Users -- the DACL looks right in
// Properties because the explicit entry is listed first.
//
// Owner and group are left alone (nil, with no OWNER_ flag). Changing owner
// needs privileges that a normal process does not hold, and the owner of a
// file is already whoever created it. An owner can always re-open for WRITE_DAC
// regardless of the DACL, so this is not a lockout risk either.
func restrict(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	ea := []windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.NO_INHERITANCE,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_USER,
			TrusteeValue: windows.TrusteeValueFromSID(sid),
		},
	}}

	acl, err := windows.ACLFromEntries(ea, nil)
	if err != nil {
		return fmt.Errorf("secfile: build DACL for %s: %w", path, err)
	}

	err = windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil, nil, acl, nil,
	)
	if err != nil {
		return fmt.Errorf("secfile: set DACL on %s: %w", path, err)
	}
	return nil
}

// verify reads the DACL back and checks that it is protected and that nothing
// other than the calling user is granted anything.
func verify(path string) error {
	sid, err := currentUserSID()
	if err != nil {
		return err
	}

	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("secfile: read DACL of %s: %w", path, err)
	}
	// String() swallows its error and returns "", which would otherwise reach
	// the parser as "no DACL" and blame the file for a conversion failure.
	sddl := sd.String()
	if sddl == "" {
		return fmt.Errorf("secfile: %s: the security descriptor did not convert to SDDL", path)
	}

	flags, aces, err := daclFromSDDL(sddl)
	if err != nil {
		return fmt.Errorf("secfile: %s: %w (SDDL %q)", path, err, sddl)
	}
	if !strings.ContainsRune(flags, 'P') {
		return fmt.Errorf("secfile: %s has an unprotected DACL (flags %q); "+
			"it still inherits access from its directory (SDDL %q)", path, flags, sddl)
	}
	if len(aces) == 0 {
		// An empty DACL denies everyone, which is not what was asked for but
		// is not a leak either. Report it rather than passing silently.
		return fmt.Errorf("secfile: %s has an empty DACL; nothing can open it (SDDL %q)", path, sddl)
	}

	for _, ace := range aces {
		f := strings.Split(ace, ";")
		if len(f) < 6 {
			return fmt.Errorf("secfile: %s: unparsable ACE %q (SDDL %q)", path, ace, sddl)
		}
		typ, trustee := f[0], f[5]
		if isDenyACE(typ) {
			continue
		}
		other, err := windows.StringToSid(trustee)
		if err != nil {
			return fmt.Errorf("secfile: %s: ACE trustee %q: %w (SDDL %q)", path, trustee, err, sddl)
		}
		if !other.Equals(sid) {
			return fmt.Errorf("secfile: %s grants access to %s, not only to %s (SDDL %q)",
				path, other.String(), sid.String(), sddl)
		}
	}
	return nil
}

func currentUserSID() (*windows.SID, error) {
	tok, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return nil, fmt.Errorf("secfile: open process token: %w", err)
	}
	defer tok.Close()

	u, err := tok.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("secfile: token user: %w", err)
	}
	// u.User.Sid points into a buffer GetTokenUser allocated and that Go will
	// collect; Copy lifts it out.
	sid, err := u.User.Sid.Copy()
	if err != nil {
		return nil, fmt.Errorf("secfile: copy user SID: %w", err)
	}
	return sid, nil
}

// isDenyACE reports whether an SDDL ace_type string is a denial. Denials for
// other trustees are not a leak, so verify skips them.
func isDenyACE(t string) bool {
	switch t {
	case "D", "OD", "XD":
		return true
	}
	return false
}

// daclFromSDDL pulls the flag letters and the top-level ACE bodies out of the
// D: section of an SDDL string.
//
// A regex is the wrong tool: conditional ACEs (XA, XD) carry a parenthesised
// expression inside the ACE, so parentheses nest, and nesting is what a depth
// counter is for. The strings this reads are ones Windows produced, not user
// input, but an SDDL that does not parse is reported rather than guessed at.
func daclFromSDDL(sddl string) (flags string, aces []string, err error) {
	i := indexTopLevel(sddl, "D:")
	if i < 0 {
		return "", nil, fmt.Errorf("no DACL in the security descriptor")
	}
	s := sddl[i+2:]

	// "D:NO_ACCESS_CONTROL" is a NULL DACL: everyone gets everything.
	if strings.HasPrefix(s, "NO_ACCESS_CONTROL") {
		return "", nil, fmt.Errorf("the DACL is NULL, which grants everyone full access")
	}

	// Flag letters run until the first ACE, or until the SACL, or the end.
	n := 0
	for n < len(s) && s[n] != '(' {
		if s[n] == 'S' && n+1 < len(s) && s[n+1] == ':' {
			break
		}
		n++
	}
	flags, s = s[:n], s[n:]

	depth := 0
	start := 0
	for j := 0; j < len(s); j++ {
		switch s[j] {
		case '(':
			if depth == 0 {
				start = j + 1
			}
			depth++
		case ')':
			depth--
			if depth == 0 {
				aces = append(aces, s[start:j])
			}
			if depth < 0 {
				return "", nil, fmt.Errorf("unbalanced ')' in the DACL")
			}
		default:
			if depth == 0 {
				// Anything at depth 0 between ACEs ends the DACL -- in
				// practice the "S:" that starts the SACL.
				return flags, aces, nil
			}
		}
	}
	if depth != 0 {
		return "", nil, fmt.Errorf("unterminated ACE in the DACL")
	}
	return flags, aces, nil
}

// indexTopLevel finds sub outside of any parentheses, so a SID or a
// conditional expression inside an ACE cannot be mistaken for a section marker.
func indexTopLevel(s, sub string) int {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
		default:
			if depth == 0 && strings.HasPrefix(s[i:], sub) {
				return i
			}
		}
	}
	return -1
}
