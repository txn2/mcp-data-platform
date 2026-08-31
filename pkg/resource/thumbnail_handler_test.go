package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A resource's thumbnail (#1554). Before it the library drew the original file
// scaled down, so a non-image had no tile and an image cost its full size; the
// four routes below are what replaced that.

// errThumbnailWrite stands for a store that could not record a capture.
var errThumbnailWrite = errors.New("write failed")

// pngBody is the PUT that carries a capture. Only an upload sends a body, so
// the method is not a parameter.
func pngBody(t *testing.T, path string, data []byte) *http.Request {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPut, path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "image/png")
	return req
}

// captured seeds a resource that already carries a capture.
func captured(store *mockStore, id string, at time.Time) *Resource {
	r := &Resource{
		ID: id, Scope: ScopeGlobal, Path: "visual", Filename: id + ".png",
		DisplayName: id, MIMEType: "image/png", SizeBytes: 100,
		S3Key: "resources/" + id + "/" + id + ".png", UpdatedAt: at,
		ThumbnailS3Key: "resources/" + id + "/.thumbnail.png", ThumbnailCapturedAt: &at,
	}
	store.resources[id] = r
	return r
}

func TestDeriveThumbnailKey(t *testing.T) {
	// Beside the object, under a hidden name: a visible one would be read as
	// data by a query engine pointed at the same prefix.
	if got := deriveThumbnailKey("resources/r1/report.csv", ThumbnailVariantLight); got != "resources/r1/.thumbnail.png" {
		t.Errorf("light key = %q", got)
	}
	if got := deriveThumbnailKey("resources/r1/report.csv", ThumbnailVariantDark); got != "resources/r1/.thumbnail_dark.png" {
		t.Errorf("dark key = %q", got)
	}
	// A key with no prefix still names a file rather than an empty path.
	if got := deriveThumbnailKey("report.csv", ThumbnailVariantLight); got != ".thumbnail.png" {
		t.Errorf("bare key = %q", got)
	}
}

func TestStoredThumbnailKeyFallsBackToLight(t *testing.T) {
	// A type carrying its own colors stores one image and serves it in both
	// modes, so an empty dark key means "use the light one".
	r := &Resource{ThumbnailS3Key: "light.png"}
	if got := storedThumbnailKey(r, ThumbnailVariantDark); got != "light.png" {
		t.Errorf("dark fell back to %q, want the light capture", got)
	}
	r.ThumbnailDarkS3Key = "dark.png"
	if got := storedThumbnailKey(r, ThumbnailVariantDark); got != "dark.png" {
		t.Errorf("dark = %q", got)
	}
	if got := storedThumbnailKey(&Resource{}, ThumbnailVariantLight); got != "" {
		t.Errorf("uncaptured = %q, want empty", got)
	}
}

func TestHandleUploadThumbnail(t *testing.T) {
	now := time.Now().UTC()
	store := newMockStore()
	r := captured(store, "res-1", now)
	r.ThumbnailS3Key, r.ThumbnailCapturedAt = "", nil
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pngBody(t, "/api/v1/resources/res-1/thumbnail", []byte("png-bytes")))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	stored := store.resources["res-1"]
	if stored.ThumbnailS3Key != "resources/res-1/.thumbnail.png" {
		t.Errorf("key = %q", stored.ThumbnailS3Key)
	}
	// Stamped with the resource's own updated_at, not with now: a wall-clock
	// time would make the capture look newer than content written between the
	// read and this write, which is exactly the file still needing one.
	if stored.ThumbnailCapturedAt == nil || !stored.ThumbnailCapturedAt.Equal(now) {
		t.Errorf("captured at %v, want the resource's updated_at %v", stored.ThumbnailCapturedAt, now)
	}
	// And the resource's own updated_at is untouched: bumping it would mark the
	// capture that just landed as behind, queueing it forever.
	if !stored.UpdatedAt.Equal(now) {
		t.Errorf("updated_at moved to %v", stored.UpdatedAt)
	}
}

func TestHandleUploadThumbnail_Refusals(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        []byte
		extractor   ClaimsExtractor
		want        int
	}{
		{"a body that is not a PNG", "text/plain", []byte("nope"), okExtractor, http.StatusBadRequest},
		{"a capture past the size cap", "image/png", bytes.Repeat([]byte("x"), MaxThumbnailUploadBytes+1), okExtractor, http.StatusRequestEntityTooLarge},
		{"a caller who may not change the file", "image/png", []byte("png"), memberExtractor, http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMockStore()
			// A persona library the ordinary caller can see and not write.
			r := captured(store, "res-1", time.Now().UTC())
			r.Scope, r.ScopeID = ScopePersona, "analyst"
			h := newTestHandler(store, newMockS3(), tt.extractor)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodPut,
				"/api/v1/resources/res-1/thumbnail", bytes.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("expected %d, got %d: %s", tt.want, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestHandleGetThumbnail(t *testing.T) {
	now := time.Now().UTC()
	store := newMockStore()
	r := captured(store, "res-1", now)
	s3 := newMockS3()
	s3.objects[r.ThumbnailS3Key] = []byte("png-bytes")
	h := newTestHandler(store, s3, okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/thumbnail", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "image/png") {
		t.Errorf("content type = %q", ct)
	}
	// The capture is immutable and its URL carries the moment it was taken, so
	// it can be held rather than re-fetched on every render of the library.
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "max-age=3600") {
		t.Errorf("cache control = %q", cc)
	}
}

// A resource with no capture, and one whose recorded object is gone, answer the
// same way: 404 is what tells a card to draw its content-type icon.
func TestHandleGetThumbnail_MissingIsNotFound(t *testing.T) {
	for _, tt := range []struct {
		name string
		seed func(*mockStore, *mockS3)
	}{
		{"never captured", func(store *mockStore, _ *mockS3) {
			r := captured(store, "res-1", time.Now().UTC())
			r.ThumbnailS3Key, r.ThumbnailCapturedAt = "", nil
		}},
		{"recorded but the object is gone", func(store *mockStore, _ *mockS3) {
			captured(store, "res-1", time.Now().UTC())
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store, s3 := newMockStore(), newMockS3()
			tt.seed(store, s3)
			h := newTestHandler(store, s3, okExtractor)

			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/thumbnail", http.NoBody)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// Which resources exist in a library the caller cannot see is not theirs to
// learn, so a refusal is the same answer a missing resource gives.
func TestHandleGetThumbnail_UnreachableLibraryIsNotFound(t *testing.T) {
	store := newMockStore()
	r := captured(store, "res-1", time.Now().UTC())
	r.Scope, r.ScopeID = ScopePersona, "finance"
	h := newTestHandler(store, newMockS3(), memberExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/res-1/thumbnail", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleClearThumbnail(t *testing.T) {
	store := newMockStore()
	captured(store, "res-1", time.Now().UTC())
	h := newTestHandler(store, newMockS3(), okExtractor)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1/thumbnail", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	if store.resources["res-1"].ThumbnailS3Key != "" {
		t.Errorf("capture survived the clear")
	}
}

func TestHandlePendingThumbnails(t *testing.T) {
	now := time.Now().UTC()
	store := newMockStore()

	// Captured and current: nothing to do.
	captured(store, "current", now)
	// Captured before the file moved on.
	behind := captured(store, "behind", now)
	stale := now.Add(-time.Hour)
	behind.ThumbnailCapturedAt = &stale
	// Never captured.
	fresh := captured(store, "fresh", now)
	fresh.ThumbnailS3Key, fresh.ThumbnailCapturedAt = "", nil
	// A type nothing can rasterize is never offered, whatever its state.
	pdf := captured(store, "pdf", now)
	pdf.MIMEType, pdf.ThumbnailS3Key, pdf.ThumbnailCapturedAt = "application/pdf", "", nil

	h := newTestHandler(store, newMockS3(), okExtractor)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/resources/thumbnails/pending", http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	got := map[string]bool{}
	var env struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decoding the pending envelope: %v\n%s", err, rec.Body.String())
	}
	for _, r := range env.Resources {
		got[r.ID] = true
	}
	for id, want := range map[string]bool{
		"behind": true, "fresh": true, "current": false, "pdf": false,
	} {
		if got[id] != want {
			t.Errorf("pending[%s] = %v, want %v (all: %v)", id, got[id], want, got)
		}
	}
}

func TestThumbnailLimitIsClamped(t *testing.T) {
	for query, want := range map[string]int{
		"":            DefaultListLimit,
		"?limit=25":   25,
		"?limit=0":    DefaultListLimit,
		"?limit=abc":  DefaultListLimit,
		"?limit=9999": MaxListLimit,
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x"+query, http.NoBody)
		if got := thumbnailLimit(req); got != want {
			t.Errorf("limit for %q = %d, want %d", query, got, want)
		}
	}
}

func TestReadVariant(t *testing.T) {
	for query, want := range map[string]string{
		"":                  ThumbnailVariantLight,
		"?variant=dark":     ThumbnailVariantDark,
		"?variant=light":    ThumbnailVariantLight,
		"?variant=nonsense": ThumbnailVariantLight,
	} {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/x"+query, http.NoBody)
		if got := readVariant(req); got != want {
			t.Errorf("variant for %q = %q, want %q", query, got, want)
		}
	}
}

// The dark variant is stored and served under its own key, so a themeable file
// can have one current and the other behind.
func TestThumbnailDarkVariantIsItsOwnCapture(t *testing.T) {
	now := time.Now().UTC()
	store := newMockStore()
	r := captured(store, "res-1", now)
	r.MIMEType = "text/markdown"
	s3 := newMockS3()
	h := newTestHandler(store, s3, okExtractor)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pngBody(t, "/api/v1/resources/res-1/thumbnail?variant=dark", []byte("png")))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	stored := store.resources["res-1"]
	if stored.ThumbnailDarkS3Key != "resources/res-1/.thumbnail_dark.png" {
		t.Errorf("dark key = %q", stored.ThumbnailDarkS3Key)
	}
	// The light one it already had is untouched.
	if stored.ThumbnailS3Key != "resources/res-1/.thumbnail.png" {
		t.Errorf("light key changed to %q", stored.ThumbnailS3Key)
	}

	// Clearing one leaves the other.
	rec = httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/resources/res-1/thumbnail?variant=dark", http.NoBody)
	h.ServeHTTP(rec, req)
	if store.resources["res-1"].ThumbnailDarkS3Key != "" {
		t.Errorf("dark capture survived its own clear")
	}
	if store.resources["res-1"].ThumbnailS3Key == "" {
		t.Errorf("clearing dark took the light capture with it")
	}
}

// Every thumbnail route refuses a caller with no identity, and answers 404 for
// a resource that is not there, before it touches storage.
func TestThumbnailRoutes_RefuseTheAnonymousAndTheAbsent(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/resources/res-1/thumbnail"},
		{http.MethodPut, "/api/v1/resources/res-1/thumbnail"},
		{http.MethodDelete, "/api/v1/resources/res-1/thumbnail"},
		{http.MethodGet, "/api/v1/resources/thumbnails/pending"},
	}
	for _, rt := range routes {
		t.Run("unauthenticated "+rt.method+" "+rt.path, func(t *testing.T) {
			h := newTestHandler(newMockStore(), newMockS3(), failExtractor)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), rt.method, rt.path, http.NoBody))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", rec.Code)
			}
		})
	}
	for _, rt := range routes[:3] {
		t.Run("absent "+rt.method, func(t *testing.T) {
			h := newTestHandler(newMockStore(), newMockS3(), okExtractor)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), rt.method, rt.path, http.NoBody))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("expected 404, got %d", rec.Code)
			}
		})
	}
}

// Without storage there is nowhere to put a capture, and saying so is better
// than recording a row that points at nothing.
func TestThumbnailRoutes_ReportMissingStorage(t *testing.T) {
	store := newMockStore()
	captured(store, "res-1", time.Now().UTC())
	h := NewHandler(Deps{Store: store, URIScheme: "mcp"}, okExtractor, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, pngBody(t, "/api/v1/resources/res-1/thumbnail", []byte("png")))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("upload without storage: expected 503, got %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/resources/res-1/thumbnail", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("read without storage: expected 503, got %d", rec.Code)
	}
}

// A write that fails is reported. A capture the caller believes landed, on a
// row that never recorded it, is worse than a refusal: the queue would stop
// offering the resource and the tile would stay an icon forever.
func TestThumbnailRoutes_ReportAFailedWrite(t *testing.T) {
	now := time.Now().UTC()

	t.Run("recording the capture", func(t *testing.T) {
		store := newMockStore()
		captured(store, "res-1", now)
		h := NewHandler(Deps{Store: &failingSetThumbnail{store}, S3Client: newMockS3(), S3Bucket: "test-bucket", URIScheme: "mcp"}, okExtractor, nil)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, pngBody(t, "/api/v1/resources/res-1/thumbnail", []byte("png")))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("clearing the capture", func(t *testing.T) {
		store := newMockStore()
		captured(store, "res-1", now)
		h := NewHandler(Deps{Store: &failingClearThumbnail{store}, S3Client: newMockS3(), S3Bucket: "test-bucket", URIScheme: "mcp"}, okExtractor, nil)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
			"/api/v1/resources/res-1/thumbnail", http.NoBody))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("listing what is pending", func(t *testing.T) {
		h := NewHandler(Deps{Store: &failingPending{newMockStore()}, S3Client: newMockS3(), S3Bucket: "test-bucket", URIScheme: "mcp"}, okExtractor, nil)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
			"/api/v1/resources/thumbnails/pending", http.NoBody))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("expected 500, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

type failingSetThumbnail struct{ *mockStore }

func (failingSetThumbnail) SetThumbnail(_ context.Context, _ string, _ ThumbnailCapture) error {
	return errThumbnailWrite
}

type failingClearThumbnail struct{ *mockStore }

func (failingClearThumbnail) ClearThumbnail(_ context.Context, _, _ string) error {
	return errThumbnailWrite
}

type failingPending struct{ *mockStore }

func (failingPending) PendingThumbnails(_ context.Context, _ Filter, _ int) ([]Resource, error) {
	return nil, errThumbnailWrite
}
