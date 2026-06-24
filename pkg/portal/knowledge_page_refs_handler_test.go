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
)

type refsResp struct {
	Refs []struct {
		URN        string `json:"urn"`
		TargetType string `json:"target_type"`
		AssetID    string `json:"asset_id"`
		EntityURN  string `json:"entity_urn"`
		Source     string `json:"source"`
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

	t.Run("returns refs with serialized urn", func(t *testing.T) {
		store := &mockKnowledgePageStore{page: livePage(), refs: []knowledgepage.EntityRef{
			{TargetType: knowledgepage.RefTargetDataHub, EntityURN: "urn:li:dataset:x", Source: knowledgepage.RefSourcePromoted},
			{TargetType: knowledgepage.RefTargetAsset, AssetID: "asset-001", Source: knowledgepage.RefSourceManual},
		}}
		h := newKnowledgePageHandler(store, kpViewer)
		w := doKP(h, "GET", "/api/v1/portal/knowledge-pages/kp1/refs", "")
		require.Equal(t, http.StatusOK, w.Code)
		var resp refsResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.Len(t, resp.Refs, 2)
		assert.Equal(t, "urn:li:dataset:x", resp.Refs[0].URN)
		assert.Equal(t, "mcp:asset:asset-001", resp.Refs[1].URN)
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
			{TargetType: knowledgepage.RefTargetAsset, AssetID: "old-asset", Source: knowledgepage.RefSourceManual},
		}}
		h := newKnowledgePageHandler(store, kpAdmin)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/kp1/refs", `{"refs":["mcp:collection:coll-1"]}`)
		require.Equal(t, http.StatusOK, w.Code)

		var resp refsResp
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		urns := make([]string, 0, len(resp.Refs))
		for _, r := range resp.Refs {
			urns = append(urns, r.URN)
		}
		// promoted datahub ref survives; the old manual asset is gone; new manual collection present.
		assert.Contains(t, urns, "urn:li:dataset:x")
		assert.Contains(t, urns, "mcp:collection:coll-1")
		assert.NotContains(t, urns, "mcp:asset:old-asset")
	})

	t.Run("page not found", func(t *testing.T) {
		h := newKnowledgePageHandler(&mockKnowledgePageStore{}, kpAdmin)
		w := doKP(h, "PUT", "/api/v1/portal/knowledge-pages/missing/refs", `{"refs":[]}`)
		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}
