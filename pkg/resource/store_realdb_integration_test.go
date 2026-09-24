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
	"github.com/txn2/mcp-data-platform/internal/thumbtypes"
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

// thumbnailTestRenderer is the renderer generation these tests claim for.
const thumbnailTestRenderer = 1

// A resource's tile against a real PostgreSQL (#1554, #1787). The claim is
// nullable timestamp comparisons and an ILIKE over a bound array under a
// locking subquery; sqlmock returns whatever rows a test supplies and would
// agree with any of it.
func TestResourceStore_Thumbnails_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	work, ok := store.(ThumbnailWork)
	require.True(t, ok, "the PostgreSQL store does not implement ThumbnailWork")
	ctx := context.Background()

	insert := func(id, mime string, size int64) {
		require.NoError(t, store.Insert(ctx, Resource{
			ID: id, Scope: ScopeGlobal, Path: "visual", Filename: id,
			DisplayName: id, MIMEType: mime, SizeBytes: size,
			S3Key: "resources/" + id + "/" + id, URI: "mcp://global/visual/" + id,
		}))
	}
	insert("res_t_md", "text/markdown", 100)
	insert("res_t_svg", "image/svg+xml", 100)
	insert("res_t_png", "image/png", 100)
	// The two families this store had lost against the other copies of the rule
	// (#1568): the capturer renders JSX and has prose CSS that draws plain text,
	// and neither was ever offered the work.
	insert("res_t_jsx", "text/jsx", 100)
	insert("res_t_txt", "text/plain; charset=utf-8", 100)
	// A PDF is drawn: the tile page rasterizes page one itself (#1794).
	insert("res_t_pdf", "application/pdf", 100)
	// The source bound is per family. A PDF is held to LargeSourceLimit and
	// every other family to DefaultSourceLimit, so a document that is past the
	// default and would be refused as markdown is drawn as a PDF -- which is
	// the whole point, a single 300dpi scanned page already being past it.
	insert("res_t_pdf_mid", "application/pdf", thumbtypes.DefaultSourceLimit+1)
	insert("res_t_pdf_big", "application/pdf", thumbtypes.LargeSourceLimit+1)
	// A type nothing can rasterize, and one past its own bound: neither is ever
	// offered, so neither can crowd out the ones that would succeed.
	insert("res_t_zip", "application/zip", 100)
	insert("res_t_big", "text/markdown", thumbtypes.DefaultSourceLimit+1)

	// Claims what the renderer is owed, then releases the leases, so the
	// criterion can ask more than once.
	pendingIDs := func() map[string]bool {
		out, err := work.ClaimThumbnailWork(ctx, thumbnailTestRenderer, time.Minute, 100)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `UPDATE resources SET thumbnail_claimed_until = NULL`)
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
	assert.True(t, ids["res_t_pdf"], "a PDF is drawn as its first page, so it is offered")
	assert.True(t, ids["res_t_pdf_mid"],
		"a PDF past the default bound is still within its own, which is why the bound was raised")
	assert.False(t, ids["res_t_pdf_big"], "past the PDF bound the renderer is never handed the file")
	assert.False(t, ids["res_t_zip"], "nothing rasterizes an archive, so it is never offered")
	assert.False(t, ids["res_t_big"],
		"past the default bound the renderer is never handed a file of any other family")

	// Capturing the light variant is not enough for a themeable type: markdown
	// renders on a forced background and needs the dark pass too.
	md, err := store.Get(ctx, "res_t_md")
	require.NoError(t, err)
	require.NoError(t, store.SetThumbnail(ctx, "res_t_md", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/light.png", CapturedAt: md.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	assert.True(t, pendingIDs()["res_t_md"], "still pending on its dark variant")

	require.NoError(t, store.SetThumbnail(ctx, "res_t_md", ThumbnailCapture{
		Variant: ThumbnailVariantDark, S3Key: "k/dark.png", CapturedAt: md.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	assert.False(t, pendingIDs()["res_t_md"], "both variants captured and current")

	// An image carries its own colours: one capture settles it.
	png, err := store.Get(ctx, "res_t_png")
	require.NoError(t, err)
	require.NoError(t, store.SetThumbnail(ctx, "res_t_png", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/png.png", CapturedAt: png.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	assert.False(t, pendingIDs()["res_t_png"], "one capture is enough for a type with its own colours")

	// image/svg+xml contains "xml"; it is an SVG, with its own colors, and one
	// capture settles it rather than owing a dark tile nothing draws.
	svg, err := store.Get(ctx, "res_t_svg")
	require.NoError(t, err)
	require.NoError(t, store.SetThumbnail(ctx, "res_t_svg", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/svg.png", CapturedAt: svg.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	assert.False(t, pendingIDs()["res_t_svg"], "an SVG is not owed a dark tile")

	// Plain text is drawn on a forced background, so like markdown it is asked
	// for both variants rather than settled by the light one.
	txt, err := store.Get(ctx, "res_t_txt")
	require.NoError(t, err)
	require.NoError(t, store.SetThumbnail(ctx, "res_t_txt", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/txt.png", CapturedAt: txt.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	assert.True(t, pendingIDs()["res_t_txt"], "still pending on its dark variant")
	require.NoError(t, store.SetThumbnail(ctx, "res_t_txt", ThumbnailCapture{
		Variant: ThumbnailVariantDark, S3Key: "k/txt_dark.png", CapturedAt: txt.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	assert.False(t, pendingIDs()["res_t_txt"], "both variants captured and current")

	// JSX answers the scheme the renderer emulates (#1789), so a light tile
	// alone leaves it owed the dark one.
	jsx, err := store.Get(ctx, "res_t_jsx")
	require.NoError(t, err)
	require.NoError(t, store.SetThumbnail(ctx, "res_t_jsx", ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/jsx.png", CapturedAt: jsx.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	assert.True(t, pendingIDs()["res_t_jsx"], "still pending on its dark variant")

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

// TestResourceStore_ThumbnailClaim_RealDB pins what the renderer's claim adds
// to the owed predicate (#1787): an older renderer is owed again, a failure
// holds until the file changes, a lease is exclusive until it lapses, and a
// drawn tile clears both the failure and the lease.
func TestResourceStore_ThumbnailClaim_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	work := store.(ThumbnailWork)
	ctx := context.Background()
	insert := func(id string) *Resource {
		require.NoError(t, store.Insert(ctx, Resource{
			// A raster image stores one tile, so a light capture alone is a
			// current one and this criterion is about the renderer, the lease
			// and the failure only.
			ID: id, Scope: ScopeGlobal, Path: "visual", Filename: id, DisplayName: id,
			MIMEType: "image/png", SizeBytes: 100, S3Key: "resources/" + id + "/" + id,
			URI: "mcp://global/visual/" + id,
		}))
		r, err := store.Get(ctx, id)
		require.NoError(t, err)
		return r
	}
	claim := func() []string {
		out, err := work.ClaimThumbnailWork(ctx, thumbnailTestRenderer, time.Minute, 100)
		require.NoError(t, err)
		ids := make([]string, 0, len(out))
		for _, r := range out {
			ids = append(ids, r.ID)
		}
		return ids
	}
	release := func() {
		_, err := db.ExecContext(ctx, `UPDATE resources SET thumbnail_claimed_until = NULL`)
		require.NoError(t, err)
	}

	old := insert("res_c_old")
	require.NoError(t, work.SetThumbnail(ctx, old.ID, ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/old.png", CapturedAt: old.UpdatedAt, Renderer: 0,
	}))
	current := insert("res_c_current")
	require.NoError(t, work.SetThumbnail(ctx, current.ID, ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/cur.png", CapturedAt: current.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	failed := insert("res_c_failed")
	require.NoError(t, work.RecordThumbnailFailure(ctx, failed.ID, "the document did not finish drawing", failed.UpdatedAt))

	first := claim()
	assert.ElementsMatch(t, []string{"res_c_old"}, first,
		"an older renderer's tile is owed; a current one is not; a failure at the file as it stands is not retried")
	assert.Empty(t, claim(), "a leased row was claimed a second time")
	release()

	// The failure is readable, and a write to the file puts it back.
	got, err := store.Get(ctx, failed.ID)
	require.NoError(t, err)
	assert.Equal(t, "the document did not finish drawing", got.ThumbnailFailure)
	require.NotNil(t, got.ThumbnailFailedAt)
	name := "Renamed"
	require.NoError(t, store.Update(ctx, failed.ID, Update{DisplayName: &name}))
	assert.Contains(t, claim(), "res_c_failed", "a file that changed after its failure is tried again")
	release()

	// Drawing the tile clears the failure. The claim is the worker's to end
	// once every variant is drawn (#1868), so a drawn variant leaves it, and
	// a release with no hold and no attempts ends it.
	require.Contains(t, claim(), "res_c_failed")
	fresh, err := store.Get(ctx, failed.ID)
	require.NoError(t, err)
	require.NoError(t, work.SetThumbnail(ctx, failed.ID, ThumbnailCapture{
		Variant: ThumbnailVariantLight, S3Key: "k/drawn.png", CapturedAt: fresh.UpdatedAt, Renderer: thumbnailTestRenderer,
	}))
	drawn, err := store.Get(ctx, failed.ID)
	require.NoError(t, err)
	assert.Empty(t, drawn.ThumbnailFailure)
	assert.Nil(t, drawn.ThumbnailFailedAt)
	var leased bool
	var attempts int
	state := func() {
		t.Helper()
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COALESCE(thumbnail_claimed_until > now(), false), thumbnail_attempts FROM resources WHERE id = $1`, failed.ID).
			Scan(&leased, &attempts))
	}
	state()
	assert.True(t, leased, "a drawn variant ended a claim another variant may still be owed under")
	assert.Positive(t, attempts)
	require.NoError(t, work.HoldThumbnailWork(ctx, failed.ID, 0, 0))
	state()
	assert.False(t, leased)
	assert.Zero(t, attempts)

	assert.Error(t, work.RecordThumbnailFailure(ctx, "res_c_missing", "x", time.Now()))
}

// TestResourceStore_ThumbnailAttempts_RealDB holds #1868 for resources: the
// claim charges an attempt and returns the count, a hold keeps the file out
// with its count until it passes, a clear starts the count over without
// ending a lease, a recorded failure clears it, and new content starts over.
func TestResourceStore_ThumbnailAttempts_RealDB(t *testing.T) {
	db := testdb.New(t)
	store := NewPostgresStore(db)
	work := store.(ThumbnailWork)
	ctx := context.Background()
	require.NoError(t, store.Insert(ctx, Resource{
		ID: "res_gone", Scope: ScopeGlobal, Path: "visual", Filename: "gone.png", DisplayName: "gone",
		MIMEType: "image/png", SizeBytes: 100, S3Key: "resources/res_gone/gone.png", URI: "mcp://global/visual/gone.png",
	}))
	claim := func() []Resource {
		t.Helper()
		out, err := work.ClaimThumbnailWork(ctx, thumbnailTestRenderer, time.Minute, 100)
		require.NoError(t, err)
		return out
	}
	attempts := func() (int, bool) {
		t.Helper()
		var n int
		var leased bool
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT thumbnail_attempts, COALESCE(thumbnail_claimed_until > now(), false) FROM resources WHERE id = 'res_gone'`).
			Scan(&n, &leased))
		return n, leased
	}

	first := claim()
	require.Len(t, first, 1)
	assert.Equal(t, 1, first[0].ThumbnailAttempts)
	require.NoError(t, work.HoldThumbnailWork(ctx, "res_gone", time.Hour, 1))
	assert.Empty(t, claim(), "a held file was claimed before its hold passed")

	require.NoError(t, work.HoldThumbnailWork(ctx, "res_gone", 0, 1))
	second := claim()
	require.Len(t, second, 1)
	assert.Equal(t, 2, second[0].ThumbnailAttempts)

	require.NoError(t, store.ClearThumbnail(ctx, "res_gone", ThumbnailVariantLight))
	n, leased := attempts()
	assert.Zero(t, n)
	assert.True(t, leased, "a clear ended a worker's lease")

	require.NoError(t, work.HoldThumbnailWork(ctx, "res_gone", 0, 5))
	require.NoError(t, work.RecordThumbnailFailure(ctx, "res_gone", "the stored file could not be read", second[0].UpdatedAt))
	n, leased = attempts()
	assert.Zero(t, n)
	assert.False(t, leased)
	assert.Empty(t, claim(), "a file recorded as not drawable left the queue")
}
