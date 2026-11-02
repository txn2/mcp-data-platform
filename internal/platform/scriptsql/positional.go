package scriptsql

import (
	"fmt"
	"strconv"
	"strings"

	"go.starlark.net/starlark"
)

// Positional rewrites a statement for an application database (#2428): each
// :name placeholder becomes $n, the same name the same n, and the values are
// returned in that order to be bound natively by the database. No value is
// written into the SQL, which is the difference from Bind.
//
// What is skipped is what Bind skips (string literals, quoted identifiers,
// comments, the :: cast) plus PostgreSQL's dollar-quoted strings, so a
// function body in a statement keeps its text. A list binds as one array
// parameter: `= ANY(:ids)`, not `IN :ids`, which a list cannot fill as a single
// parameter. A table record a register= made is a Trino table and binds
// nowhere here.
func Positional(sql string, params *starlark.Dict) (rewritten string, args []any, err error) {
	values, err := bindValues(params)
	if err != nil {
		return "", nil, err
	}
	var (
		out   strings.Builder
		order = make(map[string]int, len(values))
	)
	out.Grow(len(sql))
	for i := 0; i < len(sql); {
		if skipped, next := skipPostgresNonCode(sql, i); next > i {
			_, _ = out.WriteString(skipped)
			i = next
			continue
		}
		name, next := placeholderAt(sql, i)
		if name == "" {
			_ = out.WriteByte(sql[i])
			i++
			continue
		}
		n, err := positionOf(name, values, order, &args, precededByIn(sql, i))
		if err != nil {
			return "", nil, err
		}
		_, _ = out.WriteString("$" + strconv.Itoa(n))
		i = next
	}
	for _, name := range sortedBindNames(values) {
		if _, used := order[name]; !used {
			return "", nil, fmt.Errorf("params has %q but the SQL has no :%s placeholder", name, name)
		}
	}
	return out.String(), args, nil
}

// positionOf is the parameter number name binds, assigning the next one on its
// first use.
func positionOf(name string, values map[string]any, order map[string]int, args *[]any, afterIn bool) (int, error) {
	if n, ok := order[name]; ok {
		return n, nil
	}
	v, ok := values[name]
	if !ok {
		return 0, fmt.Errorf("placeholder :%s has no bound value; pass it in params", name)
	}
	switch v.(type) {
	case []any:
		if afterIn {
			return 0, fmt.Errorf("parameter %q is a list after IN; on an application database write = ANY(:%s), which binds the list as one array", name, name)
		}
	case map[string]any:
		return 0, fmt.Errorf("parameter %q is a table record; a registered table is a Trino table and is not on an application database", name)
	}
	*args = append(*args, v)
	order[name] = len(*args)
	return len(*args), nil
}

// precededByIn reports whether the last word before position i is IN.
func precededByIn(sql string, i int) bool {
	before := strings.TrimRight(sql[:i], " \t\r\n(")
	return len(before) >= 2 && strings.EqualFold(before[len(before)-2:], "in") &&
		(len(before) == 2 || !isNameByte(before[len(before)-3]))
}

// skipPostgresNonCode is skipNonCode plus PostgreSQL's dollar-quoted string:
// $$...$$ or $tag$...$tag$.
func skipPostgresNonCode(sql string, i int) (text string, next int) {
	if sql[i] == '$' {
		if tag, ok := dollarTag(sql, i); ok {
			if end := strings.Index(sql[i+len(tag):], tag); end >= 0 {
				stop := i + len(tag) + end + len(tag)
				return sql[i:stop], stop
			}
			return sql[i:], len(sql)
		}
	}
	return skipNonCode(sql, i)
}

// dollarTag reads the opening tag of a dollar-quoted string at i: $$ or $name$.
// A $1 parameter is not one.
func dollarTag(sql string, i int) (string, bool) {
	j := i + 1
	if j < len(sql) && sql[j] == '$' {
		return "$$", true
	}
	if j >= len(sql) || !isNameStart(sql[j]) {
		return "", false
	}
	for j < len(sql) && isNameByte(sql[j]) {
		j++
	}
	if j < len(sql) && sql[j] == '$' {
		return sql[i : j+1], true
	}
	return "", false
}
