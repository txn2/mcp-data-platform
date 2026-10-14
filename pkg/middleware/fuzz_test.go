package middleware

import (
	"testing"
)

// FuzzParseTableIdentifier fuzzes table identifier parsing.
func FuzzParseTableIdentifier(f *testing.F) {
	f.Add("")
	f.Add("table")
	f.Add("schema.table")
	f.Add("catalog.schema.table")
	f.Add("a.b.c.d.e.f")
	f.Add(".")
	f.Add("..")
	f.Add("...")
	f.Add("catalog..table")
	f.Add(".schema.table")
	f.Add("catalog.schema.")
	f.Add("table with spaces")
	f.Add("table\twith\ttabs")

	f.Fuzz(func(_ *testing.T, name string) {
		// Should never panic
		_ = parseTableIdentifier(name)
	})
}

// FuzzSplitTableName fuzzes the table name splitting function.
func FuzzSplitTableName(f *testing.F) {
	f.Add("")
	f.Add("simple")
	f.Add("a.b")
	f.Add("a.b.c")
	f.Add("...")
	f.Add(".a.b.")
	f.Add("a..b")
	f.Add("very.long.table.name.with.many.parts")

	f.Fuzz(func(_ *testing.T, name string) {
		// Should never panic
		_ = splitTableName(name)
	})
}
