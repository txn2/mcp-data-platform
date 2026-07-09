// Package logsan provides a single, shared log-sanitization helper used
// across the platform to neutralize log-injection (CWE-117) vectors in
// user-derived string values before they are passed to a logger.
//
// The production logger is a slog JSON handler, which already escapes
// control characters, so log injection is not exploitable in the default
// configuration. This helper is defense-in-depth: it keeps the code
// correct if the handler is ever switched to a text handler, or if logs
// are line-split before parsing, and it resolves the static-analysis
// findings (CodeQL go/log-injection, gosec G706) legitimately rather than
// by suppression. Consolidating the previously duplicated per-package
// sanitizers here ensures a single behavior that cannot drift.
package logsan

import "strings"

// SanitizeForLog strips ASCII control characters (including CR, LF, tab,
// and DEL) from s so a hostile or malformed value cannot forge log lines
// or inject terminal control sequences. Non-control runes, including all
// printable multibyte Unicode, are preserved unchanged.
//
// The CR/LF/tab removal is done first via a strings.Replacer built from
// literal "\n"/"\r" arguments. That explicit newline-replacement form is
// what the CodeQL go/log-injection query recognizes as a sanitizer
// barrier (a strings.Map with a rune predicate is not recognized), so
// routing every value through it resolves the findings legitimately. The
// subsequent strings.Map pass strips any remaining control characters
// (NUL, ESC, DEL, ...) for defense in depth.
func SanitizeForLog(s string) string {
	s = strings.NewReplacer("\n", "", "\r", "", "\t", "").Replace(s)
	return strings.Map(func(r rune) rune {
		if isControl(r) {
			return -1
		}
		return r
	}, s)
}

// asciiSpace and asciiDel bound the printable ASCII range: any rune
// below space (0x20) or equal to DEL (0x7f) is an ASCII control
// character that isControl strips.
const (
	asciiSpace = 0x20
	asciiDel   = 0x7f
)

// isControl reports whether r is an ASCII control character or DEL.
func isControl(r rune) bool {
	return r < asciiSpace || r == asciiDel
}
