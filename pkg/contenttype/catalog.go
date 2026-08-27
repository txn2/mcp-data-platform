package contenttype

import (
	"slices"
	"strings"
)

// Family is one media type the platform names, with the file extension a
// stored object of that type carries and whether a write path that receives
// its content as a string may be told to store it.
type Family struct {
	// Type is the canonical media type: the one spelling every alias of the
	// family normalizes to.
	Type string
	// Extension is the extension the stored object key carries, leading dot
	// included.
	Extension string
	// Storable reports whether IsStorableText accepts the type.
	Storable bool
}

// Catalog returns every media type this package names, sorted by type.
//
// It exists so the platform's own documentation of what a caller may declare
// is generated from the tables that decide it rather than transcribed beside
// them: a family added to the extension table appears in the documentation on
// the next build, and a page cannot list a type the code does not handle.
//
// The extension table is the enumeration because it is the widest set with a
// canonical spelling -- every family the platform detects, stores an object
// key for, or renders is in it. The storable subset is marked rather than
// separated so a caller of this decides how to present the difference.
func Catalog() []Family {
	families := make([]Family, 0, len(extensions))
	for ct, ext := range extensions {
		families = append(families, Family{
			Type:      ct,
			Extension: ext,
			Storable:  storableTextTypes[ct],
		})
	}
	slices.SortFunc(families, func(a, b Family) int { return strings.Compare(a.Type, b.Type) })
	return families
}
