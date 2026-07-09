package logsan

import (
	"strings"
	"testing"
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
