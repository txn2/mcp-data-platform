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
package tablejsonl

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/platform/tablecsv"
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
// first seen. It refuses a file the reader would fail on or read wrongly,
// naming the line.
//
// The names are the keys, lowercased and otherwise untouched, because the
// reader finds a value by matching its key to a column name without regard to
// case. A CSV column can be renamed (tablecsv.ColumnsFrom fills a blank one and
// drops a comma) because a CSV is read by position; a key renamed here would be
// a column the reader never fills.
func Columns(body []byte) ([]tablecsv.Column, error) {
	if !utf8.Valid(body) {
		return nil, errors.New("the file is not valid UTF-8, which is the only encoding a JSON-lines table is read in")
	}
	body = bytes.TrimPrefix(body, bom)

	cols := &columnSet{seen: map[string]bool{}}
	lines := bytes.Split(body, []byte("\n"))
	for i, line := range lines {
		keys, err := lineKeys(bytes.TrimSuffix(line, []byte("\r")), i+1, i == len(lines)-1)
		if err != nil {
			return nil, err
		}
		if err := cols.add(keys); err != nil {
			return nil, err
		}
	}
	if len(cols.columns) == 0 {
		return nil, ErrNoRecords
	}
	return cols.columns, nil
}

// lineKeys reads one line's keys. The reader fails on a blank line anywhere
// but the end: the newline that terminates the last record leaves one empty
// fragment after it, and that one is not a line.
func lineKeys(line []byte, number int, last bool) ([]string, error) {
	if len(bytes.TrimSpace(line)) == 0 {
		if last {
			return nil, nil
		}
		return nil, fmt.Errorf("line %d is blank, and a JSON-lines table fails every query on a file with "+
			"a blank line in it; remove it", number)
	}
	keys, err := recordKeys(line)
	if err != nil {
		return nil, fmt.Errorf("line %d: %w", number, err)
	}
	return keys, nil
}

// columnSet is the union of the keys the records carry, in first-seen order.
type columnSet struct {
	columns []tablecsv.Column
	seen    map[string]bool
}

// add takes one record's keys into the set.
func (c *columnSet) add(keys []string) error {
	for _, key := range keys {
		if c.seen[key] {
			continue
		}
		c.seen[key] = true
		c.columns = append(c.columns, tablecsv.Column{Name: key, Type: tablecsv.ColumnType})
	}
	if len(c.columns) > maxColumns {
		return fmt.Errorf("the records carry more than the %d keys a registered table may declare", maxColumns)
	}
	return nil
}

// recordKeys reads one line as one record and returns its keys, lowercased,
// in the order written.
func recordKeys(line []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	dec.UseNumber()
	var record map[string]json.RawMessage
	if err := dec.Decode(&record); err != nil {
		return nil, notAnObject(err)
	}
	if record == nil {
		return nil, errors.New("the line is null rather than an object; each line has to be one JSON object")
	}
	// Anything after the object is a second value on the same line, which
	// the reader drops without a word: the one defect here that returns
	// wrong rows rather than failing.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("the line holds more than one JSON value, and the reader keeps only the first; " +
			"put each record on its own line")
	}
	ordered, err := keysInOrder(line)
	if err != nil {
		return nil, notAnObject(err)
	}
	folded := make(map[string]string, len(ordered))
	keys := make([]string, 0, len(ordered))
	for _, key := range ordered {
		if err := checkKey(key); err != nil {
			return nil, err
		}
		lower := strings.ToLower(key)
		if prior, dup := folded[lower]; dup {
			return nil, fmt.Errorf("the keys %q and %q are one column to the reader, which matches a key to "+
				"its column without regard to case, and it fails on the repeat; give each its own name", prior, key)
		}
		folded[lower] = key
		if nested(record[key]) {
			return nil, fmt.Errorf("the value of %q is a nested object or list, and the reader fails every query "+
				"on a file holding one; write it as a string of its JSON text", key)
		}
		keys = append(keys, lower)
	}
	return keys, nil
}

// notAnObject words a line that did not decode as one JSON object.
func notAnObject(err error) error {
	return fmt.Errorf("the line is not one JSON object (%s); each line has to be exactly one", err.Error())
}

// keysInOrder returns an object's keys in the order they are written,
// including a repeat, which decoding into a map would fold away. The line has
// already decoded as an object, so this walks tokens it knows are there.
func keysInOrder(line []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(line))
	if _, err := dec.Token(); err != nil { // the opening brace
		return nil, err //nolint:wrapcheck // worded by notAnObject
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err //nolint:wrapcheck // worded by notAnObject
		}
		key, _ := tok.(string)
		keys = append(keys, key)
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err //nolint:wrapcheck // worded by notAnObject
		}
	}
	return keys, nil
}

// checkKey refuses a key that cannot be a column the reader fills.
func checkKey(key string) error {
	switch {
	case key == "":
		return errors.New("a key is empty, and a column needs a name")
	case strings.TrimSpace(key) != key:
		return fmt.Errorf("the key %q begins or ends with whitespace, which a Hive column name may not", key)
	case strings.Contains(key, ","):
		return fmt.Errorf("the key %q holds a comma, which a Hive column name may not", key)
	}
	for _, r := range key {
		if r > utf8.RuneSelf-1 {
			return fmt.Errorf("the key %q holds a character outside ASCII, and the reader leaves such a column "+
				"empty on every row; rename it using ASCII", key)
		}
	}
	return nil
}

// nested reports whether a raw value is an object or a list.
func nested(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[')
}
