package connview

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// mockTK implements registry.Toolkit without ConnectionLister (the fallback path).
type mockTK struct {
	kind, name, conn string
}

func (m *mockTK) Kind() string                          { return m.kind }
func (m *mockTK) Name() string                          { return m.name }
func (m *mockTK) Connection() string                    { return m.conn }
func (*mockTK) RegisterTools(_ *mcp.Server)             {}
func (*mockTK) Tools() []string                         { return nil }
func (*mockTK) SetSemanticProvider(_ semantic.Provider) {}
func (*mockTK) SetQueryProvider(_ query.Provider)       {}
func (*mockTK) Close() error                            { return nil }

// listerTK additionally implements toolkit.ConnectionLister.
type listerTK struct {
	mockTK
	conns []toolkit.ConnectionDetail
}

func (m *listerTK) ListConnections() []toolkit.ConnectionDetail { return m.conns }

type fakeSource struct{ names map[string]string }

func (f fakeSource) DataHubSourceName(kind, name string) string { return f.names[kind+"/"+name] }

type fakePages struct {
	byConn map[string][]knowledgepage.PageRef
	err    error
}

func (f fakePages) ListPagesReferencing(_ context.Context, ref knowledgepage.EntityRef) ([]knowledgepage.PageRef, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byConn[ref.ConnectionKind+"/"+ref.ConnectionName], nil
}

func pageRefs(n int) []knowledgepage.PageRef {
	out := make([]knowledgepage.PageRef, 0, n)
	for i := range n {
		id := string(rune('a' + i))
		out = append(out, knowledgepage.PageRef{ID: "kp" + id, Slug: "s" + id, Title: "Page " + id})
	}
	return out
}

// TestBuild_KnowledgeBoundedByCap proves the token guard: a connection referenced by
// many pages reports the full count but lists only maxKnowledgePages of them.
func TestBuild_KnowledgeBoundedByCap(t *testing.T) {
	tk := &listerTK{
		mockTK: mockTK{kind: "trino"},
		conns:  []toolkit.ConnectionDetail{{Name: "acme", IsDefault: true}},
	}
	pages := fakePages{byConn: map[string][]knowledgepage.PageRef{
		"trino/acme": pageRefs(7), // more than the cap
	}}
	src := fakeSource{names: map[string]string{"trino/acme": "trino_src"}}
	out := Build(context.Background(), []registry.Toolkit{tk}, src, pages)

	require.Len(t, out.Connections, 1)
	e := out.Connections[0]
	assert.Equal(t, "mcp:connection:(trino,acme)", e.Reference, "lister path emits the canonical reference")
	assert.Equal(t, "trino_src", e.DataHubSourceName, "the lister path resolves the source")
	assert.Equal(t, 7, e.KnowledgePageCount, "the full total is reported")
	assert.Len(t, e.KnowledgePages, maxKnowledgePages, "the listed sample is capped")
	assert.Equal(t, "Page a", e.KnowledgePages[0].Title)
}

func TestBuild_FallbackKindFilterAndSource(t *testing.T) {
	toolkits := []registry.Toolkit{
		&mockTK{kind: "trino", name: "warehouse", conn: "warehouse-conn"}, // data kind -> entry
		&mockTK{kind: "api", name: "gw", conn: "gw-conn"},                 // not a data kind -> skipped
	}
	src := fakeSource{names: map[string]string{"trino/warehouse": "trino_src"}}
	out := Build(context.Background(), toolkits, src, nil)

	require.Len(t, out.Connections, 1, "non-data kinds are dropped in the fallback path")
	assert.Equal(t, "trino", out.Connections[0].Kind)
	assert.Equal(t, "mcp:connection:(trino,warehouse)", out.Connections[0].Reference, "fallback path emits the canonical reference")
	assert.Equal(t, "trino_src", out.Connections[0].DataHubSourceName)
	assert.Equal(t, 1, out.Count)
}

func TestBuild_NoEnrichmentWhenLookupNilOrEmpty(t *testing.T) {
	tk := &listerTK{mockTK: mockTK{kind: "trino"}, conns: []toolkit.ConnectionDetail{{Name: "acme"}}}

	// Nil lookup: no enrichment fields.
	out := Build(context.Background(), []registry.Toolkit{tk}, nil, nil)
	require.Len(t, out.Connections, 1)
	assert.Zero(t, out.Connections[0].KnowledgePageCount)
	assert.Empty(t, out.Connections[0].KnowledgePages)

	// Lookup with no referencing pages for this connection.
	out = Build(context.Background(), []registry.Toolkit{tk}, nil, fakePages{})
	assert.Zero(t, out.Connections[0].KnowledgePageCount)
}

func TestBuild_LookupErrorSkipped(t *testing.T) {
	tk := &listerTK{mockTK: mockTK{kind: "trino"}, conns: []toolkit.ConnectionDetail{{Name: "acme"}}}
	out := Build(context.Background(), []registry.Toolkit{tk}, nil, fakePages{err: errors.New("boom")})
	require.Len(t, out.Connections, 1)
	assert.Zero(t, out.Connections[0].KnowledgePageCount, "a lookup error leaves the connection unenriched, not failed")
}

// concurrencyRecorder is a PageLookup that records the peak number of
// simultaneously in-flight lookups. Each arm signals arrival on a barrier and
// waits for the others, so overlap is observed deterministically; a timeout
// stops a serial regression from hanging (it leaves maxSeen at 1 and the
// assertion fails cleanly).
type concurrencyRecorder struct {
	barrier sync.WaitGroup
	mu      sync.Mutex
	live    int
	maxSeen int
	byConn  map[string][]knowledgepage.PageRef
}

func (r *concurrencyRecorder) ListPagesReferencing(_ context.Context, ref knowledgepage.EntityRef) ([]knowledgepage.PageRef, error) {
	r.mu.Lock()
	r.live++
	if r.live > r.maxSeen {
		r.maxSeen = r.live
	}
	r.mu.Unlock()

	r.barrier.Done()
	waited := make(chan struct{})
	go func() { r.barrier.Wait(); close(waited) }()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
	}

	r.mu.Lock()
	r.live--
	r.mu.Unlock()
	return r.byConn[ref.ConnectionKind+"/"+ref.ConnectionName], nil
}

// TestBuild_KnowledgeEnrichmentFansOut proves the per-connection knowledge
// lookups run concurrently (maxSeen > 1) while producing output identical to
// the previous serial loop: every connection enriched, order preserved.
func TestBuild_KnowledgeEnrichmentFansOut(t *testing.T) {
	const n = 5
	conns := make([]toolkit.ConnectionDetail, 0, n)
	byConn := map[string][]knowledgepage.PageRef{}
	for i := range n {
		name := "c" + string(rune('0'+i))
		conns = append(conns, toolkit.ConnectionDetail{Name: name})
		byConn["trino/"+name] = pageRefs(2)
	}
	tk := &listerTK{mockTK: mockTK{kind: "trino"}, conns: conns}
	rec := &concurrencyRecorder{byConn: byConn}
	rec.barrier.Add(n)

	out := Build(context.Background(), []registry.Toolkit{tk}, nil, rec)

	require.Len(t, out.Connections, n)
	for i, e := range out.Connections {
		assert.Equal(t, "c"+string(rune('0'+i)), e.Name, "connection order preserved")
		assert.Equal(t, 2, e.KnowledgePageCount)
		assert.Len(t, e.KnowledgePages, 2)
	}

	rec.mu.Lock()
	defer rec.mu.Unlock()
	assert.Greater(t, rec.maxSeen, 1, "per-connection lookups must run concurrently")
}
