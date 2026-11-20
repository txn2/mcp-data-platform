package scriptre

import (
	"testing"
	"time"
)

// FuzzValidate holds the re module's pattern check to a script author's
// patterns (#2713): a pattern is refused or accepted within a bound and never
// panics; RE2 has no backtracking, so no pattern takes long to check.
func FuzzValidate(f *testing.F) {
	for _, s := range []string{`^\d{3}-\d{4}$`, `(?P<name>\w+)`, `(a+)+$`, `[`, `(?<=x)y`, `\Z`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, pattern string) {
		if len(pattern) > 2048 {
			return
		}
		start := time.Now()
		_ = Validate(pattern)
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Fatalf("checking a %d-byte pattern took %s", len(pattern), took)
		}
	})
}
