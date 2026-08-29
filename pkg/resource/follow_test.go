package resource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// A revision moves the resource's head, so the tables registered over the
// file follow it (#1536). Both revision routes -- a replacement and a restore
// -- hand the version they recorded to the hook and carry its answer beside
// the resource in the response.

type followRecorder struct {
	asked  []string
	answer []string
}

func (f *followRecorder) hook(_ context.Context, id string, version int) []string {
	f.asked = append(f.asked, id+"@"+strconv.Itoa(version))
	return f.answer
}

func TestReplaceContent_FollowsTheTablesOverTheFile(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	follow := &followRecorder{answer: []string{"scratch.uploads.t on scratch now reads version 1."}}
	fx.handler.deps.OnRevised = follow.hook
	seedResource(fx.store, fx.s3, "res-1", ScopeGlobal, "", "user-123")

	req := buildMultipartRequest(t, nil, []byte("revised,content\n1,2\n"), "x.csv")
	req.URL.Path = "/api/v1/resources/res-1/content"
	w := httptest.NewRecorder()
	fx.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	body := decodeJSON(t, w.Body)
	if body["id"] != "res-1" {
		t.Errorf("the response is still the resource: id = %v", body["id"])
	}
	tables, _ := body["tables"].([]any)
	if len(tables) != 1 || tables[0] != "scratch.uploads.t on scratch now reads version 1." {
		t.Errorf("tables = %v, want the hook's report beside the resource", body["tables"])
	}
	if len(follow.asked) != 1 || follow.asked[0] != "res-1@1" {
		t.Errorf("hook asked about %v, want res-1 at the version the revision recorded", follow.asked)
	}
}

func TestRestoreVersion_FollowsTheTablesOverTheFile(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	follow := &followRecorder{}
	fx.handler.deps.OnRevised = follow.hook
	seedVersionedResource(t, fx.store, fx.s3, fx.versions)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/api/v1/resources/"+seedResourceID+"/versions/1/restore", http.NoBody)
	w := httptest.NewRecorder()
	fx.handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if len(follow.asked) != 1 || follow.asked[0] != seedResourceID+"@2" {
		t.Errorf("hook asked about %v, want the restore's own version", follow.asked)
	}
	if _, present := decodeJSON(t, w.Body)["tables"]; present {
		t.Error("an empty report is absent from the response, not an empty list")
	}
}
