package user

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"
)

// MaxNameLen caps a first or last name.
const MaxNameLen = 100

// NormalizeEmail trims, lowercases, and validates an email address, returning
// the bare normalized address. Lowercasing matches the convention used by
// portal_shares.shared_with_email so directory lookups and share matching
// agree on identity.
func NormalizeEmail(email string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(email))
	if e == "" {
		return "", fmt.Errorf("email is required")
	}
	addr, err := mail.ParseAddress(e)
	if err != nil {
		return "", fmt.Errorf("invalid email address: %w", err)
	}
	// mail.ParseAddress also accepts "Display Name <addr>"; require a bare
	// address so a display name cannot smuggle a different identity in.
	if addr.Address != e {
		return "", fmt.Errorf("invalid email address: %q", email)
	}
	return e, nil
}

// ValidateName checks a first or last name length. An empty name is allowed:
// names are optional, since a person may be known only by email until they
// log in and their claims fill it in.
func ValidateName(name string) error {
	if utf8.RuneCountInString(name) > MaxNameLen {
		return fmt.Errorf("name exceeds %d characters", MaxNameLen)
	}
	return nil
}

// SplitFullName splits a display name into first and last components on the
// first whitespace run. A single token becomes the first name with an empty
// last name; empty input yields two empty strings.
func SplitFullName(full string) (first, last string) {
	fields := strings.Fields(full)
	switch len(fields) {
	case 0:
		return "", ""
	case 1:
		return fields[0], ""
	default:
		return fields[0], strings.Join(fields[1:], " ")
	}
}

// NameFromClaims derives a first and last name from OIDC claims. It prefers the
// standard given_name/family_name pair and falls back to splitting a full name
// (the provided fullName, or the "name" claim when fullName is empty). This is
// the single source of truth for name derivation across the token auth path and
// the browser-session login path.
func NameFromClaims(claims map[string]any, fullName string) (first, last string) {
	if claims != nil {
		given, _ := claims["given_name"].(string)
		family, _ := claims["family_name"].(string)
		if strings.TrimSpace(given) != "" || strings.TrimSpace(family) != "" {
			return strings.TrimSpace(given), strings.TrimSpace(family)
		}
		if fullName == "" {
			fullName, _ = claims["name"].(string)
		}
	}
	return SplitFullName(fullName)
}

// SanitizeName trims, strips control characters, and bounds the length of a
// name taken from an untrusted source (token claims on the auth path). The
// admin API rejects oversized/invalid names outright; the auth upsert instead
// sanitizes, since blocking is not an option there — a malformed claim must
// not let a hostile or misconfigured IdP inject control characters or a
// multi-kilobyte string into the shared directory.
func SanitizeName(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f { // ASCII control characters + DEL
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if utf8.RuneCountInString(name) > MaxNameLen {
		name = string([]rune(name)[:MaxNameLen])
	}
	return name
}
