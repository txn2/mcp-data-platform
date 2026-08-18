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

// syntheticAPIKeyDomain is the domain pkg/auth mints an address in for an API
// key that configured none (auth.SyntheticEmailDomain). It is duplicated here
// rather than imported: pkg/notification is a lower-level domain than pkg/auth
// and must not depend on it for a string. A test asserts the two agree, so the
// copies cannot drift silently.
const syntheticAPIKeyDomain = "apikey.local"

// Deliverable reports whether an address is one the platform should ever try to
// send mail to (#1345).
//
// It is not a deliverability oracle and makes no claim about addresses in
// general: it recognizes the one domain the platform mints for itself. An API
// key with no configured email authenticates as name@apikey.local, which is an
// identity rather than a mailbox. That address becomes an asset's owner_email
// when an agent saves an asset under the key, and the owner is an unconditional
// recipient of feedback on their asset, so without this check every comment on
// an agent-produced asset queues a message no MX will ever accept -- five retry
// attempts, then a failed row that buries the genuine failures in the admin
// delivery history.
//
// An API key configured with a real email address is a real mailbox and is
// deliverable like anyone else.
func Deliverable(addr string) bool {
	addr = NormalizeAddress(addr)
	if addr == "" {
		return false
	}
	at := strings.LastIndex(addr, "@")
	return at < 0 || addr[at+1:] != syntheticAPIKeyDomain
}
