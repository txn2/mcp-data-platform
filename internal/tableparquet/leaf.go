package tableparquet

import (
	"strconv"

	"github.com/parquet-go/parquet-go/deprecated"
	"github.com/parquet-go/parquet-go/format"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// The converted types a file written before logical types annotates with. A
// writer that knows logical types writes both, and the logical one decides.
const (
	convertedList        = deprecated.List
	convertedMap         = deprecated.Map
	convertedMapKeyValue = deprecated.MapKeyValue
)

// leafType maps one primitive field to the type it is declared as, or refuses
// it naming its Parquet type.
func leafType(el format.SchemaElement, path string) (tabletype.Type, error) {
	physical, _ := el.Type.Get()
	lt := el.LogicalType.Value
	if lt == nil {
		lt = fromConverted(el)
	}
	if d, ok := lt.(*format.DecimalType); ok {
		return decimalType(d, path, describe(el))
	}
	mapFn, known := physicalTypes[physical]
	if !known {
		return tabletype.Type{}, unsupported(path, describe(el))
	}
	t, ok := mapFn(lt)
	if !ok {
		return tabletype.Type{}, unsupported(path, describe(el))
	}
	return t, nil
}

// physicalTypes maps each physical type to how its annotation declares it.
var physicalTypes = map[format.Type]func(format.LogicalTypeValue) (tabletype.Type, bool){
	format.Boolean:   plainOnly(tabletype.Boolean),
	format.Int32:     int32Type,
	format.Int64:     int64Type,
	format.Int96:     plainOnly(tabletype.Timestamp),
	format.Float:     plainOnly(tabletype.Real),
	format.Double:    plainOnly(tabletype.Double),
	format.ByteArray: byteArrayType,
}

// plainOnly declares a physical type that carries no annotation as kind, and
// refuses it with one.
func plainOnly(kind tabletype.Kind) func(format.LogicalTypeValue) (tabletype.Type, bool) {
	return func(lt format.LogicalTypeValue) (tabletype.Type, bool) {
		return tabletype.Scalar(kind), lt == nil
	}
}

// decimalType declares a DECIMAL, which Trino holds to 38 digits.
func decimalType(d *format.DecimalType, path, what string) (tabletype.Type, error) {
	if d.Precision < 1 || d.Precision > maxDecimalPrecision || d.Scale < 0 || d.Scale > d.Precision {
		return tabletype.Type{}, unsupported(path, what)
	}
	return tabletype.DecimalOf(int(d.Precision), int(d.Scale)), nil
}

func int32Type(lt format.LogicalTypeValue) (tabletype.Type, bool) {
	switch x := lt.(type) {
	case nil:
		return tabletype.Scalar(tabletype.Integer), true
	case *format.DateType:
		return tabletype.Scalar(tabletype.Date), true
	case *format.IntType:
		return intType(x)
	}
	return tabletype.Type{}, false
}

func int64Type(lt format.LogicalTypeValue) (tabletype.Type, bool) {
	switch x := lt.(type) {
	case nil:
		return tabletype.Scalar(tabletype.Bigint), true
	case *format.TimestampType:
		return tabletype.Scalar(tabletype.Timestamp), true
	case *format.IntType:
		return intType(x)
	}
	return tabletype.Type{}, false
}

// The bit widths an annotated integer declares.
const (
	bits8  = 8
	bits16 = 16
	bits32 = 32
	bits64 = 64
)

// signedKinds and unsignedKinds are the Trino integer each annotated width is
// declared as.
var (
	signedKinds = map[int8]tabletype.Kind{
		bits8: tabletype.Tinyint, bits16: tabletype.Smallint,
		bits32: tabletype.Integer, bits64: tabletype.Bigint,
	}
	unsignedKinds = map[int8]tabletype.Kind{bits8: tabletype.Smallint, bits16: tabletype.Integer}
)

// intType declares an annotated integer as the narrowest Trino integer that
// holds every value of it: a signed one as itself, an unsigned 8- or 16-bit
// one as the next width up.
//
// An unsigned 32- or 64-bit integer has no declaration that reads back
// exactly, observed on Trino 453's Parquet reader. Declared BIGINT, a UINT32
// of 4294967295 reads as -1: the reader takes the physical INT32 as signed
// whatever the column says. A UINT64 declared DECIMAL(20,0), the only type
// wide enough, is refused outright ("Unsupported Trino column type
// (decimal(20,0)) for Parquet column ... INTEGER(64,false)").
func intType(it *format.IntType) (tabletype.Type, bool) {
	table := unsignedKinds
	if it.IsSigned {
		table = signedKinds
	}
	kind, ok := table[it.BitWidth]
	return tabletype.Scalar(kind), ok
}

func byteArrayType(lt format.LogicalTypeValue) (tabletype.Type, bool) {
	switch lt.(type) {
	case nil:
		return tabletype.Scalar(tabletype.Varbinary), true
	case *format.StringType, *format.EnumType, *format.JsonType:
		return tabletype.Scalar(tabletype.Varchar), true
	}
	return tabletype.Type{}, false
}

// fromConverted reads the logical type a converted type implies, for a file
// that carries only the older annotation. A converted type with no logical
// equivalent the mapping reads is returned as an annotation leafType refuses.
func fromConverted(el format.SchemaElement) format.LogicalTypeValue {
	ct, ok := el.ConvertedType.Get()
	if !ok {
		return nil
	}
	if lt, known := convertedLogical[ct]; known {
		return lt
	}
	switch ct {
	case deprecated.Decimal:
		scale, _ := el.Scale.Get()
		precision, _ := el.Precision.Get()
		return &format.DecimalType{Scale: scale, Precision: precision}
	case deprecated.TimestampMillis, deprecated.TimestampMicros:
		return &format.TimestampType{}
	}
	return &format.NullType{}
}

// convertedLogical maps the converted types with a fixed logical equivalent.
var convertedLogical = map[deprecated.ConvertedType]format.LogicalTypeValue{
	deprecated.UTF8:   &format.StringType{},
	deprecated.Enum:   &format.EnumType{},
	deprecated.Json:   &format.JsonType{},
	deprecated.Date:   &format.DateType{},
	deprecated.Int8:   &format.IntType{BitWidth: bits8, IsSigned: true},
	deprecated.Int16:  &format.IntType{BitWidth: bits16, IsSigned: true},
	deprecated.Int32:  &format.IntType{BitWidth: bits32, IsSigned: true},
	deprecated.Int64:  &format.IntType{BitWidth: bits64, IsSigned: true},
	deprecated.Uint8:  &format.IntType{BitWidth: bits8},
	deprecated.Uint16: &format.IntType{BitWidth: bits16},
	deprecated.Uint32: &format.IntType{BitWidth: bits32},
	deprecated.Uint64: &format.IntType{BitWidth: bits64},
}

// describe names a leaf's Parquet type the way a refusal speaks of it: the
// physical type, then its annotation when it has one.
func describe(el format.SchemaElement) string {
	physical, _ := el.Type.Get()
	s := physical.String()
	if physical == format.FixedLenByteArray {
		if n, ok := el.TypeLength.Get(); ok {
			s += "(" + strconv.Itoa(int(n)) + ")"
		}
	}
	switch {
	case el.LogicalType.Value != nil:
		s += " " + el.LogicalType.String()
	case el.ConvertedType.Valid:
		s += " " + convertedName(el.ConvertedType.V)
	}
	return "a Parquet " + s
}

// convertedNames are the specification's names for the converted types.
var convertedNames = []string{
	"UTF8", "MAP", "MAP_KEY_VALUE", "LIST", "ENUM", "DECIMAL", "DATE", "TIME_MILLIS",
	"TIME_MICROS", "TIMESTAMP_MILLIS", "TIMESTAMP_MICROS", "UINT_8", "UINT_16", "UINT_32", "UINT_64", "INT_8",
	"INT_16", "INT_32", "INT_64", "JSON", "BSON", "INTERVAL",
}

func convertedName(ct deprecated.ConvertedType) string {
	if int(ct) >= 0 && int(ct) < len(convertedNames) {
		return convertedNames[ct]
	}
	return "converted type " + strconv.Itoa(int(ct))
}
