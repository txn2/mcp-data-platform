package notification

import (
	"net/mail"
	"strings"
)

// NormalizeAddress reduces an email address to its comparison and storage
// form: the bare address, lowercased, with any display name stripped.
// "Display Name <User@Example.com>" and " user@example.com " both yield
// "user@example.com". A value that does not parse as an address falls back to
// trimmed-and-lowercased, so a malformed address still compares equal to
// itself.
//
// Every address that reaches the queue passes through here, so the
// self-notification check, recipient de-duplication, and the preference
// lookup all agree on which strings name the same person regardless of the
// shape each store happens to hold.
func NormalizeAddress(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if addr, err := mail.ParseAddress(s); err == nil {
		return strings.ToLower(strings.TrimSpace(addr.Address))
	}
	return strings.ToLower(s)
}
