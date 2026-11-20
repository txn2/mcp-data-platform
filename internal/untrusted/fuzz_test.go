package untrusted

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzClean holds the cleaning of third-party text (#2713): no hidden rune
// survives it, it is idempotent, and Line leaves no line break.
func FuzzClean(f *testing.F) {
	for _, s := range []string{"plain", "a\u202eb\u200dc", "line\r\nbreak\tand\x00nul", "\xff\xfe", "zero\u200bwidth"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		c := Clean(s)
		for _, r := range c {
			if Hidden(r) {
				t.Fatalf("Clean(%q) = %q keeps hidden rune %U", s, c, r)
			}
		}
		if again := Clean(c); again != c {
			t.Fatalf("Clean is not idempotent: %q then %q", c, again)
		}
		l := Line(s)
		if strings.ContainsAny(l, "\r\n") {
			t.Fatalf("Line(%q) = %q keeps a line break", s, l)
		}
		if utf8.ValidString(s) && !utf8.ValidString(c) {
			t.Fatalf("Clean made valid UTF-8 invalid: %q", c)
		}
	})
}
