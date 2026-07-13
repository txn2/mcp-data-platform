package gen

import (
	"encoding/json"
	"fmt"
)

// DataHub URN construction for the bench warehouse.
func benchURN(table string) string {
	return fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,memory.bench.%s,PROD)", table)
}

// mcp is one metadata change proposal in the `datahub put --file` format the
// e2e testdata uses (test/e2e/testdata/datahub/datasets.json).
type mcp struct {
	EntityURN  string `json:"entityUrn"`
	EntityType string `json:"entityType"`
	AspectName string `json:"aspectName"`
	ChangeType string `json:"changeType"`
	Aspect     any    `json:"aspect"`
}

// Dataset descriptions are the A2 knowledge channel: they carry the facts the
// schema alone does not (units in cents, the net-revenue policy, deprecation,
// the gross-only nature of the pre-aggregated index).
const (
	ordersDescription = "Current order transactions, one row per order. Monetary columns (amount, discount) " +
		"are stored as INTEGERS IN US CENTS; divide by 100 for USD. Company revenue reporting policy: " +
		"revenue = amount - discount, COMPLETED orders only (refunded and pending orders are excluded). " +
		"See the 'Revenue Reporting Policy' knowledge page for the authoritative definition."
	customersDescription = "Customer directory: one row per customer with profile attributes " +
		"(name, region, tier, account created_at). Join key: customer_id."
	legacyDescription = "DEPRECATED order extract from the retired ingestion pipeline. Partial coverage, " +
		"totals in dollars. Use memory.bench.orders for all order analysis."
	dailyDescription = "Pre-aggregated daily revenue by region, derived from completed orders. Values are " +
		"GROSS of discounts (USD), so this index must not be used for policy net-revenue figures; " +
		"use memory.bench.orders per the Revenue Reporting Policy."
)

// DataHubMCEs emits the metadata proposals for `datahub put --file`.
func (d *Dataset) DataHubMCEs() ([]byte, error) {
	proposals := make([]mcp, 0, 10)
	proposals = append(proposals,
		datasetProps("orders", ordersDescription, map[string]string{"team": "bench", "unit": "cents"}),
		editableSchema("orders", map[string]string{
			"amount":   "Order amount in US cents (integer). Divide by 100 for USD.",
			"discount": "Discount in US cents (integer), non-zero on completed orders only.",
		}),
		datasetProps("customers", customersDescription, map[string]string{"team": "bench"}),
		datasetProps("legacy_orders", legacyDescription, map[string]string{"team": "bench"}),
		deprecation("legacy_orders", "Deprecated. Use memory.bench.orders instead."),
		datasetProps("daily_region_revenue", dailyDescription, map[string]string{"team": "bench", "grain": "day,region"}),
	)
	for _, table := range []string{"orders", "customers", "legacy_orders", "daily_region_revenue"} {
		proposals = append(proposals, tag(table))
	}
	return json.MarshalIndent(proposals, "", "  ")
}

// datasetProps builds a datasetProperties proposal.
func datasetProps(table, description string, custom map[string]string) mcp {
	return mcp{
		EntityURN:  benchURN(table),
		EntityType: "dataset",
		AspectName: "datasetProperties",
		ChangeType: "UPSERT",
		Aspect: map[string]any{
			"name":             table,
			"description":      description,
			"customProperties": custom,
		},
	}
}

// editableSchema builds column descriptions.
func editableSchema(table string, fields map[string]string) mcp {
	infos := make([]map[string]any, 0, len(fields))
	for _, field := range []string{"amount", "discount"} {
		if desc, ok := fields[field]; ok {
			infos = append(infos, map[string]any{"fieldPath": field, "description": desc})
		}
	}
	return mcp{
		EntityURN:  benchURN(table),
		EntityType: "dataset",
		AspectName: "editableSchemaMetadata",
		ChangeType: "UPSERT",
		Aspect:     map[string]any{"editableSchemaFieldInfo": infos},
	}
}

// deprecation builds the deprecation aspect (the S1 deprecated-table trap).
func deprecation(table, note string) mcp {
	return mcp{
		EntityURN:  benchURN(table),
		EntityType: "dataset",
		AspectName: "deprecation",
		ChangeType: "UPSERT",
		Aspect: map[string]any{
			"deprecated": true,
			"note":       note,
			"actor":      "urn:li:corpuser:bench-seed",
		},
	}
}

// tag applies the bench tag so seeded entities are identifiable.
func tag(table string) mcp {
	return mcp{
		EntityURN:  benchURN(table),
		EntityType: "dataset",
		AspectName: "globalTags",
		ChangeType: "UPSERT",
		Aspect:     map[string]any{"tags": []map[string]string{{"tag": "urn:li:tag:bench"}}},
	}
}
