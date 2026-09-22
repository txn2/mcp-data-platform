package tableparquet

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// The widest DECIMAL each integer physical type holds.
const (
	int32DecimalDigits = 9
	int64DecimalDigits = 18
)

// millisPrecision is the widest TIMESTAMP precision written in milliseconds.
const millisPrecision = 3

// leafNode is the schema node a scalar type is written as.
func leafNode(t tabletype.Type) (parquet.Node, error) {
	switch t.Kind {
	case tabletype.Decimal:
		return decimalNode(t)
	case tabletype.Timestamp:
		return parquet.TimestampAdjusted(timestampUnit(t), false), nil
	}
	if n, ok := scalarNodes[t.Kind]; ok {
		return n, nil
	}
	return nil, fmt.Errorf("no Parquet type is written for %s", t.SQL())
}

// scalarNodes are the schema nodes of the scalar kinds that take no argument.
var scalarNodes = map[tabletype.Kind]parquet.Node{
	tabletype.Boolean:     parquet.Leaf(parquet.BooleanType),
	tabletype.Tinyint:     parquet.Int(bits8),
	tabletype.Smallint:    parquet.Int(bits16),
	tabletype.Integer:     parquet.Int(bits32),
	tabletype.Bigint:      parquet.Int(bits64),
	tabletype.Real:        parquet.Leaf(parquet.FloatType),
	tabletype.Double:      parquet.Leaf(parquet.DoubleType),
	tabletype.Varchar:     parquet.String(),
	tabletype.Text:        parquet.String(),
	tabletype.Varbinary:   parquet.Leaf(parquet.ByteArrayType),
	tabletype.Date:        parquet.Date(),
	tabletype.TimestampTZ: parquet.TimestampAdjusted(parquet.Microsecond, true),
}

// timestampUnit is the unit a TIMESTAMP(p) is written in: milliseconds when p
// fits them, microseconds otherwise, which is the finest the scratch catalogs
// read (hive.timestamp-precision=MICROSECONDS).
func timestampUnit(t tabletype.Type) parquet.TimeUnit {
	if t.Precision <= millisPrecision {
		return parquet.Millisecond
	}
	return parquet.Microsecond
}

// decimalNode stores a DECIMAL in the narrowest physical type that holds its
// precision, as the format recommends.
func decimalNode(t tabletype.Type) (parquet.Node, error) {
	if t.Precision < 1 || t.Precision > maxDecimalPrecision {
		return nil, fmt.Errorf("a DECIMAL of precision %d cannot be written", t.Precision)
	}
	switch {
	case t.Precision <= int32DecimalDigits:
		return parquet.Decimal(t.Scale, t.Precision, parquet.Int32Type), nil
	case t.Precision <= int64DecimalDigits:
		return parquet.Decimal(t.Scale, t.Precision, parquet.Int64Type), nil
	}
	return parquet.Decimal(t.Scale, t.Precision, parquet.FixedLenByteArrayType(decimalBytes(t.Precision))), nil
}

// decimalBytes is the fewest bytes whose two's complement holds every unscaled
// value of a precision: the bits of 10^precision, one sign bit, rounded up to
// whole bytes.
func decimalBytes(precision int) int {
	limit := new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(int64(precision)), nil)
	return (limit.BitLen() + 1 + bitsPerByte - 1) / bitsPerByte
}

// The number bases and widths the conversions read and write in.
const (
	decimalBase = 10
	bitsPerByte = 8
	// maxExactFloatInt is the largest integer a float64 holds exactly, 2^53.
	maxExactFloatInt = 1 << 53
)

// leafValue converts one scalar to the Parquet value its type is written as.
func leafValue(t tabletype.Type, v any) (parquet.Value, error) {
	switch t.Kind {
	case tabletype.Boolean:
		b, ok := v.(bool)
		if !ok {
			return parquet.Value{}, mismatch(t, v)
		}
		return parquet.BooleanValue(b), nil
	case tabletype.Tinyint, tabletype.Smallint, tabletype.Integer, tabletype.Bigint:
		return intValue(t, v)
	case tabletype.Real, tabletype.Double:
		return floatValue(t, v)
	case tabletype.Decimal:
		return decimalValue(t, v)
	case tabletype.Varbinary:
		b, err := toBytes(v)
		return parquet.ByteArrayValue(b), err
	case tabletype.Date:
		return dateValue(v)
	case tabletype.Timestamp, tabletype.TimestampTZ:
		return timestampValue(t, v)
	}
	s, err := textOf(t, v)
	return parquet.ByteArrayValue([]byte(s)), err
}

// textOf reads a value as the text its column holds, with the one part of that
// the value alone does not say: a TIME WITH TIME ZONE arrives as a time.Time
// carrying its offset, and a TIME arrives as one in the local zone, so the
// layout is chosen from the type rather than from the value. Writing both
// without a zone dropped the offset a zoned column exists to carry, while the
// same query exported as CSV or JSON kept it.
func textOf(t tabletype.Type, v any) (string, error) {
	if at, ok := v.(time.Time); ok && zonedSource(t.Source) {
		return at.Format("15:04:05.999999999-07:00"), nil
	}
	return toText(v)
}

// zonedSource reports whether a type's reported text names a time zone.
func zonedSource(source string) bool {
	return strings.Contains(strings.ToLower(source), "with time zone")
}

// intValue writes an integer in the physical width its type is stored in.
func intValue(t tabletype.Type, v any) (parquet.Value, error) {
	n, err := toInt(v)
	if err != nil || t.Kind == tabletype.Bigint {
		return parquet.Int64Value(n), err
	}
	small, err := toInt32(n, t)
	return parquet.Int32Value(small), err
}

// intBounds is what each narrow integer type holds. A TINYINT and a SMALLINT
// are both stored in a physical INT32, annotated INT(8) and INT(16), so a
// value outside the annotation's range is written into a leaf that cannot
// hold it and reads back as something else; the bound is the type's, not the
// physical width's.
var intBounds = map[tabletype.Kind][2]int64{
	tabletype.Tinyint:  {math.MinInt8, math.MaxInt8},
	tabletype.Smallint: {math.MinInt16, math.MaxInt16},
	tabletype.Integer:  {math.MinInt32, math.MaxInt32},
}

// toInt32 narrows a value to the INT32 a type is stored in, refusing one the
// type cannot hold rather than wrapping it.
func toInt32(n int64, t tabletype.Type) (int32, error) {
	if n < math.MinInt32 || n > math.MaxInt32 {
		return 0, fmt.Errorf("the value %d does not fit %s", n, t.SQL())
	}
	if bounds, ok := intBounds[t.Kind]; ok && (n < bounds[0] || n > bounds[1]) {
		return 0, fmt.Errorf("the value %d does not fit %s", n, t.SQL())
	}
	return int32(n), nil
}

// floatValue writes a REAL or a DOUBLE. A REAL is stored in a physical FLOAT,
// so a value outside float32 is refused rather than written as an infinity a
// query would read back in place of the number.
func floatValue(t tabletype.Type, v any) (parquet.Value, error) {
	f, err := toFloat(v)
	if err != nil || t.Kind != tabletype.Real {
		return parquet.DoubleValue(f), err
	}
	narrowed := float32(f)
	if math.IsInf(float64(narrowed), 0) && !math.IsInf(f, 0) {
		return parquet.Value{}, fmt.Errorf("the value %v does not fit %s", f, t.SQL())
	}
	return parquet.FloatValue(narrowed), nil
}

func mismatch(t tabletype.Type, v any) error {
	return fmt.Errorf("a %T value cannot be written as %s", v, t.SQL())
}

// toInt reads an integer from a driver value, a JSON number, or text.
func toInt(v any) (int64, error) {
	switch x := v.(type) {
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case json.Number:
		return strconv.ParseInt(string(x), decimalBase, bits64) //nolint:wrapcheck // named by the caller
	case string:
		return strconv.ParseInt(x, decimalBase, bits64) //nolint:wrapcheck // named by the caller
	case float64:
		if x == math.Trunc(x) && math.Abs(x) < maxExactFloatInt {
			return int64(x), nil
		}
	}
	return 0, fmt.Errorf("value %v is not an integer", v)
}

// toFloat reads a floating-point value. The JSON protocol carries the values a
// number cannot spell as strings, which are read back as themselves.
func toFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case float32:
		return float64(x), nil
	case int64:
		return float64(x), nil
	case json.Number:
		return strconv.ParseFloat(string(x), bits64) //nolint:wrapcheck // named by the caller
	case string:
		return parseSpecialFloat(x)
	}
	return 0, fmt.Errorf("value %v is not a number", v)
}

func parseSpecialFloat(s string) (float64, error) {
	switch s {
	case "NaN":
		return math.NaN(), nil
	case "Infinity":
		return math.Inf(1), nil
	case "-Infinity":
		return math.Inf(-1), nil
	}
	return strconv.ParseFloat(s, bits64) //nolint:wrapcheck // named by the caller
}

// decimalText is a decimal written in digits, the only form a DECIMAL column's
// value arrives in.
var decimalText = regexp.MustCompile(`^[+-]?(\d+(\.\d*)?|\.\d+)$`)

// decimalValue writes a DECIMAL's unscaled value in the physical type its
// precision chose. The value arrives as text (Trino sends a decimal as a
// string, so none of its digits pass through a float) or as a JSON number; a
// value with more fractional digits than the scale is refused rather than
// rounded.
func decimalValue(t tabletype.Type, v any) (parquet.Value, error) {
	text, err := toText(v)
	if err != nil {
		return parquet.Value{}, err
	}
	// big.Rat reads more than decimal notation -- "0x10" is 16, "1_000" is a
	// thousand, "1/3" is a rational -- and a DECIMAL column's value is written
	// out in digits. Anything else is refused rather than read as a number
	// nobody wrote.
	if !decimalText.MatchString(text) {
		return parquet.Value{}, fmt.Errorf("text %q is not a decimal", text)
	}
	r, ok := new(big.Rat).SetString(text)
	if !ok {
		return parquet.Value{}, fmt.Errorf("text %q is not a decimal", text)
	}
	scale := new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(int64(t.Scale)), nil)
	scaled := new(big.Rat).Mul(r, new(big.Rat).SetInt(scale))
	if !scaled.IsInt() {
		return parquet.Value{}, fmt.Errorf("the value %s has more digits after the point than %s holds", text, t.SQL())
	}
	unscaled := scaled.Num()
	limit := new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(int64(t.Precision)), nil)
	if new(big.Int).Abs(unscaled).Cmp(limit) >= 0 {
		return parquet.Value{}, fmt.Errorf("the value %s has more digits than %s holds", text, t.SQL())
	}
	switch {
	case t.Precision <= int32DecimalDigits:
		small, err := toInt32(unscaled.Int64(), t)
		return parquet.Int32Value(small), err
	case t.Precision <= int64DecimalDigits:
		return parquet.Int64Value(unscaled.Int64()), nil
	}
	return parquet.FixedLenByteArrayValue(twosComplement(unscaled, decimalBytes(t.Precision))), nil
}

// twosComplement renders n big-endian in size bytes.
func twosComplement(n *big.Int, size int) []byte {
	out := make([]byte, size)
	if n.Sign() >= 0 {
		n.FillBytes(out)
		return out
	}
	// -n = (2^(8*size)) - |n|
	mod := new(big.Int).Lsh(big.NewInt(1), uint(bitsPerByte*size)) //nolint:gosec // size is at most 16
	new(big.Int).Add(mod, n).FillBytes(out)
	return out
}

// toBytes reads a VARBINARY: the driver's bytes, or the base64 text the JSON
// protocol carries a nested one as.
func toBytes(v any) ([]byte, error) {
	switch x := v.(type) {
	case []byte:
		return x, nil
	case string:
		return base64.StdEncoding.DecodeString(x) //nolint:wrapcheck // named by the caller
	}
	return nil, fmt.Errorf("value %v is not binary", v)
}

// dateValue writes a DATE as days since the epoch.
func dateValue(v any) (parquet.Value, error) {
	var t time.Time
	switch x := v.(type) {
	case time.Time:
		t = time.Date(x.Year(), x.Month(), x.Day(), 0, 0, 0, 0, time.UTC)
	case string:
		parsed, err := time.Parse(time.DateOnly, x)
		if err != nil {
			return parquet.Value{}, fmt.Errorf("text %q is not a date", x)
		}
		t = parsed
	default:
		return parquet.Value{}, fmt.Errorf("value %v is not a date", v)
	}
	days, err := toInt32(t.Unix()/secondsPerDay, tabletype.Scalar(tabletype.Date))
	return parquet.Int32Value(days), err
}

const secondsPerDay = 24 * 60 * 60

// timestampValue writes a TIMESTAMP as the instant its wall-clock reading
// names in UTC -- it is "not adjusted to UTC", so the reading is what is kept
// -- and a TIMESTAMP WITH TIME ZONE as the instant itself, which is all
// Parquet keeps of it.
func timestampValue(t tabletype.Type, v any) (parquet.Value, error) {
	ts, err := toTime(v)
	if err != nil {
		return parquet.Value{}, err
	}
	if t.Kind == tabletype.Timestamp {
		ts = time.Date(ts.Year(), ts.Month(), ts.Day(), ts.Hour(), ts.Minute(), ts.Second(), ts.Nanosecond(), time.UTC)
		if timestampUnit(t) == parquet.Millisecond {
			return parquet.Int64Value(ts.UnixMilli()), nil
		}
	}
	return parquet.Int64Value(ts.UnixMicro()), nil
}

// trinoTimestampLayouts are the forms Trino's JSON protocol writes a nested
// timestamp in: a wall-clock reading, then an offset or a zone name.
var trinoTimestampLayouts = []string{
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05.999999999 -07:00",
}

// toTime reads a timestamp: the driver's time.Time, or the text a nested one
// arrives as ("2024-05-01 12:34:56.789123", with " UTC", an offset, or a zone
// name after it for one with a time zone).
func toTime(v any) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return x, nil
	case string:
		return parseTrinoTimestamp(x)
	}
	return time.Time{}, fmt.Errorf("value %v is not a timestamp", v)
}

func parseTrinoTimestamp(s string) (time.Time, error) {
	for _, layout := range trinoTimestampLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	// A zone name: the reading, then the zone it is read in.
	if i := strings.LastIndexByte(s, ' '); i > 0 {
		if loc, err := time.LoadLocation(s[i+1:]); err == nil {
			if t, err := time.ParseInLocation(trinoTimestampLayouts[0], s[:i], loc); err == nil {
				return t, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("text %q is not a timestamp", s)
}

// toText reads any scalar as the text a string column holds: text as itself,
// a number as it was written, a boolean as true or false, bytes as UTF-8, a
// time of day as Trino prints one, and a nested value as its JSON.
func toText(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case json.Number:
		return string(x), nil
	case bool:
		return strconv.FormatBool(x), nil
	case []byte:
		return string(x), nil
	case int64:
		return strconv.FormatInt(x, decimalBase), nil
	case float64:
		return strconv.FormatFloat(x, 'g', -1, bits64), nil
	case time.Time:
		return x.Format("15:04:05.999999999"), nil
	}
	b, err := json.Marshal(jsonable(v))
	if err != nil {
		return "", fmt.Errorf("a %T value cannot be written as text: %w", v, err)
	}
	return string(b), nil
}

// jsonable turns an ordered object back into something encoding/json writes,
// keeping its key order.
func jsonable(v any) any {
	switch x := v.(type) {
	case *tabletype.Object:
		return orderedJSON{x}
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = jsonable(e)
		}
		return out
	}
	return v
}

// orderedJSON marshals an Object with its keys in order.
type orderedJSON struct{ o *tabletype.Object }

// MarshalJSON writes the object with its keys in the order they were read.
func (j orderedJSON) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	_ = b.WriteByte('{')
	for i, k := range j.o.Keys {
		if i > 0 {
			_ = b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(jsonable(j.o.Values[i]))
		if err != nil {
			return nil, err //nolint:wrapcheck // named by toText
		}
		_, _ = b.Write(kb)
		_ = b.WriteByte(':')
		_, _ = b.Write(vb)
	}
	_ = b.WriteByte('}')
	return b.Bytes(), nil
}
