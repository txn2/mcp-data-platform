package sitesvc

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siterender"
)

const (
	layoutPath = "_layouts/base.html"
	recordPath = "_data/products/a.json"
)

func renderedFiles() []InputFile {
	return []InputFile{
		{Path: layoutPath, Body: []byte("<b>{{ content }}</b>")},
		{Path: recordPath, Body: []byte(`{"name":"Anvil"}`)},
		{Path: "_data/products/z.json", Body: []byte(`{"name":"Zip"}`)},
		{Path: "product.html", Body: []byte("---\nlayout: base\nfor_each: products\npermalink: /p/{{ item.slug }}/\n---\n{{ item.name }}")},
		{Path: "index.html", Body: []byte("---\nlayout: base\n---\nhome")},
		{Path: "logo.png", Body: []byte{0x89, 'P', 'N', 'G'}},
		{Path: "_redirects", Body: []byte("/old /new 302\n")},
	}
}

func seededRender(t *testing.T) (*Service, *memStore, *memObjects) {
	t.Helper()
	svc, store, objects, _ := fixture(t)
	_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Files: renderedFiles()})
	require.NoError(t, err)
	return svc, store, objects
}

func paths(files []site.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func treeOfNewest(t *testing.T, store *memStore, tree string) []site.File {
	t.Helper()
	files, err := store.Tree(context.Background(), store.releases[siteID][0].ID, tree)
	require.NoError(t, err)
	return files
}

func refAt(path string) site.Ref {
	return site.Ref{Site: "marketing", Environment: site.EnvDefault, Path: path}
}

func TestPublish_ASiteWithLayoutsHoldsTwoTrees(t *testing.T) {
	_, store, objects := seededRender(t)
	rel := store.releases[siteID][0]
	assert.True(t, rel.Rendered)
	assert.Equal(t, []string{"index.html", "logo.png", "p/a/index.html", "p/z/index.html"}, paths(treeOfNewest(t, store, site.TreeOutput)))
	source := paths(treeOfNewest(t, store, site.TreeSource))
	assert.Contains(t, source, layoutPath)
	assert.Contains(t, source, recordPath)
	assert.Equal(t, []site.Redirect{{From: "/old", To: "/new", Status: 302}}, rel.Redirects)
	assert.Equal(t, 4, rel.FileCount, "a release counts what it serves")
	assert.Equal(t, Digest(treeOfNewest(t, store, site.TreeOutput)), rel.ManifestSHA256)

	require.NotNil(t, rel.RenderIndex)
	var page site.File
	for _, f := range treeOfNewest(t, store, site.TreeOutput) {
		if f.Path == "p/a/index.html" {
			page = f
		}
	}
	assert.Equal(t, []byte("<b>Anvil</b>"), objects.put["portal/"+ObjectKey(siteID, page, "")])
}

func TestPublish_ASiteWithNoLayoutsHasOneTree(t *testing.T) {
	svc, store, objects, _ := fixture(t)
	raw := []byte("---\nlooks: like front matter\n---\n{{ x }}")
	_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Files: []InputFile{{Path: "index.html", Body: raw}}})
	require.NoError(t, err)
	rel := store.releases[siteID][0]
	assert.False(t, rel.Rendered)
	assert.Empty(t, rel.SourceFiles)
	assert.Nil(t, rel.RenderIndex)
	assert.Equal(t, raw, objects.put["portal/"+ObjectKey(siteID, rel.Files[0], "")])
}

func TestPatch_ARecordRendersOnlyItsPage(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seededRender(t)
	before := len(objectsOf(store))
	done, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: refAt(recordPath), Edits: edit("Anvil", "Hammer")})
	require.NoError(t, err)
	assert.Equal(t, 2, done.Release)

	rel := store.releases[siteID][0]
	assert.Equal(t, []string{"p/a/index.html"}, paths(rel.Files), "only what read the record is written")
	assert.Equal(t, []string{recordPath}, paths(rel.SourceFiles))
	assert.NotEmpty(t, rel.ManifestParent)
	assert.Len(t, treeOfNewest(t, store, site.TreeOutput), 4, "and the release still serves everything")
	assert.Greater(t, len(objectsOf(store)), before)
}

func objectsOf(store *memStore) []site.File {
	files, _ := store.StoredFiles(context.Background(), siteID)
	return files
}

func TestPatch_ALayoutChangeUpdatesEveryPageInOneRelease(t *testing.T) {
	svc, store, objects := seededRender(t)
	_, err := svc.PatchFile(context.Background(), owner, PatchRequest{Ref: refAt(layoutPath), Edits: edit("<b>", "<i>")})
	require.NoError(t, err)
	rel := store.releases[siteID][0]
	assert.Len(t, rel.Files, 3, "every page using the layout, and not the copied image")
	for _, f := range treeOfNewest(t, store, site.TreeOutput) {
		if f.Path != "logo.png" {
			assert.Contains(t, string(objects.put["portal/"+ObjectKey(siteID, f, "")]), "<i>", f.Path)
		}
	}
}

func TestPatch_ATemplateErrorWritesNothingAndNamesTheFile(t *testing.T) {
	svc, store, _ := seededRender(t)
	_, err := svc.PatchFile(context.Background(), owner, PatchRequest{Ref: refAt("index.html"), Edits: edit("home", "{% if %}")})
	var re *siterender.RenderError
	require.ErrorAs(t, err, &re)
	assert.Equal(t, "index.html", re.Failures[0].File)
	assert.Len(t, store.releases[siteID], 1)
	assert.Equal(t, 1, envRelease(store))
}

func TestPatch_ADryRunRendersAndStoresNothing(t *testing.T) {
	ctx := context.Background()
	svc, store, objects := seededRender(t)
	before := objects.keys()
	done, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: refAt(layoutPath), Edits: edit("<b>", "<i>"), DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 3, done.OutputsChanged)
	assert.Equal(t, 1, done.Release)
	assert.Equal(t, before, objects.keys())
	assert.Len(t, store.releases[siteID], 1)

	_, err = svc.PatchFile(ctx, owner, PatchRequest{Ref: refAt("index.html"), Edits: edit("home", "{% if %}"), DryRun: true})
	var re *siterender.RenderError
	require.ErrorAs(t, err, &re, "a dry run reports what a publish would refuse")
}

func TestDryRun_OnASiteWithNoLayouts(t *testing.T) {
	svc, _, _ := seeded(t)
	done, err := svc.PatchFile(context.Background(), owner, PatchRequest{Ref: homeRef, Edits: edit("$99", "$89"), DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 1, done.OutputsChanged)
}

func TestContent_ManagersReadSourceAndViewersWhatIsServed(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seededRender(t)
	grant(store, viewer.Email, site.LevelView)

	got, err := svc.ReadFile(ctx, owner, refAt(layoutPath))
	require.NoError(t, err)
	assert.Contains(t, got.Body, "{{ content }}")
	_, err = svc.ReadFile(ctx, viewer, refAt(layoutPath))
	require.ErrorContains(t, err, "no file", "source is not what someone with view reads")
	served, err := svc.ReadFile(ctx, viewer, refAt("p/a/index.html"))
	require.NoError(t, err)
	assert.Equal(t, "<b>Anvil</b>", served.Body)

	list, err := svc.Tree(ctx, viewer, site.Ref{Site: "marketing", Environment: site.EnvDefault}, 0, 0)
	require.NoError(t, err)
	assert.NotContains(t, paths(list.Files), layoutPath)
}

func TestDiff_ComparesSource(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := seededRender(t)
	_, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: refAt(recordPath), Edits: edit("Anvil", "Hammer")})
	require.NoError(t, err)
	got, err := svc.Diff(ctx, owner, site.Ref{Site: "marketing", Environment: site.EnvDefault}, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{recordPath}, got.Comparison.Changed)
	file, err := svc.Diff(ctx, owner, refAt(recordPath), 0, 0)
	require.NoError(t, err)
	assert.Contains(t, file.Diff, "+{\"name\":\"Hammer\"}")
}

func TestPublish_TurningRenderingOnAndOffWritesWholeTrees(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seeded(t)
	_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Files: []InputFile{{Path: layoutPath, Body: []byte("{{ content }}")}}})
	require.NoError(t, err)
	on := store.releases[siteID][0]
	assert.True(t, on.Rendered)
	assert.Empty(t, on.ManifestParent, "a release that starts rendering stands alone")
	assert.NotEmpty(t, on.SourceFiles)

	_, err = svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 2, Remove: []string{layoutPath}})
	require.NoError(t, err)
	off := store.releases[siteID][0]
	assert.False(t, off.Rendered)
	assert.Empty(t, off.ManifestParent)
}

func TestPublish_AWholeRepublishReusesEveryUnchangedOutput(t *testing.T) {
	svc, store, objects := seededRender(t)
	before := objects.keys()
	got, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Files: renderedFiles()})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Release.Number)
	assert.Len(t, treeOfNewest(t, store, site.TreeOutput), 4)
	assert.Equal(t, before, withoutIndex(objects.keys(), before))
}

// withoutIndex is keys with any object not in before removed: a new render
// writes a new index, and nothing else.
func withoutIndex(keys, before []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		for _, b := range before {
			if k == b {
				out = append(out, k)
			}
		}
	}
	return out
}

func TestRender_LimitsAreTheSitesOwn(t *testing.T) {
	ctx := context.Background()
	t.Run("output files", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		svc.Config.MaxFiles = 2
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Files: []InputFile{
			{Path: layoutPath, Body: []byte("{{ content }}")},
			{Path: "a.png", Body: []byte{1}},
			{Path: "b.png", Body: []byte{2}},
		}})
		require.Error(t, err)
	})
	t.Run("an output path no release can key", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Files: []InputFile{
			{Path: layoutPath, Body: []byte("{{ content }}")},
			{Path: "a.html", Body: []byte("---\npermalink: /.env\n---\nx")},
		}})
		require.ErrorContains(t, err, "dotted segment")
	})
}

func TestReadIndex_ALostRecordCostsAFullRender(t *testing.T) {
	ctx := context.Background()
	svc, store, objects := seededRender(t)
	rel := store.releases[siteID][0]
	require.NoError(t, objects.DeleteObject(ctx, "portal", ObjectKey(siteID, *rel.RenderIndex, "")))
	_, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: refAt(recordPath), Edits: edit("Anvil", "Hammer")})
	require.NoError(t, err)
	assert.Equal(t, 2, envRelease(store))

	assert.Nil(t, decodeIndex([]byte("not gzip")))
	body, err := gzipped([]byte("not json"))
	require.NoError(t, err)
	assert.Nil(t, decodeIndex(body))
}

func TestDeltaOf(t *testing.T) {
	changed, removed := deltaOf(
		[]site.File{{Path: "a", SHA256: "1"}, {Path: "b", SHA256: "2"}, {Path: "c", SHA256: "3"}},
		[]site.File{{Path: "a", SHA256: "1"}, {Path: "b", SHA256: "9"}, {Path: "d", SHA256: "4"}},
	)
	assert.Equal(t, []string{"b", "d"}, paths(changed))
	assert.Equal(t, []string{"c"}, removed)
}

func TestTreeFS_ReadsNewFilesFromMemoryAndTheRestFromStorage(t *testing.T) {
	ctx := context.Background()
	svc, _, objects, _ := fixture(t)
	stored := site.File{Path: "stored.txt", SHA256: "aa", Size: 4}
	require.NoError(t, objects.PutObject(ctx, "portal", ObjectKey(siteID, stored, ""), []byte("kept"), "text/plain"))
	fsys := svc.treeFS(ctx, siteID, []site.File{stored, {Path: "new.txt", SHA256: "bb", Size: 3}, {Path: "gone.txt", SHA256: "cc", Size: 1}}, []InputFile{{Path: "new.txt", Body: []byte("new")}})
	for name, want := range map[string]string{"stored.txt": "kept", "new.txt": "new"} {
		body, err := fs.ReadFile(fsys, name)
		require.NoError(t, err)
		assert.Equal(t, want, string(body))
	}
	_, err := fs.ReadFile(fsys, "gone.txt")
	require.ErrorContains(t, err, "reading gone.txt")
}

func TestReadFailuresInRendering(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")
	svc, store, _ := seededRender(t)
	store.failFiles = boom
	_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Files: []InputFile{page("a.html")}})
	require.ErrorIs(t, err, boom)
}

func TestReadIndex_ARenderWithNoRecordIsAFullRender(t *testing.T) {
	ctx := context.Background()
	svc, store, _, _ := fixture(t)
	rel := &site.Release{ID: "srel_x", SiteID: siteID, Number: 1, Rendered: true}
	store.releases[siteID] = []site.Release{*rel}
	index, err := svc.readIndex(ctx, siteID, rel)
	require.NoError(t, err)
	assert.Nil(t, index)
	store.failFiles = errors.New("down")
	_, err = svc.readIndex(ctx, siteID, rel)
	require.Error(t, err)
}

func TestStoreIndex_AFailureIsReported(t *testing.T) {
	svc, _, objects, _ := fixture(t)
	objects.fail = ""
	objects.failPut = errors.New("the bucket refused it")
	_, err := svc.storeIndex(context.Background(), siteID, &siterender.Index{})
	require.ErrorContains(t, err, "render index")
}

func TestOutputSink_LimitsAndUnknownFiles(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := fixture(t)
	svc.Config.MaxReleaseBytes = 10
	p := publication{st: &site.Site{ID: siteID}, base: newBasis(nil, nil), built: &built{}, req: PublishRequest{}}
	sink := svc.newOutputSink(ctx, p, []site.File{{Path: "big", SHA256: "a", Size: 100}}, false)
	require.ErrorContains(t, sink.Copy("big"), "max_release_bytes")
	require.ErrorContains(t, sink.Copy("absent"), "not in the source")
	require.ErrorContains(t, sink.Keep("absent"), "does not serve")

	failing := svc.newOutputSink(ctx, p, nil, false)
	svc.Objects = nil
	failing.s.Objects = objectsThatFail{}
	require.NoError(t, failing.Put("a.html", []byte("x")))
	require.ErrorContains(t, failing.wait(), "storing a.html")
}

type objectsThatFail struct{}

func (objectsThatFail) PutObject(context.Context, string, string, []byte, string) error {
	return errors.New("no")
}

func (objectsThatFail) DeleteObject(context.Context, string, string) error { return nil }

func (objectsThatFail) GetObject(context.Context, string, string) (body []byte, contentType string, err error) {
	return nil, "", errors.New("no")
}
