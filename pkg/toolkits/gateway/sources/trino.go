// Package sources provides concrete enrichment Source adapters for the
// platform's built-in toolkits (Trino, DataHub). Each adapter is a thin
// shim that satisfies the enrichment.Source interface and delegates to a
// caller-supplied client function so the underlying SDK version stays a
// detail of platform startup, not of this package.
package sources

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/gateway/enrichment"
)

// TrinoQueryFunc executes a SQL query against a named Trino connection
// and returns the row set. Implementations are platform-side.
type TrinoQueryFunc func(ctx context.Context, connection, sql string) ([]map[string]any, error)

// TrinoSource exposes a single read-only "query" operation that runs a
// SQL template against a named Trino connection. The :name placeholders
// in the template are substituted with **escaped ANSI-SQL literals**
// derived from the binding values — single quotes are doubled, NULL is
// emitted for nil, ints/floats/bools are rendered as literals. This is
// not server-side parameter binding (the Trino Go client does not expose
// a parameterized exec path on our existing query interface); the
// escaping is sound for the supported value types but identifiers are
// not substitutable.
type TrinoSource struct {
	exec TrinoQueryFunc
}

// NewTrinoSource builds a Source backed by the given query function.
func NewTrinoSource(exec TrinoQueryFunc) *TrinoSource {
	return &TrinoSource{exec: exec}
}

// Name returns the canonical source name "trino".
func (*TrinoSource) Name() string { return enrichment.SourceTrino }

// Operations returns the read-only operation allowlist.
func (*TrinoSource) Operations() []string { return []string{"query"} }

// Execute runs the requested operation. Recognized parameters for "query":
//
//	connection    string  required — name of a configured Trino connection
//	sql_template  string  required — SQL with optional :name placeholders
//
// Any additional parameters are treated as bindings and substituted into
// :<name> placeholders. Strings are single-quoted with embedded quotes
// doubled per ANSI SQL; numbers and bools are rendered as literals; nil
// becomes NULL. Other types are rejected.
func (s *TrinoSource) Execute(ctx context.Context, op string, params map[string]any) (any, error) {
	if op != "query" {
		return nil, fmt.Errorf("trino: operation %q not supported", op)
	}
	connection, err := requireString(params, "connection")
	if err != nil {
		return nil, err
	}
	tmpl, err := requireString(params, "sql_template")
	if err != nil {
		return nil, err
	}
	sql, err := renderSQL(tmpl, params)
	if err != nil {
		return nil, err
	}
	rows, err := s.exec(ctx, connection, sql)
	if err != nil {
		return nil, fmt.Errorf("trino: %w", err)
	}
	return rows, nil
}

// requireString fetches a string parameter or returns a clear error.
func requireString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok {
		return "", fmt.Errorf("missing required parameter %q", key)
	}
	s, ok := v.(string)
	if !ok || s == "" {
		return "", fmt.Errorf("parameter %q must be a non-empty string", key)
	}
	return s, nil
}

// renderSQL replaces :name placeholders in tmpl with safely-quoted literal
// values from params. Reserved parameter keys (connection, sql_template)
// are skipped. Unknown :name placeholders cause a clear error so misnamed
// bindings don't silently leave injection holes.
//
// Placeholder matching is greedy on identifier characters so :i and :i64
// are distinct bindings (a naive ReplaceAll would chew :i out of :i64).
func renderSQL(tmpl string, params map[string]any) (string, error) {
	skip := map[string]bool{"connection": true, "sql_template": true}
	literals := make(map[string]string, len(params))
	for k, v := range params {
		if skip[k] {
			continue
		}
		lit, err := sqlLiteral(v)
		if err != nil {
			return "", fmt.Errorf("binding %q: %w", k, err)
		}
		literals[k] = lit
	}

	var (
		out      strings.Builder
		i        int
		inString bool
	)
	for i < len(tmpl) {
		ch := tmpl[i]
		if ch == '\'' {
			i = handleQuote(&out, tmpl, i, &inString)
			continue
		}
		if inString || ch != ':' {
			out.WriteByte(ch)
			i++
			continue
		}
		next, err := substitutePlaceholder(&out, tmpl, i, literals)
		if err != nil {
			return "", err
		}
		i = next
	}
	return out.String(), nil
}

// handleQuote toggles string-literal state and writes the quote, handling
// the SQL ” escape (a doubled quote inside a string is a literal quote).
func handleQuote(out *strings.Builder, tmpl string, i int, inString *bool) int {
	out.WriteByte('\'')
	i++
	if *inString && i < len(tmpl) && tmpl[i] == '\'' {
		out.WriteByte('\'')
		return i + 1
	}
	*inString = !*inString
	return i
}

// substitutePlaceholder reads :ident at tmpl[i:] and writes its literal
// from the bindings map. Returns the index past the placeholder.
func substitutePlaceholder(out *strings.Builder, tmpl string, i int, literals map[string]string) (int, error) {
	j := i + 1
	for j < len(tmpl) && isIdentByte(tmpl[j]) {
		j++
	}
	if j == i+1 {
		out.WriteByte(':')
		return i + 1, nil
	}
	name := tmpl[i+1 : j]
	lit, ok := literals[name]
	if !ok {
		return 0, fmt.Errorf("unresolved placeholder %q", ":"+name)
	}
	out.WriteString(lit)
	return j, nil
}

// sqlLiteral renders a Go value as an ANSI-SQL literal that's safe to
// concatenate into a query. Returns an error for unsupported types so
// the source never produces a query containing untyped data.
//
// Hardening:
//   - Strings containing a NUL byte are rejected (Trino's behavior with
//     embedded NUL is undefined; cleaner to refuse).
//   - NaN and Inf floats are rejected (they format as "NaN"/"+Inf"
//     which Trino can't parse, producing an opaque error at runtime;
//     fail fast at binding time instead).
func sqlLiteral(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "NULL", nil
	case string:
		if strings.ContainsRune(x, 0) {
			return "", errors.New("string binding contains a NUL byte")
		}
		return "'" + strings.ReplaceAll(x, "'", "''") + "'", nil
	case bool:
		if x {
			return "TRUE", nil
		}
		return "FALSE", nil
	case int, int32, int64:
		return fmt.Sprintf("%d", x), nil
	case float32:
		return formatFloatLiteral(float64(x))
	case float64:
		return formatFloatLiteral(x)
	}
	return "", errors.New("unsupported binding type")
}

// formatFloatLiteral rejects NaN/Inf and otherwise renders the value
// in shortest decimal form Trino can parse.
func formatFloatLiteral(f float64) (string, error) {
	if math.IsNaN(f) {
		return "", errors.New("float binding is NaN")
	}
	if math.IsInf(f, 0) {
		return "", errors.New("float binding is +Inf or -Inf")
	}
	return fmt.Sprintf("%g", f), nil
}

// isIdentByte reports whether b can be part of a SQL identifier
// (used to scan :name placeholders).
func isIdentByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || b == '_' ||
		(b >= '0' && b <= '9')
}
