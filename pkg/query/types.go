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

// Verifiable is the queryable identity behind a delivered claim: the table one
// query would settle the claim against, and the connection that table lives on.
//
// It is the delivery-side projection of TableAvailability, carrying only what a
// consumer needs to check a claim for itself rather than take it on trust. URN
// names which of a record's entities resolved, so a claim linked to several
// entities is unambiguous about the one it can be checked against.
//
// It is only ever produced for an entity a query provider reported as available,
// so its presence means "this can be checked here", not "this was checked".
type Verifiable struct {
	URN        string `json:"urn" example:"urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)"`
	QueryTable string `json:"query_table" example:"iceberg.retail.orders"`
	Connection string `json:"connection,omitempty" example:"primary"`
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
