package jsonwalk

import (
	"strings"
	"testing"
)

// A recorded run's arguments are redacted by a walk over their strings
// (#2716): a tool call's arguments as they arrive, nested maps and lists, one
// string the walk rewrites.
func BenchmarkStringsOverArguments(b *testing.B) {
	args := map[string]any{
		"connection": "warehouse",
		"sql":        "SELECT * FROM memory.sales.orders WHERE token = 'sk-live-secret'",
		"params":     []any{"west", 100, true, map[string]any{"after": "2026-01-01", "tags": []any{"a", "b", "c"}}},
		"headers":    map[string]any{"Authorization": "Bearer sk-live-secret", "Accept": "application/json"},
		"limit":      1000,
	}
	redact := func(s string) string { return strings.ReplaceAll(s, "sk-live-secret", "[REDACTED:key]") }
	b.ReportAllocs()
	for range b.N {
		_, _ = Strings(args, redact)
	}
}
