// Package catalogfake is an in-memory catalog.Store for tests of the code that
// writes curation (#1920): it keeps the contract the PostgreSQL store keeps,
// so a test that passes against it is not passing against a looser store.
package catalogfake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// Store is an in-memory catalog.Store holding the contract the
// PostgreSQL store keeps: every write is versioned per aspect with the value it
// replaced, WriteAspects and RevertChanges are all-or-nothing, a revert refuses
// a change its aspect was written after, and a value is normalized as the
// store normalizes it.
type Store struct {
	mu       sync.Mutex
	entities map[string]*entity // by reference
	history  []catalog.Change
	// FailWrite, when set, fails every write before anything is applied.
	FailWrite error
	// beforeWrite, when set, runs once at the start of the next WriteAspects.
	beforeWrite func()
}

// entity is one stored entity: a dataset, column or definition.
type entity struct {
	ref     catalog.Ref
	kind    catalog.Kind
	name    string
	slug    string
	parent  string   // the glossary node a term or node sits under
	columns []string // a dataset's column names, in order
	aspects map[catalog.AspectName]json.RawMessage
}

// sameJSON reports whether two values are the same JSON, either absent or
// decoding to equal values.
func sameJSON(a, b []byte) bool {
	if len(a) == 0 || len(b) == 0 {
		return len(a) == len(b)
	}
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// SetBeforeWrite has the next WriteAspects run fn before it checks anything,
// so a test can land a concurrent write between a plan's read and its write.
func (f *Store) SetBeforeWrite(fn func()) { f.beforeWrite = fn }

// New returns an empty in-memory catalog.
func New() *Store {
	return &Store{entities: map[string]*entity{}}
}

// AddDataset stores a Trino dataset with the given columns and returns its
// reference.
func (f *Store) AddDataset(connection, path string, columns ...string) catalog.Ref {
	parts := strings.Split(path, ".")
	ref := catalog.DatasetRef("trino", connection, parts...)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entities[ref.String()] = &entity{
		ref: ref, kind: catalog.KindDataset, name: parts[len(parts)-1], columns: columns,
		aspects: map[catalog.AspectName]json.RawMessage{},
	}
	for _, c := range columns {
		cref := catalog.ColumnRef(ref, c)
		f.entities[cref.String()] = &entity{
			ref: cref, kind: catalog.KindColumn, name: c, aspects: map[catalog.AspectName]json.RawMessage{},
		}
	}
	return ref
}

// Aspects decodes the curation stored on ref.
func (f *Store) Aspects(ref catalog.Ref) catalog.Aspects {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.find(ref)
	if e == nil {
		return catalog.Aspects{}
	}
	return decodeAspects(e)
}

func decodeAspects(e *entity) catalog.Aspects {
	var a catalog.Aspects
	for name, v := range e.aspects {
		if err := a.Set(name, v); err != nil {
			panic(err)
		}
	}
	return a
}

// find returns the entity ref names, a definition by id or slug.
func (f *Store) find(ref catalog.Ref) *entity {
	if e, ok := f.entities[ref.String()]; ok {
		return e
	}
	for _, e := range f.entities {
		if e.kind == ref.Kind && ref.Key != "" && (e.slug == ref.Key || e.ref.Key == ref.Key) {
			return e
		}
	}
	return nil
}

// PutDataset implements catalog.Store.
func (*Store) PutDataset(context.Context, catalog.DatasetInput) (*catalog.Dataset, error) {
	return nil, errors.New("declaring a dataset is not modeled by the fake")
}

// GetDataset implements catalog.Store.
func (f *Store) GetDataset(_ context.Context, ref catalog.Ref) (*catalog.Dataset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.entities[ref.String()]
	if e == nil || e.kind != catalog.KindDataset {
		return nil, catalog.ErrNotFound
	}
	d := &catalog.Dataset{Reference: ref.String(), Ref: ref, Name: e.name, Aspects: decodeAspects(e), Columns: []catalog.Column{}}
	for i, c := range e.columns {
		col := f.entities[catalog.ColumnRef(ref, c).String()]
		d.Columns = append(d.Columns, catalog.Column{Name: c, Ordinal: i, Aspects: decodeAspects(col)})
	}
	return d, nil
}

// PutDefinition implements catalog.Store.
func (f *Store) PutDefinition(_ context.Context, in catalog.DefinitionInput) (*catalog.Definition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	slug := in.Slug
	if slug == "" {
		slug = catalog.Slug(in.Name)
	}
	if e := f.find(catalog.KeyRef(in.Kind, slug)); e != nil {
		e.name, e.parent = in.Name, in.Parent
		return &catalog.Definition{
			ID: e.ref.Key, Reference: e.ref.String(), Kind: e.kind, Slug: e.slug, Name: e.name,
			Parent: e.parent, Aspects: decodeAspects(e),
		}, nil
	}
	id := uuid.NewString()
	if in.Kind == catalog.KindTag {
		id = slug
	}
	ref := catalog.KeyRef(in.Kind, id)
	f.entities[ref.String()] = &entity{
		ref: ref, kind: in.Kind, name: in.Name, slug: slug, parent: in.Parent, aspects: map[catalog.AspectName]json.RawMessage{},
	}
	return &catalog.Definition{ID: id, Reference: ref.String(), Kind: in.Kind, Slug: slug, Name: in.Name, Parent: in.Parent}, nil
}

// GetDefinition implements catalog.Store.
func (f *Store) GetDefinition(_ context.Context, ref catalog.Ref) (*catalog.Definition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e := f.find(ref)
	if e == nil {
		return nil, catalog.ErrNotFound
	}
	return &catalog.Definition{
		ID: e.ref.Key, Reference: e.ref.String(), Kind: e.kind, Slug: e.slug, Name: e.name, Parent: e.parent,
		Aspects: decodeAspects(e),
	}, nil
}

// WriteAspect implements catalog.Store.
func (f *Store) WriteAspect(ctx context.Context, ref catalog.Ref, name catalog.AspectName, value json.RawMessage, by string) (*catalog.Change, error) {
	out, err := f.WriteAspects(ctx, []catalog.AspectEdit{{Ref: ref, Aspect: name, Value: value}}, by)
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// WriteAspects implements catalog.Store.
func (f *Store) WriteAspects(_ context.Context, edits []catalog.AspectEdit, by string) ([]catalog.Change, error) {
	if fn := f.beforeWrite; fn != nil {
		f.beforeWrite = nil
		fn()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailWrite != nil {
		return nil, f.FailWrite
	}
	type staged struct {
		e     *entity
		name  catalog.AspectName
		value json.RawMessage
	}
	var all []staged
	for _, ed := range edits {
		e := f.find(ed.Ref)
		if e == nil {
			return nil, fmt.Errorf("writing %s: %w", ed.Ref, catalog.ErrNotFound)
		}
		v, err := catalog.NormalizeAspect(e.kind, ed.Aspect, ed.Value)
		if err != nil {
			return nil, fmt.Errorf("writing %s: %w", ed.Ref, err)
		}
		if ed.Checked && !sameJSON(e.aspects[ed.Aspect], ed.Expect) {
			return nil, fmt.Errorf("writing %s: %w", ed.Ref, catalog.ErrStale)
		}
		all = append(all, staged{e, ed.Aspect, v})
	}
	out := make([]catalog.Change, 0, len(all))
	for _, s := range all {
		out = append(out, f.write(s.e, s.name, s.value, by, ""))
	}
	return out, nil
}

// write records one change; the caller holds the lock.
func (f *Store) write(e *entity, name catalog.AspectName, value json.RawMessage, by, reverts string) catalog.Change {
	version := 1
	for _, h := range f.history {
		if h.Reference == e.ref.String() && h.Aspect == name && h.Version >= version {
			version = h.Version + 1
		}
	}
	c := catalog.Change{
		ID: uuid.NewString(), Reference: e.ref.String(), Aspect: name, Version: version,
		Previous: e.aspects[name], Value: value, WrittenBy: by, WrittenAt: time.Now(), Reverts: reverts,
	}
	if value == nil {
		delete(e.aspects, name)
	} else {
		e.aspects[name] = value
	}
	f.history = append(f.history, c)
	return c
}

// History implements catalog.Store.
func (f *Store) History(_ context.Context, ref catalog.Ref, name catalog.AspectName) ([]catalog.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]catalog.Change, 0)
	for _, h := range f.history {
		if h.Reference == ref.String() && h.Aspect == name {
			out = append(out, h)
		}
	}
	slices.Reverse(out)
	return out, nil
}

// Rollback implements catalog.Store.
func (f *Store) Rollback(ctx context.Context, changeID, by string) (*catalog.Change, error) {
	out, err := f.RevertChanges(ctx, []string{changeID}, by)
	if err != nil {
		return nil, err
	}
	return &out[0], nil
}

// RevertChanges implements catalog.Store.
func (f *Store) RevertChanges(_ context.Context, ids []string, by string) ([]catalog.Change, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FailWrite != nil {
		return nil, f.FailWrite
	}
	// Checked against the history as it stands, then applied one by one so a
	// later revert in the set sees an earlier one's write, as the store's
	// single transaction does.
	snapshot := slices.Clone(f.history)
	entities := map[string]map[catalog.AspectName]json.RawMessage{}
	for ref, e := range f.entities {
		entities[ref] = cloneAspects(e.aspects)
	}
	out := make([]catalog.Change, 0, len(ids))
	for _, id := range ids {
		c, err := f.revertOne(id, by)
		if err != nil {
			f.history = snapshot
			for ref, a := range entities {
				f.entities[ref].aspects = a
			}
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func cloneAspects(m map[catalog.AspectName]json.RawMessage) map[catalog.AspectName]json.RawMessage {
	return maps.Clone(m)
}

// revertOne reverts one change; the caller holds the lock.
func (f *Store) revertOne(id, by string) (catalog.Change, error) {
	var target *catalog.Change
	latest := map[string]catalog.Change{}
	for i := range f.history {
		h := f.history[i]
		if h.ID == id {
			target = &f.history[i]
		}
		latest[h.Reference+"|"+string(h.Aspect)] = h
	}
	if target == nil {
		return catalog.Change{}, catalog.ErrNotFound
	}
	if l := latest[target.Reference+"|"+string(target.Aspect)]; l.ID != target.ID {
		return catalog.Change{}, &catalog.ConflictError{Change: id, Reference: target.Reference, Aspect: target.Aspect, Newer: l}
	}
	e := f.entities[target.Reference]
	return f.write(e, target.Aspect, target.Previous, by, id), nil
}

// MarkRemoved implements catalog.Store.
func (*Store) MarkRemoved(context.Context, catalog.Removal) (int, error) { return 0, nil }

// MarkDatasetRemoved implements catalog.Store.
func (*Store) MarkDatasetRemoved(context.Context, catalog.Ref) error { return nil }

// AddEdges implements catalog.Store.
func (*Store) AddEdges(context.Context, []catalog.Edge) error { return nil }

// Lineage implements catalog.Store.
func (*Store) Lineage(context.Context, string, catalog.Direction, int) ([]catalog.Hop, error) {
	return []catalog.Hop{}, nil
}

// DatasetsByPath implements catalog.Store.
func (f *Store) DatasetsByPath(_ context.Context, connectionKind, path string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0)
	for ref, e := range f.entities {
		if e.kind == catalog.KindDataset && e.ref.ConnectionKind == connectionKind && e.ref.PathString() == path {
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out, nil
}

// SearchDatasets implements catalog.Store.
func (*Store) SearchDatasets(context.Context, catalog.DatasetQuery) ([]catalog.DatasetSummary, error) {
	return []catalog.DatasetSummary{}, nil
}

// TaggedEntities implements catalog.Store.
func (f *Store) TaggedEntities(_ context.Context, tag string, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0)
	for ref, e := range f.entities {
		if e.kind != catalog.KindDataset && e.kind != catalog.KindColumn {
			continue
		}
		if slices.Contains(decodeAspects(e).Tags, tag) {
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

var _ catalog.Store = (*Store)(nil)

// SetDescription stores a description on the entity ref names, as curation
// written before the test.
func (f *Store) SetDescription(ref, description string) {
	r, err := catalog.ParseRef(ref)
	if err != nil {
		panic(err)
	}
	if _, err := f.WriteAspect(context.Background(), r, catalog.AspectDescription,
		json.RawMessage(fmt.Sprintf("%q", description)), "seed"); err != nil {
		panic(err)
	}
}
