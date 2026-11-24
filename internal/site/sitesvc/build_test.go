package sitesvc

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
	"github.com/txn2/mcp-data-platform/internal/site/siterender"
)

func builtFiles() []InputFile {
	return []InputFile{
		{Path: sitebuild.ControlFile, Body: []byte(`{"entries":["app.tsx"],"imports":{"react":"https://esm.sh/react@18.3.1"}}`)},
		{Path: "app.tsx", Body: []byte(`import { Table } from "./Table"; document.body.append(String(<Table />));`)},
		{Path: "Table.tsx", Body: []byte(`export function Table(): unknown { return <table data-v="one" />; }`)},
		{Path: "index.html", Body: []byte(`<script type="module" src="/app.js"></script>`)},
	}
}

// #2774: a release with a build file serves its compiled output and holds its
// TypeScript as source, read and patched as text.
func TestPublish_ABuiltReleaseServesTheOutputAndKeepsTheSource(t *testing.T) {
	ctx := context.Background()
	svc, store, objects, _ := fixture(t)
	_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Files: builtFiles()})
	require.NoError(t, err)
	rel := store.releases[siteID][0]
	assert.True(t, rel.Rendered)
	assert.Equal(t, []string{"app.js", "index.html"}, paths(treeOfNewest(t, store, site.TreeOutput)))
	assert.ElementsMatch(t, []string{sitebuild.ControlFile, "Table.tsx", "app.tsx", "index.html"}, paths(treeOfNewest(t, store, site.TreeSource)))
	for _, f := range treeOfNewest(t, store, site.TreeOutput) {
		if f.Path == "app.js" {
			assert.Equal(t, "text/javascript; charset=utf-8", f.ContentType)
			assert.Contains(t, string(objects.put["portal/"+ObjectKey(siteID, f, "")]), `"data-v": "one"`)
		}
	}

	text, err := svc.ReadFile(ctx, owner, refAt("Table.tsx"))
	require.NoError(t, err)
	assert.Equal(t, "text/plain; charset=utf-8", text.ContentType)

	dry, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: refAt("Table.tsx"), Edits: edit(`"one"`, `"two"`), DryRun: true})
	require.NoError(t, err)
	assert.Equal(t, 1, dry.OutputsChanged, "the dry run names the one output the edit changes")

	done, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: refAt("Table.tsx"), Edits: edit(`"one"`, `"two"`)})
	require.NoError(t, err)
	assert.Equal(t, 2, done.Release)
	assert.Equal(t, []string{"app.js"}, paths(store.releases[siteID][0].Files), "only the entry that read the file is written")

	_, err = svc.PatchFile(ctx, owner, PatchRequest{Ref: refAt("Table.tsx"), Edits: edit(`return`, `return (`)})
	var re *siterender.RenderError
	require.ErrorAs(t, err, &re)
	assert.Equal(t, "Table.tsx", re.Failures[0].File)
	assert.Positive(t, re.Failures[0].Column)
	assert.Equal(t, 2, envRelease(store), "dev does not move")
}

func TestPublish_ATreeWithoutABuildFileServesItsTypeScriptAsText(t *testing.T) {
	svc, store, _, _ := fixture(t)
	files := builtFiles()[1:]
	_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Files: files})
	require.NoError(t, err)
	rel := store.releases[siteID][0]
	assert.False(t, rel.Rendered)
	for _, f := range treeOfNewest(t, store, site.TreeOutput) {
		if strings.HasSuffix(f.Path, ".tsx") {
			assert.Equal(t, "text/plain; charset=utf-8", f.ContentType, f.Path)
		}
	}
	assert.ElementsMatch(t, []string{"Table.tsx", "app.tsx", "index.html"}, paths(treeOfNewest(t, store, site.TreeOutput)))
}

// #2783-#2785: ReadBuild hands an editor a release's source, output and
// render record, refuses anyone else, and reads release numbers it is given.
func TestReadBuild(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := fixture(t)
	pub, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Files: builtFiles()})
	require.NoError(t, err)
	require.Len(t, pub.Builds, 1, "a publish reports what its entry compiled to")
	b, err := svc.ReadBuild(ctx, owner, "marketing", "", 0)
	require.NoError(t, err)
	assert.Equal(t, 1, b.Release.Number)
	require.NotNil(t, b.Index)
	assert.Len(t, b.Index.Builds, 1)
	src, err := fs.ReadFile(b.Source, "Table.tsx")
	require.NoError(t, err)
	assert.Contains(t, string(src), "data-v")
	out, err := fs.ReadFile(b.Output, "app.js")
	require.NoError(t, err)
	assert.Contains(t, string(out), "data-v")
	again, err := svc.ReadBuild(ctx, owner, "marketing", "", 1)
	require.NoError(t, err)
	assert.Equal(t, b.Release.ID, again.Release.ID)

	_, err = svc.ReadBuild(ctx, Actor{Email: "someone@else.example"}, "marketing", "", 0)
	require.Error(t, err)
	_, err = svc.ReadBuild(ctx, owner, "marketing", "", 99)
	require.Error(t, err)
	_, err = svc.ReadBuild(ctx, owner, "nope", "", 0)
	require.Error(t, err)
}
