package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
)

// fakeSearchRouter records the query it received and returns a canned result.
type fakeSearchRouter struct {
	got    SearchQuery
	result SearchResult
	err    error
}

func (f *fakeSearchRouter) Search(_ context.Context, q SearchQuery) (SearchResult, error) {
	f.got = q
	return f.result, f.err
}

func newSearchHandler(router SearchRouter, user *User, resolver PersonaResolver) *Handler {
	deps := Deps{
		SearchRouter:    router,
		AdminRoles:      []string{"admin"},
		PersonaResolver: resolver,
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	return NewHandler(deps, testAuthMiddleware(user))
}

func TestSearch_Unauthenticated(t *testing.T) {
	router := &fakeSearchRouter{}
	h := newSearchHandler(router, nil, nil)
	rec := doKP(h, "GET", "/api/v1/portal/search?q=churn", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestSearch_RequiresQueryOrEntityURNs(t *testing.T) {
	router := &fakeSearchRouter{}
	h := newSearchHandler(router, kpViewer, nil)
	rec := doKP(h, "GET", "/api/v1/portal/search", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestSearch_ForwardsAndGroups(t *testing.T) {
	router := &fakeSearchRouter{result: SearchResult{
		Ranking: "hybrid",
		Groups: []SearchGroup{
			{Source: "catalog", Hits: []SearchHit{{Text: "daily_sales", Source: "catalog", Ref: "urn:1", Score: 0.9}}},
			{Source: "knowledge_pages", Hits: []SearchHit{{Text: "Fiscal Calendar", Source: "knowledge_pages", Ref: "kp1", Score: 0.8}}},
		},
		Coverage: []SearchCoverage{{Source: "catalog", Matched: 14, Shown: 1}},
	}}
	resolver := func([]string) *PersonaInfo { return &PersonaInfo{Name: "analyst", Tools: []string{"search"}} }
	h := newSearchHandler(router, kpViewer, resolver)

	rec := doKP(h, "GET", "/api/v1/portal/search?q=churn&sources=catalog,memory&entity_urns=urn:a&status=pending&limit=5", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	// Query forwarded with parsed params and resolved caller.
	if router.got.Intent != "churn" {
		t.Errorf("intent = %q, want churn", router.got.Intent)
	}
	if len(router.got.Sources) != 2 || router.got.Sources[0] != "catalog" || router.got.Sources[1] != "memory" {
		t.Errorf("sources = %v, want [catalog memory]", router.got.Sources)
	}
	if len(router.got.EntityURNs) != 1 || router.got.EntityURNs[0] != "urn:a" {
		t.Errorf("entity_urns = %v, want [urn:a]", router.got.EntityURNs)
	}
	if router.got.Status != "pending" || router.got.Limit != 5 {
		t.Errorf("status/limit = %q/%d, want pending/5", router.got.Status, router.got.Limit)
	}
	if router.got.Caller.Email != kpViewer.Email || router.got.Caller.Persona != "analyst" {
		t.Errorf("caller = %+v, want email=%s persona=analyst", router.got.Caller, kpViewer.Email)
	}

	var resp searchResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Count != 2 {
		t.Errorf("count = %d, want 2", resp.Count)
	}
	if resp.Ranking != "hybrid" || len(resp.Groups) != 2 || len(resp.Coverage) != 1 {
		t.Errorf("unexpected response: %+v", resp)
	}
}

func TestSearch_EntityURNsOnly(t *testing.T) {
	router := &fakeSearchRouter{}
	h := newSearchHandler(router, kpViewer, nil)
	rec := doKP(h, "GET", "/api/v1/portal/search?entity_urns=urn:li:dataset:x", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if len(router.got.EntityURNs) != 1 {
		t.Errorf("entity_urns = %v, want one", router.got.EntityURNs)
	}
}

func TestSearch_RouterErrorReturns500(t *testing.T) {
	router := &fakeSearchRouter{err: errors.New("all providers down")}
	h := newSearchHandler(router, kpViewer, nil)
	rec := doKP(h, "GET", "/api/v1/portal/search?q=x", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSearch_NotRegisteredWithoutRouter(t *testing.T) {
	deps := Deps{
		AdminRoles: []string{"admin"},
		RateLimit:  RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}
	h := NewHandler(deps, testAuthMiddleware(kpViewer))
	rec := doKP(h, "GET", "/api/v1/portal/search?q=x", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route should be absent)", rec.Code)
	}
}

func TestUserHasToolAccess(t *testing.T) {
	grants := func(tools ...string) PersonaResolver {
		return func([]string) *PersonaInfo { return &PersonaInfo{Name: "p", Tools: tools} }
	}
	tests := []struct {
		name     string
		user     *User
		resolver PersonaResolver
		want     bool
	}{
		{"nil user", nil, grants(applyKnowledgeTool), false},
		{"persona grants capability, no admin role", kpViewer, grants(applyKnowledgeTool, "search"), true},
		{"non-admin without capability denied", kpViewer, grants("search"), false},
		{"admin without capability still allowed (tool may be unregistered)", kpAdmin, grants("search"), true},
		{"no resolver falls back to admin role", kpAdmin, nil, true},
		{"no resolver, non-admin denied", kpViewer, nil, false},
		{"resolver returns nil falls back to admin", kpAdmin, func([]string) *PersonaInfo { return nil }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newSearchHandler(nil, tt.user, tt.resolver)
			if got := h.userHasApplyKnowledge(tt.user); got != tt.want {
				t.Errorf("userHasApplyKnowledge = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestQueryValues(t *testing.T) {
	tests := []struct {
		name string
		url  string
		key  string
		want []string
	}{
		{"absent", "/x", "sources", nil},
		{"single", "/x?sources=catalog", "sources", []string{"catalog"}},
		{"comma-separated", "/x?sources=catalog,memory", "sources", []string{"catalog", "memory"}},
		{"repeated", "/x?sources=catalog&sources=memory", "sources", []string{"catalog", "memory"}},
		{"blanks dropped", "/x?sources=,%20,catalog", "sources", []string{"catalog"}},
		{"all blank collapses to nil", "/x?sources=,%20,", "sources", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(context.Background(), "GET", tt.url, http.NoBody)
			got := queryValues(r, tt.key)
			if len(got) != len(tt.want) {
				t.Fatalf("queryValues = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("queryValues[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestKnowledgePage_CreateByCapabilityNotRole proves the REST write gate grants
// on the apply_knowledge capability, not solely on admin role: a non-admin
// persona that is granted apply_knowledge may write, while a non-admin persona
// without it may not. (Admins are additionally allowed, since the tool may be
// unregistered on a deployment; see userHasApplyKnowledge.)
func TestKnowledgePage_CreateByCapabilityNotRole(t *testing.T) {
	t.Run("capability grants non-admin", func(t *testing.T) {
		store := &mockKnowledgePageStore{page: &knowledgepage.Page{ID: "kp1", Title: "X"}}
		deps := Deps{
			KnowledgePageStore: store,
			AdminRoles:         []string{"admin"},
			PersonaResolver:    func([]string) *PersonaInfo { return &PersonaInfo{Name: "curator", Tools: []string{applyKnowledgeTool}} },
			RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		}
		h := NewHandler(deps, testAuthMiddleware(kpViewer))
		rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"X","body":"y"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("capability create = %d, want 201 (body %s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("non-admin without capability is denied", func(t *testing.T) {
		store := &mockKnowledgePageStore{}
		deps := Deps{
			KnowledgePageStore: store,
			AdminRoles:         []string{"admin"},
			PersonaResolver:    func([]string) *PersonaInfo { return &PersonaInfo{Name: "readonly", Tools: []string{"search"}} },
			RateLimit:          RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		}
		h := NewHandler(deps, testAuthMiddleware(kpViewer))
		rec := doKP(h, "POST", "/api/v1/portal/knowledge-pages", `{"title":"X","body":"y"}`)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin-without-capability create = %d, want 403", rec.Code)
		}
		if store.inserted != nil {
			t.Error("store must not be written without apply_knowledge capability")
		}
	})
}
