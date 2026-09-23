package tabletype

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Column is one column an inference declares.
type Column struct {
	Name string
	Type Type
}

// Inferrer reads the type of every column across a sequence of records, every
// record rather than a sample: one value on the last line that does not fit
// what the first thousand said is a query that fails on the last line.
//
// The rules, per key, ignoring null and an absent key:
//
//   - every value a JSON boolean: BOOLEAN;
//   - every value an integer within int64: BIGINT;
//   - any number with a fraction or an exponent, alone or among integers:
//     DOUBLE;
//   - anything else a scalar can be -- strings, a mix of kinds, an integer
//     too large for BIGINT, nothing but nulls: VARCHAR, which is the text of
//     the value and loses nothing;
//   - objects: a ROW over the union of their keys, each field inferred the
//     same way; lists: an ARRAY of what their elements infer to.
//
// A key whose values cannot be one type is refused, naming the key, because no
// declaration reads both: an object, a list and a scalar are three shapes, and
// a key holding any two of them in different records is a conflict. Two
// scalars are not -- they meet at VARCHAR.
type Inferrer struct {
	cols  []*column
	index map[string]int
}

type column struct {
	name  string
	shape *shape
}

// NewInferrer returns an Inferrer with no columns.
func NewInferrer() *Inferrer { return &Inferrer{index: map[string]int{}} }

// Observe takes one record: its column names, in the order written, and the
// value under each. A name seen for the first time adds a column at the end.
// Values are what DecodeJSON produces.
func (in *Inferrer) Observe(names []string, values []any) error {
	for i, name := range names {
		idx, ok := in.index[name]
		if !ok {
			idx = len(in.cols)
			in.index[name] = idx
			in.cols = append(in.cols, &column{name: name, shape: &shape{}})
		}
		if err := in.cols[idx].shape.observe(values[i], name); err != nil {
			return err
		}
	}
	return nil
}

// Len is how many columns the records have named so far.
func (in *Inferrer) Len() int { return len(in.cols) }

// Columns returns the columns in first-seen order with the type each infers
// to.
func (in *Inferrer) Columns() ([]Column, error) {
	out := make([]Column, 0, len(in.cols))
	for _, c := range in.cols {
		t, err := c.shape.typ(c.name)
		if err != nil {
			return nil, err
		}
		out = append(out, Column{Name: c.name, Type: t})
	}
	return out, nil
}

// shapeKind is what the values under one key have been so far.
type shapeKind int

const (
	shapeNone shapeKind = iota
	shapeBool
	shapeInt
	shapeDouble
	shapeText
	shapeMixed
	shapeObject
	shapeArray
)

// describe names a kind the way a refusal speaks of it.
func (k shapeKind) describe() string {
	switch k {
	case shapeObject:
		return "an object"
	case shapeArray:
		return "a list"
	case shapeNone:
		return "null"
	default:
		return "a scalar"
	}
}

// shape is the running inference for one key: its kind, and for an object its
// fields, lowercased, in first-seen order, and for a list its element. A field
// is lowercased because the reader matches a key to it without regard to case
// and the metastore stores it lowercased whatever it was declared as, so the
// name a registration records is the one a query sees.
type shape struct {
	kind   shapeKind
	fields []string
	sub    map[string]*shape
	elem   *shape
}

func (s *shape) observe(v any, path string) error {
	switch x := v.(type) {
	case nil:
		return nil
	case *Object:
		return s.observeObject(x, path)
	case []any:
		return s.observeArray(x, path)
	default:
		return s.observeScalar(scalarKind(x), path)
	}
}

// scalarKind classifies one scalar value.
func scalarKind(v any) shapeKind {
	switch x := v.(type) {
	case bool:
		return shapeBool
	case json.Number:
		return numberKind(x)
	default:
		return shapeText
	}
}

// numberKind classifies a JSON number. A number outside the type that would
// hold it is text, which keeps the digits the file holds: a BIGINT cannot hold
// an integer outside int64 and a DOUBLE would round it, and a fractional
// number outside float64 would be stored as an infinity.
func numberKind(n json.Number) shapeKind {
	s := string(n)
	if strings.ContainsAny(s, ".eE") {
		f, err := strconv.ParseFloat(s, float64Bits)
		if err != nil || math.IsInf(f, 0) {
			return shapeText
		}
		return shapeDouble
	}
	if _, err := strconv.ParseInt(s, decimalBase, int64Bits); err != nil {
		return shapeText
	}
	return shapeInt
}

// The base and width an integer is read at to decide whether BIGINT holds it.
const (
	decimalBase = 10
	int64Bits   = 64
	float64Bits = 64
)

func isNumber(k shapeKind) bool { return k == shapeInt || k == shapeDouble }

func (s *shape) observeScalar(k shapeKind, path string) error {
	switch {
	case s.kind == shapeNone:
		s.kind = k
	case s.kind == shapeObject || s.kind == shapeArray:
		return conflict(path, s.kind, k)
	case s.kind == k:
	case isNumber(s.kind) && isNumber(k):
		s.kind = shapeDouble
	default:
		s.kind = shapeMixed
	}
	return nil
}

func (s *shape) observeObject(o *Object, path string) error {
	if err := s.become(shapeObject, path); err != nil {
		return err
	}
	seen := make(map[string]string, len(o.Keys))
	for i, key := range o.Keys {
		if err := CheckFieldName(key); err != nil {
			return fmt.Errorf("in %q, %w", path, err)
		}
		lower := strings.ToLower(key)
		if prior, dup := seen[lower]; dup {
			return fmt.Errorf("in %q, the keys %q and %q are one field to the reader, which matches a key "+
				"without regard to case; give each its own name", path, prior, key)
		}
		seen[lower] = key
		child, ok := s.sub[lower]
		if !ok {
			child = &shape{}
			s.sub[lower] = child
			s.fields = append(s.fields, lower)
		}
		if err := child.observe(o.Values[i], path+"."+key); err != nil {
			return err
		}
	}
	return nil
}

func (s *shape) observeArray(values []any, path string) error {
	if err := s.become(shapeArray, path); err != nil {
		return err
	}
	for _, v := range values {
		if err := s.elem.observe(v, path+"[]"); err != nil {
			return err
		}
	}
	return nil
}

// become settles a nested kind on a shape that has seen none, or refuses one
// that has seen something else.
func (s *shape) become(k shapeKind, path string) error {
	if s.kind == k {
		return nil
	}
	if s.kind != shapeNone {
		return conflict(path, s.kind, k)
	}
	s.kind = k
	if k == shapeObject {
		s.sub = map[string]*shape{}
	} else {
		s.elem = &shape{}
	}
	return nil
}

// conflict words a key whose values cannot be one type.
func conflict(path string, earlier, now shapeKind) error {
	return fmt.Errorf("the value of %q is %s here and was %s before it, and a column declares one type; "+
		"write every value of it the same way, or write it as a string of its JSON text",
		path, now.describe(), earlier.describe())
}

// typ renders the inferred type.
func (s *shape) typ(path string) (Type, error) {
	switch s.kind {
	case shapeBool:
		return Scalar(Boolean), nil
	case shapeInt:
		return Scalar(Bigint), nil
	case shapeDouble:
		return Scalar(Double), nil
	case shapeObject:
		return s.rowType(path)
	case shapeArray:
		elem, err := s.elem.typ(path + "[]")
		if err != nil {
			return Type{}, err
		}
		return ArrayOf(elem), nil
	default:
		return Scalar(Varchar), nil
	}
}

func (s *shape) rowType(path string) (Type, error) {
	if len(s.fields) == 0 {
		return Type{}, fmt.Errorf("the value of %q is an object with no keys on every line, and a column "+
			"cannot be declared as a row with no fields; remove it or write it as a string", path)
	}
	fields := make([]Field, 0, len(s.fields))
	for _, name := range s.fields {
		t, err := s.sub[name].typ(path + "." + name)
		if err != nil {
			return Type{}, err
		}
		fields = append(fields, Field{Name: name, Type: t})
	}
	return RowOf(fields...), nil
}
