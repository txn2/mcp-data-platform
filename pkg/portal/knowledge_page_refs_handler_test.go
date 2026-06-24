package portal

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
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
	// A reference to a deleted page resolves as non-existent.
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpViewer) // nil page -> ErrNotFound
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", `{"urns":["mcp:knowledge_page:gone"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	var resp resolveResp
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Refs, 1)
	assert.False(t, resp.Refs[0].Exists)
	assert.Equal(t, "gone", resp.Refs[0].Label)
}

func TestResolveKnowledgePageRefs_Unauthenticated(t *testing.T) {
	h := newKnowledgePageHandler(&mockKnowledgePageStore{}, nil)
	w := doKP(h, "POST", "/api/v1/portal/knowledge-pages/refs/resolve", `{"urns":[]}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
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
