package notification

import "strings"

// snippetLimit bounds the message excerpt included in an email.
const snippetLimit = 280

// Snippet bounds a message body for an email excerpt without splitting a
// multi-byte rune.
func Snippet(s string) string {
	if len(s) <= snippetLimit {
		return s
	}
	runes := []rune(s)
	if len(runes) <= snippetLimit {
		return s
	}
	return string(runes[:snippetLimit]) + "..."
}

// PortalLink builds an absolute portal SPA link, or empty when no public
// base URL is configured.
func PortalLink(baseURL, route string) string {
	if baseURL == "" {
		return ""
	}
	return strings.TrimSuffix(baseURL, "/") + "/portal" + route
}

// RecipientsExcluding returns the de-duplicated candidate list minus the
// actor and empties, in NormalizeAddress form. Used to fan a thread event out
// to the target owner and thread author without self-notification.
//
// Both sides are normalized before comparison, so an owner or grantee stored
// as "Display Name <addr>" is still recognized as the actor, and the same
// person recorded in two address shapes yields one recipient rather than two.
func RecipientsExcluding(actor string, candidates ...string) []string {
	var out []string
	seen := map[string]bool{NormalizeAddress(actor): true, "": true}
	for _, c := range candidates {
		key := NormalizeAddress(c)
		if !seen[key] {
			seen[key] = true
			out = append(out, key)
		}
	}
	return out
}
