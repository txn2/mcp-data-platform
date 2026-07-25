// Package mention implements @-mention tagging on portal feedback threads
// (#627). It owns the token grammar an author writes into a comment body, the
// audience rule that decides who may be mentioned on a given thread target,
// the eligibility filter both write paths apply before a mention is persisted
// or notified, and the REST surface the composer's type-ahead reads.
//
// It sits beside pkg/portal rather than inside it so the portal package stays
// within the package-size budget (#594): portal declares the narrow interface
// it needs and the composition root injects this implementation, the same seam
// the notification bridge uses.
package mention

import (
	"regexp"
	"strings"
)

// Token grammar: "@" local "(" domain ")", e.g. @marcus.johnson(example.com).
//
// The parenthesized domain is deliberate. The obvious spelling,
// @marcus.johnson@example.com, reads as two addresses run together and has no
// terminator, so a mention written immediately before sentence punctuation
// absorbs that punctuation into the token -- the defect the knowledge-page
// reference scanner carries a trailing-punctuation trim for (#704). This form
// terminates at ")", converts losslessly to an address (local + "@" + domain),
// and matches the parenthesized shape the platform already uses for compound
// references (mcp:connection:(kind,name), urn:li:dataset:(...)).
var tokenRe = regexp.MustCompile(
	`@([A-Za-z0-9._%+\-]+)\(([A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,})\)`)

// maxTokensPerBody bounds how many distinct mentions one body contributes.
// A body is author-controlled free text, so the scan must not turn a pathological
// paste into an unbounded fan-out of audience lookups. Beyond the cap the
// remaining tokens are left as ordinary text: they resolve to nobody and notify
// nobody, exactly like a mention of someone outside the audience.
const maxTokensPerBody = 50

// Mention is one parsed token: the text as written, and the address it names.
// Email is lower-cased so it compares directly against an audience set and a
// notification recipient, both of which are normalized the same way.
type Mention struct {
	Raw   string `json:"raw"`
	Email string `json:"email"`
}

// Scan extracts the mentions in a comment body, in the order written and
// de-duplicated by address. It never fails: text that does not match the
// grammar is simply not a mention.
func Scan(body string) []Mention {
	matches := tokenRe.FindAllStringSubmatchIndex(body, -1)
	if len(matches) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(matches))
	out := make([]Mention, 0, min(len(matches), maxTokensPerBody))
	for _, m := range matches {
		if len(out) == maxTokensPerBody {
			break
		}
		if start := m[0]; start > 0 && addressByte(body[start-1]) {
			continue
		}
		email := strings.ToLower(body[m[2]:m[3]] + "@" + body[m[4]:m[5]])
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		out = append(out, Mention{Raw: body[m[0]:m[1]], Email: email})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Emails returns the addresses of the given mentions, preserving order.
func Emails(ms []Mention) []string {
	if len(ms) == 0 {
		return nil
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Email)
	}
	return out
}

// Format renders an address as a mention token, returning "" for anything that
// is not a single-"@" address. The result is checked by scanning it back, so
// Format can never emit a token Scan would not read as exactly that address --
// an address holding a character outside the grammar (a quoted local part, an
// IP-literal domain) yields "" rather than a token that silently means
// something else.
func Format(email string) string {
	email = strings.TrimSpace(email)
	local, domain, ok := strings.Cut(email, "@")
	if !ok || local == "" || domain == "" {
		return ""
	}
	token := "@" + local + "(" + domain + ")"
	if scanned := Scan(token); len(scanned) != 1 || !strings.EqualFold(scanned[0].Email, email) {
		return ""
	}
	return token
}

// normalize lower-cases and trims an address, so every comparison in this
// package -- audience membership, de-duplication, what is stored on an event --
// uses one spelling of it.
func normalize(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// addressByte reports whether b could be part of an email address written
// immediately before a token. When it is, the "@" belongs to that address as
// its separator rather than starting a mention, so the match is skipped.
func addressByte(b byte) bool {
	return b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z' || b >= '0' && b <= '9' ||
		strings.IndexByte(addressPunct, b) >= 0
}

// addressPunct is the non-alphanumeric set an address's local part or domain
// can hold, plus the "@" that separates them.
const addressPunct = "._%+-@"
