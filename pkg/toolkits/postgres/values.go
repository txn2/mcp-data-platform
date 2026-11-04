package postgres

import (
	"database/sql/driver"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/txn2/mcp-data-platform/internal/pgtext"
)

// The type mapping, stated once and used by both tools (and, with #2402, by a
// script's native binding). A value is turned into the JSON a caller reads by
// what PostgreSQL said its type is:
//
//   - integers and floats are numbers; an integer outside the range a JSON
//     reader holds exactly (beyond 2^53 - 1 either way) is a decimal string,
//     and a float that JSON cannot hold (NaN, Infinity) is its PostgreSQL
//     spelling as a string
//   - numeric is a decimal string, since a JSON number loses its precision
//   - timestamptz is RFC 3339 in UTC; timestamp is the same without a zone;
//     date is YYYY-MM-DD; time is HH:MM:SS[.ffffff]
//   - json and jsonb are the decoded document
//   - uuid is its canonical string
//   - an array is a list of its elements, each mapped by the element type
//   - bytea is base64, its column's type naming it as bytea
//   - a range, a multirange, a composite, an interval, an hstore and
//     pgvector's vector are structures of their own (structuredValue, #2455),
//     accepted in the same shape as a bound parameter (encodeArg)
//   - anything else (inet, cidr, an enum's label, a domain, ltree, tsvector)
//     is the text PostgreSQL itself writes for it, the column's type naming
//     the type, so an enum is told from text by its type

// mappedTypes are the scalar types the mapping turns into JSON of their own.
// Any other scalar type is returned as the text PostgreSQL wrote.
//
//nolint:gochecknoglobals // a read-only lookup set
var mappedTypes = map[string]bool{
	"int2": true, "int4": true, "int8": true, "oid": true, "float4": true, "float8": true,
	"numeric": true, "bool": true, "json": true, "jsonb": true, "uuid": true, "bytea": true,
	"timestamptz": true, "timestamp": true, "date": true,
}

const (
	layoutTimestamp = "2006-01-02T15:04:05.999999"
	layoutDate      = "2006-01-02"
)

// jsonValue maps one decoded value of a column whose type is typeName.
func jsonValue(v any, typeName string) any {
	switch t := v.(type) {
	case nil:
		return nil
	case pgtype.Numeric:
		return numericText(t)
	case time.Time:
		return timeText(t, typeName)
	case [uuidLen]byte:
		return uuidText(t)
	case []byte:
		return base64.StdEncoding.EncodeToString(t)
	case []any:
		return arrayValue(t, typeName)
	case int64:
		return intValue(t)
	case int32:
		return int64(t)
	case int16:
		return int64(t)
	case float64:
		return floatValue(t)
	case float32:
		return floatValue(float64(t))
	}
	return scalarValue(v)
}

// scalarValue is a value whose JSON form needs no type: a number, a string, a
// bool, a decoded json document, or a pgtype value written as its text.
func scalarValue(v any) any {
	switch t := v.(type) {
	case string, bool, int64, map[string]any:
		return t
	case pgtype.Time:
		return pgTimeText(t)
	case driver.Valuer:
		inner, err := t.Value()
		if err != nil {
			return fmt.Sprint(v)
		}
		return scalarValue(inner)
	case fmt.Stringer:
		return t.String()
	}
	return v
}

// arrayValue maps each element of an array by the element type ("_int4"
// names an array of int4).
func arrayValue(elems []any, typeName string) []any {
	elemType := strings.TrimPrefix(typeName, "_")
	out := make([]any, len(elems))
	for i, e := range elems {
		out[i] = jsonValue(e, elemType)
	}
	return out
}

// numericText is a numeric as a decimal string, keeping every digit.
func numericText(n pgtype.Numeric) any {
	if !n.Valid {
		return nil
	}
	v, err := n.Value()
	if err != nil {
		return nil
	}
	return v
}

// timeText writes a timestamptz as RFC 3339 in UTC, a timestamp without a
// zone, and a date as the day.
func timeText(t time.Time, typeName string) string {
	switch typeName {
	case "date":
		return t.Format(layoutDate)
	case "timestamp":
		return t.Format(layoutTimestamp)
	default:
		return t.UTC().Format(time.RFC3339Nano)
	}
}

// pgTimeText writes a time of day.
func pgTimeText(t pgtype.Time) any {
	if !t.Valid {
		return nil
	}
	d := time.Duration(t.Microseconds) * time.Microsecond
	return time.Time{}.Add(d).Format("15:04:05.999999")
}

// uuidLen is a uuid's size in bytes.
const uuidLen = 16

// uuidText writes a uuid in its canonical 8-4-4-4-12 form.
func uuidText(b [uuidLen]byte) string {
	h := hex.EncodeToString(b[:])
	return strings.Join([]string{h[:8], h[8:12], h[12:16], h[16:20], h[20:]}, "-") //nolint:revive // the canonical form's group boundaries
}

// maxSafeInteger is the largest integer a JSON reader that decodes numbers
// as doubles (every JavaScript client, Go's encoding/json into any) holds
// exactly.
const maxSafeInteger = 1<<53 - 1

// intValue is an integer as a number, or as a decimal string when a reader
// would change it.
func intValue(v int64) any {
	if v > maxSafeInteger || v < -maxSafeInteger {
		return strconv.FormatInt(v, decimal)
	}
	return v
}

// floatValue is a float as a number, or as PostgreSQL's spelling when JSON
// cannot hold it.
func floatValue(f float64) any {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	default:
		return f
	}
}

// textTypes decodes the text of a scalar inside a structured value (a range's
// bound, a row's field) by the scalar's own type. Its codecs are read, never
// registered into, so one map serves every connection.
//
//nolint:gochecknoglobals // a read-only codec registry
var textTypes = pgtype.NewMap()

// structuredKind reports whether ti's values are mapped from their text into
// a structure of their own (#2455): a range, a multirange, a composite, an
// interval, an hstore or pgvector's vector.
func structuredKind(ti *typeInfo) bool {
	switch ti.kind {
	case kindRange, kindMultirange, kindComposite:
		return true
	}
	// interval is the server's own; hstore and vector are named by the
	// extensions that define them as base types, so a domain or an enum a
	// schema happens to call vector is read as its text.
	switch ti.name {
	case typeInterval, typeHstore, typeVector:
		return ti.kind == kindBase
	}
	return false
}

// structuredValue maps the text PostgreSQL wrote for a value of a structured
// type:
//
//   - a range is {"lower", "upper", "lower_inc", "upper_inc", "empty"}, its
//     bounds mapped by the subtype and an unbounded one null
//   - a multirange is a list of ranges
//   - a composite is an object keyed by attribute name, each mapped by its type
//   - an interval is {"months", "days", "microseconds", "text"}, the three
//     parts PostgreSQL keeps and the text it wrote
//   - an hstore is an object of string to string or null
//   - a vector is a list of numbers
func structuredValue(ti *typeInfo, text string) (any, error) {
	switch ti.kind {
	case kindRange:
		r, err := pgtext.ParseRange(text)
		if err != nil {
			return nil, err //nolint:wrapcheck // names the syntax; the caller names the type
		}
		return rangeValue(r, ti.elem), nil
	case kindMultirange:
		return multirangeValue(ti, text)
	case kindComposite:
		return compositeValue(ti, text)
	}
	return baseValue(ti.name, text)
}

// multirangeValue is a multirange as a list of ranges, each mapped by the
// range type's element.
func multirangeValue(ti *typeInfo, text string) (any, error) {
	rs, err := pgtext.ParseMultirange(text)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the syntax; the caller names the type
	}
	inner := ti.elem
	if inner != nil {
		inner = inner.elem
	}
	out := make([]any, len(rs))
	for i, r := range rs {
		out[i] = rangeValue(r, inner)
	}
	return out, nil
}

// baseValue maps the structured base types: an interval, an hstore and a
// vector.
func baseValue(name, text string) (any, error) {
	switch name {
	case typeInterval:
		return intervalValue(text)
	case typeHstore:
		return hstoreValue(text)
	}
	v, err := pgtext.ParseVector(text)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the syntax; the caller names the type
	}
	out := make([]any, len(v))
	for i, f := range v {
		out[i] = floatValue(f)
	}
	return out, nil
}

// The structured base types, by name: PostgreSQL's interval, and the types
// the hstore and pgvector extensions define.
const (
	typeInterval = "interval"
	typeHstore   = "hstore"
	typeVector   = "vector"
)

// rangeValue is one range with its bounds mapped by elem.
func rangeValue(r pgtext.Range, elem *typeInfo) map[string]any {
	return map[string]any{
		"lower": partValue(r.Lower, elem), "upper": partValue(r.Upper, elem),
		"lower_inc": !r.Empty && r.LowerInc, "upper_inc": !r.Empty && r.UpperInc, "empty": r.Empty,
	}
}

// compositeValue is a row as an object keyed by attribute name.
func compositeValue(ti *typeInfo, text string) (any, error) {
	fields, err := pgtext.ParseComposite(text)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the syntax; the caller names the type
	}
	out := make(map[string]any, len(ti.attrs))
	for i, a := range ti.attrs {
		if i < len(fields) {
			out[a.name] = partValue(fields[i], a.typ)
		}
	}
	return out, nil
}

// intervalValue is an interval's three parts and its text.
func intervalValue(text string) (any, error) {
	var iv pgtype.Interval
	if err := textTypes.Scan(pgtype.IntervalOID, pgtype.TextFormatCode, []byte(text), &iv); err != nil {
		return nil, fmt.Errorf("reading interval %q: %w", text, err)
	}
	return map[string]any{"months": int64(iv.Months), "days": int64(iv.Days), "microseconds": intValue(iv.Microseconds), "text": text}, nil
}

// hstoreValue is an hstore as an object.
func hstoreValue(text string) (any, error) {
	pairs, err := pgtext.ParseHstore(text)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the syntax; the caller names the type
	}
	out := make(map[string]any, len(pairs))
	for k, v := range pairs {
		if v == nil {
			out[k] = nil
			continue
		}
		out[k] = *v
	}
	return out, nil
}

// partValue maps the text of one part of a structured value (a bound, a
// field) by its type: a structured type as above, a scalar the driver decodes
// as the mapping states, anything else as its text. A nil part is null.
func partValue(text *string, ti *typeInfo) any {
	if text == nil {
		return nil
	}
	if ti == nil {
		return *text
	}
	if structuredKind(ti) {
		v, err := structuredValue(ti, *text)
		if err != nil {
			return *text
		}
		return v
	}
	if ti.array || !mappedTypes[ti.name] {
		return *text
	}
	t, ok := textTypes.TypeForOID(ti.oid)
	if !ok {
		return *text
	}
	v, err := t.Codec.DecodeValue(textTypes, ti.oid, pgtype.TextFormatCode, []byte(*text))
	if err != nil {
		return *text
	}
	return jsonValue(v, ti.name)
}
