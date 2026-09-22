package tableparquet

import (
	"bytes"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress"
	"github.com/parquet-go/parquet-go/encoding"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// ContentType is the media type of a Parquet file.
const ContentType = "application/vnd.apache.parquet"

// Extension is the file extension of a Parquet file.
const Extension = ".parquet"

// Write renders rows as a Parquet file with one column per entry of columns,
// in that order, compressed with ZSTD. Every column, element, map value and
// row field is optional, so a null anywhere is kept a null.
//
// A row's values are positional, matching columns. The values accepted for a
// type are the ones the two writers hand in: a query's (the Trino driver's own
// values, and its nested values as the JSON protocol carries them) and a
// script's (the values DecodeJSON produces). See convert.go.
//
// Every file written here registers with manage_table: the types it writes are
// the ones Columns reads back, which is why a type Parquet could store more
// precisely -- a UUID, a TIME -- is written as a string, and the names are
// held to the rules Columns holds them to before anything is written.
func Write(columns []tabletype.Column, rows [][]any) ([]byte, error) {
	if err := CheckNames(columns); err != nil {
		return nil, err
	}
	root := &orderedGroup{}
	plans := make([]*plan, 0, len(columns))
	next := 0
	for _, c := range columns {
		p, n, err := planFor(c.Type, &next)
		if err != nil {
			return nil, fmt.Errorf("the column %q: %w", c.Name, err)
		}
		root.fields = append(root.fields, field{Node: parquet.Optional(n), name: c.Name})
		plans = append(plans, p)
	}
	schema := parquet.NewSchema("table", root)

	var buf bytes.Buffer
	w := parquet.NewWriter(&buf, schema, parquet.Compression(&parquet.Zstd))
	for i, row := range rows {
		out, err := shredRow(plans, columns, row)
		if err != nil {
			return nil, fmt.Errorf("row %d, %w", i+1, err)
		}
		if _, err := w.WriteRows([]parquet.Row{out}); err != nil {
			return nil, fmt.Errorf("writing row %d: %w", i+1, err)
		}
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("closing the Parquet file: %w", err)
	}
	return buf.Bytes(), nil
}

// CheckNames refuses column and field names a table over the written file
// could not declare, before a byte of it is written.
//
// Parquet itself keeps any name, and Columns does not: a Hive column name may
// not be empty, edged with whitespace, hold a comma or leave ASCII, a ROW
// field name is narrower still, and two names one apart by case are one name
// to the reader. Without this an export writes a file and stores it, and the
// registration the export was made for then refuses it (#1833).
func CheckNames(columns []tabletype.Column) error {
	seen := make(map[string]string, len(columns))
	for _, c := range columns {
		if err := tabletype.CheckName(c.Name); err != nil {
			return fmt.Errorf("the column %q cannot be written: %w", c.Name, err)
		}
		lower := strings.ToLower(c.Name)
		if prior, dup := seen[lower]; dup {
			return fmt.Errorf("the columns %q and %q are one column to a table over the file, which matches a "+
				"column by name without regard to case; rename one of them", prior, c.Name)
		}
		seen[lower] = c.Name
		if err := checkFieldNames(c.Type, c.Name); err != nil {
			return err
		}
	}
	return nil
}

// checkFieldNames holds every ROW field under a type to CheckFieldName, at any
// depth. path names the column a refusal is about.
func checkFieldNames(t tabletype.Type, path string) error {
	switch t.Kind {
	case tabletype.Array:
		return checkFieldNames(*t.Elem, path)
	case tabletype.Map:
		return checkFieldNames(*t.Elem, path)
	case tabletype.Row:
		return checkRowFields(t, path)
	}
	return nil
}

// checkRowFields holds one ROW's field names to the metastore's rule and to
// each other.
func checkRowFields(t tabletype.Type, path string) error {
	seen := make(map[string]string, len(t.Fields))
	for _, f := range t.Fields {
		if err := tabletype.CheckFieldName(f.Name); err != nil {
			return fmt.Errorf("in the column %q, %w", path, err)
		}
		lower := strings.ToLower(f.Name)
		if prior, dup := seen[lower]; dup {
			return fmt.Errorf("in the column %q, the fields %q and %q are one field to the reader, which matches "+
				"a field by name without regard to case; rename one of them", path, prior, f.Name)
		}
		seen[lower] = f.Name
		if err := checkFieldNames(f.Type, path+"."+f.Name); err != nil {
			return err
		}
	}
	return nil
}

// shredRow turns one row into the leaf values of every column, each with the
// repetition and definition levels that place it (the Dremel encoding), in
// column order.
func shredRow(plans []*plan, columns []tabletype.Column, row []any) (parquet.Row, error) {
	s := &shredder{}
	for i, p := range plans {
		var v any
		if i < len(row) {
			v = row[i]
		}
		if err := s.write(p, v, 0, 1, 0); err != nil {
			return nil, fmt.Errorf("column %q: %w", columns[i].Name, err)
		}
	}
	sort.SliceStable(s.out, func(a, b int) bool { return s.out[a].Column() < s.out[b].Column() })
	return s.out, nil
}

// plan is one type with the leaf column indexes it writes to, assigned in the
// depth-first order the schema lists its columns in.
type plan struct {
	t      tabletype.Type
	leaf   int     // a scalar's column index
	elem   *plan   // an array's element, a map's value
	key    *plan   // a map's key
	fields []*plan // a row's fields
	names  []string
}

// planFor builds the plan and the schema node for a type.
func planFor(t tabletype.Type, next *int) (*plan, parquet.Node, error) {
	p := &plan{t: t}
	switch t.Kind {
	case tabletype.Array:
		elem, n, err := planFor(*t.Elem, next)
		if err != nil {
			return nil, nil, err
		}
		p.elem = elem
		return p, parquet.List(parquet.Optional(n)), nil
	case tabletype.Map:
		return planMap(p, t, next)
	case tabletype.Row:
		return planRow(p, t, next)
	}
	n, err := leafNode(t)
	if err != nil {
		return nil, nil, err
	}
	p.leaf = *next
	*next++
	return p, n, nil
}

func planMap(p *plan, t tabletype.Type, next *int) (*plan, parquet.Node, error) {
	if t.Key.Nested() {
		return nil, nil, fmt.Errorf("a map key cannot be %s", t.Key.SQL())
	}
	key, kn, err := planFor(*t.Key, next)
	if err != nil {
		return nil, nil, err
	}
	value, vn, err := planFor(*t.Elem, next)
	if err != nil {
		return nil, nil, err
	}
	p.key, p.elem = key, value
	return p, parquet.Map(parquet.Required(kn), parquet.Optional(vn)), nil
}

func planRow(p *plan, t tabletype.Type, next *int) (*plan, parquet.Node, error) {
	g := &orderedGroup{}
	for _, f := range t.Fields {
		fp, n, err := planFor(f.Type, next)
		if err != nil {
			return nil, nil, err
		}
		p.fields = append(p.fields, fp)
		p.names = append(p.names, f.Name)
		g.fields = append(g.fields, field{Node: parquet.Optional(n), name: f.Name})
	}
	return p, g, nil
}

// orderedGroup is a Parquet group whose fields keep the order they were
// declared in. parquet.Group is a map and lists its fields sorted by name,
// which would write a query's columns and a row's fields alphabetically: a
// table registered over the file would declare them in an order nobody chose,
// and SELECT * would not return the columns the query did.
type orderedGroup struct {
	fields []parquet.Field
}

// ID is the field id, which a group written here does not carry.
func (*orderedGroup) ID() int { return 0 }

// String names the node in a schema listing.
func (*orderedGroup) String() string { return "group" }

// Type is the group type parquet-go gives every group.
func (*orderedGroup) Type() parquet.Type { return parquet.Group{}.Type() }

// Optional reports false: optionality is the wrapper's, as it is for Group.
func (*orderedGroup) Optional() bool { return false }

// Repeated reports false, as for Group.
func (*orderedGroup) Repeated() bool { return false }

// Required reports true, as for Group.
func (*orderedGroup) Required() bool { return true }

// Leaf reports false: a group holds fields.
func (*orderedGroup) Leaf() bool { return false }

// Fields are the group's fields in the order they were declared.
func (g *orderedGroup) Fields() []parquet.Field { return g.fields }

// Encoding is the writer's default.
func (*orderedGroup) Encoding() encoding.Encoding { return nil }

// Compression is the writer's, set for the whole file.
func (*orderedGroup) Compression() compress.Codec { return nil }

// GoType is what a reflecting reader would decode a row into.
func (*orderedGroup) GoType() reflect.Type { return reflect.TypeFor[map[string]any]() }

// field names one child of an orderedGroup. Value is the reflection hook a
// Go-struct writer uses; this package writes rows it shredded itself and never
// calls it.
type field struct {
	parquet.Node
	name string
}

// Name is the field's name in its group.
func (f field) Name() string { return f.name }

// Value is unused; see field.
func (field) Value(reflect.Value) reflect.Value { return reflect.Value{} }
