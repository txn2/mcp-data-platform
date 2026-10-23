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

// An upload through the route is handed to OnUploaded with the uploader's
// claims, which is where an external person's upload is shared (#2258).
func TestOnUploadedSeesTheUpload(t *testing.T) {
	store := newMockStore()
	var got *Resource
	var who string
	h := NewHandler(Deps{
		Store: store, S3Client: newMockS3(), S3Bucket: "b", URIScheme: "mcp",
		OnUploaded: func(_ context.Context, c Claims, res *Resource) { got, who = res, c.Email },
	}, okExtractor, nil)
	req := buildMultipartRequest(t, map[string]string{"scope": "global", "path": "samples", "display_name": "Extract", "description": "d"}, []byte("a,b\n1,2\n"), "extract.csv")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || got == nil || who != testClaims().Email {
		t.Fatalf("HTTP %d, uploaded %v by %q", rec.Code, got, who)
	}
}
