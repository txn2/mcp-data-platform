package tablecsv

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Column is one column a CSV header declares, as the table over the file
// records it. Type is recorded even though Hive CSV admits exactly one: a
// reader of the record should not have to know the connector's rule to know
// what a query will get back, and a stored type is what a later format would
// vary.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// ColumnType is the only column type a Hive CSV table admits. Declaring a
// table with any other type is refused by Trino itself -- "Hive CSV storage
// format only supports VARCHAR (unbounded)" -- so this is the connector's rule
// rather than a choice the platform makes, and a join against a typed
// warehouse column needs a CAST.
const ColumnType = "VARCHAR"

// ErrEmptyHeader means the file had no header row to take columns from.
var ErrEmptyHeader = errors.New("the file has no header row, so the table has no column names")

// ErrUncorrectable marks a file that cannot be read as a table and cannot be
// corrected here either: the reason names what is wrong and where it has to
// be fixed. A registrar answers it as a refusal the caller can act on.
var ErrUncorrectable = errors.New("the file cannot be corrected")

// uncorrectable carries the reason without the sentinel's text in front of
// it: the sentence already says what to do.
type uncorrectable struct{ reason string }

func (e *uncorrectable) Error() string { return e.reason }

// Is answers errors.Is for the sentinel.
func (*uncorrectable) Is(target error) bool { return target == ErrUncorrectable }

// uncorrectablef builds an ErrUncorrectable refusal.
func uncorrectablef(format string, args ...any) error {
	return &uncorrectable{reason: strings.TrimSpace(fmt.Sprintf(format, args...))}
}

// ColumnsFrom names the columns a header record declares. It is separate from
// the header read so that a refusal describing a file calls its columns what
// the table over that file would have called them, without parsing the header
// a second time.
func ColumnsFrom(record []string) []Column {
	seen := make(map[string]int, len(record))
	columns := make([]Column, 0, len(record))
	for i, raw := range record {
		name := strings.TrimSpace(raw)
		// A UTF-8 BOM leads the first field of a file many spreadsheet tools
		// write, and it would otherwise become part of the first column's name.
		if i == 0 {
			name = strings.TrimPrefix(name, bomUTF8)
		}
		if name == "" {
			name = "column_" + strconv.Itoa(i+1)
		}
		columns = append(columns, Column{Name: uniqueName(name, seen), Type: ColumnType})
	}
	return columns
}

// uniqueName disambiguates a repeated column name by suffixing it, and records
// the result so the suffix itself cannot collide.
func uniqueName(name string, seen map[string]int) string {
	key := strings.ToLower(name)
	n, taken := seen[key]
	if !taken {
		seen[key] = 1
		return name
	}
	for {
		n++
		candidate := name + "_" + strconv.Itoa(n)
		if _, clash := seen[strings.ToLower(candidate)]; !clash {
			seen[key] = n
			seen[strings.ToLower(candidate)] = 1
			return candidate
		}
	}
}

// JoinAnd renders a short list in prose so a refusal names what is in the way
// rather than printing a slice.
func JoinAnd(items []string) string {
	if len(items) == 0 {
		return ""
	}
	head, last := items[:len(items)-1], items[len(items)-1]
	switch len(head) {
	case 0:
		return last
	case 1:
		return head[0] + " and " + last
	default:
		return strings.Join(head, ", ") + ", and " + last
	}
}
