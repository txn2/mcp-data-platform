package gqlschema

import (
	"strings"
	"testing"
	"time"
)

// FuzzParseDepth holds the GraphQL document reader to an agent's document
// (#2713): parsing and measuring a document's depth end within a bound (a
// fragment that spreads itself cannot loop), and a document nested n levels
// measures n, so a document deeper than the configured depth is refused.
func FuzzParseDepth(f *testing.F) {
	f.Add("query { a { b { c } } }", uint8(3))
	f.Add("fragment F on T { ...F } query { a { ...F } }", uint8(1))
	f.Add("{ __schema { types { name } } }", uint8(5))
	f.Add("mutation M { x }", uint8(0))
	f.Fuzz(func(t *testing.T, raw string, n uint8) {
		if len(raw) > 1<<14 {
			return
		}
		start := time.Now()
		if d, err := Parse(raw, ""); err == nil {
			_ = d.Depth()
			_ = d.TopLevelFields()
			_ = d.HasIntrospectionSelection()
		}
		if took := time.Since(start); took > time.Second {
			t.Fatalf("a %d-byte document took %s", len(raw), took)
		}
		levels := int(n%40) + 1
		nested := "query { " + strings.Repeat("f { ", levels-1) + "leaf" + strings.Repeat(" }", levels-1) + " }"
		d, err := Parse(nested, "")
		if err != nil {
			t.Fatalf("a nested document does not parse: %v", err)
		}
		if got := d.Depth(); got != levels {
			t.Fatalf("a document nested %d levels measures %d", levels, got)
		}
	})
}

// FuzzLoadAny: an introspection payload or SDL from an upstream loads or is
// refused, never panics, and the operations walked from what loads are bounded.
func FuzzLoadAny(f *testing.F) {
	f.Add([]byte("type Query { a: Int }"))
	f.Add([]byte(`{"data":{"__schema":{"queryType":{"name":"Query"},"types":[]}}}`))
	f.Add([]byte("{"))
	f.Fuzz(func(_ *testing.T, payload []byte) {
		if len(payload) > 1<<15 {
			return
		}
		s, err := LoadAny(payload)
		if err != nil || s == nil {
			return
		}
		_ = Operations(s, 3)
	})
}
