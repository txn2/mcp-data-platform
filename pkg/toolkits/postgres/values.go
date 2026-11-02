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
//   - anything else (interval, inet, an enum, a domain, a range) is the text
//     PostgreSQL itself writes for it

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
