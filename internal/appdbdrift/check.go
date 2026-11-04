package appdbdrift

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// Kit is the part of the postgres toolkit a drift check reads.
type Kit interface {
	Describe(ctx context.Context, connection, schema string) (*postgres.Description, error)
	DescribeBuilt(ctx context.Context, connection, schema string, migrations []postgres.Migration) (*postgres.Description, error)
	Status(ctx context.Context, connection, schema string, migrations []postgres.Migration) (*postgres.LedgerStatus, error)
}

// Migrations lists the migrations the platform keeps for a schema.
type Migrations interface {
	List(ctx context.Context, connection, schema string) ([]postgres.Migration, error)
}

// Result is one drift check of a schema.
type Result struct {
	Connection string    `json:"connection"`
	Schema     string    `json:"schema"`
	CheckedAt  time.Time `json:"checked_at"`
	// Checked is false when the check could not be made, and Reason says why:
	// the schema has no migrations, the ledger disagrees with them, or the
	// expected schema could not be built.
	Checked bool   `json:"checked"`
	Reason  string `json:"reason,omitempty"`
	// AppliedVersion is the latest applied migration the expected schema was
	// built through.
	AppliedVersion int          `json:"applied_version"`
	Differences    []Difference `json:"differences"`
}

// Check compares schema's live objects with the ones its applied migrations
// build. Pending migrations are left out of the build: they are not drift.
func Check(ctx context.Context, kit Kit, store Migrations, connection, schema string) Result {
	res := Result{Connection: connection, Schema: schema, CheckedAt: time.Now().UTC(), Differences: []Difference{}}
	migrations, err := store.List(ctx, connection, schema)
	if err != nil {
		res.Reason = "reading the migrations: " + err.Error()
		return res
	}
	if len(migrations) == 0 {
		res.Reason = "the platform keeps no migrations for this schema; adopt it to manage it"
		return res
	}
	st, err := kit.Status(ctx, connection, schema, migrations)
	if err != nil {
		res.Reason = err.Error()
		return res
	}
	if len(st.Drift) > 0 {
		res.Reason = fmt.Sprintf("the ledger disagrees with the migrations at version %d: %s", st.Drift[0].Version, st.Drift[0].Reason)
		return res
	}
	applied := appliedOf(migrations, st.Applied)
	if len(applied) == 0 {
		res.Reason = "no migration is applied yet"
		return res
	}
	res.AppliedVersion = applied[len(applied)-1].Version
	expected, err := kit.DescribeBuilt(ctx, connection, schema, applied)
	if err != nil {
		res.Reason = "building the schema the applied migrations produce: " + err.Error()
		return res
	}
	live, err := kit.Describe(ctx, connection, schema)
	if err != nil {
		res.Reason = "reading the live schema: " + err.Error()
		return res
	}
	res.Checked, res.Differences = true, Compare(live, expected)
	return res
}

// appliedOf is the migrations the ledger records as applied, oldest first.
func appliedOf(migrations []postgres.Migration, applied []postgres.Applied) []postgres.Migration {
	versions := make([]int, 0, len(applied))
	for _, a := range applied {
		versions = append(versions, a.Version)
	}
	return slices.DeleteFunc(slices.Clone(migrations), func(m postgres.Migration) bool {
		return !slices.Contains(versions, m.Version)
	})
}
