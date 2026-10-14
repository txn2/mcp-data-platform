//go:build integration

package helpers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/catalogstore"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// E2ETrinoConnection is the Trino connection the e2e platform declares, and so
// the connection the seeded catalog datasets are cataloged under.
const E2ETrinoConnection = "e2e"

// The seeded catalog fixture: the Trino table memory.e2e_test.test_orders
// (test/e2e/init/trino/setup.sql) with a description, two owners and two tags,
// and memory.e2e_test.customer_metrics with a description.
const (
	TestOrdersPath      = "memory.e2e_test.test_orders"
	CustomerMetricsPath = "memory.e2e_test.customer_metrics"
	TestOrdersOwnerA    = "alice@example.com"
	TestOrdersOwnerB    = "bob@example.com"
)

// TestOrdersTags are the tags the seeded test_orders dataset carries.
var TestOrdersTags = []string{"e2e-test", "ecommerce"}

// DatasetRef is the catalog reference of a dataset on the e2e Trino connection.
func DatasetRef(path string) catalog.Ref {
	return catalog.DatasetRef("trino", E2ETrinoConnection, mustParsePath(path)...)
}

func mustParsePath(path string) []string {
	parts, err := catalog.ParsePath(path)
	if err != nil {
		panic(fmt.Sprintf("e2e catalog path %q: %v", path, err))
	}
	return parts
}

// SeedCatalog writes the e2e catalog fixture into the platform catalog behind
// dsn. Every write is an upsert, so seeding a catalog that already holds the
// fixture leaves it as it was.
func SeedCatalog(ctx context.Context, dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("opening the catalog database: %w", err)
	}
	defer func() { _ = db.Close() }()
	store := catalogstore.New(db)

	datasets := []struct {
		path    string
		columns []string
		aspects map[catalog.AspectName]any
	}{
		{
			path:    TestOrdersPath,
			columns: []string{"order_id", "customer_id", "amount", "status"},
			aspects: map[catalog.AspectName]any{
				catalog.AspectDescription: "E-commerce test orders table for E2E testing",
				catalog.AspectOwners: []catalog.Owner{
					{Owner: TestOrdersOwnerA, Type: "technical_owner"},
					{Owner: TestOrdersOwnerB, Type: "business_owner"},
				},
				catalog.AspectTags: TestOrdersTags,
			},
		},
		{
			path:    CustomerMetricsPath,
			columns: []string{"customer_id"},
			aspects: map[catalog.AspectName]any{
				catalog.AspectDescription: "Per-customer metrics for E2E testing",
			},
		},
	}
	for _, d := range datasets {
		ref := DatasetRef(d.path)
		cols := make([]catalog.ColumnInput, 0, len(d.columns))
		for _, c := range d.columns {
			cols = append(cols, catalog.ColumnInput{Name: c})
		}
		if _, err := store.PutDataset(ctx, catalog.DatasetInput{Ref: ref, Type: catalog.DatasetTable, Columns: cols}); err != nil {
			return fmt.Errorf("seeding %s: %w", ref, err)
		}
		for name, value := range d.aspects {
			raw, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("encoding %s of %s: %w", name, ref, err)
			}
			if _, err := store.WriteAspect(ctx, ref, name, raw, "e2e-seed"); err != nil {
				return fmt.Errorf("writing %s of %s: %w", name, ref, err)
			}
		}
	}
	return nil
}
