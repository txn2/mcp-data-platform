// Package schemaref finds the names a text qualifies with an application's
// schema (#2709): SQL such as "SELECT * FROM orders.invoices". Each of an
// application's environments runs in a schema of its own with the search path
// set to it, so a name written without the schema reads the environment's own
// table, and one qualified by the schema reaches that schema from every
// environment. Prepare removes the qualifier and the save gate refuses an edit
// that adds one; both read text through this one package.
package schemaref

import (
	"regexp"
	"slices"
	"strings"
	"unicode"

	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
)

// qualifier matches a name qualified by one schema, as SQL writes it: the
// schema, bare, double-quoted, or double-quoted with the quotes escaped as a
// Starlark string's source writes them, then a dot and the name. A name
// before it (a catalog's, as Trino's three-part names write it) is another
// database's and left alone, as is a word that only ends in the schema's
// name, and a sentence that ends in it ("the open orders."). The name is the
// fourth group.
func qualifier(schema string) *regexp.Regexp {
	q := regexp.QuoteMeta(schema)
	return regexp.MustCompile(`(^|[^A-Za-z0-9_$".])("` + q + `"|\\"` + q + `\\"|` + q + `)\.(([A-Za-z_"\\])[A-Za-z0-9_$"\\]*)`)
}

// groupSchema and groupName are where qualifier's schema and name groups
// start in a FindAllStringSubmatchIndex result.
const (
	groupSchema = 4
	groupName   = 6
)

// match is one qualified name in a text: where the schema starts and the
// name ends, the schema and the name.
type match struct {
	start, nameStart, end int
	schema                string
	name                  string
}

// find is every name text qualifies with one of schemas where SQL names an
// object (references) and every one inside a quoted SQL value (values). A
// name anywhere else, such as a file (/_static/orders.css) or a sentence
// ("the orders.invoices table"), is neither: it is not SQL, and the schema
// is only a word in it.
func find(text string, schemas []string) (references, values []match) {
	quoted := quotedSpans(text)
	for _, s := range schemas {
		for _, m := range qualifier(s).FindAllStringSubmatchIndex(text, -1) {
			mt := match{start: m[groupSchema], nameStart: m[groupName], end: m[1], schema: s, name: strings.Trim(text[m[groupName]:m[groupName+1]], `"\`)}
			switch {
			case fileExtensions[strings.ToLower(mt.name)]:
				// hello.css is a file, whatever word precedes it.
			case inside(quoted, mt.start):
				values = append(values, mt)
			case sqlPosition(text, mt.start):
				references = append(references, mt)
			}
		}
	}
	slices.SortFunc(references, func(a, b match) int { return a.start - b.start })
	slices.SortFunc(values, func(a, b match) int { return a.start - b.start })
	return references, values
}

// quotedSpans is each quoted SQL value of text, as [open, close] offsets: a
// single quote, as SQL or a Starlark source escaping it writes it, to the
// next one that is not doubled. A dollar-quoted body is code and is not one.
func quotedSpans(text string) [][2]int {
	var out [][2]int
	open := -1
	for i := 0; i < len(text); i++ {
		if text[i] != '\'' {
			continue
		}
		if open < 0 {
			open = i
			continue
		}
		if i+1 < len(text) && text[i+1] == '\'' {
			i++
			continue
		}
		out = append(out, [2]int{open, i})
		open = -1
	}
	if open >= 0 {
		out = append(out, [2]int{open, len(text)})
	}
	return out
}

func inside(spans [][2]int, at int) bool {
	for _, s := range spans {
		if at > s[0] && at < s[1] {
			return true
		}
	}
	return false
}

// fileExtensions are the names that make a file of schema.name: a page's
// /_static/hello.css or a sentence naming hello.js, never a table.
var fileExtensions = map[string]bool{
	"css": true, "js": true, "mjs": true, "map": true, "html": true, "htm": true, "json": true, "xml": true,
	"svg": true, "png": true, "jpg": true, "jpeg": true, "gif": true, "webp": true, "ico": true,
	"woff": true, "woff2": true, "ttf": true, "pdf": true, "csv": true, "txt": true, "md": true,
}

// sqlKeywords are the words after which SQL names a table, a view, a
// function or a column.
var sqlKeywords = map[string]bool{
	"from": true, "join": true, "into": true, "update": true, "table": true, "on": true, "references": true,
	"truncate": true, "call": true, "execute": true, "select": true, "where": true, "and": true, "or": true,
	"not": true, "by": true, "set": true, "view": true, "exists": true, "in": true, "as": true, "returning": true,
	"using": true, "when": true, "then": true, "else": true, "case": true, "distinct": true, "all": true,
	"any": true, "some": true, "like": true, "ilike": true, "is": true, "lateral": true, "only": true,
	"with": true, "values": true, "perform": true, "return": true, "copy": true, "between": true,
	"having": true, "if": true, "to": true, "function": true, "procedure": true, "sequence": true,
	"type": true, "index": true, "trigger": true, "of": true,
}

// sqlPosition reports whether the name starting at start sits where SQL names
// an object: at the start of the text, after an opening parenthesis, a comma
// or an operator, or after a keyword such as FROM, JOIN or WHERE. Whitespace,
// and a Starlark source's \n and \t, are skipped first.
func sqlPosition(text string, start int) bool {
	j := before(text, start)
	if j < 0 {
		return true
	}
	if strings.IndexByte("(,=<>+-*|;!", text[j]) >= 0 {
		return true
	}
	end := j + 1
	for j >= 0 && (text[j] == '_' || unicode.IsLetter(rune(text[j]))) {
		j--
	}
	return sqlKeywords[strings.ToLower(text[j+1:end])]
}

// before is the offset of the last character of text ahead of start that is
// not whitespace, a Starlark source's \n, \t and \r skipped too; -1 when
// there is none.
func before(text string, start int) int {
	j := start - 1
	for j >= 0 {
		switch {
		case strings.IndexByte(" \t\n\r", text[j]) >= 0:
			j--
		case j >= 1 && text[j-1] == '\\' && strings.IndexByte("ntr", text[j]) >= 0:
			j -= 2
		default:
			return j
		}
	}
	return -1
}

// Unqualified is text with every name qualified by one of schemas written
// without it, and how many it rewrote: each name where SQL names an object.
// A name inside a quoted SQL value is data and is left as written (Values
// lists it), as is a word that is not SQL.
func Unqualified(text string, schemas []string) (out string, n int) {
	refs, _ := find(text, schemas)
	if len(refs) == 0 {
		return text, 0
	}
	var b strings.Builder
	at := 0
	for _, m := range refs {
		if m.start < at {
			continue
		}
		_, _ = b.WriteString(text[at:m.start])
		_, _ = b.WriteString(text[m.nameStart:m.end])
		at = m.end
		n++
	}
	_, _ = b.WriteString(text[at:])
	return b.String(), n
}

// Value is a qualified name inside a quoted SQL value: data, such as an
// outbox row's source table, which prepare leaves as written for a person
// to change, since the right text differs per environment.
type Value struct {
	Line int    `json:"line" example:"86"`
	Text string `json:"text" example:"orders.invoices"`
}

// ValuesIn is each qualified name inside a quoted SQL value of text.
func ValuesIn(text string, schemas []string) []Value {
	_, vals := find(text, schemas)
	out := make([]Value, 0, len(vals))
	for _, m := range vals {
		out = append(out, Value{Line: 1 + strings.Count(text[:m.start], "\n"), Text: m.schema + "." + m.name})
	}
	return out
}

// Script is a script's or a library's source with the schema qualifier
// removed from every string it holds, how many names that rewrote, and the
// libraries it loads. Only strings are read: a Starlark name that happens to
// be the schema's (orders.append) is code, not SQL. A source that does not
// parse names nothing.
func Script(source string, schemas []string) (out string, names int, loads []scriptlib.Ref) {
	out = source
	seen := map[string]bool{}
	strs(source, func(lit *syntax.Literal, _ string) {
		if seen[lit.Raw] {
			return
		}
		seen[lit.Raw] = true
		open, body, end := literalParts(lit.Raw)
		rewritten, k := Unqualified(body, schemas)
		if k > 0 {
			out = strings.ReplaceAll(out, lit.Raw, open+rewritten+end)
			names += k * strings.Count(source, lit.Raw)
		}
	}, func(ref scriptlib.Ref) { loads = append(loads, ref) })
	return out, names, loads
}

// Values is each qualified name inside a quoted SQL value of source's
// strings, on the line of the string's source text.
func Values(source string, schemas []string) []Value {
	out := []Value{}
	strs(source, func(lit *syntax.Literal, _ string) {
		_, body, _ := literalParts(lit.Raw)
		for _, v := range ValuesIn(body, schemas) {
			out = append(out, Value{Line: int(lit.TokenPos.Line) + v.Line - 1, Text: v.Text})
		}
	}, nil)
	return out
}

// literalParts splits a string literal's source into its opening (prefix
// and quotes), its body as written, and its closing quotes, so a rewrite of
// the body keeps the literal's own form.
func literalParts(raw string) (open, body, end string) {
	i := 0
	for i < len(raw) && strings.IndexByte("rRbB", raw[i]) >= 0 {
		i++
	}
	q := 1
	if strings.HasPrefix(raw[i:], `"""`) || strings.HasPrefix(raw[i:], "'''") {
		q = len(`"""`)
	}
	if len(raw) < i+2*q {
		return raw, "", ""
	}
	return raw[:i+q], raw[i+q : len(raw)-q], raw[len(raw)-q:]
}

// strs calls each for every string literal of source and load for every
// library it loads; a source that does not parse calls neither.
func strs(source string, each func(lit *syntax.Literal, value string), load func(scriptlib.Ref)) {
	file, err := scriptrun.Parse(source)
	if err != nil {
		return
	}
	syntax.Walk(file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.LoadStmt:
			if ref, err := scriptlib.ParseModule(n.ModuleName()); err == nil && load != nil {
				load(ref)
			}
			return false
		case *syntax.Literal:
			if v, ok := n.Value.(string); ok {
				each(n, v)
			}
		}
		return true
	})
}

// WithLoads is source with each load of a library in moved pointed at its
// new version.
func WithLoads(source string, moved map[string]int, loads []scriptlib.Ref) string {
	for _, ref := range loads {
		if v, ok := moved[ref.Name]; ok && v != ref.Version {
			source = strings.ReplaceAll(source, `"`+ref.String()+`"`, `"`+scriptlib.Ref{Name: ref.Name, Version: v}.String()+`"`)
		}
	}
	return source
}

// Name is one qualified name a source's string holds: the line of the
// string, the schema and the name it qualifies.
type Name struct {
	Line   int
	Schema string
	Name   string
}

// Added is a qualified name the strings of after hold more often than the
// strings of before: what an edit adds, which the save gate refuses while
// what a script already said is left alone. ok is false when the edit adds
// none; the name is the first string's.
func Added(before, after string, schemas []string) (added Name, ok bool) {
	had := counts(before, schemas)
	found := map[string]int{}
	strs(after, func(lit *syntax.Literal, value string) {
		refs, _ := find(value, schemas)
		for _, m := range refs {
			key := m.schema + "." + m.name
			found[key]++
			if !ok && found[key] > had[key] {
				added, ok = Name{Line: int(lit.TokenPos.Line), Schema: m.schema, Name: m.name}, true
			}
		}
	}, nil)
	return added, ok
}

// counts is how often the strings of source hold each qualified name.
func counts(source string, schemas []string) map[string]int {
	out := map[string]int{}
	strs(source, func(_ *syntax.Literal, value string) {
		refs, _ := find(value, schemas)
		for _, m := range refs {
			out[m.schema+"."+m.name]++
		}
	}, nil)
	return out
}
