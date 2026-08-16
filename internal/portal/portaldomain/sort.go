package portaldomain

// Asset and collection listing both order by a caller-chosen column. ORDER BY
// cannot be parameterized, so the column is spliced into SQL and every value
// that reaches a query passes through ResolveOrder first: an unknown column or
// direction falls back to the default rather than reaching Postgres.

// Sort directions accepted by AssetFilter.SortDir and CollectionFilter.SortDir.
const (
	SortAsc  = "ASC"
	SortDesc = "DESC"
)

// SortUpdatedAt is the default ordering column for both lists. "Newest first"
// means most recently touched: an asset created in June and revised today
// belongs above one created yesterday and never opened since, and a version
// bump writes updated_at (internal/portal/portalversions/versions.go:60) so the
// column tracks revision, not just metadata edits.
const SortUpdatedAt = "updated_at"

// AssetSortColumns lists the portal_assets columns AssetFilter.SortBy may
// name. Only these values are ever spliced into an ORDER BY clause.
var AssetSortColumns = map[string]bool{
	"updated_at": true,
	"created_at": true,
	"name":       true,
	"size_bytes": true,
}

// CollectionSortColumns lists the portal_collections columns
// CollectionFilter.SortBy may name. Collections carry no size, so the set is
// the asset set minus size_bytes.
var CollectionSortColumns = map[string]bool{
	"updated_at": true,
	"created_at": true,
	"name":       true,
}

// validSortDirections guards the direction half of the ORDER BY splice.
var validSortDirections = map[string]bool{
	SortAsc:  true,
	SortDesc: true,
}

// ResolveOrder returns the ORDER BY clauses for a listing query: the validated
// sort column and direction, then the primary key in the same direction.
//
// The tie-breaker makes the ordering a total order. None of the sortable
// columns is unique — two assets saved in the same second share an updated_at,
// and names are not unique at all — and with LIMIT/OFFSET pagination a
// non-unique sort lets Postgres return a straddling row on both pages or on
// neither, so a caller walking pages silently sees a duplicate or a hole.
func ResolveOrder(sortBy, sortDir string, allowed map[string]bool, defaultCol string) []string {
	col := defaultCol
	if allowed[sortBy] {
		col = sortBy
	}
	dir := SortDesc
	if validSortDirections[sortDir] {
		dir = sortDir
	}
	return []string{col + " " + dir, "id " + dir}
}
