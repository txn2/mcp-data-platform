package contenttype_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
)

// The catalog is what the platform's own documentation of declarable types is
// generated from, so every row has to be usable as a declaration: a canonical
// spelling the doors normalize to, and the extension the stored object carries.
func TestCatalogRowsAreCanonicalAndComplete(t *testing.T) {
	catalog := contenttype.Catalog()
	require.NotEmpty(t, catalog)

	seen := map[string]bool{}
	for _, f := range catalog {
		assert.Equalf(t, f.Type, contenttype.Normalize(f.Type),
			"%s is not the canonical spelling of its family", f.Type)
		assert.Truef(t, strings.HasPrefix(f.Extension, "."), "%s: extension %q has no leading dot",
			f.Type, f.Extension)
		assert.Equalf(t, contenttype.Extension(f.Type), f.Extension,
			"%s: the catalog and the extension table disagree", f.Type)
		assert.Equalf(t, contenttype.IsStorableText(f.Type), f.Storable,
			"%s: the catalog and the storable set disagree", f.Type)
		assert.Falsef(t, seen[f.Type], "duplicate row for %s", f.Type)
		seen[f.Type] = true
	}
	assert.True(t, slices.IsSortedFunc(catalog, func(a, b contenttype.Family) int {
		return strings.Compare(a.Type, b.Type)
	}), "the catalog is sorted so a page generated from it has a stable order")
}

// Every type a string-carrying door accepts is in the catalog: a caller told
// to declare one of them has to be able to find it in the list.
func TestCatalogCoversEveryStorableType(t *testing.T) {
	inCatalog := map[string]bool{}
	for _, f := range contenttype.Catalog() {
		if f.Storable {
			inCatalog[f.Type] = true
		}
	}

	for _, ct := range contenttype.StorableTextTypes() {
		assert.Truef(t, inCatalog[ct], "%s is accepted by a door and absent from the catalog", ct)
	}
}

// The families an agent writes and cannot have detected for it are the reason
// a declaration is required, so they are named in the catalog, and so are the
// binary families that reach a tool as base64.
func TestCatalogNamesTheFamiliesAWriterHasToDeclare(t *testing.T) {
	byType := map[string]contenttype.Family{}
	for _, f := range contenttype.Catalog() {
		byType[f.Type] = f
	}

	for _, ct := range []string{contenttype.SVG, contenttype.HTML, contenttype.JSX, contenttype.Markdown} {
		f, ok := byType[ct]
		require.Truef(t, ok, "%s is absent from the catalog", ct)
		assert.Truef(t, f.Storable, "%s reaches a door as a string and must be declarable there", ct)
	}
	for _, ct := range []string{"image/png", "application/pdf", "video/mp4"} {
		f, ok := byType[ct]
		require.Truef(t, ok, "%s is absent from the catalog", ct)
		assert.Falsef(t, f.Storable, "%s cannot survive a JSON string and must not read as declarable", ct)
	}

	_, ok := byType[contenttype.XHTML]
	assert.False(t, ok, "the family every door refuses must not be listed as one to declare")
}
