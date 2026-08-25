package configenv

import (
	"strings"
	"testing"
)

// The expansion syntax does not nest, and the failure that produces is remote
// from its cause: an inner default survives as a literal value and surfaces
// layers away (a Trino username of "${TRINO_USER:-}" became "invalid DSN ...
// invalid userinfo" during a table registration). These pin the behavior so
// the shape is stated somewhere other than a regex.

func TestExpandEnvVars_Nested(t *testing.T) {
	t.Setenv("EXPAND_OUTER", "")
	t.Setenv("EXPAND_INNER", "inner-value")

	got := Expand("user: ${EXPAND_OUTER:-${EXPAND_INNER:-}}")
	// Not "inner-value": the class stops at the first "}", so the outer default
	// is the literal text "${EXPAND_INNER:-" and a stray "}" follows it.
	if strings.Contains(got, "inner-value") {
		t.Fatalf("nesting appears to work now; the warning and the docs both say it does not: %q", got)
	}
	if !strings.Contains(got, "${EXPAND_INNER") {
		t.Errorf("expected the inner placeholder to survive verbatim, got %q", got)
	}
}

func TestExpandEnvVars_SingleLevel(t *testing.T) {
	t.Setenv("EXPAND_SET", "set-value")

	if got := Expand("${EXPAND_SET:-fallback}"); got != "set-value" {
		t.Errorf("a set variable did not win over its default: %q", got)
	}
	t.Setenv("EXPAND_SET", "")
	if got := Expand("${EXPAND_SET:-fallback}"); got != "fallback" {
		t.Errorf("an empty variable did not fall back: %q", got)
	}
	if got := Expand("${EXPAND_SET}"); got != "" {
		t.Errorf("a variable with no default did not expand to empty: %q", got)
	}
}

// A "${" left standing is almost always a substitution that did not happen.
// The scan must find it wherever it sits, and must not fire on a clean config.
func TestUnexpandedPattern(t *testing.T) {
	for _, s := range []string{
		"user: ${TRINO_USER:-}",
		"host: q-demo.example.com\nuser: ${A:-${B:-}}",
	} {
		if !unexpandedPattern.MatchString(s) {
			t.Errorf("an unexpanded placeholder went unnoticed in %q", s)
		}
	}
	if unexpandedPattern.MatchString("host: q-demo.example.com\nuser: analyst") {
		t.Error("a fully expanded config was reported as carrying a placeholder")
	}
}

func TestExpand(t *testing.T) {
	t.Setenv("MY_VAR", "value123")
	t.Setenv("ANOTHER_VAR", "another")

	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{"single var", "prefix-${MY_VAR}-suffix", "prefix-value123-suffix"},
		{"multiple vars", "${MY_VAR} and ${ANOTHER_VAR}", "value123 and another"},
		{"no vars", "no variables here", "no variables here"},
		{"empty var", "${UNDEFINED_VAR}", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := Expand(tt.input)
			if result != tt.expect {
				t.Errorf("Expand(%q) = %q, want %q", tt.input, result, tt.expect)
			}
		})
	}
}
