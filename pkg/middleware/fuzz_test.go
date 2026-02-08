package middleware

import (
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
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

// FuzzBuildTableSemanticContext fuzzes building semantic context.
func FuzzBuildTableSemanticContext(f *testing.F) {
	f.Add("urn:li:dataset:1", "users", "User data", true, 0.9)                                  //nolint:revive // fuzz seed corpus value
	f.Add("", "", "", false, 0.0)                                                               //nolint:revive // fuzz seed corpus value
	f.Add("urn", "a", "b", true, -1.0)                                                          //nolint:revive // fuzz seed corpus value
	f.Add("urn:li:dataset:(urn:li:dataPlatform:s3,bucket/key,PROD)", "dataset", "", false, 1.5) //nolint:revive // fuzz seed corpus value

	f.Fuzz(func(_ *testing.T, urn, name, desc string, deprecated bool, quality float64) {
		sr := semantic.TableSearchResult{
			URN:  urn,
			Name: name,
		}

		var deprecation *semantic.Deprecation
		if deprecated {
			deprecation = &semantic.Deprecation{
				Deprecated: true,
				Note:       "deprecated",
			}
		}

		var qualityPtr *float64
		if quality >= 0 && quality <= 1 {
			qualityPtr = &quality
		}

		tableCtx := &semantic.TableContext{
			Description:  desc,
			Deprecation:  deprecation,
			QualityScore: qualityPtr,
			Owners:       []semantic.Owner{{Name: "owner", Type: semantic.OwnerTypeUser}},
			Tags:         []string{"tag1"},
			Domain:       &semantic.Domain{Name: "domain"},
		}

		// Should never panic
		_ = buildTableSemanticContext(sr, tableCtx)
	})
}
