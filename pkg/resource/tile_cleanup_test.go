package resource

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// drawTilesBeside stores both tile variants beside a blob, as the thumbnail
// worker does after drawing it.
func drawTilesBeside(s3 *mockS3, key string) {
	for _, tile := range tileKeysBeside(key) {
		s3.objects[tile] = []byte("png")
	}
}

func revise(t *testing.T, h *Handler, content string) {
	t.Helper()
	req := buildMultipartRequest(t, nil, []byte(content), "f.csv")
	req.URL.Path = "/api/v1/resources/res-1/content"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("revision: status = %d: %s", w.Code, w.Body.String())
	}
}

// TestDelete_RemovesTilesOfEveryRevision covers #1903: a resource revised
// after each draw has tiles beside every version's key, and deleting it leaves
// nothing under its key directory.
func TestDelete_RemovesTilesOfEveryRevision(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
	seedVersionedResource(t, store, s3, versions)
	drawTilesBeside(s3, store.resources["res-1"].S3Key)
	for i := range 2 {
		revise(t, h, fmt.Sprintf("rev-%d", i))
		latest := store.resources["res-1"].S3Key
		drawTilesBeside(s3, latest)
		store.resources["res-1"].ThumbnailS3Key = ThumbnailKeyFor(latest, ThumbnailVariantLight)
		store.resources["res-1"].ThumbnailDarkS3Key = ThumbnailKeyFor(latest, ThumbnailVariantDark)
	}
	if len(s3.objects) != 9 {
		t.Fatalf("seeded objects = %d, want 3 blobs and 6 tiles", len(s3.objects))
	}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodDelete, "/api/v1/resources/res-1", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", w.Code, w.Body.String())
	}
	if len(s3.objects) != 0 {
		t.Errorf("objects left in storage = %v, want none: a deleted resource's tiles must go with it", keysOf(s3.objects))
	}
}

// TestPrune_RemovesThePrunedVersionsTiles covers #1903: pruning a version
// deletes the tiles drawn beside it, except a tile the row still names, which
// is served until the new head is drawn.
func TestPrune_RemovesThePrunedVersionsTiles(t *testing.T) {
	fx := newVersionedHandler(t, okExtractor)
	h, store, s3, versions := fx.handler, fx.store, fx.s3, fx.versions
	seedVersionedResource(t, store, s3, versions)
	h.deps.MaxVersions = 2

	v1 := store.resources["res-1"].S3Key
	drawTilesBeside(s3, v1)
	// The row still names version 1's light tile: nothing has been drawn since.
	named := ThumbnailKeyFor(v1, ThumbnailVariantLight)
	store.resources["res-1"].ThumbnailS3Key = named

	revise(t, h, "rev-0")
	v2 := store.resources["res-1"].S3Key
	drawTilesBeside(s3, v2)
	revise(t, h, "rev-1") // prunes version 1

	if _, ok := s3.objects[named]; !ok {
		t.Error("the tile the row names was deleted before the new head was drawn")
	}
	if _, ok := s3.objects[ThumbnailKeyFor(v1, ThumbnailVariantDark)]; ok {
		t.Error("the pruned version's dark tile, which the row does not name, was left behind")
	}
	for _, tile := range tileKeysBeside(v2) {
		if _, ok := s3.objects[tile]; !ok {
			t.Errorf("tile %s of a kept version was deleted", tile)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
