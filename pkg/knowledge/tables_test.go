package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A table reference is an addition to a hit: it says the file behind it can be
// joined, and to what. What is asserted here is that it reaches the hits it
// belongs to and that losing it never loses the hit.

// stubLookup answers from a fixed map, keyed by subject id. The value is the
// file's whole registration list, newest first, as the real lookup returns.
type stubLookup struct {
	tables map[string][]HitTable
	err    error
	calls  int
	seen   []TableSubject
}

func (s *stubLookup) TablesFor(_ context.Context, subjects []TableSubject) (map[string][]HitTable, error) {
	s.calls++
	s.seen = append(s.seen, subjects...)
	if s.err != nil {
		return nil, s.err
	}
	return s.tables, nil
}

func subjectByRef(h Hit) (TableSubject, bool) {
	if h.Ref == "" {
		return TableSubject{}, false
	}
	return TableSubject{Kind: TableKindAsset, ID: h.Ref, Bucket: "b", HeadKey: "d/" + h.Ref + "/content.csv"}, true
}

func TestAttachTables(t *testing.T) {
	lookup := &stubLookup{tables: map[string][]HitTable{
		"a1": {{Connection: "scratch", Table: "scratch.uploads.analyst_a1"}},
	}}
	hits := []Hit{{Ref: "a1"}, {Ref: "a2"}}

	attachTables(context.Background(), lookup, hits, subjectByRef)

	require.NotNil(t, hits[0].Table)
	assert.Equal(t, "scratch.uploads.analyst_a1", hits[0].Table.Table)
	assert.Nil(t, hits[1].Table, "a file nobody registered carries no reference")

	// One call for the page, not one per hit.
	assert.Equal(t, 1, lookup.calls)
	assert.Len(t, lookup.seen, 2)
}

// TestAttachTables_FailureLosesTheReferenceNotTheHit. A table reference is an
// addition; a lookup that failed must not remove results from the answer.
func TestAttachTables_FailureLosesTheReferenceNotTheHit(t *testing.T) {
	hits := []Hit{{Ref: "a1", Text: "still here"}}
	attachTables(context.Background(), &stubLookup{err: errors.New("db down")}, hits, subjectByRef)

	assert.Nil(t, hits[0].Table)
	assert.Equal(t, "still here", hits[0].Text)
}

func TestAttachTables_NothingToDo(t *testing.T) {
	hits := []Hit{{Ref: "a1"}}

	// No lookup wired: the deployment has no registration mechanism.
	attachTables(context.Background(), nil, hits, subjectByRef)
	assert.Nil(t, hits[0].Table)

	// No hits: the lookup is never called.
	lookup := &stubLookup{}
	attachTables(context.Background(), lookup, nil, subjectByRef)
	assert.Zero(t, lookup.calls)

	// Hits that map to no subject: nothing to ask about.
	attachTables(context.Background(), lookup, []Hit{{}}, subjectByRef)
	assert.Zero(t, lookup.calls)
}

func TestLookupTables(t *testing.T) {
	lookup := &stubLookup{tables: map[string][]HitTable{
		"res_1": {
			{Connection: "scratch", Table: "scratch.uploads.analyst_keys_v2", Stale: true},
			{Connection: "warehouse", Table: "warehouse.uploads.analyst_keys"},
		},
	}}

	got := lookupTables(context.Background(), lookup,
		TableSubject{Kind: TableKindResource, ID: "res_1", Bucket: "b", HeadKey: "d/keys.csv"})
	require.Len(t, got, 2, "a fetched file reports every registration over it, not one")
	assert.Equal(t, "scratch.uploads.analyst_keys_v2", got[0].Table)
	assert.True(t, got[0].Stale)
	assert.Equal(t, "warehouse.uploads.analyst_keys", got[1].Table)

	assert.Empty(t, lookupTables(context.Background(), lookup,
		TableSubject{Kind: TableKindResource, ID: "unregistered"}))
	assert.Empty(t, lookupTables(context.Background(), nil, TableSubject{ID: "res_1"}),
		"no lookup wired")
	assert.Empty(t, lookupTables(context.Background(), lookup, TableSubject{}),
		"a subject with no id names nothing")
	assert.Empty(t, lookupTables(context.Background(), &stubLookup{err: errors.New("db down")},
		TableSubject{ID: "res_1"}), "a failed lookup drops the reference, not the document")
}

// TestAttachTables_PicksTheNewestFollowableRegistration is #1627: a file
// registered twice put its newest registration on the hit whatever state it
// was in, so a follow reporting the table gone still handed out sample SQL
// over it. The hit points at a table a follow has not disowned, or at none.
func TestAttachTables_PicksTheNewestFollowableRegistration(t *testing.T) {
	lookup := &stubLookup{tables: map[string][]HitTable{
		"a1": {
			{
				Connection: "scratch", Table: "scratch.uploads.feed_norepair",
				FollowError: "the table no longer exists",
			},
			{Connection: "scratch", Table: "scratch.uploads.feed"},
		},
		"a2": {
			{Connection: "scratch", Table: "scratch.uploads.gone", FollowError: "the table no longer exists"},
		},
	}}
	hits := []Hit{{Ref: "a1"}, {Ref: "a2"}}

	attachTables(context.Background(), lookup, hits, subjectByRef)

	require.NotNil(t, hits[0].Table)
	assert.Equal(t, "scratch.uploads.feed", hits[0].Table.Table,
		"the newest registration a follow has not disowned")
	assert.Nil(t, hits[1].Table,
		"a file whose every registration is broken points the caller at no table")
}

// TestPreferredTable covers the rule directly: the list arrives newest first
// and the first healthy entry wins.
func TestPreferredTable(t *testing.T) {
	assert.Nil(t, preferredTable(nil))
	assert.Nil(t, preferredTable([]HitTable{{Table: "a", FollowError: "gone"}}))

	got := preferredTable([]HitTable{
		{Table: "newest", FollowError: "gone"},
		{Table: "middle"},
		{Table: "oldest"},
	})
	require.NotNil(t, got)
	assert.Equal(t, "middle", got.Table)
}

// TestDocumentOmitsTablesWhenNothingIsRegistered pins the wire shape: no
// tables key at all, rather than an empty list to interpret.
func TestDocumentOmitsTablesWhenNothingIsRegistered(t *testing.T) {
	body, err := json.Marshal(Document{Reference: "mcp:resource:res_1", Source: SourceResources})
	require.NoError(t, err)
	assert.NotContains(t, string(body), "tables")

	body, err = json.Marshal(Document{
		Reference: "mcp:resource:res_1",
		Tables:    []HitTable{{Connection: "scratch", Table: "scratch.uploads.analyst_keys"}},
	})
	require.NoError(t, err)
	assert.Contains(t, string(body), `"tables"`)
	assert.Contains(t, string(body), `"query_table":"scratch.uploads.analyst_keys"`)
}

// sinkProvider records that the router pushed a lookup into it.
type sinkProvider struct {
	searchOnlyProvider
	got TableLookup
}

func (s *sinkProvider) SetTableLookup(l TableLookup) { s.got = l }

// TestRouterSetTableLookup pins the wiring: the composition root hands the
// lookup to the router, which pushes it into every provider that can carry a
// table reference and leaves the rest alone.
func TestRouterSetTableLookup(t *testing.T) {
	sink := &sinkProvider{searchOnlyProvider: searchOnlyProvider{ScopeShared}}
	plain := searchOnlyProvider{ScopeShared}
	router := NewRouter(nil, nil, sink, plain)

	lookup := &stubLookup{}
	router.SetTableLookup(lookup)
	assert.Same(t, lookup, sink.got)

	// Nil is the deployment with no registration mechanism, and clears rather
	// than panics.
	router.SetTableLookup(nil)
	assert.Nil(t, sink.got)
}
