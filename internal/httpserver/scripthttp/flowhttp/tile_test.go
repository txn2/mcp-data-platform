package flowhttp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scripttiles"
)

type fakeTiles struct {
	err     error
	variant string
	older   bool
}

func (f *fakeTiles) Tile(_ context.Context, _, variant string) (data []byte, current bool, err error) {
	f.variant = variant
	if f.err != nil {
		return nil, false, f.err
	}
	return []byte("\x89PNG"), !f.older, nil
}

func TestScriptTile(t *testing.T) {
	tiles := &fakeTiles{}
	deps := Deps{Load: version("x = 1\n"), SignedIn: signedIn(true), Tiles: tiles}
	rec := get(t, deps, "/api/v1/portal/scripts/s1/thumbnail")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	assert.Equal(t, "private, max-age=3600", rec.Header().Get("Cache-Control"))
	assert.Equal(t, scripttiles.VariantLight, tiles.variant)

	// A version saved since the tile was drawn: the older tile is answered
	// under the new version's address and revalidated, not kept for an hour.
	tiles.older = true
	rec = get(t, deps, "/api/v1/portal/scripts/s1/thumbnail?v=4")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Header().Get("Cache-Control"), "max-age=3600")
	assert.Contains(t, rec.Header().Get("Cache-Control"), "no-cache")
	assert.NotEmpty(t, rec.Header().Get("ETag"))
	tiles.older = false

	get(t, deps, "/api/v1/portal/scripts/s1/thumbnail?variant=dark")
	assert.Equal(t, scripttiles.VariantDark, tiles.variant)
	get(t, deps, "/api/v1/portal/scripts/s1/thumbnail?variant=other")
	assert.Equal(t, scripttiles.VariantLight, tiles.variant, "an unknown variant is the light one")

	tiles.err = scripttiles.ErrNoTile
	assert.Equal(t, http.StatusNotFound, get(t, deps, "/api/v1/portal/scripts/s1/thumbnail").Code)
	tiles.err = errors.New("boom")
	assert.Equal(t, http.StatusInternalServerError, get(t, deps, "/api/v1/portal/scripts/s1/thumbnail").Code)

	deps.SignedIn = func(*http.Request) bool { return false }
	assert.Equal(t, http.StatusUnauthorized, get(t, deps, "/api/v1/portal/scripts/s1/thumbnail").Code)
	deps.Tiles = nil
	assert.Equal(t, http.StatusNotFound, get(t, deps, "/api/v1/portal/scripts/s1/thumbnail").Code, "no reader, no route")
}
