package tabletype

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Parse reads the type text Trino reports for a query column -- "bigint",
// "decimal(12,2)", "timestamp(6) with time zone", "array(row(a bigint, "b c"
// varchar))" -- into a Type. A type with no Parquet form of its own (TIME,
// JSON, UUID, an interval, anything this parse does not know) is Text, which a
// writer stores as a string, so a query never fails to export over one column.
//
// The text is case-insensitive and a ROW field name may be quoted. Precision
// and scale a caller learned elsewhere -- a top-level DECIMAL reported as the
// bare "DECIMAL" with its numbers beside it -- are applied with WithPrecision.
func Parse(text string) (Type, error) {
	p := &parser{s: strings.TrimSpace(text)}
	t, err := p.parseType()
	if err != nil {
		return Type{}, fmt.Errorf("reading the type %q: %w", text, err)
	}
	p.skipSpace()
	if p.i != len(p.s) {
		return Type{}, fmt.Errorf("reading the type %q: unexpected %q", text, p.s[p.i:])
	}
	return t, nil
}

// WithPrecision applies a precision and scale reported beside a bare type
// name: a DECIMAL's two numbers, or a TIMESTAMP's fractional digits. A type
// that already carries its own is returned unchanged.
func (t Type) WithPrecision(precision, scale int) Type {
	switch {
	case t.Kind == Decimal && t.Precision == 0 && precision > 0:
		t.Precision, t.Scale = precision, scale
	case (t.Kind == Timestamp || t.Kind == TimestampTZ) && t.Precision == 0 && precision > 0:
		t.Precision = precision
	}
	return t
}

// maxDepth bounds how deeply a type may nest, so a hostile type text cannot
// run the parse out of stack.
const maxDepth = 32

// parser is a recursive-descent reader over a type text.
type parser struct {
	s     string
	i     int
	depth int
}

func (p *parser) skipSpace() {
	for p.i < len(p.s) && p.s[p.i] == ' ' {
		p.i++
	}
}

// word reads a run of letters, digits and underscores, lowercased.
func (p *parser) word() string {
	p.skipSpace()
	start := p.i
	for p.i < len(p.s) && isWordByte(p.s[p.i]) {
		p.i++
	}
	return strings.ToLower(p.s[start:p.i])
}

func isWordByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// peek reports whether the next non-space byte is c.
func (p *parser) peek(c byte) bool {
	p.skipSpace()
	return p.i < len(p.s) && p.s[p.i] == c
}

// expect consumes c or fails.
func (p *parser) expect(c byte) error {
	if !p.peek(c) {
		return fmt.Errorf("expected %q at %d", c, p.i)
	}
	p.i++
	return nil
}

// parseType reads one type, whole: a name, its arguments, and a trailing
// "with time zone".
func (p *parser) parseType() (Type, error) {
	p.depth++
	defer func() { p.depth-- }()
	if p.depth > maxDepth {
		return Type{}, fmt.Errorf("nested more than %d deep", maxDepth)
	}
	start := p.i
	name := p.word()
	if name == "" {
		return Type{}, fmt.Errorf("expected a type name at %d", p.i)
	}
	t, err := p.parseNamed(name)
	if err != nil {
		return Type{}, err
	}
	if t.Kind == Text {
		t.Source = strings.TrimSpace(p.s[start:p.i])
	}
	return t, nil
}

// parseNamed reads what follows a type name.
func (p *parser) parseNamed(name string) (Type, error) {
	switch name {
	case "array":
		return p.parseArray()
	case "map":
		return p.parseMap()
	case "row":
		return p.parseRow()
	case "decimal":
		nums, err := p.numbers()
		if err != nil {
			return Type{}, err
		}
		return decimalFrom(nums), nil
	case "timestamp":
		return p.parseTimestamp()
	}
	// Every other type's arguments -- varchar(10), char(3), time(3) -- say
	// nothing a file keeps.
	if _, err := p.numbers(); err != nil {
		return Type{}, err
	}
	if kind, ok := scalarKinds[name]; ok {
		return Scalar(kind), nil
	}
	// A type this does not know may run to several words -- "time(3) with
	// time zone", "interval day to second" -- all of which are its name.
	p.skipWords()
	return Type{Kind: Text}, nil
}

// scalarKinds maps the names of the declarable scalar types.
var scalarKinds = map[string]Kind{
	"boolean": Boolean, "tinyint": Tinyint, "smallint": Smallint, "integer": Integer, "int": Integer,
	"bigint": Bigint, "real": Real, "double": Double, "varchar": Varchar, "varbinary": Varbinary,
	"date": Date,
}

// decimalFrom builds a DECIMAL from its arguments. A bare "decimal" is left
// at zero precision for WithPrecision to fill.
func decimalFrom(nums []int) Type {
	t := Type{Kind: Decimal}
	if len(nums) > 0 {
		t.Precision = nums[0]
	}
	if len(nums) > 1 {
		t.Scale = nums[1]
	}
	return t
}

// parseTimestamp reads a timestamp's precision and zone.
func (p *parser) parseTimestamp() (Type, error) {
	nums, err := p.numbers()
	if err != nil {
		return Type{}, err
	}
	t := Type{Kind: Timestamp}
	if len(nums) > 0 {
		t.Precision = nums[0]
	}
	if p.skipZone() {
		t.Kind = TimestampTZ
	}
	return t, nil
}

// skipWords consumes words up to the next punctuation.
func (p *parser) skipWords() {
	for {
		if p.word() == "" {
			return
		}
	}
}

// skipZone consumes a trailing "with time zone" and reports whether there was
// one.
func (p *parser) skipZone() bool {
	save := p.i
	for _, want := range [...]string{"with", "time", "zone"} {
		if p.word() != want {
			p.i = save
			return false
		}
	}
	return true
}

// numbers reads an optional parenthesized list of integers.
func (p *parser) numbers() ([]int, error) {
	if !p.peek('(') {
		return nil, nil
	}
	p.i++
	var out []int
	for {
		n, err := strconv.Atoi(p.word())
		if err != nil {
			return nil, fmt.Errorf("expected a number at %d", p.i)
		}
		out = append(out, n)
		if p.peek(',') {
			p.i++
			continue
		}
		return out, p.expect(')')
	}
}

func (p *parser) parseArray() (Type, error) {
	if err := p.expect('('); err != nil {
		return Type{}, err
	}
	elem, err := p.parseType()
	if err != nil {
		return Type{}, err
	}
	return ArrayOf(elem), p.expect(')')
}

func (p *parser) parseMap() (Type, error) {
	if err := p.expect('('); err != nil {
		return Type{}, err
	}
	key, err := p.parseType()
	if err != nil {
		return Type{}, err
	}
	if err := p.expect(','); err != nil {
		return Type{}, err
	}
	value, err := p.parseType()
	if err != nil {
		return Type{}, err
	}
	return MapOf(key, value), p.expect(')')
}

func (p *parser) parseRow() (Type, error) {
	if err := p.expect('('); err != nil {
		return Type{}, err
	}
	var fields []Field
	for {
		f, err := p.rowField(len(fields))
		if err != nil {
			return Type{}, err
		}
		fields = append(fields, f)
		if p.peek(',') {
			p.i++
			continue
		}
		return RowOf(fields...), p.expect(')')
	}
}

// rowField reads one ROW field. A field Trino reports without a name --
// "row(bigint, varchar)" is an anonymous row -- takes the name "field<n>", its
// position, because a file cannot store a field with none.
func (p *parser) rowField(index int) (Field, error) {
	save := p.i
	quoted := p.peek('"')
	if name, err := p.fieldName(); err == nil {
		// A type this parser does not know runs to several words ("interval
		// day to second", "time with time zone"), so reading the first word
		// as a name always succeeds and names the field after its own type.
		// An unquoted name is taken only when what follows it is a type this
		// parser knows; Trino quotes the name of a field that has one.
		if ft, err := p.parseType(); err == nil && (quoted || ft.Kind != Text) {
			return Field{Name: name, Type: ft}, nil
		}
	}
	p.i = save
	ft, err := p.parseType()
	if err != nil {
		return Field{}, err
	}
	return Field{Name: "field" + strconv.Itoa(index), Type: ft}, nil
}

// fieldName reads a ROW field's name, quoted or not, lowercased. Trino folds a
// row's field names to lowercase, and a column's type is reported uppercased
// whole (the driver's ColumnTypeDatabaseTypeName), quoted names included, so
// the case in the text is not the field's.
func (p *parser) fieldName() (string, error) {
	if !p.peek('"') {
		name := p.word()
		if name == "" {
			return "", fmt.Errorf("expected a field name at %d", p.i)
		}
		return name, nil
	}
	p.i++
	var b strings.Builder
	for p.i < len(p.s) {
		c := p.s[p.i]
		p.i++
		if c != '"' {
			_ = b.WriteByte(c)
			continue
		}
		if p.i < len(p.s) && p.s[p.i] == '"' {
			_ = b.WriteByte('"')
			p.i++
			continue
		}
		return strings.ToLower(b.String()), nil
	}
	return "", errors.New("unterminated field name")
}
