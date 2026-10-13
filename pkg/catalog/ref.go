// Package catalog is the platform's own data catalog: the datasets its
// connections can see, their columns, the glossary, tags, domains and data
// products, and the curation written on them (#1916). This package holds the
// domain model and the reference form; pkg/catalog/postgres stores it.
package catalog

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Kind names what a catalog reference points at.
type Kind string

// The catalog's entity kinds. Each is also the type segment of its mcp:
// reference, so mcp:glossary_term:<id> names a KindGlossaryTerm.
const (
	KindDataset      Kind = "dataset"
	KindColumn       Kind = "column"
	KindGlossaryTerm Kind = "glossary_term"
	KindGlossaryNode Kind = "glossary_node"
	KindTag          Kind = "tag"
	KindDomain       Kind = "domain"
	KindDataProduct  Kind = "data_product"
)

// scheme prefixes every catalog reference, as it does every other internal
// platform reference (mcp:asset:, mcp:connection:, ...).
const scheme = "mcp:"

// ErrInvalidRef is returned for a string that is not a well-formed catalog
// reference. Callers that route references by form treat it as "not mine".
var ErrInvalidRef = errors.New("invalid catalog reference")

// Ref identifies one catalog entity.
//
// A dataset is determined by the connection that sees it and the path that
// connection names it by, so its reference is derived rather than assigned:
// mcp:dataset:(trino,warehouse,hive.sales.orders). The path is the dotted name
// the connection uses — catalog.schema.table for Trino, the operation id for an
// API connection, the dotted operation for a GraphQL one. A column adds its
// name: mcp:column:(trino,warehouse,hive.sales.orders,amount). The same table
// seen through two connections is two datasets.
//
// A glossary term, glossary node, domain or data product is referenced by its
// stored id (mcp:glossary_term:<uuid>), so renaming one changes no reference to
// it; its slug is accepted in place of the id when reading. A tag is its name
// (mcp:tag:pii).
type Ref struct {
	Kind Kind
	// ConnectionKind and Connection name the connection a dataset or column is
	// seen through. Connection is the instance name, the same name an
	// mcp:connection:(kind,name) reference carries.
	ConnectionKind string
	Connection     string
	// Path is the dataset's name under its connection, one element per dotted
	// segment.
	Path []string
	// Column is the column name, set only on a KindColumn reference.
	Column string
	// Key is the id or slug of a glossary term, glossary node, domain or data
	// product, or the name of a tag.
	Key string
}

// DatasetRef returns the reference of the dataset a connection names by path.
func DatasetRef(connectionKind, connection string, path ...string) Ref {
	return Ref{Kind: KindDataset, ConnectionKind: connectionKind, Connection: connection, Path: path}
}

// ColumnRef returns the reference of one column of a dataset.
func ColumnRef(dataset Ref, column string) Ref {
	return Ref{
		Kind: KindColumn, ConnectionKind: dataset.ConnectionKind, Connection: dataset.Connection,
		Path: dataset.Path, Column: column,
	}
}

// KeyRef returns the reference of a glossary term, glossary node, domain, data
// product or tag.
func KeyRef(kind Kind, key string) Ref {
	return Ref{Kind: kind, Key: key}
}

// Dataset returns the dataset a column reference belongs to, or the reference
// itself for a dataset.
func (r Ref) Dataset() Ref {
	return DatasetRef(r.ConnectionKind, r.Connection, r.Path...)
}

// PathString is the dataset's path in reference form, each segment quoted
// where it needs to be.
func (r Ref) PathString() string {
	parts := make([]string, len(r.Path))
	for i, p := range r.Path {
		parts[i] = quotePart(p)
	}
	return strings.Join(parts, string(pathSep))
}

// String renders the canonical reference. Two references to the same entity
// render identically, which is what lets the string be stored and compared.
func (r Ref) String() string {
	switch r.Kind {
	case KindDataset:
		return scheme + string(r.Kind) + ":(" + quotePart(r.ConnectionKind) + string(fieldSep) +
			quotePart(r.Connection) + string(fieldSep) + r.PathString() + ")"
	case KindColumn:
		return scheme + string(r.Kind) + ":(" + quotePart(r.ConnectionKind) + string(fieldSep) +
			quotePart(r.Connection) + string(fieldSep) + r.PathString() + string(fieldSep) + quotePart(r.Column) + ")"
	default:
		return scheme + string(r.Kind) + ":" + r.Key
	}
}

// Validate reports whether the reference names an entity: every part present
// and, for a keyed kind, a key.
func (r Ref) Validate() error {
	switch r.Kind {
	case KindDataset, KindColumn:
		return r.validateTuple()
	case KindGlossaryTerm, KindGlossaryNode, KindTag, KindDomain, KindDataProduct:
		if strings.TrimSpace(r.Key) == "" {
			return fmt.Errorf("a %s reference needs a key: %w", r.Kind, ErrInvalidRef)
		}
		return nil
	default:
		return fmt.Errorf("unknown kind %q: %w", r.Kind, ErrInvalidRef)
	}
}

// validateTuple checks the parts of a dataset or column reference.
func (r Ref) validateTuple() error {
	if r.ConnectionKind == "" || r.Connection == "" || len(r.Path) == 0 {
		return fmt.Errorf("a %s reference needs a connection kind, a connection and a path: %w", r.Kind, ErrInvalidRef)
	}
	if slices.Contains(r.Path, "") {
		return fmt.Errorf("empty path segment: %w", ErrInvalidRef)
	}
	if r.Kind == KindColumn && r.Column == "" {
		return fmt.Errorf("a column reference needs a column name: %w", ErrInvalidRef)
	}
	return nil
}

// ParseRef parses a catalog reference. A string that is not one — another
// platform reference, a urn:li: URN, or a malformed catalog reference — is
// ErrInvalidRef.
func ParseRef(s string) (Ref, error) {
	s = strings.TrimSpace(s)
	rest, ok := strings.CutPrefix(s, scheme)
	if !ok {
		return Ref{}, fmt.Errorf("reference %q is not an mcp: reference: %w", s, ErrInvalidRef)
	}
	kindText, body, ok := strings.Cut(rest, ":")
	if !ok {
		return Ref{}, fmt.Errorf("reference %q has no type: %w", s, ErrInvalidRef)
	}
	kind := Kind(kindText)
	var r Ref
	var err error
	switch kind {
	case KindDataset, KindColumn:
		r, err = parseTuple(kind, body)
	default:
		r = Ref{Kind: kind, Key: body}
	}
	if err != nil {
		return Ref{}, fmt.Errorf("reference %q: %w: %w", s, err, ErrInvalidRef)
	}
	if err := r.Validate(); err != nil {
		return Ref{}, err
	}
	return r, nil
}

// The number of fields in a dataset and a column reference.
const (
	datasetFields = 3
	columnFields  = 4
)

// tupleFields splits the parenthesized body of a dataset or column reference
// into its want fields.
func tupleFields(body string, want int) ([]string, error) {
	inner, ok := strings.CutPrefix(body, "(")
	if !ok {
		return nil, errors.New("want (connection_kind,connection,path)")
	}
	inner, ok = strings.CutSuffix(inner, ")")
	if !ok {
		return nil, errors.New("unclosed parenthesis")
	}
	fields, err := splitOutside(inner, fieldSep)
	if err != nil {
		return nil, err
	}
	if len(fields) != want {
		return nil, fmt.Errorf("want %d fields, got %d", want, len(fields))
	}
	return fields, nil
}

// parseTuple reads the parenthesized body of a dataset or column reference.
func parseTuple(kind Kind, body string) (Ref, error) {
	want := datasetFields
	if kind == KindColumn {
		want = columnFields
	}
	fields, err := tupleFields(body, want)
	if err != nil {
		return Ref{}, err
	}
	path, err := parsePath(fields[2])
	if err != nil {
		return Ref{}, err
	}
	// The connection kind, the connection and, for a column, the column name
	// are single parts; the path is the only dotted field.
	parts := make([]string, 0, len(fields))
	for i, f := range fields {
		if i == 2 {
			continue
		}
		p, err := unquotePart(f)
		if err != nil {
			return Ref{}, err
		}
		parts = append(parts, p)
	}
	r := Ref{Kind: kind, ConnectionKind: parts[0], Connection: parts[1], Path: path}
	if kind == KindColumn {
		r.Column = parts[2]
	}
	return r, nil
}

// parsePath splits a dotted path into its segments, honoring quoted segments.
func parsePath(s string) ([]string, error) {
	segs, err := splitOutside(s, pathSep)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(segs))
	for i, seg := range segs {
		if out[i], err = unquotePart(seg); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// The grammar's separators and quote.
const (
	fieldSep = ','
	pathSep  = '.'
	quote    = `"`
)

// specialChars are the characters that force a reference part into quotes:
// the separators of the grammar and the quote itself.
const specialChars = `.,()"`

// quotePart renders one part of a dataset or column reference. A part holding
// a separator, a quote, or surrounding whitespace is double-quoted with any
// inner quote doubled, the way SQL quotes an identifier; every other part is
// written bare.
func quotePart(p string) string {
	if p != "" && !strings.ContainsAny(p, specialChars) && strings.TrimSpace(p) == p {
		return p
	}
	return quote + strings.ReplaceAll(p, quote, quote+quote) + quote
}

// unquotePart reverses quotePart. A bare part may not hold a special
// character; a quoted one may hold anything, with inner quotes doubled.
func unquotePart(p string) (string, error) {
	if !strings.HasPrefix(p, quote) {
		if strings.ContainsAny(p, specialChars) {
			return "", fmt.Errorf("part %q must be quoted", p)
		}
		return p, nil
	}
	if len(p) < len(quote+quote) || !strings.HasSuffix(p, quote) {
		return "", fmt.Errorf("unterminated quote in %q", p)
	}
	inner := p[1 : len(p)-1]
	if strings.Contains(strings.ReplaceAll(inner, quote+quote, ""), quote) {
		return "", fmt.Errorf("stray quote in %q", p)
	}
	return strings.ReplaceAll(inner, quote+quote, quote), nil
}

// splitOutside splits s on sep wherever sep is not inside a quoted part.
func splitOutside(s string, sep byte) ([]string, error) {
	var out []string
	start, quoted := 0, false
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == quote[0]:
			quoted = !quoted
		case s[i] == sep && !quoted:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if quoted {
		return nil, errors.New("unterminated quote")
	}
	return append(out, s[start:]), nil
}
