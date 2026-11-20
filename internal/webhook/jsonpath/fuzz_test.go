package jsonpath

import (
	"encoding/json"
	"testing"
	"time"
)

// fuzzBound is the most one input of a few kilobytes may take: a path a
// source configures and the document it reads are each read in linear time,
// and a quadratic or worse case shows up as far more than this (#2713).
const fuzzBound = 250 * time.Millisecond

// FuzzParseAndLookup: a parsed path prints as text that parses to the same
// path, and parsing and reading a document stay within the bound.
func FuzzParseAndLookup(f *testing.F) {
	for _, s := range []struct{ path, doc string }{
		{"$.data.id", `{"data":{"id":"evt_1"}}`},
		{"$.events[*].type", `{"events":[{"type":"a"},{"type":"b"}]}`},
		{"$['weird key'][0]", `{"weird key":[1,2]}`},
		{"$..", `{}`},
	} {
		f.Add(s.path, s.doc)
	}
	f.Fuzz(func(t *testing.T, text, doc string) {
		if len(text) > 4096 || len(doc) > 1<<16 {
			return
		}
		start := time.Now()
		p, err := Parse(text)
		if err == nil {
			again, err := Parse(p.String())
			if err != nil {
				t.Fatalf("a parsed path does not parse again: %q: %v", text, err)
			}
			if again.String() != p.String() {
				t.Fatalf("Parse(%q) prints %q, which prints %q", text, p.String(), again.String())
			}
			var v any
			if json.Unmarshal([]byte(doc), &v) == nil {
				_, _ = p.Lookup(v)
				_, _ = p.LookupAll(v)
			}
		}
		if d := time.Since(start); d > fuzzBound {
			t.Fatalf("path %q over a %d-byte document took %s", text, len(doc), d)
		}
	})
}
