package postgres

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/txn2/mcp-data-platform/internal/pgtext"
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
//   - a range, a multirange, a composite, an interval, an hstore and a vector
//     take the shape the result returns them in (structuredText)
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
	case structuredKind(&ti):
		if s, ok := a.(string); ok {
			return s, nil
		}
		return structuredText(a, &ti)
	}
	return scalarText(a)
}

// structuredText writes a structured parameter (#2455) in the text form its
// type reads, from the shape the result returns it in: a range object, a list
// of them for a multirange, an object of attributes for a composite, the
// interval's parts, an object for an hstore, a list of numbers for a vector.
// A string is the caller's own literal and is sent as it is.
func structuredText(a any, ti *typeInfo) (string, error) {
	switch {
	case ti.kind == kindRange:
		r, err := rangeOf(a, ti.elem)
		if err != nil {
			return "", err
		}
		return pgtext.FormatRange(r), nil
	case ti.kind == kindMultirange:
		return multirangeText(a, ti)
	case ti.kind == kindComposite:
		return compositeText(a, ti)
	case ti.name == typeInterval:
		return intervalText(a)
	case ti.name == typeHstore:
		return hstoreText(a)
	}
	list, ok := a.([]any)
	if !ok {
		return "", errors.New("a vector takes a list of numbers")
	}
	return jsonText(list)
}

// rangeOf reads a range object. An absent or null bound is unbounded; the
// bounds are inclusive below and exclusive above unless lower_inc or
// upper_inc says otherwise, PostgreSQL's own default.
func rangeOf(a any, elem *typeInfo) (pgtext.Range, error) {
	m, ok := a.(map[string]any)
	if !ok {
		return pgtext.Range{}, errors.New(`a range takes {"lower", "upper", "lower_inc", "upper_inc", "empty"} or its text`)
	}
	for k := range m {
		switch k {
		case "lower", "upper", "lower_inc", "upper_inc", "empty":
		default:
			return pgtext.Range{}, fmt.Errorf("a range has no %q; it takes lower, upper, lower_inc, upper_inc and empty", k)
		}
	}
	if empty, _ := m["empty"].(bool); empty {
		return pgtext.Range{Empty: true}, nil
	}
	r := pgtext.Range{LowerInc: true}
	if v, ok := m["lower_inc"].(bool); ok {
		r.LowerInc = v
	}
	r.UpperInc, _ = m["upper_inc"].(bool)
	var err error
	if r.Lower, err = partText(m["lower"], elem); err != nil {
		return pgtext.Range{}, fmt.Errorf("lower: %w", err)
	}
	if r.Upper, err = partText(m["upper"], elem); err != nil {
		return pgtext.Range{}, fmt.Errorf("upper: %w", err)
	}
	return r, nil
}

func multirangeText(a any, ti *typeInfo) (string, error) {
	list, ok := a.([]any)
	if !ok {
		return "", errors.New("a multirange takes a list of ranges")
	}
	var inner *typeInfo
	if ti.elem != nil {
		inner = ti.elem.elem
	}
	ranges := make([]pgtext.Range, len(list))
	for i, item := range list {
		r, err := rangeOf(item, inner)
		if err != nil {
			return "", fmt.Errorf("range %d: %w", i+1, err)
		}
		ranges[i] = r
	}
	return pgtext.FormatMultirange(ranges), nil
}

// compositeText writes an object of attributes as a row in attribute order, an
// absent attribute as null. An attribute the type does not have is refused
// rather than dropped.
func compositeText(a any, ti *typeInfo) (string, error) {
	m, ok := a.(map[string]any)
	if !ok {
		return "", fmt.Errorf("composite %s takes an object of its attributes", ti.display)
	}
	known := make(map[string]bool, len(ti.attrs))
	fields := make([]*string, len(ti.attrs))
	for i, attr := range ti.attrs {
		known[attr.name] = true
		f, err := partText(m[attr.name], attr.typ)
		if err != nil {
			return "", fmt.Errorf("attribute %s: %w", attr.name, err)
		}
		fields[i] = f
	}
	for k := range m {
		if !known[k] {
			return "", fmt.Errorf("composite %s has no attribute %q", ti.display, k)
		}
	}
	return pgtext.FormatComposite(fields), nil
}

// intervalText writes an interval from its parts, or takes its text.
func intervalText(a any) (string, error) {
	m, ok := a.(map[string]any)
	if !ok {
		return "", errors.New(`an interval takes {"months", "days", "microseconds"} or its text`)
	}
	if text, ok := m["text"].(string); ok && len(m) == 1 {
		return text, nil
	}
	var parts [3]string
	for i, k := range []string{"months", "days", "microseconds"} {
		v, ok := m[k]
		if !ok {
			parts[i] = "0"
			continue
		}
		text, err := scalarText(v)
		if err != nil {
			return "", err
		}
		s, _ := text.(string)
		parts[i] = s
	}
	return parts[0] + " months " + parts[1] + " days " + parts[2] + " microseconds", nil
}

// hstoreText writes an object of strings (or nulls) as an hstore, its keys in
// order.
func hstoreText(a any) (string, error) {
	m, ok := a.(map[string]any)
	if !ok {
		return "", errors.New("an hstore takes an object of strings")
	}
	keys := make([]string, 0, len(m))
	pairs := make(map[string]*string, len(m))
	for k, v := range m {
		keys = append(keys, k)
		if v == nil {
			pairs[k] = nil
			continue
		}
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("hstore value %q is not a string or null", k)
		}
		pairs[k] = &s
	}
	slices.Sort(keys)
	return pgtext.FormatHstore(keys, pairs), nil
}

// partText is one part of a structured parameter (a bound, an attribute) as
// the text its type reads; null is nil.
func partText(v any, ti *typeInfo) (*string, error) {
	if v == nil {
		return nil, nil //nolint:nilnil // a null part is written as null
	}
	if ti != nil {
		enc, err := encodeArg(v, *ti)
		if err != nil {
			return nil, err
		}
		s, _ := enc.(string)
		return &s, nil
	}
	text, err := scalarText(v)
	if err != nil {
		return nil, err
	}
	s, _ := text.(string)
	return &s, nil
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
