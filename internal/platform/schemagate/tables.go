package schemagate

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/sqltables"
	"github.com/txn2/mcp-data-platform/pkg/catalog"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// connectionKindTrino is the connection kind of a Trino dataset reference.
const connectionKindTrino = "trino"

// threePartName is the length of a catalog.schema.table name.
const threePartName = 3

// tableRef is one table on one connection, resolved to three parts.
type tableRef struct {
	connection string
	catalog    string
	schema     string
	table      string
}

// fullName is the table's catalog.schema.table, lowercased: Trino folds
// unquoted identifiers, so `Orders` and `orders` are one table.
func (t tableRef) fullName() string {
	return strings.ToLower(t.catalog + "." + t.schema + "." + t.table)
}

// resolve fills a table's missing catalog and schema from the connection's
// defaults, the way the engine resolves a two- or one-part name.
func resolve(ns Namespace, t tableRef) tableRef {
	t.connection = ns.Connection
	if t.catalog == "" {
		t.catalog = ns.Catalog
	}
	if t.schema == "" {
		t.schema = ns.Schema
	}
	return t
}

// exemptTable reports a table whose columns no statement needs read first:
// the engine's own metadata tables.
func exemptTable(t tableRef) bool {
	return strings.EqualFold(t.catalog, "system") || strings.EqualFold(t.schema, "information_schema")
}

// statementTables is the tables a statement reads, resolved on the
// connection, metadata tables and table functions left out.
func statementTables(ns Namespace, stmt string) []tableRef {
	refs := sqltables.Extract(stmt)
	out := make([]tableRef, 0, len(refs))
	for _, r := range refs {
		if r.Source == "TABLE_FUNCTION" || r.Table == "" {
			continue
		}
		t := resolve(ns, tableRef{catalog: unquote(r.Catalog), schema: unquote(r.Schema), table: unquote(r.Table)})
		if t.catalog == "" || t.schema == "" || exemptTable(t) {
			continue
		}
		out = append(out, t)
	}
	return out
}

// schemaStatementPattern matches a statement that reads a table's schema:
// DESCRIBE t, DESC t, SHOW COLUMNS FROM|IN t, SHOW CREATE TABLE|VIEW t.
var schemaStatementPattern = regexp.MustCompile(
	`(?is)^\s*(?:describe|desc|show\s+columns\s+(?:from|in)|show\s+create\s+(?:table|view))\s+([A-Za-z0-9_."]+)`)

// schemaStatementTable reports whether a statement reads a table's schema, and
// which table.
func schemaStatementTable(stmt string) (tableRef, bool) {
	m := schemaStatementPattern.FindStringSubmatch(stmt)
	if m == nil {
		return tableRef{}, false
	}
	parts := splitName(m[1])
	t := tableRef{table: parts[len(parts)-1]}
	if len(parts) > 1 {
		t.schema = parts[len(parts)-2]
	}
	if len(parts) >= threePartName {
		t.catalog = parts[len(parts)-threePartName]
	}
	return t, true
}

// splitName splits a dotted, possibly quoted, table name into its parts.
func splitName(name string) []string {
	parts := strings.Split(name, ".")
	for i, p := range parts {
		parts[i] = unquote(p)
	}
	return parts
}

func unquote(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"`)
}

// datasetTable reads a Trino table out of an mcp:dataset reference.
func datasetTable(reference string) (tableRef, bool) {
	r, err := catalog.ParseRef(reference)
	if err != nil || r.Kind != catalog.KindDataset || r.ConnectionKind != connectionKindTrino || len(r.Path) != threePartName {
		return tableRef{}, false
	}
	return tableRef{connection: r.Connection, catalog: r.Path[0], schema: r.Path[1], table: r.Path[2]}, true
}

// fetchFound reports whether a fetch result resolved its reference: a
// reference fetch could not resolve is a normal answer with found false, and
// read nothing.
func fetchFound(res mcp.Result) bool {
	var out struct {
		Found bool `json:"found"`
	}
	return decodeStructured(res, &out) && out.Found
}

// schemaErrorNames are the engine error names a wrong column or type produces:
// the failures the table's real columns answer.
var schemaErrorNames = map[string]bool{
	"COLUMN_NOT_FOUND":         true,
	"TYPE_MISMATCH":            true,
	"AMBIGUOUS_NAME":           true,
	"MISSING_COLUMN_NAME":      true,
	"INVALID_COLUMN_REFERENCE": true,
	"FUNCTION_NOT_FOUND":       true, // "Unexpected parameters (varchar) for function ..."
}

// isSchemaError reports a failed SQL tool result whose engine error is one of
// schemaErrorNames, read from the classification the Trino tools put on
// structuredContent.error. A timeout, an access denial or an outage is not.
func isSchemaError(res mcp.Result) bool {
	cr, ok := res.(*mcp.CallToolResult)
	if !ok || cr == nil || !cr.IsError {
		return false
	}
	var out struct {
		Error *struct {
			Trino *struct {
				ErrorName string `json:"error_name"`
			} `json:"trino"`
		} `json:"error"`
	}
	if !decodeStructured(res, &out) || out.Error == nil || out.Error.Trino == nil {
		return false
	}
	return schemaErrorNames[out.Error.Trino.ErrorName]
}

// decodeStructured decodes a tool result's structuredContent into dst.
func decodeStructured(res mcp.Result, dst any) bool {
	cr, ok := res.(*mcp.CallToolResult)
	if !ok || cr == nil || cr.StructuredContent == nil {
		return false
	}
	raw, err := json.Marshal(cr.StructuredContent)
	if err != nil {
		return false
	}
	return json.Unmarshal(raw, dst) == nil
}

// describedTable is a table with its columns read.
type describedTable struct {
	ref     tableRef
	columns []Column
}

// describeAll reads each table's columns at once, adds the catalog's column
// curation, and returns the tables that could be read, in statement order.
func (g *Gate) describeAll(ctx context.Context, tables []tableRef) []describedTable {
	results := make([]*describedTable, len(tables))
	var wg sync.WaitGroup
	for i, t := range tables {
		wg.Go(func() {
			cols, err := g.cfg.Describer.Describe(ctx, t.connection, t.catalog, t.schema, t.table)
			if err != nil || len(cols) == 0 {
				return
			}
			g.addCuration(ctx, t, cols)
			results[i] = &describedTable{ref: t, columns: cols}
		})
	}
	wg.Wait()
	out := make([]describedTable, 0, len(results))
	for _, r := range results {
		if r != nil {
			out = append(out, *r)
		}
	}
	return out
}

// addCuration fills each column's catalog description and glossary terms.
// The catalog is optional context: without it the engine's columns stand.
func (g *Gate) addCuration(ctx context.Context, t tableRef, cols []Column) {
	if g.cfg.Semantic == nil {
		return
	}
	curated, err := g.cfg.Semantic.GetColumnsContext(ctx, semantic.TableIdentifier{Catalog: t.catalog, Schema: t.schema, Table: t.table})
	if err != nil || len(curated) == 0 {
		return
	}
	for i := range cols {
		cc, ok := curated[cols[i].Name]
		if !ok || cc == nil {
			continue
		}
		cols[i].Description = cc.Description
		for _, term := range cc.GlossaryTerms {
			if term.Name != "" {
				cols[i].GlossaryTerms = append(cols[i].GlossaryTerms, term.Name)
			}
		}
	}
}

func joinNames(tables []describedTable) string {
	names := make([]string, len(tables))
	for i, t := range tables {
		names[i] = t.ref.fullName()
	}
	return strings.Join(names, ", ")
}
