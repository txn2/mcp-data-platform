package portal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// graphPageStore is a knowledgepage.Store that also serves the corpus-wide
// reference read, over an in-memory set of pages and references. It resolves Get
// per id (the shared mock returns one canned page for every id), which the graph
// needs so a page-to-page reference resolves to the right title.
type graphPageStore struct {
	*mockKnowledgePageStore
	byID       map[string]*knowledgepage.Page
	corpusRefs []knowledgepage.EntityRef
	corpusErr  error
}

func (g *graphPageStore) Get(_ context.Context, id string) (*knowledgepage.Page, error) {
	if p, ok := g.byID[id]; ok {
		return p, nil
	}
	return nil, knowledgepage.ErrNotFound
}

// ListEntityRefsForPages returns only the references of the requested pages, so a
// test that narrows the page window also narrows the edges, as the SQL does.
func (g *graphPageStore) ListEntityRefsForPages(_ context.Context, ids []string) ([]knowledgepage.EntityRef, error) {
	if g.corpusErr != nil {
		return nil, g.corpusErr
	}
	want := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		want[id] = struct{}{}
	}
	out := make([]knowledgepage.EntityRef, 0, len(g.corpusRefs))
	for _, ref := range g.corpusRefs {
		if _, ok := want[ref.PageID]; ok {
			out = append(out, ref)
		}
	}
	return out, nil
}

// newGraphStore builds a graph-capable store over the given pages, wiring the
// list result, the per-id lookup, and the corpus references together.
func newGraphStore(pages []knowledgepage.Page, refs []knowledgepage.EntityRef) *graphPageStore {
	byID := make(map[string]*knowledgepage.Page, len(pages))
	for i := range pages {
		byID[pages[i].ID] = &pages[i]
	}
	return &graphPageStore{
		mockKnowledgePageStore: &mockKnowledgePageStore{pages: pages, total: len(pages)},
		byID:                   byID,
		corpusRefs:             refs,
	}
}

// graphResp mirrors knowledgeGraphResponse for decoding in tests.
type graphResp struct {
	Nodes      []knowledgeGraphNode `json:"nodes"`
	Edges      []knowledgeGraphEdge `json:"edges"`
	TotalPages int                  `json:"total_pages"`
	Truncated  bool                 `json:"truncated"`
	Notice     string               `json:"notice"`
}

func (g graphResp) nodeByID(id string) (knowledgeGraphNode, bool) {
	for _, n := range g.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return knowledgeGraphNode{}, false
}

func fetchGraph(t *testing.T, h *Handler, query string) graphResp {
	t.Helper()
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/graph"+query, "")
	require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
	var resp graphResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func TestKnowledgeGraph_TypedNodesEdgesAndIsolatedPages(t *testing.T) {
	updated := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	pages := []knowledgepage.Page{
		{ID: "kp1", Title: "Fiscal Calendar", Tags: []string{"finance"}, UpdatedAt: updated},
		{ID: "kp2", Title: "Revenue Model", Tags: []string{"finance"}, UpdatedAt: updated},
		{ID: "kp3", Title: "Orphan Note", Tags: []string{}, UpdatedAt: updated},
	}
	refs := []knowledgepage.EntityRef{
		{PageID: "kp1", TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:(trino,sales.orders,PROD)", Source: knowledgepage.RefSourcePromoted},
		{PageID: "kp1", TargetType: knowledgepage.RefTargetKnowledgePage, RefPageID: "kp2", Source: knowledgepage.RefSourceInline},
		{PageID: "kp2", TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:(trino,sales.orders,PROD)", Source: knowledgepage.RefSourceManual},
		{PageID: "kp2", TargetType: knowledgepage.RefTargetConnection, ConnectionKind: "trino", ConnectionName: "warehouse", Source: knowledgepage.RefSourceManual},
	}
	h := newKnowledgePageHandler(newGraphStore(pages, refs), kpViewer)

	resp := fetchGraph(t, h, "")

	// Three page nodes plus two distinct referenced entities (the dataset is cited
	// by both pages and is one vertex, not two).
	assert.Len(t, resp.Nodes, 5)
	assert.Equal(t, 3, resp.TotalPages)
	assert.False(t, resp.Truncated)
	assert.Empty(t, resp.Notice)

	kp1, ok := resp.nodeByID("mcp:knowledge_page:kp1")
	require.True(t, ok, "page node must be keyed by its reference URN")
	assert.True(t, kp1.Page)
	assert.Equal(t, "Fiscal Calendar", kp1.Label)
	assert.Equal(t, []string{"finance"}, kp1.Tags)
	require.NotNil(t, kp1.UpdatedAt)
	assert.True(t, updated.Equal(*kp1.UpdatedAt))

	dataset, ok := resp.nodeByID("urn:li:dataset:(trino,sales.orders,PROD)")
	require.True(t, ok)
	assert.False(t, dataset.Page)
	assert.Equal(t, knowledgepage.RefTargetDataHub, dataset.Type)
	assert.Equal(t, "sales.orders", dataset.Label, "entity nodes carry a resolved name, not the raw URN")

	conn, ok := resp.nodeByID("mcp:connection:(trino,warehouse)")
	require.True(t, ok)
	assert.Equal(t, "warehouse (trino)", conn.Label)

	// A page with no references is still a vertex, so an isolated page is visible.
	orphan, ok := resp.nodeByID("mcp:knowledge_page:kp3")
	require.True(t, ok)
	assert.True(t, orphan.Page)
	for _, e := range resp.Edges {
		assert.NotEqual(t, "mcp:knowledge_page:kp3", e.Source)
		assert.NotEqual(t, "mcp:knowledge_page:kp3", e.Target)
	}

	// Every stored reference is an edge, carrying its type and provenance, and
	// every endpoint is a node the response contains.
	require.Len(t, resp.Edges, 4)
	ids := map[string]struct{}{}
	for _, n := range resp.Nodes {
		ids[n.ID] = struct{}{}
	}
	for _, e := range resp.Edges {
		assert.Contains(t, ids, e.Source)
		assert.Contains(t, ids, e.Target)
	}
	assert.Contains(t, resp.Edges, knowledgeGraphEdge{
		Source: "mcp:knowledge_page:kp1", Target: "mcp:knowledge_page:kp2",
		Type: knowledgepage.RefTargetKnowledgePage, RefSource: knowledgepage.RefSourceInline,
	})
	assert.Contains(t, resp.Edges, knowledgeGraphEdge{
		Source: "mcp:knowledge_page:kp2", Target: "mcp:connection:(trino,warehouse)",
		Type: knowledgepage.RefTargetConnection, RefSource: knowledgepage.RefSourceManual,
	})
}

func TestKnowledgeGraph_InaccessibleEntityHasNeitherNodeNorEdge(t *testing.T) {
	pages := []knowledgepage.Page{{ID: "kp1", Title: "Fiscal Calendar", Tags: []string{}}}
	refs := []knowledgepage.EntityRef{
		{PageID: "kp1", TargetType: knowledgepage.RefTargetAsset, AssetID: "secret", Source: knowledgepage.RefSourceManual},
		{PageID: "kp1", TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:(trino,public.orders,PROD)", Source: knowledgepage.RefSourceManual},
	}
	deps := Deps{
		KnowledgePageStore: newGraphStore(pages, refs),
		// The asset exists but belongs to someone else and is not shared, so the
		// viewer may not see it: neither its node nor its edge may appear.
		AssetStore: &mockAssetStore{getAsset: &Asset{ID: "secret", OwnerID: "someone-else", Name: "Confidential Q4 Layoffs"}},
		ShareStore: &mockShareStore{},
		AdminRoles: []string{"admin"},
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpViewer))

	resp := fetchGraph(t, h, "")

	_, ok := resp.nodeByID("mcp:asset:secret")
	assert.False(t, ok, "an inaccessible entity must have no node")
	for _, e := range resp.Edges {
		assert.NotEqual(t, "mcp:asset:secret", e.Target, "an inaccessible entity must have no edge")
	}
	assert.Len(t, resp.Edges, 1, "only the accessible reference remains")
	assert.NotContains(t, mustJSON(t, resp), "Confidential", "the inaccessible asset's name must not be returned")
	assert.NotContains(t, mustJSON(t, resp), "mcp:asset:secret", "the inaccessible asset's id must not be returned")
}

func TestKnowledgeGraph_SelfReferenceIsNotAnEdge(t *testing.T) {
	pages := []knowledgepage.Page{{ID: "kp1", Title: "Fiscal Calendar", Tags: []string{}}}
	refs := []knowledgepage.EntityRef{
		{PageID: "kp1", TargetType: knowledgepage.RefTargetKnowledgePage, RefPageID: "kp1", Source: knowledgepage.RefSourceInline},
	}
	h := newKnowledgePageHandler(newGraphStore(pages, refs), kpViewer)

	resp := fetchGraph(t, h, "")

	assert.Len(t, resp.Nodes, 1)
	assert.Empty(t, resp.Edges, "a page citing itself is not drawable as an edge between two vertices")
}

func TestKnowledgeGraph_ReferenceToRemovedPageIsBroken(t *testing.T) {
	deleted := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	pages := []knowledgepage.Page{{ID: "kp1", Title: "Fiscal Calendar", Tags: []string{}}}
	store := newGraphStore(pages, []knowledgepage.EntityRef{
		{PageID: "kp1", TargetType: knowledgepage.RefTargetKnowledgePage, RefPageID: "kp-gone", Source: knowledgepage.RefSourceInline},
	})
	// The referenced page is soft-deleted: it is still readable by id, but a
	// citation to it is broken, not a live vertex carrying its title.
	store.byID["kp-gone"] = &knowledgepage.Page{ID: "kp-gone", Title: "Retired Policy", DeletedAt: &deleted}
	h := newKnowledgePageHandler(store, kpViewer)

	resp := fetchGraph(t, h, "")

	node, ok := resp.nodeByID("mcp:knowledge_page:kp-gone")
	require.True(t, ok)
	assert.False(t, node.Exists, "a citation to a removed page is a broken reference")
	assert.NotContains(t, node.Label, "Retired Policy", "a removed page's title must not be resurfaced")
}

func TestKnowledgeGraph_TruncationIsReported(t *testing.T) {
	t.Run("page window", func(t *testing.T) {
		pages := []knowledgepage.Page{{ID: "kp1", Title: "One", Tags: []string{}}}
		store := newGraphStore(pages, nil)
		store.total = 340 // more pages match than the window returned

		resp := fetchGraph(t, newKnowledgePageHandler(store, kpViewer), "")

		assert.True(t, resp.Truncated)
		assert.Equal(t, 340, resp.TotalPages)
		assert.Contains(t, resp.Notice, "1 most recently updated of 340 pages")
	})

	t.Run("node cap", func(t *testing.T) {
		pages := []knowledgepage.Page{{ID: "kp1", Title: "One", Tags: []string{}}}
		refs := make([]knowledgepage.EntityRef, 0, maxGraphNodes+50)
		for i := range maxGraphNodes + 50 {
			refs = append(refs, knowledgepage.EntityRef{
				PageID: "kp1", TargetType: knowledgepage.RefTargetDataHub,
				EntityURN: fmt.Sprintf("urn:li:dataset:(trino,sales.t%d,PROD)", i),
				Source:    knowledgepage.RefSourceManual,
			})
		}
		resp := fetchGraph(t, newKnowledgePageHandler(newGraphStore(pages, refs), kpViewer), "")

		assert.True(t, resp.Truncated)
		assert.Len(t, resp.Nodes, maxGraphNodes)
		assert.Len(t, resp.Edges, maxGraphNodes-1, "an edge is dropped with the node it would dangle from")
		assert.Contains(t, resp.Notice, "node limit")
	})
}

func TestKnowledgeGraph_EmptyCorpusReturnsEmptyArrays(t *testing.T) {
	resp := fetchGraph(t, newKnowledgePageHandler(newGraphStore(nil, nil), kpViewer), "")

	assert.Empty(t, resp.Nodes)
	assert.Empty(t, resp.Edges)
	assert.False(t, resp.Truncated)
	// Nodes/edges must serialize as [] rather than null so the client can render
	// the empty state without a null guard.
	assert.Contains(t, mustJSON(t, resp), `"nodes":[]`)
	assert.Contains(t, mustJSON(t, resp), `"edges":[]`)
}

func TestKnowledgeGraph_TagFilterNarrowsTheCorpus(t *testing.T) {
	pages := []knowledgepage.Page{{ID: "kp1", Title: "Fiscal Calendar", Tags: []string{"finance"}}}
	store := newGraphStore(pages, nil)
	h := newKnowledgePageHandler(store, kpViewer)

	fetchGraph(t, h, "?tag=finance&limit=25")

	require.NotNil(t, store.lastFilter)
	assert.Equal(t, "finance", store.lastFilter.Tag)
	assert.Equal(t, 25, store.lastFilter.Limit)
}

func TestKnowledgeGraph_RequestedLimitNeverExceedsTheStoresHonoredLimit(t *testing.T) {
	// The store does not clamp an over-large limit down to its cap; it falls back
	// to its small default. So an over-large request must reach the store already
	// bounded, or asking for more pages would silently return far fewer.
	store := newGraphStore([]knowledgepage.Page{{ID: "kp1", Title: "One"}}, nil)
	h := newKnowledgePageHandler(store, kpViewer)

	fetchGraph(t, h, "?limit=9999")

	require.NotNil(t, store.lastFilter)
	assert.LessOrEqual(t, store.lastFilter.Limit, knowledgepage.MaxHonoredLimit)
	assert.Equal(t, knowledgepage.MaxHonoredLimit, store.lastFilter.Limit,
		"the largest honored window should be requested, not a smaller one")
}

func TestKnowledgeGraph_RequiresAuthentication(t *testing.T) {
	h := newKnowledgePageHandler(newGraphStore(nil, nil), nil)
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/graph", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestKnowledgeGraph_StoreErrorsAre500(t *testing.T) {
	t.Run("page listing", func(t *testing.T) {
		store := newGraphStore(nil, nil)
		store.listErr = errors.New("boom")
		w := doKP(newKnowledgePageHandler(store, kpViewer), "GET", "/api/v1/portal/knowledge-pages/graph", "")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
	t.Run("reference read", func(t *testing.T) {
		store := newGraphStore([]knowledgepage.Page{{ID: "kp1", Title: "One"}}, nil)
		store.corpusErr = errors.New("boom")
		w := doKP(newKnowledgePageHandler(store, kpViewer), "GET", "/api/v1/portal/knowledge-pages/graph", "")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})
}

func TestKnowledgeGraph_NotRegisteredWithoutGraphCapableStore(t *testing.T) {
	// The shared mock is a Store but not a GraphReader, so /graph must fall through
	// to the by-id route (a page named "graph" does not exist) rather than serve a
	// graph from a store that cannot supply the corpus-wide reference read.
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpViewer)
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/graph", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NotContains(t, w.Body.String(), `"nodes"`)
}

func TestClampGraphLimit(t *testing.T) {
	tests := []struct {
		raw  string
		want int
	}{
		{"", defaultGraphPages},
		{"abc", defaultGraphPages},
		{"0", defaultGraphPages},
		{"-5", defaultGraphPages},
		{"25", 25},
		{"9999", maxGraphPages},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, clampGraphLimit(tc.raw), "limit=%q", tc.raw)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestKnowledgeGraphRefsAreScopedToTheListedPages guards the store contract the
// handler relies on: references are read for exactly the pages in the window, so
// narrowing the window cannot leave an edge whose source page is absent.
func TestKnowledgeGraphRefsAreScopedToTheListedPages(t *testing.T) {
	store := newGraphStore(nil, []knowledgepage.EntityRef{
		{PageID: "kp1", TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:(trino,a,PROD)"},
		{PageID: "kp2", TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:(trino,b,PROD)"},
	})
	got, err := store.ListEntityRefsForPages(context.Background(), []string{"kp1"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "kp1", got[0].PageID)
	assert.True(t, strings.Contains(got[0].EntityURN, "trino,a"))
}
