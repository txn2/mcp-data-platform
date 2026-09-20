package resource

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A resource's thumbnail (#1554). Before it the library drew the original file
// scaled down, so a non-image had no tile and an image cost its full size. The
// platform draws the tile now (#1787); the two routes below read and clear it.

// errThumbnailWrite stands for a store that could not record a capture.
var errThumbnailWrite = errors.New("write failed")

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
	if got := ThumbnailKeyFor("resources/r1/report.csv", ThumbnailVariantLight); got != "resources/r1/.thumbnail.png" {
		t.Errorf("light key = %q", got)
	}
	if got := ThumbnailKeyFor("resources/r1/report.csv", ThumbnailVariantDark); got != "resources/r1/.thumbnail_dark.png" {
		t.Errorf("dark key = %q", got)
	}
	// A key with no prefix still names a file rather than an empty path.
	if got := ThumbnailKeyFor("report.csv", ThumbnailVariantLight); got != ".thumbnail.png" {
		t.Errorf("bare key = %q", got)
	}
}

func TestStoredThumbnailKeyFallsBackToLight(t *testing.T) {
	// A type carrying its own colors stores one image and serves it in both
	// modes, so an empty dark key means "use the light one".
	r := &Resource{ThumbnailS3Key: "light.png"}
	if got := StoredThumbnailKey(r, ThumbnailVariantDark); got != "light.png" {
		t.Errorf("dark fell back to %q, want the light capture", got)
	}
	r.ThumbnailDarkS3Key = "dark.png"
	if got := StoredThumbnailKey(r, ThumbnailVariantDark); got != "dark.png" {
		t.Errorf("dark = %q", got)
	}
	if got := StoredThumbnailKey(&Resource{}, ThumbnailVariantLight); got != "" {
		t.Errorf("uncaptured = %q, want empty", got)
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

// A clear takes both variants, whatever the reader's own color mode is.
//
// They are two views of one file, and asking for the tile to be drawn again
// means the tile (#1568). Clearing the light one alone would be enough to have
// the renderer draw the resource again, but it would leave a themeable file
// serving the stale dark tile until the replacement landed -- which is the
// wrong picture that was being complained about.
func TestClearThumbnailTakesBothVariants(t *testing.T) {
	now := time.Now().UTC()
	store := newMockStore()
	r := captured(store, "res-1", now)
	r.MIMEType = "text/markdown"
	r.ThumbnailDarkS3Key = "resources/res-1/.thumbnail_dark.png"
	r.ThumbnailDarkCapturedAt = &now
	h := newTestHandler(store, newMockS3(), okExtractor)

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/resources/res-1/thumbnail", http.NoBody)
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
	stored := store.resources["res-1"]
	if stored.ThumbnailS3Key != "" {
		t.Errorf("light capture survived the clear: %q", stored.ThumbnailS3Key)
	}
	if stored.ThumbnailDarkS3Key != "" {
		t.Errorf("dark capture survived the clear: %q", stored.ThumbnailDarkS3Key)
	}
}

// Every thumbnail route refuses a caller with no identity, and answers 404 for
// a resource that is not there, before it touches storage.
func TestThumbnailRoutes_RefuseTheAnonymousAndTheAbsent(t *testing.T) {
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/resources/res-1/thumbnail"},
		{http.MethodDelete, "/api/v1/resources/res-1/thumbnail"},
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
	for _, rt := range routes {
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

// Without storage there is nowhere to read a tile from, and saying so is
// better than answering as if the tile did not exist.
func TestThumbnailRoutes_ReportMissingStorage(t *testing.T) {
	store := newMockStore()
	captured(store, "res-1", time.Now().UTC())
	h := NewHandler(Deps{Store: store, URIScheme: "mcp"}, okExtractor, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/api/v1/resources/res-1/thumbnail", http.NoBody))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("read without storage: expected 503, got %d", rec.Code)
	}
}

// A clear that fails is reported: a caller told the tile will be drawn again,
// on a row that still holds the old one, would wait for a tile that never
// comes.
func TestThumbnailRoutes_ReportAFailedWrite(t *testing.T) {
	now := time.Now().UTC()

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
}

type failingClearThumbnail struct{ *mockStore }

func (failingClearThumbnail) ClearThumbnail(_ context.Context, _, _ string) error {
	return errThumbnailWrite
}
