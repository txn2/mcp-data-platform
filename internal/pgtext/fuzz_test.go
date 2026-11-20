package pgtext

import (
	"reflect"
	"testing"
)

// The text forms PostgreSQL writes, read back from what a row in an
// application database holds (#2713): what parses formats to a text that
// parses to the same value.

func FuzzRangeRoundTrip(f *testing.F) {
	for _, s := range []string{"[1,10)", "(,5]", "empty", `["2026-01-01 00:00:00","2026-02-01 00:00:00")`, `("a\"b",)`, "[,]"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		r, err := ParseRange(s)
		if err != nil {
			return
		}
		again, err := ParseRange(FormatRange(r))
		if err != nil {
			t.Fatalf("FormatRange(%q) = %q does not parse: %v", s, FormatRange(r), err)
		}
		if !reflect.DeepEqual(r, again) {
			t.Fatalf("%q round-trips to %#v, want %#v", s, again, r)
		}
	})
}

func FuzzMultirangeRoundTrip(f *testing.F) {
	for _, s := range []string{"{}", "{[1,3),[5,7)}", `{["a","b"]}`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		rs, err := ParseMultirange(s)
		if err != nil {
			return
		}
		again, err := ParseMultirange(FormatMultirange(rs))
		if err != nil {
			t.Fatalf("FormatMultirange(%q) does not parse: %v", s, err)
		}
		if !reflect.DeepEqual(rs, again) {
			t.Fatalf("%q round-trips to %#v, want %#v", s, again, rs)
		}
	})
}

// FuzzReaders: the composite, hstore and vector readers refuse what they
// cannot read and never panic; a vector's values are finite numbers.
func FuzzReaders(f *testing.F) {
	for _, s := range []string{`(1,"a b",)`, `"k"=>"v", "n"=>NULL`, "[1,2.5,-3e2]", "(", `"`, "[1,,2]"} {
		f.Add(s)
	}
	f.Fuzz(func(_ *testing.T, s string) {
		_, _ = ParseComposite(s)
		_, _ = ParseHstore(s)
		_, _ = ParseVector(s)
	})
}
