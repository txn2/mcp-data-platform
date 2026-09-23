package starlarkconv

import (
	"fmt"

	"go.starlark.net/starlark"
)

// ResultToStarlark converts a tool result as ToStarlark does, except that a
// tabular result -- a "rows" list beside a "columns" list naming them, which is
// what trino_query and trino_execute answer -- has each row built in column
// order (#1852). A row is a JSON object and carries no order of its own, so
// without the columns it would reach the script alphabetized, and an export
// written straight from it would move the query's columns.
func ResultToStarlark(out map[string]any) (starlark.Value, error) {
	rows, ok := out["rows"].([]any)
	names := ColumnNames(out["columns"])
	if !ok || names == nil {
		return ToStarlark(out)
	}
	rest := make(map[string]any, len(out))
	for k, v := range out {
		if k != "rows" {
			rest[k] = v
		}
	}
	converted, err := convertDict(rest, 0)
	if err != nil {
		return nil, err
	}
	dict, _ := converted.(*starlark.Dict) // convertDict returns a *starlark.Dict
	value, err := RowsToStarlark(rows, names)
	if err != nil {
		return nil, err
	}
	if err := dict.SetKey(starlark.String("rows"), value); err != nil {
		return nil, fmt.Errorf("setting rows: %w", err)
	}
	return dict, nil
}

// RowsToStarlark converts a list of rows, setting each object row's keys in
// the order columns names them. A key the columns do not name follows in
// sorted order, so nothing a row carries is dropped and every run of a script
// sees the same order.
func RowsToStarlark(rows []any, columns []string) (starlark.Value, error) {
	out := make([]starlark.Value, 0, len(rows))
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			v, err := convertToStarlark(row, 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
			continue
		}
		v, err := orderedDict(m, columns)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return starlark.NewList(out), nil
}

// orderedDict converts one row, its named columns first.
func orderedDict(m map[string]any, columns []string) (*starlark.Dict, error) {
	d := starlark.NewDict(len(m))
	for _, k := range keyOrder(m, columns) {
		v, err := convertToStarlark(m[k], 2)
		if err == nil {
			err = d.SetKey(starlark.String(k), v)
		}
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
	}
	return d, nil
}

// keyOrder is the order a row's keys are set in: the columns it carries, once
// each, in column order, then the rest sorted.
func keyOrder(m map[string]any, columns []string) []string {
	order := make([]string, 0, len(m))
	named := make(map[string]bool, len(columns))
	for _, k := range columns {
		if _, present := m[k]; present && !named[k] {
			named[k] = true
			order = append(order, k)
		}
	}
	for _, k := range SortedKeys(m) {
		if !named[k] {
			order = append(order, k)
		}
	}
	return order
}

// ColumnNames reads the column names out of a result's "columns" field: a
// list of {"name": ...} objects, or of plain names. It returns nil when the
// field is absent or any entry names nothing, and the caller then falls back
// to sorted keys rather than to a partial order.
func ColumnNames(columns any) []string {
	list, ok := columns.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	names := make([]string, 0, len(list))
	for _, c := range list {
		switch t := c.(type) {
		case string:
			names = append(names, t)
		case map[string]any:
			name, ok := t["name"].(string)
			if !ok {
				return nil
			}
			names = append(names, name)
		default:
			return nil
		}
	}
	return names
}
