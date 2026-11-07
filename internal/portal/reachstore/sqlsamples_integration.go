//go:build integration

package reachstore

import (
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// SQLSamples renders each statement this package assembles at run time, over
// candidates that read the real tables, for the gate that hands store SQL to a
// real PostgreSQL to parse and plan (#1512).
func SQLSamples() map[string]string {
	tags, _, _ := psqlSelect("SELECT tags FROM portal_assets WHERE ", AssetPredicate(sampleReach()))
	authors, _, _ := psqlSelect("SELECT owner_email FROM portal_collections WHERE ", CollectionPredicate(sampleReach()))
	perms, _, _ := BuildPermissions([]string{"a1"}, sampleReach())
	return map[string]string{
		"BuildTagFacet":    BuildTagFacet(tags),
		"BuildAuthorFacet": BuildAuthorFacet(authors),
		"BuildPermissions": perms,
	}
}

func sampleReach() *portaldomain.Reach {
	return portaldomain.NewReach("550e8400-e29b-41d4-a716-446655440444", "analyst@example.com", "analyst", portaldomain.ScopeAll)
}

// psqlSelect renders prefix followed by cond, numbered with $ placeholders.
func psqlSelect(prefix string, cond interface {
	ToSql() (string, []any, error)
},
) (query string, args []any, err error) {
	where, args, err := cond.ToSql()
	if err != nil {
		return "", nil, fmt.Errorf("rendering condition: %w", err)
	}
	query, err = sq.Dollar.ReplacePlaceholders(prefix + where)
	if err != nil {
		return "", nil, fmt.Errorf("numbering condition: %w", err)
	}
	return query, args, nil
}
