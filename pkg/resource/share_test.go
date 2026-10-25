package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// sharedCaller is a person who sees none of the resource's folders and holds
// a share on res-1 with perm.
func sharedCaller(perm string) ClaimsExtractor {
	return func(_ *http.Request) (*Claims, error) {
		return &Claims{
			Sub: "recipient", Email: "recipient@example.com", Personas: []string{"sales"}, Roles: []string{"sales"},
			Shared: func(id string) string {
				if id == "res-1" {
					return perm
				}
				return ""
			},
		}, nil
	}
}

func doJSON(h *Handler, method, path string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	req := httptest.NewRequestWithContext(context.Background(), method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// A share reaches a resource outside every folder the recipient sees: a
// viewer reads it, an editor also renames it, and neither moves or deletes
// it (#2258).
func TestShareReachesTheResource(t *testing.T) {
	store := newMockStore()
	seedResource(store, nil, "res-1", ScopeUser, "owner", "owner")
	seedResource(store, nil, "res-2", ScopeUser, "owner", "owner")

	viewer := newTestHandler(store, nil, sharedCaller("viewer"))
	if rec := doJSON(viewer, http.MethodGet, "/api/v1/resources/res-1", nil); rec.Code != http.StatusOK {
		t.Fatalf("viewer read: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doJSON(viewer, http.MethodGet, "/api/v1/resources/res-2", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("an unshared resource: %d", rec.Code)
	}
	name := "Renamed"
	if rec := doJSON(viewer, http.MethodPatch, "/api/v1/resources/res-1", Update{DisplayName: &name}); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer rename: %d", rec.Code)
	}

	editor := newTestHandler(store, nil, sharedCaller("editor"))
	if rec := doJSON(editor, http.MethodPatch, "/api/v1/resources/res-1", Update{DisplayName: &name}); rec.Code != http.StatusOK {
		t.Fatalf("editor rename: %d %s", rec.Code, rec.Body.String())
	}
	path := "elsewhere"
	if rec := doJSON(editor, http.MethodPatch, "/api/v1/resources/res-1", Update{Path: &path}); rec.Code != http.StatusForbidden {
		t.Fatalf("editor move: %d", rec.Code)
	}
	if rec := doJSON(editor, http.MethodDelete, "/api/v1/resources/res-1", nil); rec.Code != http.StatusForbidden {
		t.Fatalf("editor delete: %d", rec.Code)
	}

	c := Claims{}
	if CanEditResource(c, &Resource{ID: "x"}) || shared(c, nil) != "" {
		t.Error("no share, no reach")
	}
}

// External files reaches an external person's folder: view opens and lists
// it, manage also writes it, and neither reaches anyone else's (#2290).
func TestExternalFiles(t *testing.T) {
	theirs := &Resource{ID: "r", Scope: ScopeUser, ScopeID: "client-sub"}
	staffs := &Resource{ID: "s", Scope: ScopeUser, ScopeID: "staff-sub"}
	isExternal := func(id string) bool { return id == "client-sub" }
	lib := ScopeFilter{Scope: ScopeUser, ScopeID: "client-sub"}

	none := Claims{Sub: "me"}
	if CanAccessResource(none, theirs) || CanSeeLibrary(none, lib) || CanWriteScope(none, ScopeUser, "client-sub") {
		t.Error("no level reaches an external folder")
	}
	view := Claims{Sub: "me", ExternalFolder: isExternal}
	if !CanAccessResource(view, theirs) || !CanSeeLibrary(view, lib) {
		t.Error("view opens and lists an external folder")
	}
	if CanWriteScope(view, ScopeUser, "client-sub") || CanModifyResource(view, theirs) {
		t.Error("view changes nothing")
	}
	if CanAccessResource(view, staffs) || CanSeeLibrary(view, ScopeFilter{Scope: ScopeUser, ScopeID: "staff-sub"}) {
		t.Error("view reaches only external people's folders")
	}
	if CanAccessResource(view, &Resource{Scope: ScopePersona, ScopeID: "client-sub"}) {
		t.Error("an external folder is a user library")
	}
	manage := Claims{Sub: "me", ExternalFolder: isExternal, ManageExternalFiles: true}
	if !CanWriteScope(manage, ScopeUser, "client-sub") || !CanModifyResource(manage, theirs) || !CanEditResource(manage, theirs) {
		t.Error("manage changes what is in an external folder")
	}
	if CanModifyResource(manage, staffs) {
		t.Error("manage reaches only external people's folders")
	}
	if CanWriteScope(Claims{Sub: "me", ManageExternalFiles: true}, ScopeUser, "client-sub") {
		t.Error("manage without the folder check reaches nothing")
	}
}
