// Package tabletype is the one model of a column type the platform declares a
// table with or writes a typed file from (#1833): the Trino types a registered
// table's columns carry, the parse of the type text Trino reports for a query's
// columns, and the inference of a column's type from JSON values.
//
// It knows nothing about registrations, files or connectors. A JSON-lines
// registration and a script's Parquet export infer their types here, so the
// two cannot disagree about what a column of numbers is, and a Parquet file's
// columns and a trino_export's columns are rendered through the same Type.
package tabletype

import (
	"strconv"
	"strings"
)

// Kind names a type. The declarable kinds are the ones a Hive table over a
// JSON-lines or Parquet file can be declared with; the source-only kinds are
// what a query can return that no registration declares, and a writer stores
// them as text.
type Kind string

// The declarable kinds.
const (
	Boolean   Kind = "BOOLEAN"
	Tinyint   Kind = "TINYINT"
	Smallint  Kind = "SMALLINT"
	Integer   Kind = "INTEGER"
	Bigint    Kind = "BIGINT"
	Real      Kind = "REAL"
	Double    Kind = "DOUBLE"
	Decimal   Kind = "DECIMAL"
	Varchar   Kind = "VARCHAR"
	Varbinary Kind = "VARBINARY"
	Date      Kind = "DATE"
	// Timestamp is declared at microseconds, TIMESTAMP(6), whatever the
	// precision its source had: a Hive table's timestamps are read at the
	// catalog's hive.timestamp-precision, the connector refuses a column
	// declared at any other ("Incorrect timestamp precision for timestamp(3);
	// the configured precision is MICROSECONDS"), and MICROSECONDS is the
	// setting a catalog reads a Parquet file's timestamps exactly at.
	Timestamp Kind = "TIMESTAMP"
	Array     Kind = "ARRAY"
	Map       Kind = "MAP"
	Row       Kind = "ROW"
)

// The source-only kinds.
const (
	// TimestampTZ is a timestamp with a time zone. A Parquet file keeps the
	// instant and not the zone.
	TimestampTZ Kind = "TIMESTAMP WITH TIME ZONE"
	// Text is any other type a query returns as text -- CHAR, TIME, JSON,
	// UUID, IPADDRESS, an interval -- which a file stores as a string.
	Text Kind = "TEXT"
)

// DeclaredTimestampPrecision is the precision every TIMESTAMP column is
// declared with; see Timestamp.
const DeclaredTimestampPrecision = 6

// Type is one column type. Precision and Scale describe a DECIMAL, and
// Precision a source TIMESTAMP's fractional digits; Elem is an ARRAY's element
// or a MAP's value, Key a MAP's key, and Fields a ROW's fields in order. Source
// is the type text a query reported, kept for a Text type so a writer can say
// what it stored as text.
type Type struct {
	Kind      Kind
	Precision int
	Scale     int
	Elem      *Type
	Key       *Type
	Fields    []Field
	Source    string
}

// Field is one field of a ROW.
type Field struct {
	Name string
	Type Type
}

// Scalar returns a Type of a kind that takes no arguments.
func Scalar(kind Kind) Type { return Type{Kind: kind} }

// ArrayOf returns an ARRAY of elem.
func ArrayOf(elem Type) Type { return Type{Kind: Array, Elem: &elem} }

// MapOf returns a MAP from key to value.
func MapOf(key, value Type) Type { return Type{Kind: Map, Key: &key, Elem: &value} }

// DecimalOf returns a DECIMAL(precision, scale).
func DecimalOf(precision, scale int) Type {
	return Type{Kind: Decimal, Precision: precision, Scale: scale}
}

// RowOf returns a ROW of fields.
func RowOf(fields ...Field) Type { return Type{Kind: Row, Fields: fields} }

// SQL renders the type as a CREATE TABLE declares it. A ROW's field names are
// quoted, because they come from a file somebody wrote.
func (t Type) SQL() string {
	switch t.Kind {
	case Decimal:
		return "DECIMAL(" + strconv.Itoa(t.Precision) + "," + strconv.Itoa(t.Scale) + closeParen
	case Array:
		return "ARRAY(" + t.Elem.SQL() + closeParen
	case Map:
		return "MAP(" + t.Key.SQL() + ", " + t.Elem.SQL() + closeParen
	case Row:
		parts := make([]string, 0, len(t.Fields))
		for _, f := range t.Fields {
			parts = append(parts, QuoteName(f.Name)+" "+f.Type.SQL())
		}
		return "ROW(" + strings.Join(parts, ", ") + closeParen
	case Text:
		return string(Varchar)
	case Timestamp:
		return "TIMESTAMP(" + strconv.Itoa(DeclaredTimestampPrecision) + closeParen
	default:
		return string(t.Kind)
	}
}

// closeParen ends a type's argument list.
const closeParen = ")"

// QuoteName renders a name as a Trino delimited identifier.
func QuoteName(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Nested reports whether the type holds other types.
func (t Type) Nested() bool {
	return t.Kind == Array || t.Kind == Map || t.Kind == Row
}
