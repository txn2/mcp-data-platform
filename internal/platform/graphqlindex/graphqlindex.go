// Package graphqlindex is the graphql connection kind's consumer of the
// shared index-jobs framework. It registers a Source/Sink pair under
// source_kind "graphql_operations" so a connection's operations are
// embedded off the request path, and so ranking a schema with hundreds
// of operations does not depend on the caller's words matching the
// schema author's.
//
// The indexing unit is one connection: its source id is the connection
// name, and one pass embeds every operation the connection's current
// schema exposes. Vectors are keyed on the schema hash as well as the
// connection, so a schema that changes does not invalidate a connection
// that was reverted to a previous one, and a pass that has not run yet
// leaves ranking lexical rather than wrong.
package graphqlindex

import (
	"context"
	"fmt"
	"sort"

	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// SourceKind is the indexjobs source_kind this package serves.
const SourceKind = "graphql_operations"

// ToolkitLister returns the live graphql toolkits. It is a function
// rather than a snapshot so a connection added through the admin API
// after startup is indexed without a restart.
type ToolkitLister func() []*graphqlkit.Toolkit

// Source enumerates the operations of one connection as embeddable
// items.
type Source struct {
	lister ToolkitLister
}

// NewSource builds the source over the live toolkits.
func NewSource(lister ToolkitLister) *Source { return &Source{lister: lister} }

// Compile-time interface check.
var _ indexjobs.Source = (*Source)(nil)

// Kind reports the graphql operations source kind.
func (*Source) Kind() string { return SourceKind }

// LoadItems returns one item per operation of the named connection. A
// connection that is gone, or that holds no schema yet, yields
// ErrSourceGone so the worker clears its vectors and completes rather
// than retrying a unit that cannot resolve.
func (s *Source) LoadItems(_ context.Context, sourceID string) ([]indexjobs.Item, error) {
	_, items, ok := s.itemsFor(sourceID)
	if !ok {
		return nil, fmt.Errorf("graphqlindex: connection %q has no readable schema: %w", sourceID, indexjobs.ErrSourceGone)
	}
	return items, nil
}

// OnSucceeded re-reads the connection's vectors so a schema that was
// ranking lexically starts ranking semantically without a restart.
func (s *Source) OnSucceeded(sourceID string) {
	for _, tk := range s.lister() {
		if tk.HasConnection(sourceID) {
			tk.ReloadVectors(context.Background(), sourceID)
			return
		}
	}
}

// itemsFor finds the toolkit holding a connection and renders its
// operations as items, sorted by id so two passes over one schema
// produce the same order.
func (s *Source) itemsFor(connection string) (schemaHash string, items []indexjobs.Item, ok bool) {
	for _, tk := range s.lister() {
		hash, texts, found := tk.IndexItems(connection)
		if !found {
			continue
		}
		ids := make([]string, 0, len(texts))
		for id := range texts {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		items = make([]indexjobs.Item, 0, len(ids))
		for _, id := range ids {
			items = append(items, indexjobs.Item{ItemID: id, Text: texts[id]})
		}
		return hash, items, true
	}
	return "", nil, false
}

// connections lists every registered graphql connection across the live
// toolkits, sorted.
func (s *Source) connections() []string {
	var out []string
	for _, tk := range s.lister() {
		for _, detail := range tk.ListConnections() {
			// A connection taking its schema from a catalog is embedded
			// as that catalog's spec, by the api-catalog source, and
			// reads its vectors back from there. Enumerating it here
			// would open a unit that resolves to no items on every
			// sweep (#1745).
			if detail.CatalogID != "" {
				continue
			}
			out = append(out, detail.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Sink stores the vectors for the graphql operations kind.
type Sink struct {
	store  *Store
	source *Source
}

// NewSink builds the sink over a store and the source it shares a kind
// with. The source is held because gap detection has to compare the
// live operation set against the persisted vectors: a schema refresh
// changes what should be indexed without changing any count in the
// database.
func NewSink(store *Store, source *Source) *Sink { return &Sink{store: store, source: source} }

// Compile-time interface check.
var _ indexjobs.Sink = (*Sink)(nil)

// Kind reports the graphql operations source kind.
func (*Sink) Kind() string { return SourceKind }

// ListExisting returns the persisted vectors for the connection's
// current schema version, for the worker's dedup pass.
func (s *Sink) ListExisting(ctx context.Context, key indexjobs.Key) (map[string]indexjobs.Vector, error) {
	hash, _, ok := s.source.itemsFor(key.SourceID)
	if !ok {
		//nolint:nilnil // a connection with no schema has no rows to dedup against
		return nil, nil
	}
	return s.store.ListVectors(ctx, key.SourceID, hash)
}

// Upsert replaces the connection's whole vector set for its current
// schema, so an operation the schema no longer exposes loses its
// vector. Vectors written for an earlier schema hash are left alone:
// they are what a connection reverted to that schema would rank on.
func (s *Sink) Upsert(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	hash, _, ok := s.source.itemsFor(key.SourceID)
	if !ok {
		return nil
	}
	return s.store.Replace(ctx, key.SourceID, hash, rows)
}

// UpsertBatch writes one chunk in place, so a pass that fails midway
// leaves its earlier chunks visible to the next attempt's dedup.
func (s *Sink) UpsertBatch(ctx context.Context, key indexjobs.Key, rows []indexjobs.Vector) error {
	hash, _, ok := s.source.itemsFor(key.SourceID)
	if !ok {
		return nil
	}
	return s.store.UpsertBatch(ctx, key.SourceID, hash, rows)
}

// StampExpected is a no-op. A successful pass writes the connection's
// complete operation set atomically, so the indexed vector count is
// also the expected count and there is nothing separate to record.
func (*Sink) StampExpected(context.Context, indexjobs.Key, int) error { return nil }

// FindGaps reports the connections whose live operation set has drifted
// from their persisted vectors: a schema read for the first time, a
// schema that changed, or an operation whose indexable text changed
// without the count changing.
func (s *Sink) FindGaps(ctx context.Context) ([]string, error) {
	var gaps []string
	for _, name := range s.source.connections() {
		hash, items, ok := s.source.itemsFor(name)
		if !ok || len(items) == 0 {
			continue
		}
		existing, err := s.store.ListVectors(ctx, name, hash)
		if err != nil {
			return nil, err
		}
		if indexjobs.ContentGap(items, existing) {
			gaps = append(gaps, name)
		}
	}
	return gaps, nil
}
