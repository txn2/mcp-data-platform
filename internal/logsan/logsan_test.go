package logsan

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeForLog(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"plain", "connection-primary", "connection-primary"},
		{"newline", "foo\nbar", "foobar"},
		{"carriage return", "foo\rbar", "foobar"},
		{"crlf forged line", "ok\r\nERROR admin deleted everything", "okERROR admin deleted everything"},
		{"tab", "a\tb", "ab"},
		{"del char", "a\x7fb", "ab"},
		{"null byte", "a\x00b", "ab"},
		{"vertical tab and form feed", "a\x0b\x0cb", "ab"},
		{"escape sequence", "a\x1b[31mred", "a[31mred"},
		{"only control chars", "\n\r\t\x00", ""},
		{"unicode preserved", "café — 日本語 ✓", "café — 日本語 ✓"},
		{"leading and trailing newlines", "\nmiddle\n", "middle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeForLog(tt.input); got != tt.want {
				t.Errorf("SanitizeForLog(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSanitizeForLogNoControlCharsRemain is a property check: the output
// must never contain any ASCII control character, whatever the input.
func TestSanitizeForLogNoControlCharsRemain(t *testing.T) {
	inputs := []string{
		"clean",
		"line1\nline2\nline3",
		"\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0a\x0b\x0c\x0d\x0e\x0f",
		"mixed\ttabs\rand\nnewlines\x7f",
	}
	for _, in := range inputs {
		out := SanitizeForLog(in)
		if strings.ContainsFunc(out, isControl) {
			t.Errorf("SanitizeForLog(%q) = %q still contains a control character", in, out)
		}
	}
}

// TestSanitizeForLogPreservesCleanIdentity verifies the fast path returns
// the identical string (no needless allocation) when there is nothing to
// strip.
func TestSanitizeForLogPreservesCleanIdentity(t *testing.T) {
	const clean = "already-clean-value_123"
	if got := SanitizeForLog(clean); got != clean {
		t.Fatalf("SanitizeForLog(%q) = %q, want unchanged", clean, got)
	}
}

// TestExcerptHoldsItsBound covers the case that makes the bound worth
// asserting: sanitizing re-encodes each byte that is not valid UTF-8 as
// U+FFFD, three bytes for one, so capping the input first would hand the
// caller up to three times the limit it asked for. An upstream chooses the
// bytes of the response bodies this bounds, so it gets to pick that input.
func TestExcerptHoldsItsBound(t *testing.T) {
	const limit = 256
	cases := map[string]string{
		"ascii":               strings.Repeat("x", 300),
		"invalid utf-8":       strings.Repeat("\xff", 300),
		"truncated multibyte": strings.Repeat("\xc3", 300),
		"valid multibyte":     strings.Repeat("é", 300),
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			got := Excerpt(in, limit)
			if len(got) > limit+len("...") {
				t.Fatalf("Excerpt exceeded its bound: len=%d, want <= %d", len(got), limit+len("..."))
			}
			if !strings.HasSuffix(got, "...") {
				t.Fatalf("Excerpt did not mark the truncation: %q", got[max(0, len(got)-8):])
			}
			// A cut that lands mid-rune would leave the excerpt invalid, and
			// a log pipeline that re-encodes it would mangle the tail.
			if !utf8.ValidString(strings.TrimSuffix(got, "...")) {
				t.Fatalf("Excerpt cut mid-rune: %q", got)
			}
		})
	}
}

// TestExcerptKeepsShortValuesWhole checks the common path: a body under the
// limit is sanitized and returned without an ellipsis.
func TestExcerptKeepsShortValuesWhole(t *testing.T) {
	got := Excerpt("invalid_grant\r\nlevel=ERROR", 256)
	if strings.ContainsAny(got, "\r\n") {
		t.Fatalf("Excerpt left a line break: %q", got)
	}
	if strings.Contains(got, "...") {
		t.Fatalf("Excerpt marked a truncation that did not happen: %q", got)
	}
	if !strings.Contains(got, "invalid_grant") {
		t.Fatalf("Excerpt dropped the diagnostic text: %q", got)
	}
}
