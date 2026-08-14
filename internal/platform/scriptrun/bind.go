package scriptrun

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"go.starlark.net/starlark"
)

// maxBindListLen bounds an IN-list binding. Past this, the set belongs in a
// table the query joins against, not in the text of the statement.
const maxBindListLen = 1000

// Number base and bit size used when rendering a bound value as a SQL literal.
const (
	decimalBase = 10
	bitSize64   = 64
)

// bindSQL substitutes :name placeholders in sql with SQL literals rendered from
// params, and returns the statement to execute.
//
// This exists so a script never has to build SQL by concatenation. An author
// who writes `"... WHERE region = '" + region + "'"` has written a statement
// whose meaning depends on the value — the classic mistake, and one an agent
// authoring under time pressure makes readily. Binding here renders each value
// according to its own type, with quoting the value cannot escape, so a region
// named `x' OR '1'='1` is a region name and nothing else.
//
// Substitution is state-aware: a `:name` inside a string literal, inside a
// quoted identifier, or inside a comment is text, not a placeholder, and `::`
// is a cast rather than the start of one. Getting that wrong in either
// direction is a correctness bug (a rewritten literal) or a security bug (an
// unbound placeholder reaching the engine).
//
// Every placeholder must have a value and every value must be used: an unbound
// placeholder would reach the query engine as syntax, and an unused value is
// nearly always a typo in one name or the other.
func bindSQL(sql string, params *starlark.Dict) (string, error) {
	values, err := bindValues(params)
	if err != nil {
		return "", err
	}
	var (
		out  strings.Builder
		used = make(map[string]bool, len(values))
	)
	out.Grow(len(sql))

	for i := 0; i < len(sql); {
		if skipped, next := skipNonCode(sql, i); next > i {
			out.WriteString(skipped)
			i = next
			continue
		}
		name, next := placeholderAt(sql, i)
		if name == "" {
			out.WriteByte(sql[i])
			i++
			continue
		}
		v, ok := values[name]
		if !ok {
			return "", fmt.Errorf("placeholder :%s has no bound value; pass it in params", name)
		}
		literal, err := sqlLiteral(v)
		if err != nil {
			return "", fmt.Errorf("parameter %q: %w", name, err)
		}
		out.WriteString(literal)
		used[name] = true
		i = next
	}

	for _, name := range sortedBindNames(values) {
		if !used[name] {
			return "", fmt.Errorf("params has %q but the SQL has no :%s placeholder", name, name)
		}
	}
	return out.String(), nil
}

// bindValues converts the Starlark params dict into Go values keyed by name.
func bindValues(params *starlark.Dict) (map[string]any, error) {
	values := map[string]any{}
	if params == nil {
		return values, nil
	}
	for _, item := range params.Items() {
		key, ok := starlark.AsString(item[0])
		if !ok {
			return nil, fmt.Errorf("params keys must be strings, got %s", item[0].Type())
		}
		v, err := fromStarlark(item[1])
		if err != nil {
			return nil, fmt.Errorf("params[%q]: %w", key, err)
		}
		values[key] = v
	}
	return values, nil
}

// sortedBindNames returns the bound names in sorted order, so the "unused
// parameter" error names the same one every time.
func sortedBindNames(values map[string]any) []string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// skipNonCode returns the run of text starting at i that must be copied
// verbatim — a string literal, a quoted identifier, a line comment, or a block
// comment — along with the index just past it. It returns "", i when position i
// is ordinary code.
//
// An unterminated literal or comment consumes the rest of the statement, which
// is the safe reading: whatever follows is inside it, so nothing there can be
// mistaken for a placeholder. The engine rejects the statement on its own.
func skipNonCode(sql string, i int) (text string, next int) {
	switch {
	case sql[i] == '\'':
		return scanQuoted(sql, i, '\'')
	case sql[i] == '"':
		return scanQuoted(sql, i, '"')
	case strings.HasPrefix(sql[i:], "::"):
		// A doubled colon is a cast operator, not the start of a placeholder.
		// Consuming BOTH characters here is what makes it safe: emitting only
		// the first would leave the scanner sitting on the second, where
		// `x::varchar` reads as a `:varchar` placeholder.
		return "::", i + 2
	case strings.HasPrefix(sql[i:], "--"):
		if end := strings.IndexByte(sql[i:], '\n'); end >= 0 {
			return sql[i : i+end], i + end
		}
		return sql[i:], len(sql)
	case strings.HasPrefix(sql[i:], "/*"):
		if end := strings.Index(sql[i+2:], "*/"); end >= 0 {
			stop := i + 2 + end + 2
			return sql[i:stop], stop
		}
		return sql[i:], len(sql)
	default:
		return "", i
	}
}

// scanQuoted consumes a quoted run starting at i, honoring the SQL doubling
// convention (a doubled quote character inside a quoted run) as an escaped
// quote rather than a terminator.
func scanQuoted(sql string, i int, quote byte) (text string, next int) {
	j := i + 1
	for j < len(sql) {
		if sql[j] != quote {
			j++
			continue
		}
		if j+1 < len(sql) && sql[j+1] == quote {
			j += 2
			continue
		}
		return sql[i : j+1], j + 1
	}
	return sql[i:], len(sql)
}

// placeholderAt reports the placeholder name starting at i and the index just
// past it, or "" when i does not begin one. Cast operators never reach it:
// skipNonCode consumes them first, and a colon followed by another colon fails
// the name-start check here regardless.
func placeholderAt(sql string, i int) (name string, next int) {
	if sql[i] != ':' {
		return "", i
	}
	j := i + 1
	if j >= len(sql) || !isNameStart(sql[j]) {
		return "", i
	}
	for j < len(sql) && isNameByte(sql[j]) {
		j++
	}
	return sql[i+1 : j], j
}

// sqlNumberLiteral renders a bound numeric value.
func sqlNumberLiteral(v any) (string, error) {
	switch t := v.(type) {
	case int64:
		return strconv.FormatInt(t, decimalBase), nil
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return "", fmt.Errorf("the value %v has no SQL literal form", t)
		}
		return strconv.FormatFloat(t, 'g', -1, bitSize64), nil
	default:
		return "", fmt.Errorf("values of type %T cannot be bound into SQL", v)
	}
}

// isNameStart reports whether c may begin a placeholder name.
func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// isNameByte reports whether c may continue a placeholder name.
func isNameByte(c byte) bool { return isNameStart(c) || (c >= '0' && c <= '9') }

// sqlLiteral renders one bound value as a SQL literal. The set of accepted
// types is closed on purpose: anything richer than a scalar or a list of
// scalars has no unambiguous literal form, and inventing one would put the
// platform in the business of guessing what an author meant.
func sqlLiteral(v any) (string, error) {
	switch t := v.(type) {
	case nil:
		return "NULL", nil
	case bool:
		if t {
			return "TRUE", nil
		}
		return "FALSE", nil
	case string:
		return quoteSQLString(t)
	case []any:
		return sqlList(t)
	default:
		return sqlNumberLiteral(v)
	}
}

// quoteSQLString renders a string as a single-quoted SQL literal, doubling
// embedded quotes. A NUL byte is refused rather than escaped: it terminates the
// statement in some client protocols, so there is no rendering of it that is
// safe everywhere.
func quoteSQLString(s string) (string, error) {
	if strings.ContainsRune(s, 0) {
		return "", errors.New("string contains a NUL byte and cannot be bound")
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'", nil
}

// sqlList renders a list of scalars as a parenthesized list, for IN clauses.
// Supporting it is the point: without a bound list an author builds one by
// joining strings, which is the concatenation this whole path exists to remove.
func sqlList(items []any) (string, error) {
	if len(items) == 0 {
		return "", errors.New("an empty list has no SQL literal form; branch on the empty case in the script")
	}
	if len(items) > maxBindListLen {
		return "", fmt.Errorf("list of %d values exceeds the %d-value bind limit; join against a table instead",
			len(items), maxBindListLen)
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		if _, nested := item.([]any); nested {
			return "", errors.New("a bound list may not contain lists")
		}
		lit, err := sqlLiteral(item)
		if err != nil {
			return "", err
		}
		parts = append(parts, lit)
	}
	return "(" + strings.Join(parts, ", ") + ")", nil
}
