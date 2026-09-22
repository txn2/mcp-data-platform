// Package tableparquet reads and writes the Parquet files a registered table
// is declared over (#1833). It reads a file's columns from its footer alone --
// the last bytes of the file -- and refuses one a table over it could not read
// back exactly; it writes query results and script rows as Parquet a table can
// be registered over. It knows nothing about registrations: tableregister asks
// it for columns, and the export paths hand it rows.
//
// The type mapping in both directions is written against the Hive connector
// the scratch catalogs use, with hive.timestamp-precision=MICROSECONDS. A
// Parquet type outside it is refused by name rather than declared as something
// that reads back differently.
package tableparquet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// Magic is the four bytes a Parquet file begins and ends with.
const Magic = "PAR1"

// footerLenBytes is the width of the footer length that precedes the trailing
// magic, and minFileSize the smallest file those two and the leading magic fit
// in.
const (
	footerLenBytes = 4
	// trailerBytes is the footer length and the trailing magic together, the
	// last bytes of every Parquet file.
	trailerBytes = footerLenBytes + len(Magic)
	minFileSize  = int64(len(Magic) + trailerBytes)
)

// maxColumns caps how wide a registered table can be, matching the bound a
// CSV header and a JSON-lines file are held to.
const maxColumns = 512

// maxDecimalPrecision is the widest DECIMAL Trino declares.
const maxDecimalPrecision = 38

// ErrNotParquet means the bytes are not a Parquet file: they do not begin and
// end with the magic, or the footer between does not parse.
var ErrNotParquet = errors.New("the file is not a Parquet file")

// notParquetError is ErrNotParquet with the reason the bytes are not Parquet.
type notParquetError struct{ reason string }

func (e *notParquetError) Error() string { return ErrNotParquet.Error() + ": " + e.reason }

// Is answers errors.Is for the sentinel.
func (*notParquetError) Is(target error) bool { return target == ErrNotParquet }

// notParquetf builds an ErrNotParquet naming why.
func notParquetf(tmpl string, args ...any) error {
	return &notParquetError{reason: fmt.Sprintf(tmpl, args...)}
}

// Columns reads a Parquet file's footer through r, which holds size bytes, and
// returns the columns a table over it declares, in file order, each lowercased
// and typed by the mapping. Only the footer is read: the first four bytes, the
// last eight, and the footer they point at, so a file of any size costs a few
// kilobytes.
func Columns(r io.ReaderAt, size int64) ([]tabletype.Column, error) {
	if err := checkMagic(r, size); err != nil {
		return nil, err
	}
	f, err := parquet.OpenFile(r, size, parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true))
	if err != nil {
		return nil, notParquetf("its footer could not be read (%s)", err.Error())
	}
	root, err := buildTree(f.Metadata().Schema)
	if err != nil {
		return nil, err
	}
	return columnsOf(root)
}

// checkMagic refuses bytes that are not a Parquet file before the footer is
// parsed, so a file with the wrong name says so rather than reporting a thrift
// error. An encrypted footer ("PARE") is named as such: it is Parquet, and the
// platform holds no key to read it with.
//
// The four bytes before the trailing magic are the footer's length, and the
// reader allocates that many bytes before reading them, so a 16-byte file
// claiming a 4 GiB footer would allocate 4 GiB to fail. The length is held to
// the file it came from here, where the refusal costs one buffer.
func checkMagic(r io.ReaderAt, size int64) error {
	if size < minFileSize {
		return notParquetf("it is %d bytes, shorter than the smallest Parquet file", size)
	}
	head := make([]byte, len(Magic))
	tail := make([]byte, trailerBytes)
	if _, err := r.ReadAt(head, 0); err != nil {
		return fmt.Errorf("reading the start of the file: %w", err)
	}
	if _, err := r.ReadAt(tail, size-int64(len(tail))); err != nil {
		return fmt.Errorf("reading the end of the file: %w", err)
	}
	trailer := string(tail[footerLenBytes:])
	if trailer == "PARE" {
		return errors.New("the Parquet file's footer is encrypted, and a table cannot be read over an encrypted file")
	}
	if string(head) != Magic || trailer != Magic {
		return notParquetf("it does not begin and end with %q", Magic)
	}
	footerSize := int64(binary.LittleEndian.Uint32(tail[:footerLenBytes]))
	if footerSize == 0 || footerSize > size-minFileSize {
		return notParquetf("its footer says it is %d bytes, which does not fit in a file of %d bytes", footerSize, size)
	}
	return nil
}

// node is one element of the schema tree the footer lists depth-first.
type node struct {
	el       format.SchemaElement
	children []*node
}

// buildTree rebuilds the schema tree from the footer's flat list.
func buildTree(els []format.SchemaElement) (*node, error) {
	if len(els) == 0 {
		return nil, notParquetf("its footer declares no schema")
	}
	root, next, err := subtree(els, 0, 0)
	if err != nil {
		return nil, err
	}
	if next != len(els) {
		return nil, notParquetf("its schema lists %d elements and describes %d", len(els), next)
	}
	return root, nil
}

func subtree(els []format.SchemaElement, i, depth int) (*node, int, error) {
	if depth > maxNesting {
		return nil, 0, fmt.Errorf("the file's schema nests more than %d levels deep", maxNesting)
	}
	n := &node{el: els[i]}
	count, _ := els[i].NumChildren.Get()
	next := i + 1
	for range count {
		if next >= len(els) {
			return nil, 0, notParquetf("its schema ends inside %q", els[i].Name)
		}
		child, after, err := subtree(els, next, depth+1)
		if err != nil {
			return nil, 0, err
		}
		n.children = append(n.children, child)
		next = after
	}
	return n, next, nil
}

// maxNesting bounds how deep a schema this reads, so a hostile footer cannot
// run the walk out of stack.
const maxNesting = 32

// columnsOf maps the root's children to columns and holds them to the rules a
// table's columns are held to.
func columnsOf(root *node) ([]tabletype.Column, error) {
	switch n := len(root.children); {
	case n == 0:
		return nil, errors.New("the Parquet file declares no columns")
	case n > maxColumns:
		return nil, fmt.Errorf("the Parquet file declares %d columns, more than the %d a registered table may declare",
			n, maxColumns)
	}
	seen := make(map[string]string, len(root.children))
	columns := make([]tabletype.Column, 0, len(root.children))
	for _, child := range root.children {
		name := child.el.Name
		if err := tabletype.CheckName(name); err != nil {
			return nil, fmt.Errorf("the column %q cannot be declared: %w", name, err)
		}
		lower := strings.ToLower(name)
		if prior, dup := seen[lower]; dup {
			return nil, fmt.Errorf("the columns %q and %q are one column to the reader, which matches a Parquet "+
				"column by name without regard to case; rename one of them", prior, name)
		}
		seen[lower] = name
		t, err := fieldType(child, name)
		if err != nil {
			return nil, err
		}
		columns = append(columns, tabletype.Column{Name: lower, Type: t})
	}
	return columns, nil
}

// fieldType maps one field -- a column, or a field inside one -- to the type
// it is declared as. path names it in a refusal.
func fieldType(n *node, path string) (tabletype.Type, error) {
	if rep, _ := n.el.RepetitionType.Get(); rep == format.Repeated {
		return tabletype.Type{}, unsupported(path, "a repeated field outside a LIST or MAP group")
	}
	if len(n.children) == 0 && n.el.Type.Valid {
		return leafType(n.el, path)
	}
	switch {
	case isList(n.el):
		return listType(n, path)
	case isMap(n.el):
		return mapType(n, path)
	case n.el.LogicalType.Value != nil:
		return tabletype.Type{}, unsupported(path, "a group annotated "+n.el.LogicalType.String())
	}
	return rowType(n, path)
}

func isList(el format.SchemaElement) bool {
	if _, ok := el.LogicalType.Value.(*format.ListType); ok {
		return true
	}
	ct, ok := el.ConvertedType.Get()
	return ok && ct == convertedList
}

func isMap(el format.SchemaElement) bool {
	if _, ok := el.LogicalType.Value.(*format.MapType); ok {
		return true
	}
	ct, ok := el.ConvertedType.Get()
	return ok && (ct == convertedMap || ct == convertedMapKeyValue)
}

// rowType maps a plain group to a ROW of its fields.
func rowType(n *node, path string) (tabletype.Type, error) {
	if len(n.children) == 0 {
		return tabletype.Type{}, unsupported(path, "a group with no fields")
	}
	seen := make(map[string]string, len(n.children))
	fields := make([]tabletype.Field, 0, len(n.children))
	for _, child := range n.children {
		name := child.el.Name
		if err := tabletype.CheckFieldName(name); err != nil {
			return tabletype.Type{}, fmt.Errorf("in the column %q, %w", path, err)
		}
		lower := strings.ToLower(name)
		if prior, dup := seen[lower]; dup {
			return tabletype.Type{}, fmt.Errorf("in the column %q, the fields %q and %q are one field to the reader, "+
				"which matches a field by name without regard to case; rename one of them", path, prior, name)
		}
		seen[lower] = name
		t, err := fieldType(child, path+"."+name)
		if err != nil {
			return tabletype.Type{}, err
		}
		fields = append(fields, tabletype.Field{Name: lower, Type: t})
	}
	return tabletype.RowOf(fields...), nil
}

// listType maps a LIST group by the Parquet format's backward-compatibility
// rules for where the element sits: a primitive repeated child is the element;
// a repeated group of more than one field, or one named "array" or
// "<list>_tuple", is itself the element (as a ROW); otherwise the repeated
// group's one field is.
func listType(n *node, path string) (tabletype.Type, error) {
	if len(n.children) != 1 {
		return tabletype.Type{}, unsupported(path, "a LIST group with other than one child")
	}
	rep := n.children[0]
	if r, _ := rep.el.RepetitionType.Get(); r != format.Repeated {
		return tabletype.Type{}, unsupported(path, "a LIST group whose child is not repeated")
	}
	var (
		elem tabletype.Type
		err  error
	)
	switch {
	case len(rep.children) == 0:
		elem, err = leafType(rep.el, path+"[]")
	case len(rep.children) > 1 || rep.el.Name == "array" || rep.el.Name == n.el.Name+"_tuple":
		elem, err = rowType(rep, path+"[]")
	default:
		elem, err = fieldType(rep.children[0], path+"[]")
	}
	if err != nil {
		return tabletype.Type{}, err
	}
	return tabletype.ArrayOf(elem), nil
}

// mapType maps a MAP group: one repeated key_value group holding a required
// primitive key and a value.
func mapType(n *node, path string) (tabletype.Type, error) {
	if len(n.children) != 1 || len(n.children[0].children) != 2 {
		return tabletype.Type{}, unsupported(path, "a MAP group not shaped as one repeated key/value pair")
	}
	kv := n.children[0]
	key, value := kv.children[0], kv.children[1]
	if len(key.children) != 0 {
		return tabletype.Type{}, unsupported(path, "a MAP whose key is a group")
	}
	kt, err := leafType(key.el, path+".key")
	if err != nil {
		return tabletype.Type{}, err
	}
	vt, err := fieldType(value, path+".value")
	if err != nil {
		return tabletype.Type{}, err
	}
	return tabletype.MapOf(kt, vt), nil
}

// unsupported refuses a field whose Parquet type no declaration reads back
// exactly, naming it and the type.
func unsupported(path, what string) error {
	return fmt.Errorf("the column %q is %s, which no column type a registered table declares "+
		"reads back exactly; write it as a supported type, or leave it out of the file", path, what)
}
