package scripttx

import (
	"fmt"
	"slices"

	"go.starlark.net/starlark"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptfail"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptout"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsql"
	"github.com/txn2/mcp-data-platform/internal/platform/starlarkconv"
)

// The members of #2453.
const (
	CapabilityCopyIn  = "platform.copy_in"
	CapabilityCopyOut = "platform.copy_out"
)

// CopyIn is platform.copy_in(connection, table, columns, rows, on_error =
// ""): rows written into table's columns through COPY, in a transaction of
// their own, and the number written. A row is a list in columns' order or a
// dict keyed by column.
func (h *Host) CopyIn(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var connection string
	call, returnFailure, err := copyInCall(b, args, kwargs, &connection)
	if err != nil {
		return nil, err
	}
	call[argConnection] = connection
	return h.copied(b, call, returnFailure, nil)
}

// copyIn is tx.copy_in(table, columns, rows, on_error = ""): the same in the
// transaction, committed with it.
func (t *txValue) copyIn(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	call, returnFailure, err := copyInCall(b, args, kwargs, nil)
	if err != nil {
		return nil, err
	}
	if err := t.usable(b); err != nil {
		return nil, err
	}
	call[argTransaction] = t.id
	return t.host.copied(b, call, returnFailure, t)
}

func (h *Host) copied(b *starlark.Builtin, call map[string]any, returnFailure bool, tx *txValue) (starlark.Value, error) {
	out, err := h.Invoke(b, scriptsession.AppDBCopyIn, call)
	if err != nil {
		if tx != nil {
			tx.note(err)
		}
		return h.Failed(b, err, returnFailure)
	}
	return starlark.MakeInt64(count(out["rows_written"])), nil
}

// count reads a count from an answer: an int64 from the database, a float64
// once a recording's JSON has carried it to a replay.
func count(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	case float64:
		return int64(n)
	}
	return 0
}

// copyInCall reads copy_in's arguments into its call.
func copyInCall(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple, connection *string) (call map[string]any, returnFailure bool, err error) {
	var (
		table, onError string
		columns, rows  *starlark.List
		pairs          []any
	)
	if connection != nil {
		pairs = append(pairs, argConnection, connection)
	}
	pairs = append(pairs, "table", &table, "columns", &columns, "rows", &rows, argOnError, &onError)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, pairs...); err != nil {
		return nil, false, fmt.Errorf(inBinding, b.Name(), err)
	}
	returnFailure, err = scriptfail.OnError(b.Name(), onError)
	if err != nil {
		return nil, false, err //nolint:wrapcheck // names the binding
	}
	names, err := columnNames(columns)
	if err != nil {
		return nil, false, fmt.Errorf(inBinding, b.Name(), err)
	}
	values, err := rowValues(rows, names)
	if err != nil {
		return nil, false, fmt.Errorf(inBinding, b.Name(), err)
	}
	cols := make([]any, len(names))
	for i, n := range names {
		cols[i] = n
	}
	return map[string]any{"table": table, "columns": cols, "rows": values}, returnFailure, nil
}

func columnNames(list *starlark.List) ([]string, error) {
	out := make([]string, 0, list.Len())
	for i := range list.Len() {
		s, ok := list.Index(i).(starlark.String)
		if !ok {
			return nil, fmt.Errorf("columns[%d] is a %s; columns is a list of column names", i, list.Index(i).Type())
		}
		out = append(out, string(s))
	}
	return out, nil
}

// rowValues reads each row into a list in column order: a list as it is, a
// dict by column name.
func rowValues(rows *starlark.List, columns []string) ([]any, error) {
	out := make([]any, rows.Len())
	for i := range rows.Len() {
		v, err := starlarkconv.FromStarlark(rows.Index(i))
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", i+1, err)
		}
		switch r := v.(type) {
		case []any:
			out[i] = r
		case map[string]any:
			if out[i], err = dictRow(i, r, columns); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("row %d is a %s; a row is a list in columns' order or a dict keyed by column", i+1, rows.Index(i).Type())
		}
	}
	return out, nil
}

// dictRow is one dict row in column order, refusing a dict that lacks a
// column copy_in names or names one it does not.
func dictRow(i int, r map[string]any, columns []string) ([]any, error) {
	row := make([]any, len(columns))
	for j, c := range columns {
		val, ok := r[c]
		if !ok {
			return nil, fmt.Errorf("row %d has no %q; a dict row holds every column copy_in names", i+1, c)
		}
		row[j] = val
	}
	for k := range r {
		if !slices.Contains(columns, k) {
			return nil, fmt.Errorf("row %d has %q, which columns does not name", i+1, k)
		}
	}
	return row, nil
}

// exportKwargs are platform.export's arguments copy_out passes on, so its
// output goes where an export's would.
var exportKwargs = []string{"destination", "key", "register", "tags", "metadata", "scope", "scope_id", "index"}

// CopyOut is platform.copy_out(connection, name, sql, params = None, format =
// "csv", timeout_seconds = 0, on_error = "", and platform.export's
// destination, key, register, tags, metadata, scope, scope_id and index): the
// query's rows written as an output named name, in CSV with a header or in
// COPY's text format, every row of it however many max_rows allows a query,
// streamed from the database to the output and never held as the script's
// values.
func (h *Host) CopyOut(thread *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		connection, name, sql, format, onError string
		params                                 *starlark.Dict
		timeout                                int
	)
	own, passed := splitKwargs(kwargs)
	if err := starlark.UnpackArgs(b.Name(), args, own, argConnection, &connection, "name", &name, argSQL, &sql,
		"params?", &params, "format?", &format, "timeout_seconds?", &timeout, argOnError, &onError); err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	returnFailure, err := scriptfail.OnError(b.Name(), onError)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the binding
	}
	if format == "" {
		format = "csv"
	}
	if format != "csv" && format != scriptout.CopyFormatText {
		return nil, fmt.Errorf("in %s: format %q is not csv or text", b.Name(), format)
	}
	positional, values, err := scriptsql.Positional(sql, params)
	if err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	call := map[string]any{argConnection: connection, argSQL: positional, "format": format, "max_bytes": scriptout.MaxBytes}
	if len(values) > 0 {
		call["params"] = values
	}
	if timeout != 0 {
		call["timeout_seconds"] = timeout
	}
	out, err := h.Invoke(b, scriptsession.AppDBCopyOut, call)
	if err != nil {
		return h.Failed(b, err, returnFailure)
	}
	data, _ := out["data"].([]byte)
	content := &scriptout.Copied{Data: data, Rows: int(count(out["rows"])), Format: format}
	passed = append(passed, starlark.Tuple{starlark.String("format"), starlark.String(format)})
	return starlark.Call(thread, h.Export, starlark.Tuple{starlark.String(name), content}, passed) //nolint:wrapcheck // the export names itself
}

// splitKwargs separates the export arguments copy_out passes on from its own.
func splitKwargs(kwargs []starlark.Tuple) (own, passed []starlark.Tuple) {
	for _, kv := range kwargs {
		if k, ok := kv[0].(starlark.String); ok && slices.Contains(exportKwargs, string(k)) {
			passed = append(passed, kv)
			continue
		}
		own = append(own, kv)
	}
	return own, passed
}
