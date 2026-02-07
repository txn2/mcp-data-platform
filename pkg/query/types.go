// Package query provides abstractions for query execution providers.
//
//nolint:revive // package contains related DTO types
package query

// TableIdentifier uniquely identifies a table in the query engine.
type TableIdentifier struct {
	Catalog    string `json:"catalog,omitempty"`
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Connection string `json:"connection,omitempty"`
}

// String returns a dot-separated representation.
func (t TableIdentifier) String() string {
	if t.Catalog != "" {
		return t.Catalog + "." + t.Schema + "." + t.Table
	}
	return t.Schema + "." + t.Table
}

// TableAvailability indicates if a table is queryable.
type TableAvailability struct {
	Available     bool   `json:"available"`
	QueryTable    string `json:"query_table,omitempty"`
	Connection    string `json:"connection,omitempty"`
	EstimatedRows *int64 `json:"estimated_rows,omitempty"`
	Error         string `json:"error,omitempty"`
}

// Example provides a sample query for a table.
type Example struct {
	Description string `json:"description"`
	SQL         string `json:"sql"`
}

// ExecutionContext provides context for executing queries against multiple tables.
type ExecutionContext struct {
	Tables      []TableInfo `json:"tables"`
	Connections []string    `json:"connections"`
}

// TableInfo provides information about a queryable table.
type TableInfo struct {
	URN           string `json:"urn"`
	QueryTable    string `json:"query_table"`
	Connection    string `json:"connection"`
	EstimatedRows *int64 `json:"estimated_rows,omitempty"`
}

// Column represents a table column.
type Column struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Comment  string `json:"comment,omitempty"`
}

// TableSchema represents the schema of a table.
type TableSchema struct {
	Columns    []Column `json:"columns"`
	PrimaryKey []string `json:"primary_key,omitempty"`
}

// Result represents the result of a query.
type Result struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Count   int      `json:"count"`
}
