package appdbhealth

import (
	"context"
	"strconv"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// QueryIDReader reads the pg_stat_statements query id of every statement the
// platform runs on an application database, and of each of extra (#2536):
// the postgres toolkit's PlatformQueryIDs. A Querier that is one has the
// report leave the platform's statements out of the ranking by id.
type QueryIDReader interface {
	PlatformQueryIDs(ctx context.Context, connection string, extra []string) ([]string, error)
}

// Querier runs one read-only statement on an application database: the
// postgres toolkit's Query, which runs it as the connection's own login in a
// READ ONLY transaction when the context carries no persona.
type Querier interface {
	Query(ctx context.Context, s postgres.Statement) (*postgres.Result, error)
}

// A bigint past 2^53 arrives as its decimal text.
const (
	decimal   = 10
	int64Bits = 64
)

// row is one result row keyed by column name.
type row map[string]any

// query runs sql on connection with params bound to $1, $2, ... and returns
// its rows keyed by column.
func query(ctx context.Context, q Querier, connection, sql string, params ...any) ([]row, error) {
	s := postgres.Statement{Connection: connection, SQL: sql}
	if len(params) > 0 {
		s.Params = params
	}
	res, err := q.Query(ctx, s)
	if err != nil {
		return nil, err //nolint:wrapcheck // the toolkit's value-free summary is the text
	}
	out := make([]row, 0, len(res.Rows))
	for _, values := range res.Rows {
		r := make(row, len(res.Columns))
		for i, c := range res.Columns {
			if i < len(values) {
				r[c.Name] = values[i]
			}
		}
		out = append(out, r)
	}
	return out, nil
}

// str is a text column, "" when null.
func (r row) str(name string) string {
	s, _ := r[name].(string)
	return s
}

// num is an integer column, 0 when null. A bigint past 2^53 arrives as its
// decimal text.
func (r row) num(name string) int64 {
	switch v := r[name].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, decimal, int64Bits)
		return n
	}
	return 0
}

// float is a double precision column, 0 when null.
func (r row) float(name string) float64 {
	switch v := r[name].(type) {
	case float64:
		return v
	case int64:
		return float64(v)
	}
	return 0
}

// flag is a boolean column, false when null.
func (r row) flag(name string) bool {
	b, _ := r[name].(bool)
	return b
}

// strs is a text[] column, [] when null.
func (r row) strs(name string) []string {
	vs, _ := r[name].([]any)
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// nums is an integer[] column, [] when null.
func (r row) nums(name string) []int64 {
	vs, _ := r[name].([]any)
	out := make([]int64, 0, len(vs))
	for _, v := range vs {
		out = append(out, row{"v": v}.num("v"))
	}
	return out
}

// The postgres toolkit reads the query ids a report leaves out.
var _ QueryIDReader = (*postgres.Toolkit)(nil)
