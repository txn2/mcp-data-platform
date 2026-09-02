package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

type refsResp struct {
	Refs []struct {
		URN    string `json:"urn"`
		Type   string `json:"type"`
		Label  string `json:"label"`
		Exists bool   `json:"exists"`
		Source string `json:"source"`
	} `json:"refs"`
}

func livePage() *knowledgepage.Page { return &knowledgepage.Page{ID: "kp1", Slug: "p", Title: "P"} }

func TestListKnowledgePageRefs(t *testing.T) {
	t.Run("unauthenticated", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{page: livePage()}, nil)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("page not found", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpViewer) // nil page -> ErrNotFound
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/missing/refs", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("store error is 500", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{getErr: errors.New("boom")}, kpViewer)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("deleted page is 404", func(t *testing.T) {
		now := time.Now()
		h := newKnowledgePageHandler(&mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", DeletedAt: &now}}, kpViewer)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
		assert.Equal(t, http.StatusNotFound, w.Code)
	})

	t.Run("returns accessible refs with labels and hides inaccessible ones", func(t *testing.T) {
		store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
			{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:x", Source: knowledgepage.RefSourcePromoted},
			{TargetType: knowledgepage.RefTargetConnection, ConnectionKind: "trino", ConnectionName: "warehouse", Source: knowledgepage.RefSourceManual},
			// No AssetStore in this handler -> the asset is inaccessible and must be hidden,
			// so its id never reaches the viewer (the GET endpoint is access-filtered).
			{TargetType: knowledgepage.RefTargetAsset, AssetID: "private-asset", Source: knowledgepage.RefSourceInline},
		}}
		h := newKnowledgePageHandler(store, kpViewer)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
		require.Equal(t, http.StatusOK, w.Code)
		var resp refsResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp.Refs, 2)
		labels := map[string]string{}
		for _, r := range resp.Refs {
			labels[r.URN] = r.Label
		}
		assert.Contains(t, labels, "urn:li:dataset:x")
		assert.Equal(t, "warehouse (trino)", labels["mcp:connection:(trino,warehouse)"])
		assert.NotContains(t, labels, "mcp:asset:private-asset", "inaccessible asset id must not be returned")
	})
}

func TestSetKnowledgePageRefs(t *testing.T) {
	t.Run("requires apply_knowledge", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{page: livePage()}, kpViewer)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1/refs", `{"refs":["mcp:asset:a"]}`)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})

	t.Run("invalid urn is 400", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{page: livePage()}, kpAdmin)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1/refs", `{"refs":["not-a-ref"]}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("malformed body is 400", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{page: livePage()}, kpAdmin)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1/refs", `{not json`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("too many refs is 400", func(t *testing.T) {
		refs := make([]string, maxEntityRefsPerPage+1)
		for i := range refs {
			refs[i] = "mcp:asset:a"
		}
		body, _ := json.Marshal(setEntityRefsRequest{Refs: refs})
		h := newKnowledgePageHandler(&mockKnowledgePageStore{page: livePage()}, kpAdmin)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1/refs", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing target is 422", func(t *testing.T) {
		store := &mockKnowledgePageStore{page: livePage(), refsErr: knowledgepage.ErrRefTargetNotFound}
		h := newKnowledgePageHandler(store, kpAdmin)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1/refs", `{"refs":["mcp:asset:ghost"]}`)
		assert.Equal(t, http.StatusUnprocessableEntity, w.Code)
	})

	t.Run("replaces manual refs and preserves promoted", func(t *testing.T) {
		store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
			{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:x", Source: knowledgepage.RefSourcePromoted},
			{TargetType: knowledgepage.RefTargetConnection, ConnectionKind: "trino", ConnectionName: "old", Source: knowledgepage.RefSourceManual},
		}}
		h := newKnowledgePageHandler(store, kpAdmin)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1/refs", `{"refs":["mcp:connection:(trino,warehouse)"]}`)
		require.Equal(t, http.StatusOK, w.Code)

		var resp refsResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		urns := make([]string, 0, len(resp.Refs))
		for _, r := range resp.Refs {
			urns = append(urns, r.URN)
		}
		// promoted datahub ref survives; the old manual connection is gone; new manual connection present.
		assert.Contains(t, urns, "urn:li:dataset:x")
		assert.Contains(t, urns, "mcp:connection:(trino,warehouse)")
		assert.NotContains(t, urns, "mcp:connection:(trino,old)")
	})

	t.Run("page not found", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpAdmin)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/missing/refs", `{"refs":[]}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

type resolveResp struct {
	Refs []resolvedRef `json:"refs"`
}

func TestResolveKnowledgePageRefs(t *testing.T) {
	store := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp-2", Title: "Fiscal Calendar"}}
	h := newKnowledgePageHandler(store, kpViewer)
	body := `{"urns":[
		"mcp:knowledge_page:kp-2",
		"mcp:connection:(trino,warehouse)",
		"urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)",
		"mcp:asset:asset-9",
		"garbage"
	]}`
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", body)
	require.Equal(t, http.StatusOK, w.Code)

	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 5)

	byURN := map[string]resolvedRef{}
	for _, r := range resp.Refs {
		byURN[r.URN] = r
	}
	assert.Equal(t, "Fiscal Calendar", byURN["mcp:knowledge_page:kp-2"].Label)
	assert.True(t, byURN["mcp:knowledge_page:kp-2"].Exists)
	assert.Equal(t, "warehouse (trino)", byURN["mcp:connection:(trino,warehouse)"].Label)
	assert.Equal(t, "iceberg.retail.daily_sales", byURN["urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"].Label)
	assert.True(t, byURN["mcp:connection:(trino,warehouse)"].Accessible)
	// AssetStore not configured: access cannot be verified, so the ref is hidden.
	assert.False(t, byURN["mcp:asset:asset-9"].Accessible)
	// Unparseable URN is inaccessible and non-existent.
	assert.False(t, byURN["garbage"].Accessible)
}

func TestResolveKnowledgePageRefs_PageNotFound(t *testing.T) {
	// A reference to a page that no longer exists resolves as non-existent.
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpViewer) // nil page -> ErrNotFound
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", `{"urns":["mcp:knowledge_page:gone"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	assert.False(t, resp.Refs[0].Exists)
	assert.Equal(t, "gone", resp.Refs[0].Label)
}

func TestResolveKnowledgePageRefs_SoftDeletedPageIsBroken(t *testing.T) {
	// Get returns soft-deleted pages so callers can inspect them, but a citation
	// to a removed page is a broken reference: resolving it as live would put the
	// removed page's title back in front of readers on every surface that renders
	// references.
	deleted := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	store := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp-gone", Title: "Retired Policy", DeletedAt: &deleted}}
	h := newKnowledgePageHandler(store, kpViewer)
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", `{"urns":["mcp:knowledge_page:kp-gone"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	assert.False(t, resp.Refs[0].Exists)
	assert.NotContains(t, resp.Refs[0].Label, "Retired Policy")
}

type backlinksResp struct {
	Pages []knowledgepage.PageRef `json:"pages"`
}

func TestKnowledgePageBacklinks(t *testing.T) {
	pages := []knowledgepage.PageRef{{ID: "kp1", Slug: "fiscal", Title: "Fiscal Calendar"}}

	t.Run("returns referencing pages for an accessible entity", func(t *testing.T) {
		store := &mockKnowledgePageStore{referencingPages: pages}
		h := newKnowledgePageHandler(store, kpViewer)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/backlinks?urn=urn:li:dataset:x", "")
		require.Equal(t, http.StatusOK, w.Code)
		var resp backlinksResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp.Pages, 1)
		assert.Equal(t, "Fiscal Calendar", resp.Pages[0].Title)
	})

	t.Run("inaccessible entity returns no backlinks", func(t *testing.T) {
		// No AssetStore -> the asset is inaccessible, so its back-references are hidden.
		store := &mockKnowledgePageStore{referencingPages: pages}
		h := newKnowledgePageHandler(store, kpViewer)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/backlinks?urn=mcp:asset:secret", "")
		require.Equal(t, http.StatusOK, w.Code)
		var resp backlinksResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Empty(t, resp.Pages)
	})

	t.Run("bad urn is 400", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpViewer)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/backlinks?urn=garbage", "")
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("unauthenticated", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{}, nil)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/backlinks?urn=urn:li:dataset:x", "")
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})
}

func TestResolveKnowledgePageRefs_Unauthenticated(t *testing.T) {
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, nil)
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", `{"urns":[]}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestKnowledgePageLineage(t *testing.T) {
	kp := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Slug: "seasons"}}
	cr := &mockChangesetReader{changesets: []knowledge.Changeset{
		// i3 is referenced but missing from the insight store (drained); it is skipped.
		{ID: "cs1", TargetURN: "kp:seasons", ChangeType: "create_knowledge_page", SourceInsightIDs: []string{"i1", "i2", "i3"}},
		{ID: "cs2", TargetURN: "kp:seasons", ChangeType: "update_knowledge_page", SourceInsightIDs: []string{"i2"}},
	}}
	is := &mockInsightStore{getResult: map[string]*knowledge.Insight{
		"i1": {ID: "i1", InsightText: "Summer is May to Oct", Status: "applied", Category: "business_context"},
		"i2": {ID: "i2", InsightText: "Winter is Nov to Apr", Status: "applied"},
	}}
	deps := Deps{
		KnowledgePageStore: kp, ChangesetReader: cr, InsightStore: is,
		AdminRoles: []string{"admin"}, RateLimit: RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpAdmin))
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/lineage", "")
	require.Equal(t, http.StatusOK, w.Code)

	var resp knowledgePageLineageResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Changesets, 2, "both promotions appear in lineage")
	require.Len(t, resp.Insights, 2, "i2 deduped across changesets; the drained i3 is skipped")
	gotIDs := []string{resp.Insights[0].ID, resp.Insights[1].ID}
	assert.ElementsMatch(t, []string{"i1", "i2"}, gotIDs)
	// The changeset query must target this page (kp:<slug>), or lineage silently empties.
	assert.Equal(t, "kp:seasons", cr.gotFilter.EntityURN)
}

func TestKnowledgePageLineage_RequiresApplyKnowledge(t *testing.T) {
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Slug: "s"}},
		ChangesetReader:    &mockChangesetReader{},
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	// kpViewer holds no apply_knowledge, so the raw insight lineage is forbidden.
	h := NewHandler(deps, testAuthMiddleware(kpViewer))
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/lineage", "")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestKnowledgePageLineage_NoChangesetReader(t *testing.T) {
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Slug: "s"}},
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpAdmin))
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/lineage", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp knowledgePageLineageResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Insights)
	assert.Empty(t, resp.Changesets)
}

func TestKnowledgePageLineage_NotFound(t *testing.T) {
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{}, // nil page -> ErrNotFound
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpAdmin))
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/missing/lineage", "")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestKnowledgePageLineage_Unauthenticated(t *testing.T) {
	h := newKnowledgePageHandler(&mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Slug: "s"}}, nil)
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/lineage", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestKnowledgePageLineage_ChangesetError(t *testing.T) {
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Slug: "s"}},
		ChangesetReader:    &mockChangesetReader{err: errors.New("boom")},
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpAdmin))
	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/lineage", "")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestResolveKnowledgePageRefs_WithStores(t *testing.T) {
	// The viewer owns the asset/collection and the prompt is global, so all resolve
	// to names and are accessible.
	ps := newMockPromptStore()
	ps.prompts["p"] = &prompt.Prompt{ID: "11111111-1111-1111-1111-111111111111", Name: "Summary Prompt", Scope: prompt.ScopeGlobal}
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{},
		AssetStore:         &mockAssetStore{getAsset: &Asset{ID: "asset-9", OwnerID: kpViewer.UserID, Name: "Revenue Dashboard"}},
		CollectionStore:    &mockCollectionStore{getResult: &Collection{ID: "coll-1", OwnerID: kpViewer.UserID, Name: "Q4 Review"}},
		PromptStore:        ps,
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpViewer))
	body := `{"urns":["mcp:asset:asset-9","mcp:collection:coll-1","mcp:prompt:11111111-1111-1111-1111-111111111111","urn:li:glossaryTerm:revenue"]}`
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", body)
	require.Equal(t, http.StatusOK, w.Code)

	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	byURN := map[string]resolvedRef{}
	for _, r := range resp.Refs {
		byURN[r.URN] = r
	}
	assert.Equal(t, "Revenue Dashboard", byURN["mcp:asset:asset-9"].Label)
	assert.True(t, byURN["mcp:asset:asset-9"].Accessible)
	assert.Equal(t, "Q4 Review", byURN["mcp:collection:coll-1"].Label)
	assert.Equal(t, "Summary Prompt", byURN["mcp:prompt:11111111-1111-1111-1111-111111111111"].Label)
	assert.Equal(t, "revenue", byURN["urn:li:glossaryTerm:revenue"].Label)
}

func TestResolveKnowledgePageRefs_AssetNotOwnedIsNotLeaked(t *testing.T) {
	// An asset the user does not own (or that is missing) yields only the id, with
	// no name and no existence signal, so the endpoint cannot enumerate them.
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{},
		AssetStore:         &mockAssetStore{getAsset: &Asset{ID: "secret", OwnerID: "someone-else", Name: "Confidential Q4 Layoffs"}},
		CollectionStore:    &mockCollectionStore{getResult: &Collection{ID: "secret-c", OwnerID: "someone-else", Name: "Confidential Board Deck"}},
		ShareStore:         &mockShareStore{}, // no shares -> viewer has no access
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpViewer))
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve",
		`{"urns":["mcp:asset:secret","mcp:collection:secret-c"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	byURN := map[string]resolvedRef{}
	for _, r := range resp.Refs {
		byURN[r.URN] = r
	}
	a, c := byURN["mcp:asset:secret"], byURN["mcp:collection:secret-c"]
	assert.False(t, a.Accessible, "non-owned asset must be inaccessible")
	assert.NotContains(t, a.Label, "Confidential", "must not leak a non-owned asset's name")
	assert.False(t, c.Accessible, "non-owned collection must be inaccessible")
	assert.NotContains(t, c.Label, "Confidential", "must not leak a non-owned collection's name")
}

func TestResolveKnowledgePageRefs_NoStoresHideAccessGated(t *testing.T) {
	// With no asset/collection/prompt stores, access cannot be verified, so every
	// access-gated reference is hidden.
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpViewer)
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve",
		`{"urns":["mcp:asset:a","mcp:collection:c","mcp:prompt:11111111-1111-1111-1111-111111111111"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	for _, r := range resp.Refs {
		assert.False(t, r.Accessible, "%s should be hidden without a store", r.URN)
	}
}

func TestResolveKnowledgePageRefs_PrivatePromptHidden(t *testing.T) {
	ps := newMockPromptStore()
	ps.prompts["p"] = &prompt.Prompt{
		ID: "11111111-1111-1111-1111-111111111111", Name: "Secret", Scope: prompt.ScopePersonal, OwnerEmail: "other@example.com",
	}
	deps := Deps{
		KnowledgePageStore: &mockKnowledgePageStore{},
		PromptStore:        ps,
		ShareStore:         &mockShareStore{}, // no shares
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpViewer))
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve",
		`{"urns":["mcp:prompt:11111111-1111-1111-1111-111111111111"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp.Refs[0].Accessible, "a personal prompt the viewer cannot see must be hidden")
	assert.NotContains(t, resp.Refs[0].Label, "Secret")
}

func TestResolveKnowledgePageRefs_BadInput(t *testing.T) {
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpViewer)
	t.Run("malformed body", func(t *testing.T) {
		w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", `{nope`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
	t.Run("too many", func(t *testing.T) {
		urns := make([]string, maxEntityRefsPerPage+1)
		for i := range urns {
			urns[i] = "mcp:asset:a"
		}
		body, _ := json.Marshal(resolveRefsRequest{URNs: urns})
		w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", string(body))
		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestDatahubLabel(t *testing.T) {
	assert.Equal(t, "iceberg.retail.daily_sales",
		datahubLabel("urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.daily_sales,PROD)"))
	assert.Equal(t, "revenue", datahubLabel("urn:li:glossaryTerm:revenue"))
	assert.Equal(t, "nocolon", datahubLabel("nocolon"))
}

// stubCatalogLabeler is a CatalogLabeler that answers from a fixed table and
// records what it was asked, so a test can assert both the name a reference
// renders with and that the batch carried only what needed resolving.
type stubCatalogLabeler struct {
	names map[string]string
	asked [][]string
}

func (s *stubCatalogLabeler) Labels(_ context.Context, urns []string) map[string]string {
	s.asked = append(s.asked, urns)
	out := map[string]string{}
	for _, urn := range urns {
		if name, ok := s.names[urn]; ok {
			out[urn] = name
		}
	}
	return out
}

// catalogPersona resolves every role to a persona that grants a DataHub tool,
// which is what catalog access is gated on.
func catalogPersona(_ []string) *PersonaInfo {
	return &PersonaInfo{Name: "analyst", Tools: []string{"search", "datahub_get_lineage"}}
}

// noCatalogPersona resolves to a persona with no DataHub tool at all.
func noCatalogPersona(_ []string) *PersonaInfo {
	return &PersonaInfo{Name: "restricted", Tools: []string{"search"}}
}

// newLabelingHandler builds a knowledge-page handler with a catalog labeler
// wired, the shape the server has when a DataHub connection is configured.
func newLabelingHandler(store knowledgepage.Store, user *User, labeler CatalogLabeler, persona PersonaResolver) *Handler {
	return NewHandler(Deps{
		KnowledgePageStore: store,
		AdminRoles:         []string{"admin"},
		RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		CatalogLabeler:     labeler,
		PersonaResolver:    persona,
	}, testAuthMiddleware(user))
}

// TestKnowledgePageRefs_GovernanceNamesResolve is the display-name contract
// (#1159): a citation of a glossary term, a tag, or a domain renders as the
// entity's name, not the generated key DataHub put inside its URN. Without the
// labeler the same reference reads as "8f3c", which names nothing.
func TestKnowledgePageRefs_GovernanceNamesResolve(t *testing.T) {
	labeler := &stubCatalogLabeler{names: map[string]string{
		"urn:li:glossaryTerm:8f3c": "Net Revenue",
		"urn:li:tag:a1b2":          "PII",
		"urn:li:domain:c3d4":       "Finance",
	}}
	store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:glossaryTerm:8f3c", Source: knowledgepage.RefSourceManual},
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:tag:a1b2", Source: knowledgepage.RefSourceManual},
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:domain:c3d4", Source: knowledgepage.RefSourceManual},
	}}
	h := newLabelingHandler(store, kpViewer, labeler, catalogPersona)

	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp refsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	labels := map[string]string{}
	for _, r := range resp.Refs {
		labels[r.URN] = r.Label
	}
	assert.Equal(t, "Net Revenue", labels["urn:li:glossaryTerm:8f3c"])
	assert.Equal(t, "PII", labels["urn:li:tag:a1b2"])
	assert.Equal(t, "Finance", labels["urn:li:domain:c3d4"])

	// One batch for the whole page: a tag and a domain each cost a vocabulary
	// listing upstream, so a per-reference resolve would multiply them.
	require.Len(t, labeler.asked, 1)
}

// TestKnowledgePageRefs_UnresolvedFallsBackToTheURN proves an unnamed reference
// keeps the name derivable from its URN rather than rendering blank, which is
// what makes the resolve safe to fail.
func TestKnowledgePageRefs_UnresolvedFallsBackToTheURN(t *testing.T) {
	labeler := &stubCatalogLabeler{names: map[string]string{}}
	store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:tag:pii", Source: knowledgepage.RefSourceManual},
	}}
	h := newLabelingHandler(store, kpViewer, labeler, catalogPersona)

	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp refsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	assert.Equal(t, "pii", resp.Refs[0].Label)
}

// TestKnowledgePageRefs_OnlyCatalogURNsAreResolved proves the batch carries only
// catalog references: an mcp: reference is resolved from the portal's own
// stores, so sending it upstream would be a pointless round trip.
func TestKnowledgePageRefs_OnlyCatalogURNsAreResolved(t *testing.T) {
	labeler := &stubCatalogLabeler{names: map[string]string{"urn:li:tag:pii": "PII"}}
	store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:tag:pii", Source: knowledgepage.RefSourceManual},
		{TargetType: knowledgepage.RefTargetConnection, ConnectionKind: "trino", ConnectionName: "warehouse", Source: knowledgepage.RefSourceManual},
		{TargetType: knowledgepage.RefTargetKnowledgePage, RefPageID: "kp2", Source: knowledgepage.RefSourceManual},
	}}
	h := newLabelingHandler(store, kpViewer, labeler, catalogPersona)

	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, labeler.asked, 1)
	assert.Equal(t, []string{"urn:li:tag:pii"}, labeler.asked[0])
}

// TestKnowledgePageRefs_CatalogAccessGatesTheName proves the display name is
// gated on catalog access, the same rule the DataHub REST surface applies. A
// persona granted no DataHub tool cannot open the Catalog tab, so it must not
// learn a governance entity's name through the reference list instead; it keeps
// the label derivable from the URN, and the catalog is never read at all.
func TestKnowledgePageRefs_CatalogAccessGatesTheName(t *testing.T) {
	labeler := &stubCatalogLabeler{names: map[string]string{"urn:li:tag:a1b2": "PII"}}
	store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:tag:a1b2", Source: knowledgepage.RefSourceManual},
	}}
	h := newLabelingHandler(store, kpViewer, labeler, noCatalogPersona)

	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp refsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	assert.Equal(t, "a1b2", resp.Refs[0].Label)
	assert.Empty(t, labeler.asked, "a persona denied the catalog must not cause a catalog read")
}

// TestKnowledgePageRefs_AdminAlwaysGetsTheName proves the admin arm of the gate:
// an admin holds catalog access regardless of what their persona resolves to,
// matching the DataHub REST surface.
func TestKnowledgePageRefs_AdminAlwaysGetsTheName(t *testing.T) {
	labeler := &stubCatalogLabeler{names: map[string]string{"urn:li:tag:a1b2": "PII"}}
	store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:tag:a1b2", Source: knowledgepage.RefSourceManual},
	}}
	h := newLabelingHandler(store, kpAdmin, labeler, noCatalogPersona)

	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp refsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	assert.Equal(t, "PII", resp.Refs[0].Label)
}

// TestKnowledgePageRefs_NoLabelerKeepsURNLabels proves a deployment with no
// DataHub connection still renders catalog citations, through the URN-derived
// name. The labeler is optional wiring, not a requirement.
func TestKnowledgePageRefs_NoLabelerKeepsURNLabels(t *testing.T) {
	store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
		{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:tag:pii", Source: knowledgepage.RefSourceManual},
	}}
	h := newKnowledgePageHandler(store, kpViewer)

	w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
	require.Equal(t, http.StatusOK, w.Code)
	var resp refsResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	assert.Equal(t, "pii", resp.Refs[0].Label)
}

// TestResolveKnowledgePageRefs_GovernanceNames covers the batch resolve endpoint
// the markdown renderer calls, so an inline citation in a page body names its
// term the same way the Related panel does.
func TestResolveKnowledgePageRefs_GovernanceNames(t *testing.T) {
	labeler := &stubCatalogLabeler{names: map[string]string{"urn:li:glossaryTerm:8f3c": "Net Revenue"}}
	h := newLabelingHandler(&mockKnowledgePageStore{page: livePage()}, kpViewer, labeler, catalogPersona)

	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve",
		`{"urns":["urn:li:glossaryTerm:8f3c"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Refs []resolvedRef `json:"refs"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	assert.Equal(t, "Net Revenue", resp.Refs[0].Label)
}

func TestCreateKnowledgePage_InlineReconcileErrorDoesNotFail(t *testing.T) {
	// A reconcile failure is logged but must not fail the page create.
	store := &mockKnowledgePageStore{refsErr: errors.New("boom")}
	h := newKnowledgePageHandler(store, kpAdmin)
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages",
		`{"title":"T","body":"see [a](mcp:asset:x)"}`)
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestCreateKnowledgePage_ReconcilesInlineRefs(t *testing.T) {
	store := &mockKnowledgePageStore{}
	h := newKnowledgePageHandler(store, kpAdmin)
	body := `{"title":"Sales","body":"see [a](mcp:asset:asset-001) and urn:li:glossaryTerm:revenue"}`
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages", body)
	require.Equal(t, http.StatusCreated, w.Code)

	urns := make([]string, 0, len(store.refs))
	for _, rf := range store.refs {
		assert.Equal(t, knowledgepage.RefSourceInline, rf.Source)
		urns = append(urns, rf.URN())
	}
	assert.Contains(t, urns, "mcp:asset:asset-001")
	assert.Contains(t, urns, "urn:li:glossaryTerm:revenue")
}

func TestUpdateKnowledgePage_InlineReconcilePreservesPromoted(t *testing.T) {
	store := &mockKnowledgePageStore{
		page: livePage(),
		refs: []knowledgepage.EntityRef{
			{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:x", Source: knowledgepage.RefSourcePromoted},
			{TargetType: knowledgepage.RefTargetAsset, AssetID: "stale-inline", Source: knowledgepage.RefSourceInline},
		},
	}
	h := newKnowledgePageHandler(store, kpAdmin)
	w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1",
		`{"title":"T","body":"now references [coll](mcp:collection:coll-1)"}`)
	require.Equal(t, http.StatusOK, w.Code)

	urns := make([]string, 0, len(store.refs))
	for _, rf := range store.refs {
		urns = append(urns, rf.URN())
	}
	assert.Contains(t, urns, "urn:li:dataset:x", "promoted ref preserved")
	assert.Contains(t, urns, "mcp:collection:coll-1", "new inline ref from body")
	assert.NotContains(t, urns, "mcp:asset:stale-inline", "old inline ref replaced")
}

// mockChangesetReader is a controllable ChangesetReader for the lineage tests.
type mockChangesetReader struct {
	changesets []knowledge.Changeset
	err        error
	gotFilter  knowledge.ChangesetFilter
}

func (m *mockChangesetReader) ListChangesets(_ context.Context, f knowledge.ChangesetFilter) ([]knowledge.Changeset, int, error) {
	m.gotFilter = f
	return m.changesets, len(m.changesets), m.err
}
