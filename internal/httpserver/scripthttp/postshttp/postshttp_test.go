package postshttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpserver/scripthttp"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type fakePosts struct {
	posts []notification.RunPost
	err   error
	asked string
}

func (f *fakePosts) RunPosts(_ context.Context, runID string) ([]notification.RunPost, error) {
	f.asked = runID
	return f.posts, f.err
}

func serve(h *Handler) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	h.Register(mux, func(next http.Handler) http.Handler { return next })
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/portal/scripts/s1/runs/r1/posts", http.NoBody))
	return w
}

func readable(_ http.ResponseWriter, _ *http.Request) (*script.Run, bool) {
	return &script.Run{ID: "r1"}, true
}

func TestList_EachPostWithWhatBecameOfIt(t *testing.T) {
	sent := time.Date(2026, 10, 10, 9, 0, 5, 0, time.UTC)
	posts := &fakePosts{posts: []notification.RunPost{
		{Channel: "ops-alerts", Title: "Feed stale", Deliveries: 1, Sent: 1, LastSentAt: &sent},
		{Channel: "ops-email", Title: "Weekly", Deliveries: 3, Sent: 1},
		{Channel: "hook", Title: "Event", Deliveries: 1, Failed: 1, LastError: "upstream answered HTTP 410 Gone"},
	}}
	w := serve(New(Deps{Run: readable, Posts: posts}))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var got postsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if posts.asked != "r1" {
		t.Errorf("read posts for %q", posts.asked)
	}
	want := []string{"sent", "queued", "failed"}
	for i, p := range got.Posts {
		if p.Status != want[i] {
			t.Errorf("post %d (%s) status = %q, want %q", i, p.Channel, p.Status, want[i])
		}
	}
	if got.Posts[0].SentAt == nil || got.Posts[2].LastError == "" {
		t.Errorf("posts = %+v", got.Posts)
	}
}

func TestList_NoPostsIsAnEmptyList(t *testing.T) {
	w := serve(New(Deps{Run: readable, Posts: &fakePosts{posts: []notification.RunPost{}}}))
	if w.Body.String() != "{\"posts\":[]}\n" {
		t.Errorf("body = %q", w.Body.String())
	}
}

func TestList_RefusalsAndFailures(t *testing.T) {
	refused := func(w http.ResponseWriter, _ *http.Request) (*script.Run, bool) {
		w.WriteHeader(http.StatusNotFound)
		return nil, false
	}
	if w := serve(New(Deps{Run: refused, Posts: &fakePosts{}})); w.Code != http.StatusNotFound {
		t.Errorf("a run the caller cannot read: %d", w.Code)
	}
	if w := serve(New(Deps{Run: readable, Posts: &fakePosts{err: errors.New("db down")}})); w.Code != http.StatusInternalServerError {
		t.Errorf("a store failure: %d", w.Code)
	}
}

func TestForPortal_MountsNothingWithoutItsDependencies(t *testing.T) {
	if ForPortal(nil, scripthttp.Deps{}, &fakePosts{}) != nil {
		t.Error("built with no run store")
	}
	var h *Handler
	mux := http.NewServeMux()
	h.Register(mux, func(next http.Handler) http.Handler { return next })
}
