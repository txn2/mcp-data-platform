// Package tablejsonl inspects a JSON-lines file before a table is registered
// over it, and reads the columns that table declares (#1820). It knows nothing
// about registrations; tableregister asks it and acts on the answer, as it
// asks tablecsv about a CSV.
//
// A JSON-lines table is read by Trino's Hive JSON reader, one object per line,
// and it is the format a table reads back exactly: every string a JSON string
// can hold, line breaks included, and a null that stays a null. What the reader
// cannot read is the shape of a line, not the text inside it, and it fails in
// one of two ways. Most defects fail every query on the table, which is loud
// but leaves a registration nobody can use. One is silent: a second object on
// the same line is dropped. Each was observed on Trino 453, and each is refused
// here, by line, before the DDL runs.
//
// The columns are typed from the values (#1833): the JSON reader returns a
// number in a BIGINT or DOUBLE column, a boolean in a BOOLEAN one, and a nested
// object or list in a ROW or an ARRAY, so declaring every column VARCHAR -- the
// rule of the CSV reader, which admits nothing else -- threw away what the file
// already said and made every aggregate cast.
package tablejsonl

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/platform/tablecsv"
	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// maxColumns caps how wide a registered table can be, matching the bound a
// CSV header is held to.
const maxColumns = 512

// ErrNoRecords means the file holds no record to take a column from. A table
// declares at least one column, and a JSON-lines file carries its columns only
// in its records.
var ErrNoRecords = errors.New("the file holds no records, so there are no columns to declare; " +
	"a JSON-lines file names its columns only in its records")

// bom is the byte-order mark some writers lead a UTF-8 file with. The reader
// skips it, so it is skipped here as well rather than refused.
var bom = []byte("\ufeff")

// Columns reads a JSON-lines file and returns the columns a table over it
// declares: every key any record carries, lowercased, in the order they are
// first seen, each with the type its values infer to (tabletype.Inferrer). It
// refuses a file the reader would fail on or read wrongly, naming the line.
//
// The names are the keys, lowercased and otherwise untouched, because the
// reader finds a value by matching its key to a column name without regard to
// case. A CSV column can be renamed (tablecsv.ColumnsFrom fills a blank one and
// drops a comma) because a CSV is read by position; a key renamed here would be
// a column the reader never fills.
//
// Every record is read, not a sample. A nested object or list is declared as
// a ROW or an ARRAY (#1833); a key that holds an object on one line and a list
// or a scalar on another is refused, because no declaration reads both.
func Columns(body []byte) ([]tablecsv.Column, error) {
	if !utf8.Valid(body) {
		return nil, errors.New("the file is not valid UTF-8, which is the only encoding a JSON-lines table is read in")
	}
	body = bytes.TrimPrefix(body, bom)

	infer := tabletype.NewInferrer()
	lines := bytes.Split(body, []byte("\n"))
	for i, line := range lines {
		if err := readLine(infer, bytes.TrimSuffix(line, []byte("\r")), i+1, i == len(lines)-1); err != nil {
			return nil, err
		}
	}
	if infer.Len() == 0 {
		return nil, ErrNoRecords
	}
	inferred, err := infer.Columns()
	if err != nil {
		return nil, err //nolint:wrapcheck // the inference's sentence is the refusal
	}
	columns := make([]tablecsv.Column, 0, len(inferred))
	for _, c := range inferred {
		columns = append(columns, tablecsv.Column{Name: c.Name, Type: c.Type.SQL()})
	}
	return columns, nil
}

// readLine reads one line into the inference. The reader fails on a blank
// line anywhere but the end: the newline that terminates the last record
// leaves one empty fragment after it, and that one is not a line.
func readLine(infer *tabletype.Inferrer, line []byte, number int, last bool) error {
	if len(bytes.TrimSpace(line)) == 0 {
		if last {
			return nil
		}
		return fmt.Errorf("line %d is blank, and a JSON-lines table fails every query on a file with "+
			"a blank line in it; remove it", number)
	}
	keys, values, err := record(line)
	if err == nil {
		err = infer.Observe(keys, values)
	}
	if err == nil && infer.Len() > maxColumns {
		err = fmt.Errorf("the records carry more than the %d keys a registered table may declare", maxColumns)
	}
	if err != nil {
		return fmt.Errorf("line %d: %w", number, err)
	}
	return nil
}

// record reads one line as one record and returns its keys, lowercased, in the
// order written, with the value under each.
func record(line []byte) (keys []string, values []any, err error) {
	v, err := tabletype.DecodeJSON(line)
	switch {
	case tabletype.IsTrailing(err):
		// Anything after the object is a second value on the same line, which
		// the reader drops without a word: the one defect here that returns
		// wrong rows rather than failing.
		return nil, nil, errors.New("the line holds more than one JSON value, and the reader keeps only the first; " +
			"put each record on its own line")
	case errors.Is(err, tabletype.ErrTooDeep):
		return nil, nil, err //nolint:wrapcheck // the sentence names the limit
	case err != nil:
		return nil, nil, notAnObject(err)
	case v == nil:
		return nil, nil, errors.New("the line is null rather than an object; each line has to be one JSON object")
	}
	obj, ok := v.(*tabletype.Object)
	if !ok {
		return nil, nil, notAnObject(errors.New("it is not an object"))
	}
	folded := make(map[string]string, len(obj.Keys))
	keys = make([]string, 0, len(obj.Keys))
	for _, key := range obj.Keys {
		if err := tabletype.CheckName(key); err != nil {
			return nil, nil, err //nolint:wrapcheck // the sentence names the key
		}
		lower := strings.ToLower(key)
		if prior, dup := folded[lower]; dup {
			return nil, nil, fmt.Errorf("the keys %q and %q are one column to the reader, which matches a key to "+
				"its column without regard to case, and it fails on the repeat; give each its own name", prior, key)
		}
		folded[lower] = key
		keys = append(keys, lower)
	}
	return keys, obj.Values, nil
}

// notAnObject words a line that did not decode as one JSON object.
func notAnObject(err error) error {
	return fmt.Errorf("the line is not one JSON object (%s); each line has to be exactly one", err.Error())
}
