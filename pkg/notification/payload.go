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
// actor (case-insensitive) and empties. Used to fan a thread event out to
// the target owner and thread author without self-notification.
func RecipientsExcluding(actor string, candidates ...string) []string {
	var out []string
	seen := map[string]bool{strings.ToLower(actor): true, "": true}
	for _, c := range candidates {
		key := strings.ToLower(c)
		if !seen[key] {
			seen[key] = true
			out = append(out, c)
		}
	}
	return out
}
