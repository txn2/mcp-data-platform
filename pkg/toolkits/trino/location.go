package trino

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
)

// LocationInterceptor refuses, in every SQL a person sends through the Trino
// tools, a statement that points a table, a schema or a partition at one of
// the platform's own buckets (#2616). A catalog that registers tables over
// managed resources holds credentials that reach the resource bucket by
// design, so an external table a person creates over another library's
// prefix reads it whatever the S3 tools refuse.
//
// A location the check cannot read is refused too: a location property or a
// procedure argument assembled from parts (concat, ||, a cast, a Unicode
// escape string) and a table that copies another's properties. The
// platform's own registrations run through Toolkit.Exec, which runs no query
// interceptor, so they are unaffected.
type LocationInterceptor struct{}

// Intercept refuses the statement when it reaches a platform bucket or names
// a location the check cannot read.
func (LocationInterceptor) Intercept(_ context.Context, sql string, _ trinotools.ToolName) (string, error) {
	if err := checkLocations(sql, 0); err != nil {
		return "", err
	}
	return sql, nil
}

// maxImmediateDepth bounds EXECUTE IMMEDIATE nested in the string it runs.
const maxImmediateDepth = 3

// locationKeys are the table and schema properties that name where data is
// read from: Hive's external_location and the location the Iceberg, Delta
// Lake and Hudi connectors and every schema take.
var locationKeys = []string{"external_location", "location"}

// errUnreadableSQL is a statement the check cannot read to the end.
var errUnreadableSQL = errors.New("the statement could not be read: it holds a string, a quoted name or a comment that never ends")

// checkLocations is the check on one statement.
func checkLocations(sql string, depth int) error {
	toks, err := lexSQL(sql)
	if err != nil {
		return err
	}
	for _, t := range toks {
		if t.kind == tokString {
			if err := reservedIn(t.value); err != nil {
				return err
			}
		}
	}
	for i := range toks {
		if err := checkAt(toks, i, depth); err != nil {
			return err
		}
	}
	return nil
}

// checkAt applies the rule the token at i starts, if any.
func checkAt(toks []sqlToken, i, depth int) error {
	if err := checkPropertiesAt(toks, i); err != nil {
		return err
	}
	return checkCallAt(toks, i, depth)
}

// errCopiedProperties refuses a table that copies another's properties.
var errCopiedProperties = errors.New("a table that copies another table's properties (LIKE ... INCLUDING PROPERTIES) could take its location, " +
	"which this check cannot read: name the new table's properties yourself (#2616)")

// checkPropertiesAt applies the property rules: a WITH (...) list, an ALTER
// ... SET PROPERTIES list, and a copy of another table's properties.
func checkPropertiesAt(toks []sqlToken, i int) error {
	switch {
	case wordAt(toks, i, "with") && symAt(toks, i+1, "("), wordAt(toks, i, "set") && wordAt(toks, i+1, "properties"):
		return checkProperties(toks, i+2)
	case wordAt(toks, i, "including") && wordAt(toks, i+1, "properties"):
		return errCopiedProperties
	}
	return nil
}

// checkCallAt applies the procedure rules: a CALL, ALTER TABLE ... EXECUTE
// procedure(...) and EXECUTE IMMEDIATE.
func checkCallAt(toks []sqlToken, i, depth int) error {
	switch {
	case wordAt(toks, i, "call") && statementStart(toks, i):
		return checkArguments(toks, i+1)
	case wordAt(toks, i, "execute") && wordAt(toks, i+1, "immediate"):
		return checkImmediate(toks, i+2, depth)
	case wordAt(toks, i, "execute") && (wordAt(toks, 0, "alter") || wordAt(toks, 0, "prepare")):
		return checkArguments(toks, i+1)
	}
	return nil
}

// statementStart reports whether the token at i begins a statement: the
// first, or the one PREPARE name FROM introduces.
func statementStart(toks []sqlToken, i int) bool {
	return i == 0 || (wordAt(toks, i-1, "from") && wordAt(toks, 0, "prepare"))
}

// symComma separates properties and arguments.
const symComma = ","

// checkProperties reads a property list from start, "key = value" entries to
// the closing parenthesis or the end, and refuses a location property whose
// value is anything but one quoted string.
func checkProperties(toks []sqlToken, start int) error {
	for i := start; i < len(toks) && !symAt(toks, i, ")"); {
		key, value := toks[i], i+2
		end := valueEnd(toks, value)
		if isName(key) && slices.Contains(locationKeys, key.text) && symAt(toks, i+1, "=") && (end != value+1 || !toks[value].plain) {
			return fmt.Errorf("give %s as one quoted string: the platform reads a location before the statement runs, "+
				"and one assembled from parts cannot be read (#2616)", key.text)
		}
		i = end
		if symAt(toks, i, symComma) {
			i++
		}
	}
	return nil
}

// errBuiltArgument refuses a procedure argument that is not a literal.
var errBuiltArgument = errors.New("give each argument of a procedure as a literal (a quoted string, a number, true, false, null or an ARRAY of them): " +
	"the platform reads a location before the call runs, and one assembled from parts cannot be read (#2616)")

// checkArguments refuses a procedure call whose arguments are not literals:
// a location among them must be readable before the call runs.
func checkArguments(toks []sqlToken, start int) error {
	open, ok := argumentsAt(toks, start)
	if !ok {
		return nil
	}
	for i := open + 1; i < len(toks) && !symAt(toks, i, ")"); {
		if isName(toks[i]) && symAt(toks, i+1, symArrow) {
			i += 2
		}
		end := valueEnd(toks, i)
		if !literalValue(toks[i:end]) {
			return errBuiltArgument
		}
		i = end
		if symAt(toks, i, symComma) {
			i++
		}
	}
	return nil
}

// argumentsAt is the index of the parenthesis that opens a procedure's
// arguments, after its dotted name starting at start; false when no name
// and parenthesis follow.
func argumentsAt(toks []sqlToken, start int) (int, bool) {
	for i := start; i < len(toks); i++ {
		switch {
		case symAt(toks, i, "("):
			return i, i > start
		case !isName(toks[i]) && !symAt(toks, i, "."):
			return 0, false
		}
	}
	return 0, false
}

func isName(t sqlToken) bool { return t.kind == tokWord || t.kind == tokIdent }

// checkImmediate reads the statement EXECUTE IMMEDIATE runs, which must be one
// quoted string, and checks it as a statement of its own.
func checkImmediate(toks []sqlToken, at, depth int) error {
	if at >= len(toks) || !toks[at].plain {
		return errors.New("EXECUTE IMMEDIATE takes one quoted string here, so the statement it runs can be read (#2616)")
	}
	if depth >= maxImmediateDepth {
		return errors.New("EXECUTE IMMEDIATE is nested too deep to read")
	}
	return checkLocations(toks[at].value, depth+1)
}

// literalValue reports whether toks are one literal: a quoted string, a
// number, true, false or null, or an ARRAY of them.
func literalValue(toks []sqlToken) bool {
	if len(toks) == 1 {
		return scalarLiteral(toks[0])
	}
	return arrayLiteral(toks)
}

func scalarLiteral(t sqlToken) bool {
	return t.plain || t.kind == tokNumber || (t.kind == tokWord && slices.Contains([]string{"true", "false", "null"}, t.text))
}

// arrayLiteral reports whether toks are ARRAY[...] of quoted strings and
// numbers, separated by commas.
func arrayLiteral(toks []sqlToken) bool {
	last := len(toks) - 1
	if !wordAt(toks, 0, "array") || !symAt(toks, 1, "[") || last < 2 || !symAt(toks, last, "]") {
		return false
	}
	for j, t := range toks[2:last] {
		if !arrayItem(t, j%2 == 0) {
			return false
		}
	}
	return true
}

// arrayItem reports whether t is what an array literal holds at its place: a
// quoted string or a number at an item, a comma between items.
func arrayItem(t sqlToken, item bool) bool {
	if item {
		return t.plain || t.kind == tokNumber
	}
	return t.kind == tokSym && t.text == symComma
}

// valueEnd is the index of the token that ends the value starting at i: the
// next comma or closing bracket at its own depth, or the end.
func valueEnd(toks []sqlToken, i int) int {
	depth := 0
	for ; i < len(toks); i++ {
		switch {
		case symAt(toks, i, "(") || symAt(toks, i, "["):
			depth++
		case symAt(toks, i, ")") || symAt(toks, i, "]"):
			if depth == 0 {
				return i
			}
			depth--
		case symAt(toks, i, symComma) && depth == 0:
			return i
		}
	}
	return i
}

// reservedIn refuses a string that names a platform bucket in a location: the
// host part of each scheme://host/... it holds, read past any user name and
// port.
func reservedIn(s string) error {
	for rest := s; ; {
		at := strings.Index(rest, "://")
		if at < 0 {
			return nil
		}
		rest = rest[at+len("://"):]
		host := rest
		if end := strings.IndexAny(host, "/?#\\ \t\r\n'\""); end >= 0 {
			host = host[:end]
		}
		if a := strings.LastIndex(host, "@"); a >= 0 {
			host = host[a+1:]
		}
		if p := strings.Index(host, ":"); p >= 0 {
			host = host[:p]
		}
		if decoded, err := url.PathUnescape(host); err == nil {
			host = decoded
		}
		host = strings.TrimSuffix(host, ".")
		if r, ok := s3kit.IsReserved(host); ok {
			return fmt.Errorf("bucket %q is where the platform keeps %s; no SQL statement may point a table, a schema or a partition at it, "+
				"for any persona: %s", host, r.Holds, r.Use)
		}
	}
}

func wordAt(toks []sqlToken, i int, w string) bool {
	return i >= 0 && i < len(toks) && toks[i].kind == tokWord && toks[i].text == w
}

func symAt(toks []sqlToken, i int, s string) bool {
	return i >= 0 && i < len(toks) && toks[i].kind == tokSym && toks[i].text == s
}
