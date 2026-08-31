//go:build integration

package resource

// Real-Postgres round-trip test for the resource store. resource.Insert binds
// pq.Array(r.Tags) into the `tags TEXT[] NOT NULL` column unconditionally, so a
// Resource with a nil Tags slice (the Go zero value) would bind SQL NULL and be
// rejected with error 23502 — the exact defect that shipped prompt creation
// broken. sqlmock cannot catch this; this test does.

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestResourceStore_Insert_RealDB_NilTags(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	r := Resource{
		ID:          "res_realdb_1",
		Scope:       ScopeGlobal,
		Path:        "runbooks",
		Filename:    "etl.md",
		DisplayName: "ETL Runbook",
		Description: "Round-trip test resource.",
		MIMEType:    "text/markdown",
		SizeBytes:   123,
		S3Key:       "resources/res_realdb_1/etl.md",
		URI:         "mcp://global/runbooks/etl.md",
		UploaderSub: "sub-1",
		// Tags intentionally nil — pq.Array(nil) would bind NULL into tags TEXT[] NOT NULL.
	}
	require.NoError(t, store.Insert(ctx, r), "insert resource with nil tags")

	got, err := store.Get(ctx, "res_realdb_1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "res_realdb_1", got.ID)
	assert.Equal(t, ScopeGlobal, got.Scope)
	assert.NotNil(t, got.Tags)
	assert.Empty(t, got.Tags)
}

func TestResourceStore_Insert_RealDB_WithTags(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	r := Resource{
		ID: "res_realdb_2", Scope: ScopeGlobal, Path: "runbooks",
		Filename: "f.md", DisplayName: "F", Description: "d", MIMEType: "text/markdown",
		SizeBytes: 1, S3Key: "k", URI: "mcp://global/runbooks/f.md", UploaderSub: "sub-2",
		Tags: []string{"a", "b"},
	}
	require.NoError(t, store.Insert(ctx, r))
	got, err := store.Get(ctx, "res_realdb_2")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.ElementsMatch(t, []string{"a", "b"}, got.Tags)
}

// The facets a library's controls are drawn from, against a real PostgreSQL
// (#1555). The rollups are a lateral expansion and an array unnest, so what
// matters is not only that they parse but that the numbers come back right:
// sqlmock returns whatever rows a test supplies and would agree with any
// arithmetic at all.
func TestResourceStore_Facets_RealDB(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()

	filed := []struct {
		id, path string
		tags     []string
	}{
		{"res_f_1", "data", []string{"finance"}},
		{"res_f_2", "data/media-manager", []string{"finance", "q3"}},
		{"res_f_3", "data/media-manager/shows", nil},
		{"res_f_4", "other", []string{"q3"}},
	}
	for _, f := range filed {
		require.NoError(t, store.Insert(ctx, Resource{
			ID: f.id, Scope: ScopeGlobal, Path: f.path,
			Filename: f.id + ".md", DisplayName: f.id, MIMEType: "text/markdown",
			S3Key: "k/" + f.id, URI: "mcp://global/" + f.path + "/" + f.id + ".md",
			Tags: f.tags,
		}))
	}

	global := Filter{Scopes: []ScopeFilter{{Scope: ScopeGlobal}}}

	folders, err := store.Folders(ctx, global)
	require.NoError(t, err)
	counts := map[string]int{}
	for _, f := range folders {
		counts[f.Path] = f.Count
	}
	// Every folder in the chain, counting everything beneath it at every depth.
	assert.Equal(t, 3, counts["data"], "data holds itself and the two below it")
	assert.Equal(t, 2, counts["data/media-manager"])
	assert.Equal(t, 1, counts["data/media-manager/shows"])
	assert.Equal(t, 1, counts["other"])
	assert.NotContains(t, counts, "", "a path segment is never the empty folder")

	tags, err := store.Tags(ctx, global)
	require.NoError(t, err)
	assert.Equal(t, []string{"finance", "q3"}, tags, "each tag once, in order")

	// A library the caller cannot read has neither.
	elsewhere := Filter{Scopes: []ScopeFilter{{Scope: ScopePersona, ScopeID: "nobody"}}}
	folders, err = store.Folders(ctx, elsewhere)
	require.NoError(t, err)
	assert.Empty(t, folders)
	tags, err = store.Tags(ctx, elsewhere)
	require.NoError(t, err)
	assert.Empty(t, tags)
}

// A resource's captured thumbnail against a real PostgreSQL (#1554). The
// pending predicate is two nullable timestamp comparisons and an ILIKE over a
// bound array; sqlmock returns whatever rows a test supplies and would agree
// with any of it.
func TestResourceStore_Thumbnails_RealDB(t *testing.T) {
	store := NewPostgresStore(testdb.New(t))
	ctx := context.Background()
	global := Filter{Scopes: []ScopeFilter{{Scope: ScopeGlobal}}}

	insert := func(id, mime string, size int64) {
		require.NoError(t, store.Insert(ctx, Resource{
			ID: id, Scope: ScopeGlobal, Path: "visual", Filename: id,
			DisplayName: id, MIMEType: mime, SizeBytes: size,
			S3Key: "resources/" + id + "/" + id, URI: "mcp://global/visual/" + id,
		}))
	}
	insert("res_t_md", "text/markdown", 100)
	insert("res_t_png", "image/png", 100)
	// The two families this store had lost against the other copies of the rule
	// (#1568): the capturer renders JSX and has prose CSS that draws plain text,
	// and neither was ever offered the work.
	insert("res_t_jsx", "text/jsx", 100)
	insert("res_t_txt", "text/plain; charset=utf-8", 100)
	// A type nothing can rasterize, and one past the source cap: neither is
	// ever offered, so neither can crowd out the ones that would succeed.
	insert("res_t_pdf", "application/pdf", 100)
	insert("res_t_big", "text/markdown", MaxThumbnailSourceBytes+1)

	pendingIDs := func() map[string]bool {
		out, err := store.PendingThumbnails(ctx, global, 100)
		require.NoError(t, err)
		ids := map[string]bool{}
		for _, r := range out {
			ids[r.ID] = true
		}
		return ids
	}

	ids := pendingIDs()
	assert.True(t, ids["res_t_md"], "a markdown resource with no capture is pending")
	assert.True(t, ids["res_t_png"], "an image is captured too: the tile used to be the file")
	assert.True(t, ids["res_t_jsx"], "the capturer renders JSX, so a JSX resource is offered")
	assert.True(t, ids["res_t_txt"], "plain text is drawn with the capturer's prose CSS")
	assert.False(t, ids["res_t_pdf"], "nothing can rasterize a PDF, so it is never offered")
	assert.False(t, ids["res_t_big"], "past the source cap the capture would stall the tab doing it")

	// Capturing the light variant is not enough for a themeable type: markdown
	// renders on a forced background and needs the dark pass too.
	md, err := store.Get(ctx, "res_t_md")
	require.NoError(t, err)
	require.NoError(t, store.SetThumbnail(ctx, "res_t_md", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/light.png", CapturedAt: md.UpdatedAt,
	}))
	assert.True(t, pendingIDs()["res_t_md"], "still pending on its dark variant")

	require.NoError(t, store.SetThumbnail(ctx, "res_t_md", ThumbnailCapture{
		Variant: ThumbnailVariantDark, S3Key: "k/dark.png", CapturedAt: md.UpdatedAt,
	}))
	assert.False(t, pendingIDs()["res_t_md"], "both variants captured and current")

	// An image carries its own colours: one capture settles it.
	png, err := store.Get(ctx, "res_t_png")
	require.NoError(t, err)
	require.NoError(t, store.SetThumbnail(ctx, "res_t_png", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/png.png", CapturedAt: png.UpdatedAt,
	}))
	assert.False(t, pendingIDs()["res_t_png"], "one capture is enough for a type with its own colours")

	// Plain text is drawn on a forced background, so like markdown it is asked
	// for both variants rather than settled by the light one.
	txt, err := store.Get(ctx, "res_t_txt")
	require.NoError(t, err)
	require.NoError(t, store.SetThumbnail(ctx, "res_t_txt", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/txt.png", CapturedAt: txt.UpdatedAt,
	}))
	assert.True(t, pendingIDs()["res_t_txt"], "still pending on its dark variant")
	require.NoError(t, store.SetThumbnail(ctx, "res_t_txt", ThumbnailCapture{
		Variant: ThumbnailVariantDark, S3Key: "k/txt_dark.png", CapturedAt: txt.UpdatedAt,
	}))
	assert.False(t, pendingIDs()["res_t_txt"], "both variants captured and current")

	// The capture round-trips onto the row.
	got, err := store.Get(ctx, "res_t_md")
	require.NoError(t, err)
	assert.Equal(t, "k/light.png", got.ThumbnailS3Key)
	assert.Equal(t, "k/dark.png", got.ThumbnailDarkS3Key)
	require.NotNil(t, got.ThumbnailCapturedAt)

	// Content that moves on puts it back: a capture older than the row it came
	// from is behind the file it shows.
	name := "Renamed"
	require.NoError(t, store.Update(ctx, "res_t_md", Update{DisplayName: &name}))
	assert.True(t, pendingIDs()["res_t_md"], "a write after the capture makes it pending again")

	// Clearing is the way back from a tile that is wrong.
	require.NoError(t, store.ClearThumbnail(ctx, "res_t_png", ThumbnailVariantLight))
	cleared, err := store.Get(ctx, "res_t_png")
	require.NoError(t, err)
	assert.Empty(t, cleared.ThumbnailS3Key)
	assert.Nil(t, cleared.ThumbnailCapturedAt)
	assert.True(t, pendingIDs()["res_t_png"], "a cleared tile is offered again")

	// A resource that is not there is an error rather than a silent no-op.
	assert.Error(t, store.SetThumbnail(ctx, "res_t_missing", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k", CapturedAt: time.Now(),
	}))
	assert.Error(t, store.ClearThumbnail(ctx, "res_t_missing", ThumbnailVariantLight))
}
