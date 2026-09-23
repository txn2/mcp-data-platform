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
		name = withoutCommas(name)
		if name == "" {
			name = "column_" + strconv.Itoa(i+1)
		}
		columns = append(columns, Column{Name: uniqueName(name, seen), Type: ColumnType})
	}
	return columns
}

// withoutCommas removes the one character a Hive column name may not hold.
//
// The metastore stores a table's column list comma-separated, so a comma in a
// name is refused by the connector itself -- "Hive column names must not
// contain commas" -- and no quoting gets past it. Every other character a
// spreadsheet puts in a heading is accepted: a space, a dot, a colon, a
// parenthesis, a slash, a semicolon, a percent and an equals were each created
// without complaint on Trino 476 while a comma was refused.
//
// It is dropped rather than refused for the same reason a blank name is filled
// in positionally and a repeated one is suffixed: the file is what it is, and
// a table that refuses to exist over an ordinary export helps nobody. A
// Facebook Insights export names a column "Reactions, Comments and Shares" and
// could not be registered at all until this (#1774). Each comma becomes a
// space and the runs collapse, so that name is "Reactions Comments and
// Shares" -- what the heading reads as, addressable.
func withoutCommas(name string) string {
	if !strings.Contains(name, ",") {
		return name
	}
	return strings.Join(strings.Fields(strings.ReplaceAll(name, ",", " ")), " ")
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

// ColumnNames lists the names of a column list.
func ColumnNames(cols []Column) []string {
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
	}
	return names
}

// ColumnChanges says how one declaration of a table's columns differs from the
// next: the columns added, the columns removed, and the columns whose type
// changed, each with its type, in the order the new declaration lists them.
// It is empty when the two declare the same columns, and when they differ only
// in order.
func ColumnChanges(before, after []Column) string {
	was := make(map[string]string, len(before))
	for _, c := range before {
		was[c.Name] = c.Type
	}
	now := make(map[string]bool, len(after))
	var added, retyped, removed []string
	for _, c := range after {
		now[c.Name] = true
		prior, ok := was[c.Name]
		switch {
		case !ok:
			added = append(added, c.Name+" "+c.Type)
		case prior != c.Type:
			retyped = append(retyped, c.Name+" is now "+c.Type+" (was "+prior+")")
		}
	}
	for _, c := range before {
		if !now[c.Name] {
			removed = append(removed, c.Name)
		}
	}
	var parts []string
	if len(added) > 0 {
		parts = append(parts, "added "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "removed "+strings.Join(removed, ", "))
	}
	parts = append(parts, retyped...)
	return strings.Join(parts, "; ")
}

// VarcharColumns declares columns under the rule a JSON-lines registration
// made before typed columns keeps (#1833): every column VARCHAR. A nested value
// was refused under that rule, because the reader fails a query on an object
// or a list in a VARCHAR column, and it still is; registering the file again
// gives a typed registration that declares it. The error is the refusal's
// sentence.
func VarcharColumns(columns []Column) ([]Column, error) {
	out := make([]Column, 0, len(columns))
	for _, c := range columns {
		if strings.HasPrefix(c.Type, "ROW(") || strings.HasPrefix(c.Type, "ARRAY(") ||
			strings.HasPrefix(c.Type, "MAP(") {
			return nil, fmt.Errorf("the value of %q is a nested object or list, and this table declares every column "+
				"VARCHAR, which the reader fails every query on for such a value; register the file again under the "+
				"same name and its columns are declared with their types", c.Name)
		}
		out = append(out, Column{Name: c.Name, Type: ColumnType})
	}
	return out, nil
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
