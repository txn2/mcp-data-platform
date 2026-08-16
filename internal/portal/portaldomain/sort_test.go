package portaldomain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func TestResolveOrder(t *testing.T) {
	tests := []struct {
		name    string
		sortBy  string
		sortDir string
		allowed map[string]bool
		want    []string
	}{
		{
			name:    "empty falls back to the default column, descending",
			allowed: portaldomain.AssetSortColumns,
			want:    []string{"updated_at DESC", "id DESC"},
		},
		{
			name:    "allowed column is used",
			sortBy:  "created_at",
			sortDir: portaldomain.SortAsc,
			allowed: portaldomain.AssetSortColumns,
			want:    []string{"created_at ASC", "id ASC"},
		},
		{
			name:    "size is sortable for assets",
			sortBy:  "size_bytes",
			allowed: portaldomain.AssetSortColumns,
			want:    []string{"size_bytes DESC", "id DESC"},
		},
		{
			name:    "size is not sortable for collections",
			sortBy:  "size_bytes",
			allowed: portaldomain.CollectionSortColumns,
			want:    []string{"updated_at DESC", "id DESC"},
		},
		{
			name:    "unknown column never reaches the clause",
			sortBy:  "owner_id",
			allowed: portaldomain.AssetSortColumns,
			want:    []string{"updated_at DESC", "id DESC"},
		},
		{
			name:    "a column bearing SQL is refused whole",
			sortBy:  "name; DROP TABLE portal_assets",
			allowed: portaldomain.AssetSortColumns,
			want:    []string{"updated_at DESC", "id DESC"},
		},
		{
			name:    "unknown direction falls back to descending",
			sortBy:  "name",
			sortDir: "sideways",
			allowed: portaldomain.AssetSortColumns,
			want:    []string{"name DESC", "id DESC"},
		},
		{
			name:    "direction is case-sensitive at this layer",
			sortBy:  "name",
			sortDir: "asc",
			allowed: portaldomain.AssetSortColumns,
			want:    []string{"name DESC", "id DESC"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := portaldomain.ResolveOrder(tc.sortBy, tc.sortDir, tc.allowed, portaldomain.SortUpdatedAt)
			assert.Equal(t, tc.want, got)
		})
	}
}

// The tie-breaker exists so LIMIT/OFFSET pagination cannot repeat or drop a row
// whose sort value it shares with another: it must follow the chosen direction,
// not a fixed one, or the second key would fight the first.
func TestResolveOrderTieBreakerFollowsDirection(t *testing.T) {
	asc := portaldomain.ResolveOrder("name", portaldomain.SortAsc, portaldomain.AssetSortColumns, portaldomain.SortUpdatedAt)
	assert.Equal(t, []string{"name ASC", "id ASC"}, asc)

	desc := portaldomain.ResolveOrder("name", portaldomain.SortDesc, portaldomain.AssetSortColumns, portaldomain.SortUpdatedAt)
	assert.Equal(t, []string{"name DESC", "id DESC"}, desc)
}

func TestFilterOrder(t *testing.T) {
	t.Run("asset filter resolves through the shared validator", func(t *testing.T) {
		f := portaldomain.AssetFilter{SortBy: "size_bytes", SortDir: portaldomain.SortAsc}
		assert.Equal(t, []string{"size_bytes ASC", "id ASC"}, f.Order())
	})

	t.Run("collection filter has no size column", func(t *testing.T) {
		f := portaldomain.CollectionFilter{SortBy: "size_bytes", SortDir: portaldomain.SortAsc}
		assert.Equal(t, []string{"updated_at ASC", "id ASC"}, f.Order())
	})

	t.Run("both default to most recently touched", func(t *testing.T) {
		assert.Equal(t, []string{"updated_at DESC", "id DESC"}, (&portaldomain.AssetFilter{}).Order())
		assert.Equal(t, []string{"updated_at DESC", "id DESC"}, (&portaldomain.CollectionFilter{}).Order())
	})
}
