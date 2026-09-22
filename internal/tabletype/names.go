package tabletype

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// CheckName refuses a name a Hive column or ROW field cannot carry, or one
// the JSON reader would leave empty on every row. It is the rule for a
// JSON-lines key at any depth and for a Parquet column or field.
func CheckName(name string) error {
	switch {
	case name == "":
		return errors.New("a key is empty, and a column needs a name")
	case strings.TrimSpace(name) != name:
		return fmt.Errorf("the key %q begins or ends with whitespace, which a Hive column name may not", name)
	case strings.Contains(name, ","):
		return fmt.Errorf("the key %q holds a comma, which a Hive column name may not", name)
	}
	for _, r := range name {
		if r > utf8.RuneSelf-1 {
			return fmt.Errorf("the key %q holds a character outside ASCII, and the reader leaves such a column "+
				"empty on every row; rename it using ASCII", name)
		}
	}
	return nil
}

// CheckFieldName refuses a ROW field name the Hive metastore cannot store.
//
// A table's nested types are kept in the metastore as Hive type text
// ("struct<a:bigint>"), whose parser admits a field name made of letters,
// digits, '_', '.', '$' and ' ' and nothing else. Trino creates a table whose
// ROW declares any other name, and every later statement on it -- a SELECT, a
// DROP -- fails with "Could not read table schema" (observed on Trino 453), so
// a registration refused here is a table nobody is left unable to remove.
// A top-level column is not held to this: its name is stored on its own.
func CheckFieldName(name string) error {
	if err := CheckName(name); err != nil {
		return err
	}
	for _, r := range name {
		if !isFieldNameRune(r) {
			return fmt.Errorf("the nested key %q holds %q, and a field inside a row may hold only letters, digits, "+
				"'_', '.', '$' and spaces; rename it", name, r)
		}
	}
	return nil
}

func isFieldNameRune(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
		r == '_' || r == '.' || r == '$' || r == ' '
}
