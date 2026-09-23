package tableparquet

import (
	"fmt"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"

	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// shredder writes one row's values as Parquet leaf values with their levels.
//
// Every node a column is made of is optional, and an array or map adds a
// repeated group, so the levels follow directly from the depth: a value
// present at an optional node of definition level d defines d; a null there
// defines d-1 for every leaf below it; the first value of a repeated group
// repeats at the level its parent did, and each later one at the depth of
// repeated groups above it plus one.
type shredder struct {
	out parquet.Row
}

// write shreds v under p. r is the repetition level its first leaf value
// takes, d the definition level p defines when v is present, and depth the
// number of repeated groups above p.
func (s *shredder) write(p *plan, v any, r, d, depth int) error {
	if v == nil {
		s.nulls(p, r, d-1)
		return nil
	}
	switch p.t.Kind {
	case tabletype.Array:
		return s.writeArray(p, v, r, d, depth)
	case tabletype.Map:
		return s.writeMap(p, v, r, d, depth)
	case tabletype.Row:
		return s.writeRow(p, v, r, d, depth)
	}
	val, err := leafValue(p.t, v)
	if err != nil {
		return err
	}
	s.out = append(s.out, val.Level(r, d, p.leaf))
	return nil
}

// nulls writes a null for every leaf under p, defined to level d.
func (s *shredder) nulls(p *plan, r, d int) {
	for _, leaf := range p.leaves(nil) {
		s.out = append(s.out, parquet.Value{}.Level(r, d, leaf))
	}
}

// leaves lists the column indexes under p, in schema order.
func (p *plan) leaves(out []int) []int {
	switch p.t.Kind {
	case tabletype.Array:
		return p.elem.leaves(out)
	case tabletype.Map:
		return p.elem.leaves(p.key.leaves(out))
	case tabletype.Row:
		for _, f := range p.fields {
			out = f.leaves(out)
		}
		return out
	}
	return append(out, p.leaf)
}

// writeArray writes a LIST: the list group at d, its repeated group at d+1,
// and the element at d+2. An empty list defines d and nothing below it.
func (s *shredder) writeArray(p *plan, v any, r, d, depth int) error {
	elems, ok := v.([]any)
	if !ok {
		return fmt.Errorf("expected a list for %s, got %T", p.t.SQL(), v)
	}
	if len(elems) == 0 {
		s.nulls(p.elem, r, d)
		return nil
	}
	for i, e := range elems {
		rr := r
		if i > 0 {
			rr = depth + 1
		}
		if err := s.write(p.elem, e, rr, d+2, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// writeMap writes a MAP: the map group at d, its repeated key_value group at
// d+1 with the required key, and the value at d+2. Entries are written in key
// order so a file written twice from one map is the same file.
func (s *shredder) writeMap(p *plan, v any, r, d, depth int) error {
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("expected a map for %s, got %T", p.t.SQL(), v)
	}
	if len(m) == 0 {
		s.nulls(p, r, d)
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		rr := r
		if i > 0 {
			rr = depth + 1
		}
		kv, err := leafValue(p.key.t, k)
		if err != nil {
			return fmt.Errorf("the map key %q: %w", k, err)
		}
		s.out = append(s.out, kv.Level(rr, d+1, p.key.leaf))
		if err := s.write(p.elem, m[k], rr, d+2, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// writeRow writes a ROW's fields, each an optional node one level below it.
func (s *shredder) writeRow(p *plan, v any, r, d, depth int) error {
	for i, fp := range p.fields {
		fv, err := fieldValue(v, i, p.names[i])
		if err != nil {
			return err
		}
		if err := s.write(fp, fv, r, d+1, depth); err != nil {
			return fmt.Errorf("the field %q: %w", p.names[i], err)
		}
	}
	return nil
}

// fieldValue finds a row field's value. A query's row arrives positional; a
// script's object and a map arrive keyed, and are matched without regard to
// case, the way the reader matches a field.
func fieldValue(v any, i int, name string) (any, error) {
	switch x := v.(type) {
	case []any:
		if i < len(x) {
			return x[i], nil
		}
		return nil, nil //nolint:nilnil // a field past the row's end is a null, which is a value
	case *tabletype.Object:
		for j, k := range x.Keys {
			if strings.EqualFold(k, name) {
				return x.Values[j], nil
			}
		}
		return nil, nil //nolint:nilnil // an absent key is a null, which is a value
	case map[string]any:
		for k, fv := range x {
			if strings.EqualFold(k, name) {
				return fv, nil
			}
		}
		return nil, nil //nolint:nilnil // an absent key is a null, which is a value
	}
	return nil, fmt.Errorf("expected a row, got %T", v)
}
