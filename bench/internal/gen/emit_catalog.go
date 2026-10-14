package gen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/txn2/mcp-data-platform/bench/internal/catalogref"
)

// benchURN is the platform catalog reference of a bench warehouse table.
func benchURN(table string) string {
	return catalogref.Dataset(table)
}

// benchTables is the fixed set of bench warehouse datasets, in emission order.
// Both the fully-documented A2 seed and the cold-start empty seed iterate it, so
// the two catalog baselines cover exactly the same datasets.
var benchTables = []string{"orders", "customers", "legacy_orders", "daily_region_revenue"}

// column is one column of a bench table, as the Trino DDL declares it.
type column struct {
	name     string
	dataType string
}

// benchColumns are each bench table's columns in table order, matching the
// CREATE TABLE statements TrinoSQL emits.
var benchColumns = map[string][]column{
	"orders": {
		{"order_id", "integer"}, {"customer_id", "integer"}, {"order_ts", "timestamp(3)"},
		{"status", "varchar"}, {"amount", "integer"}, {"discount", "integer"},
	},
	"customers": {
		{"customer_id", "integer"}, {"name", "varchar"}, {"region", "varchar"},
		{"tier", "varchar"}, {"created_at", "timestamp(3)"},
	},
	"legacy_orders": {
		{"order_id", "integer"}, {"customer_id", "integer"}, {"order_date", "date"}, {"total", "double"},
	},
	"daily_region_revenue": {
		{"day", "date"}, {"region", "varchar"}, {"gross_revenue_usd", "double"},
	},
}

// benchTag marks every seeded dataset so the seed is identifiable.
const benchTag = "bench"

// seedWriter is the catalog_aspects.written_by every seeded aspect carries.
const seedWriter = "bench-seed"

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
		"use memory.bench.orders per the Revenue Reporting Policy. FRESHNESS: this index is refreshed " +
		"only through 2025-11-30; it has NO rows for dates after that cutoff, so any question about a " +
		"period on or after 2025-12-01 must be answered from memory.bench.orders directly, not this index."
	legacyDeprecation = "Deprecated. Use memory.bench.orders instead."
)

// aspect is one curation aspect written on a dataset or one of its columns.
type aspect struct {
	ref   string
	name  string
	value any
}

// CatalogSQL emits the fully-documented A2 catalog seed: every bench dataset
// and its columns, with the descriptions, column docs, tags and properties that
// carry the knowledge traps. The platform catalog has no deprecation aspect,
// so legacy_orders states it in its description and in its properties.
func (d *Dataset) CatalogSQL() (string, error) {
	orders := benchURN("orders")
	aspects := make([]aspect, 0, len(benchTables))
	aspects = append(aspects, []aspect{
		{orders, "description", ordersDescription},
		{orders, "custom_properties", map[string]string{"team": "bench", "unit": "cents"}},
		{columnRef("orders", "amount"), "description", "Order amount in US cents (integer). Divide by 100 for USD."},
		{columnRef("orders", "discount"), "description", "Discount in US cents (integer), non-zero on completed orders only."},
		{benchURN("customers"), "description", customersDescription},
		{benchURN("customers"), "custom_properties", map[string]string{"team": "bench"}},
		{benchURN("legacy_orders"), "description", legacyDescription},
		{benchURN("legacy_orders"), "custom_properties", map[string]string{
			"team": "bench", "deprecated": "true", "deprecation_note": legacyDeprecation,
		}},
		{benchURN("daily_region_revenue"), "description", dailyDescription},
		{benchURN("daily_region_revenue"), "custom_properties", map[string]string{"team": "bench", "grain": "day,region"}},
	}...)
	for _, table := range benchTables {
		aspects = append(aspects, aspect{benchURN(table), "tags", []string{benchTag}})
	}
	var b strings.Builder
	writeCatalogHeader(&b, "the fully-documented A2 catalog")
	writeEntities(&b, true)
	if err := writeAspects(&b, aspects); err != nil {
		return "", err
	}
	return b.String(), nil
}

// CatalogSQLEmpty emits the cold-start baseline (#963): each bench dataset and
// its columns exist in the catalog but carry NO knowledge: no description, no
// column docs, no tags, no properties. The enrichment layer is therefore bare,
// so a fresh identity cannot answer a knowledge-trap question from the
// catalog. The cold-start curriculum backfills this knowledge one lesson at a
// time (promoting into these same dataset descriptions and into knowledge
// pages), and the learning curve measures accuracy climbing from this empty
// floor toward the fully-documented A2 ceiling that CatalogSQL seeds.
func (d *Dataset) CatalogSQLEmpty() string {
	var b strings.Builder
	writeCatalogHeader(&b, "the cold-start empty catalog (datasets present, undocumented)")
	writeEntities(&b, false)
	return b.String()
}

// columnRef is the catalog reference of one column of a bench table.
func columnRef(table, col string) string {
	return strings.Replace(strings.TrimSuffix(benchURN(table), ")"), "mcp:dataset:(", "mcp:column:(", 1) + "," + col + ")"
}

// writeCatalogHeader writes the generated-file banner.
func writeCatalogHeader(b *strings.Builder, what string) {
	fmt.Fprintf(b, "-- Generated by bench/seedgen (seed %d). Do not edit; regenerate with `make bench-gen`.\n", Seed)
	fmt.Fprintf(b, "-- Seeds %s on the Trino connection %q.\n", what, catalogref.Connection)
	b.WriteString("-- Requires platform migrations (catalog_entities); apply after the platform has booted.\n")
	b.WriteString("-- Idempotent: rows are keyed by reference, so a catalog scan that already\n")
	b.WriteString("-- recorded these datasets keeps its rows and the curation lands on them.\n\n")
}

// writeEntities writes the dataset and column rows, and the bench tag when the
// seed carries tags.
func writeEntities(b *strings.Builder, withTag bool) {
	b.WriteString("INSERT INTO catalog_entities\n" +
		"    (kind, reference, connection_kind, connection_name, path, name, slug, dataset_type)\nVALUES\n")
	rows := make([]string, 0, len(benchTables))
	for _, table := range benchTables {
		rows = append(rows, fmt.Sprintf("    ('dataset', %s, 'trino', %s, %s, %s, '', 'table')",
			lit(benchURN(table)), lit(catalogref.Connection), lit("memory.bench."+table), lit(table)))
	}
	if withTag {
		rows = append(rows, fmt.Sprintf("    ('tag', %s, '', '', '', %s, %s, '')",
			lit("mcp:tag:"+benchTag), lit(benchTag), lit(benchTag)))
	}
	b.WriteString(strings.Join(rows, ",\n"))
	b.WriteString("\nON CONFLICT (reference) DO NOTHING;\n\n")

	b.WriteString("INSERT INTO catalog_entities\n" +
		"    (kind, reference, connection_kind, connection_name, path, column_name, parent_id, name, data_type, ordinal)\n")
	selects := make([]string, 0, 18)
	for _, table := range benchTables {
		for i, c := range benchColumns[table] {
			selects = append(selects, fmt.Sprintf(
				"SELECT 'column', %s, 'trino', %s, %s, %s, d.id, %s, %s, %d FROM catalog_entities d WHERE d.reference = %s",
				lit(columnRef(table, c.name)), lit(catalogref.Connection), lit("memory.bench."+table),
				lit(c.name), lit(c.name), lit(c.dataType), i, lit(benchURN(table))))
		}
	}
	b.WriteString(strings.Join(selects, "\nUNION ALL "))
	b.WriteString("\nON CONFLICT (reference) DO NOTHING;\n")
}

// writeAspects writes the curation, and a history row for each so the
// catalog's change history starts from the seed.
func writeAspects(b *strings.Builder, aspects []aspect) error {
	sort.SliceStable(aspects, func(i, j int) bool { return aspects[i].ref < aspects[j].ref })
	b.WriteString("\nINSERT INTO catalog_aspects (entity_id, aspect, value, version, written_by)\n")
	selects := make([]string, 0, len(aspects))
	for _, a := range aspects {
		raw, err := json.Marshal(a.value)
		if err != nil {
			return fmt.Errorf("encode %s %s: %w", a.ref, a.name, err)
		}
		selects = append(selects, fmt.Sprintf(
			"SELECT e.id, %s, %s::jsonb, 1, %s FROM catalog_entities e WHERE e.reference = %s",
			lit(a.name), lit(string(raw)), lit(seedWriter), lit(a.ref)))
	}
	b.WriteString(strings.Join(selects, "\nUNION ALL "))
	b.WriteString("\nON CONFLICT (entity_id, aspect) DO UPDATE SET value = EXCLUDED.value,\n" +
		"    written_by = EXCLUDED.written_by, written_at = NOW();\n\n")
	fmt.Fprintf(b, "INSERT INTO catalog_aspect_history (entity_id, aspect, version, value, written_by)\n"+
		"SELECT a.entity_id, a.aspect, a.version, a.value, a.written_by FROM catalog_aspects a\n"+
		"WHERE a.written_by = %s\n"+
		"  AND NOT EXISTS (SELECT 1 FROM catalog_aspect_history h WHERE h.entity_id = a.entity_id AND h.aspect = a.aspect);\n",
		lit(seedWriter))
	return nil
}

// lit renders a SQL string literal.
func lit(s string) string {
	return "'" + sqlEscape(s) + "'"
}
