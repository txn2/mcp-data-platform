package postgres

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// MaxParams is the most parameters one statement may bind. PostgreSQL's own
// limit is 65535; a statement binding more than this is not one a script
// author wrote.
const MaxParams = 1000

// errParamsShape is the refusal of params that is neither a list nor a map.
var errParamsShape = errors.New("params must be a list (bound to $1, $2, ...) or a map (bound to @name)")

// boundStatement is one statement with its parameters in the order the
// positional SQL binds them, still as the values the caller passed.
type boundStatement struct {
	sql  string
	args []any
}

// bindValues returns the statement in positional form.
//
// A list binds $1, $2, ... in order. A map binds @name placeholders, which pgx
// rewrites to positional ones before the statement is sent; a placeholder the
// map does not name, or a name no placeholder uses, is refused rather than
// bound as NULL. Either way the values are sent as bound parameters and never
// written into the SQL text.
func bindValues(ctx context.Context, sql string, params any) (boundStatement, error) {
	switch t := params.(type) {
	case nil:
		return boundStatement{sql: sql}, nil
	case []any:
		if len(t) > MaxParams {
			return boundStatement{}, fmt.Errorf("params: at most %d values", MaxParams)
		}
		return boundStatement{sql: sql, args: t}, nil
	case map[string]any:
		if len(t) > MaxParams {
			return boundStatement{}, fmt.Errorf("params: at most %d values", MaxParams)
		}
		newSQL, args, err := pgx.StrictNamedArgs(t).RewriteQuery(ctx, nil, sql, nil)
		if err != nil {
			return boundStatement{}, fmt.Errorf("params: %w", err)
		}
		return boundStatement{sql: newSQL, args: args}, nil
	default:
		return boundStatement{}, errParamsShape
	}
}

// encodeArgs turns each JSON value into what pgx sends for the parameter type
// PostgreSQL described. Almost every value is sent as text for the server to
// parse by the parameter's own type, which keeps a numeric's every digit and
// lets one JSON spelling serve every type that reads it:
//
//   - a number, a string or a bool is its text
//   - json and jsonb take any value, encoded as JSON (an object, a list, a
//     string "abc" as the JSON string "abc")
//   - an array type takes a list, written as an array literal, or a string the
//     caller already wrote as one
//   - bytea takes base64, the form the result returns it in
//   - null is NULL
func encodeArgs(args []any, types []typeInfo) ([]any, error) {
	out := make([]any, len(args))
	for i, a := range args {
		v, err := encodeArg(a, types[i])
		if err != nil {
			return nil, fmt.Errorf("params: $%d (%s): %w", i+1, types[i].display, err)
		}
		out[i] = v
	}
	return out, nil
}

func encodeArg(a any, ti typeInfo) (any, error) {
	if a == nil {
		return nil, nil //nolint:nilnil // a JSON null binds SQL NULL, which is a nil argument
	}
	switch {
	case ti.name == "json" || ti.name == "jsonb":
		return jsonText(a)
	case ti.name == "bytea":
		s, ok := a.(string)
		if !ok {
			return nil, errors.New("bytea takes a base64 string")
		}
		b, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return nil, errors.New("bytea takes a base64 string")
		}
		return `\x` + hex.EncodeToString(b), nil
	case ti.array:
		if s, ok := a.(string); ok {
			return s, nil
		}
		list, ok := a.([]any)
		if !ok {
			return nil, errors.New("an array parameter takes a list")
		}
		return arrayLiteral(list)
	}
	return scalarText(a)
}

// scalarText is a scalar's text, or a document's JSON for a composite type.
func scalarText(a any) (any, error) {
	switch t := a.(type) {
	case string:
		return t, nil
	case json.Number:
		return t.String(), nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	}
	return jsonText(a)
}

func jsonText(a any) (string, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return "", fmt.Errorf("encoding as JSON: %w", err)
	}
	return string(b), nil
}

// arrayLiteral writes a list as a PostgreSQL array literal, each element
// quoted, a nested list as a nested array, null as NULL.
func arrayLiteral(list []any) (string, error) {
	var b strings.Builder
	_ = b.WriteByte('{')
	for i, e := range list {
		if i > 0 {
			_ = b.WriteByte(',')
		}
		if err := writeElement(&b, e); err != nil {
			return "", err
		}
	}
	_ = b.WriteByte('}')
	return b.String(), nil
}

func writeElement(b *strings.Builder, e any) error {
	switch t := e.(type) {
	case nil:
		_, _ = b.WriteString("NULL")
		return nil
	case []any:
		inner, err := arrayLiteral(t)
		if err != nil {
			return err
		}
		_, _ = b.WriteString(inner)
		return nil
	}
	text, err := scalarText(e)
	if err != nil {
		return err
	}
	s, _ := text.(string)
	_ = b.WriteByte('"')
	_, _ = b.WriteString(strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s))
	_ = b.WriteByte('"')
	return nil
}
