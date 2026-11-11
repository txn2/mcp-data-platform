package sitesvc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

const (
	homeBody  = "<h1>Pricing from $99</h1>\n"
	aboutBody = "<p>about us</p>\n"
)

var (
	homeRef = site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "index.html"}
	treeRef = site.Ref{Site: "marketing", Environment: site.EnvDev}
)

func edit(find, replace string) []textpatch.Edit {
	var e textpatch.Edit
	must(e.UnmarshalJSON(fmt.Appendf(nil, `{"find":%q,"replace":%q}`, find, replace)))
	return []textpatch.Edit{e}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

// seeded is a site with one full release on default.
func seeded(t *testing.T) (*Service, *memStore, *memObjects) {
	t.Helper()
	svc, store, objects, _ := fixture(t)
	_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Files: []InputFile{
		{Path: "index.html", Body: []byte(homeBody)},
		{Path: "about/index.html", Body: []byte(aboutBody)},
		{Path: "logo.png", Body: []byte{0x89, 'P', 'N', 'G', 0}},
		{Path: "_redirects", Body: []byte("/old /new 302\n")},
	}})
	require.NoError(t, err)
	return svc, store, objects
}

func envRelease(store *memStore) int {
	return store.sites[siteID].Environment(site.EnvDev).Release
}

func TestPublishBase_AddsReplacesAndDeletesInOneRelease(t *testing.T) {
	ctx := context.Background()
	svc, store, objects := seeded(t)
	before := len(objects.keys())

	got, err := svc.Publish(ctx, owner, PublishRequest{
		Site: "marketing", Base: 1, Note: "tidy",
		Files:  []InputFile{{Path: "index.html", Body: []byte("<h1>new</h1>\n")}, {Path: "added.html", Body: []byte("<p>added</p>\n")}},
		Remove: []string{"about/index.html"},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, got.Release.Number)
	assert.Equal(t, 1, got.Release.Base, "a derived release records what it was made from")
	assert.Equal(t, 3, got.Release.FileCount, "index, added and the logo")
	assert.Equal(t, 2, envRelease(store))
	assert.Equal(t, before+2, len(objects.keys()), "only the two new files were written")

	files, err := store.Files(ctx, got.Release.ID)
	require.NoError(t, err)
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	assert.Equal(t, []string{"added.html", "index.html", "logo.png"}, paths)
	assert.Equal(t, []site.Redirect{{From: "/old", To: "/new", Status: 302}}, got.Release.Redirects, "the map carries forward")

	first, err := store.Release(ctx, siteID, 1)
	require.NoError(t, err)
	_, err = svc.Rollback(ctx, owner, "marketing", site.EnvDev, 1)
	require.NoError(t, err)
	restored, err := store.Files(ctx, first.ID)
	require.NoError(t, err)
	assert.Equal(t, first.ManifestSHA256, Digest(restored), "rolling back restores the exact file set")
}

func TestPublishBase_Refusals(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seeded(t)

	t.Run("a base the environment has moved past", func(t *testing.T) {
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 9, Files: []InputFile{page("a.html")}})
		var e *textpatch.Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, textpatch.CodeStaleBase, e.Code)
	})
	t.Run("any environment but default", func(t *testing.T) {
		store.sites[siteID].Environment("stage").Release = 1
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Environment: "stage", Base: 1, Files: []InputFile{page("a.html")}})
		require.ErrorContains(t, err, "read-only")
	})
	t.Run("deleting a file that is not there", func(t *testing.T) {
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Remove: []string{"nope.html"}})
		require.ErrorContains(t, err, `no file "nope.html"`)
	})
	t.Run("deleting a hidden path", func(t *testing.T) {
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Remove: []string{".env"}})
		require.ErrorContains(t, err, "dotted segment")
	})
	t.Run("sending and deleting one path", func(t *testing.T) {
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Files: []InputFile{page("index.html")}, Remove: []string{"index.html"}})
		require.ErrorContains(t, err, "both sent and deleted")
	})
	t.Run("deleting with no base", func(t *testing.T) {
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Files: []InputFile{page("a.html")}, Remove: []string{"a.html"}})
		require.ErrorContains(t, err, "from a base")
	})
	t.Run("deleting everything", func(t *testing.T) {
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Remove: []string{"index.html", "about/index.html", "logo.png"}})
		require.ErrorContains(t, err, "holds no files")
	})
	assert.Len(t, store.releases[siteID], 1, "no refused publish wrote a release")
}

func TestPublishBase_DeletingTheRedirectsFileClearsTheMap(t *testing.T) {
	svc, _, _ := seeded(t)
	got, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Base: 1, Remove: []string{"_redirects"}})
	require.NoError(t, err)
	assert.False(t, got.Unchanged)
	assert.Empty(t, got.Release.Redirects)
}

func TestPublishBase_ChangingNothingWritesNothing(t *testing.T) {
	svc, store, objects := seeded(t)
	before := objects.keys()
	got, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Base: 1, Files: []InputFile{{Path: "index.html", Body: []byte(homeBody)}}})
	require.NoError(t, err)
	assert.True(t, got.Unchanged)
	assert.Equal(t, 1, got.Release.Number)
	assert.Len(t, store.releases[siteID], 1)
	assert.Equal(t, before, objects.keys())
}

func TestPublish_AWholePublishKeepsBytesItAlreadyHolds(t *testing.T) {
	svc, _, objects, _ := fixture(t)
	body := []byte(strings.Repeat("<p>repeat</p>\n", 200))
	files := []InputFile{{Path: "a.html", Body: body}}
	_, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Files: files})
	require.NoError(t, err)
	before := objects.keys()
	got, err := svc.Publish(context.Background(), owner, PublishRequest{Site: "marketing", Files: []InputFile{{Path: "renamed.html", Body: body}}})
	require.NoError(t, err)
	assert.Equal(t, before, objects.keys(), "the same bytes under another name are the same objects")
	assert.Equal(t, 2, got.Release.Number)
}

// A file written before objects were kept by digest is stored at its path, so
// it can carry over but cannot stand in for another name.
func TestPublish_AnOlderFileIsNeverReusedUnderAnotherName(t *testing.T) {
	ctx := context.Background()
	svc, store, objects, _ := fixture(t)
	legacy := site.File{Path: "old.html", SHA256: "digest", ContentType: "text/html; charset=utf-8", Size: 3, StoredIn: "srel_old"}
	store.releases[siteID] = []site.Release{{ID: "srel_old", SiteID: siteID, Number: 1, Files: []site.File{legacy}, Redirects: []site.Redirect{}}}
	store.sites[siteID].Environment(site.EnvDev).Release = 1
	require.NoError(t, objects.PutObject(ctx, "portal", ObjectKey(siteID, legacy, ""), []byte("old"), legacy.ContentType))

	got, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Files: []InputFile{{Path: "new.html", Body: []byte("<p>n</p>")}}})
	require.NoError(t, err)
	files, err := store.Files(ctx, got.Release.ID)
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, "srel_old", files[1].StoredIn, "the carried file still reads from where it is")

	base := newBasis(&site.Release{}, []site.File{legacy})
	_, ok := base.reusable(InputFile{Path: "other.html", sum: "digest"}, legacy.ContentType)
	assert.False(t, ok, "its key holds the old path")
	_, ok = base.reusable(InputFile{Path: "old.html", sum: "digest"}, legacy.ContentType)
	assert.True(t, ok, "at its own path it is the same file")
}

func TestPatchFile_MakesOneDerivedReleaseTheSizeOfTheChange(t *testing.T) {
	ctx := context.Background()
	svc, store, objects := seeded(t)
	before := len(objects.keys())

	done, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: homeRef, Base: 1, Edits: edit("$99", "$89"), Summary: "Fix the headline"})
	require.NoError(t, err)
	assert.Equal(t, 2, done.Release)
	assert.Contains(t, done.Result.Diff, "+<h1>Pricing from $89</h1>")
	assert.Equal(t, 2, envRelease(store))
	assert.Equal(t, before+1, len(objects.keys()), "one new object, for the one changed file")

	rec := store.releases[siteID][0]
	assert.Equal(t, "Fix the headline", rec.Note)
	assert.Equal(t, 1, rec.Base)
	assert.Len(t, rec.Files, 1, "the release holds only what changed")
	assert.NotEmpty(t, rec.ManifestParent)
	assert.Equal(t, 3, rec.FileCount, "and still counts the whole set")

	read, err := svc.ReadFile(ctx, owner, homeRef)
	require.NoError(t, err)
	assert.Equal(t, 2, read.Release)
	assert.Equal(t, "<h1>Pricing from $89</h1>\n", read.Body)
}

func TestPatchFile_WritesAWholeFileSetAtTheDepthBound(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seeded(t)
	for i := range maxManifestDepth {
		_, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: homeRef, Edits: []textpatch.Edit{appendEdit(fmt.Sprintf("<!-- %d -->\n", i))}})
		require.NoError(t, err)
	}
	newest := store.releases[siteID][0]
	assert.Empty(t, newest.ManifestParent, "past the bound a release stands alone")
	assert.Zero(t, newest.ManifestDepth)
	assert.Len(t, newest.Files, 3, "and holds its whole file set")
	assert.NotZero(t, newest.Base, "while still saying what it came from")

	read, err := svc.ReadFile(ctx, owner, homeRef)
	require.NoError(t, err)
	assert.Contains(t, read.Body, fmt.Sprintf("<!-- %d -->", maxManifestDepth-1))
}

func appendEdit(text string) textpatch.Edit {
	var e textpatch.Edit
	must(e.UnmarshalJSON(fmt.Appendf(nil, `{"op":"append","text":%q}`, text)))
	return e
}

func TestPatchFile_LeavesNothingBehindWhenItCannotApply(t *testing.T) {
	ctx := context.Background()
	cases := map[string]struct {
		req  PatchRequest
		code string
		want string
	}{
		"an anchor that matches nothing":  {req: PatchRequest{Ref: homeRef, Edits: edit("$12345", "$1")}, code: textpatch.CodeNoMatch},
		"a stale base":                    {req: PatchRequest{Ref: homeRef, Base: 7, Edits: edit("$99", "$89")}, code: textpatch.CodeStaleBase},
		"a dry run":                       {req: PatchRequest{Ref: homeRef, Edits: edit("$99", "$89"), DryRun: true}},
		"a binary file":                   {req: PatchRequest{Ref: site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "logo.png"}, Edits: edit("a", "b")}, code: textpatch.CodeNotText},
		"a file the release lacks":        {req: PatchRequest{Ref: site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "no.html"}, Edits: edit("a", "b")}, want: `no file "no.html"`},
		"a whole tree":                    {req: PatchRequest{Ref: treeRef, Edits: edit("a", "b")}, want: "names a tree"},
		"a hidden path":                   {req: PatchRequest{Ref: site.Ref{Site: "marketing", Environment: site.EnvDev, Path: ".env"}, Edits: edit("a", "b")}, want: "dotted segment"},
		"a parent reference":              {req: PatchRequest{Ref: site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "a/../../b"}, Edits: edit("a", "b")}, want: "relative and clean"},
		"another environment":             {req: PatchRequest{Ref: site.Ref{Site: "marketing", Environment: "stage", Path: "index.html"}, Edits: edit("a", "b")}, want: "read-only"},
		"an environment needing approval": {req: PatchRequest{Ref: site.Ref{Site: "marketing", Environment: envProd, Path: "index.html"}, Edits: edit("a", "b")}, want: `environment "prod" is read-only`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			svc, store, objects := seeded(t)
			before := objects.keys()
			_, err := svc.PatchFile(ctx, owner, tc.req)
			switch {
			case tc.code != "":
				var e *textpatch.Error
				require.ErrorAs(t, err, &e)
				assert.Equal(t, tc.code, e.Code)
			case tc.want != "":
				require.ErrorContains(t, err, tc.want)
			default:
				require.NoError(t, err)
			}
			assert.Len(t, store.releases[siteID], 1, "no release was written")
			assert.Equal(t, 1, envRelease(store), "the environment did not move")
			assert.Equal(t, before, objects.keys(), "and no object was stored")
		})
	}
}

func TestPatchFile_ThatChangesNothingWritesNoRelease(t *testing.T) {
	svc, store, _ := seeded(t)
	done, err := svc.PatchFile(context.Background(), owner, PatchRequest{Ref: homeRef, Edits: edit("$99", "$99")})
	require.NoError(t, err)
	assert.True(t, done.Unchanged)
	assert.Len(t, store.releases[siteID], 1)
}

func TestPatchFile_LosingARaceLeavesNoRelease(t *testing.T) {
	svc, store, _ := seeded(t)
	store.failPoint = site.ErrStale
	_, err := svc.PatchFile(context.Background(), owner, PatchRequest{Ref: homeRef, Edits: edit("$99", "$89")})
	require.ErrorIs(t, err, site.ErrStale)
	assert.Len(t, store.releases[siteID], 1)
}

func TestPatchFile_RedirectsFilePatchesTheMap(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seeded(t)
	ref := site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "_redirects"}
	read, err := svc.ReadFile(ctx, owner, ref)
	require.NoError(t, err)
	assert.Equal(t, "/old /new 302\n", read.Body, "it reads as the map it became")

	_, err = svc.PatchFile(ctx, owner, PatchRequest{Ref: ref, Edits: edit("/new", "/newer")})
	require.NoError(t, err)
	assert.Equal(t, []site.Redirect{{From: "/old", To: "/newer", Status: 302}}, store.releases[siteID][0].Redirects)
	assert.Empty(t, store.releases[siteID][0].Files, "the map is no file of the release")

	_, err = svc.PatchFile(ctx, owner, PatchRequest{Ref: ref, Edits: edit("/newer 302", "not a status")})
	require.ErrorContains(t, err, "_redirects")
	assert.Len(t, store.releases[siteID], 2, "a map that does not parse writes nothing")
}

func TestContentAccess(t *testing.T) {
	ctx := context.Background()
	internalOnly := func(_ context.Context, email string) bool { return strings.HasSuffix(email, "@client.example") }
	unverified := Actor{Email: manager.Email, EmailUnverified: true}

	tests := []struct {
		name     string
		actor    Actor
		internal bool
		reads    bool
		writes   bool
		diffs    bool
	}{
		{name: "an administrator", actor: admin, reads: true, writes: true, diffs: true},
		{name: "the owner", actor: owner, reads: true, writes: true, diffs: true},
		{name: "someone who manages", actor: manager, reads: true, writes: true, diffs: true},
		{name: "someone with view", actor: viewer, reads: true},
		{name: "someone with nothing", actor: stranger},
		{name: "no one", actor: Actor{}},
		{name: "an email the provider did not verify", actor: unverified},
		{name: "anyone internal on an open site", actor: Actor{Email: stranger.Email, Persona: "analyst"}, internal: true, reads: true},
		{name: "someone with no persona on an open site", actor: stranger, internal: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, store, _ := seeded(t)
			svc.External = internalOnly
			grant(store, manager.Email, site.LevelManage)
			grant(store, viewer.Email, site.LevelView)
			store.sites[siteID].Environment(site.EnvDev).InternalView = tt.internal

			_, readErr := svc.ReadFile(ctx, tt.actor, homeRef)
			_, treeErr := svc.Tree(ctx, tt.actor, treeRef, 0, 0)
			_, findErr := svc.LocateTree(ctx, tt.actor, treeRef, textpatch.LocateQuery{Find: "about"})
			_, diffErr := svc.Diff(ctx, tt.actor, homeRef, 0, 1)
			_, writeErr := svc.PatchFile(ctx, tt.actor, PatchRequest{Ref: homeRef, Edits: edit("$99", "$89"), DryRun: true})

			check := func(what string, err error, allowed bool) {
				if allowed {
					if errors.Is(err, site.ErrNotAllowed) {
						t.Errorf("%s was refused: %v", what, err)
					}
					return
				}
				require.ErrorIs(t, err, site.ErrNotAllowed, what)
			}
			check("read", readErr, tt.reads)
			check("list", treeErr, tt.reads)
			check("locate", findErr, tt.reads)
			check("diff", diffErr, tt.diffs)
			check("patch", writeErr, tt.writes)
		})
	}
}

// Someone with no access learns nothing about what a site holds: a file that
// exists and one that does not are refused in the same words.
func TestContentAccess_ARefusalSaysNothingAboutTheFiles(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := seeded(t)
	_, exists := svc.ReadFile(ctx, stranger, homeRef)
	_, missing := svc.ReadFile(ctx, stranger, site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "secret-plans.html"})
	_, noEnv := svc.ReadFile(ctx, stranger, site.Ref{Site: "marketing", Environment: "nope", Path: "index.html"})
	require.Error(t, exists)
	assert.Equal(t, exists.Error(), missing.Error())
	assert.Equal(t, exists.Error(), noEnv.Error())
	assert.NotContains(t, exists.Error(), "index.html")
}

func TestDiff(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := seeded(t)
	_, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: homeRef, Edits: edit("$99", "$89")})
	require.NoError(t, err)

	t.Run("one file, from what it was derived from", func(t *testing.T) {
		got, err := svc.Diff(ctx, owner, homeRef, 0, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, got.From)
		assert.Equal(t, 2, got.To)
		assert.Contains(t, got.Diff, "-<h1>Pricing from $99</h1>")
		assert.Contains(t, got.Diff, "+<h1>Pricing from $89</h1>")
	})
	t.Run("a tree names what changed", func(t *testing.T) {
		got, err := svc.Diff(ctx, owner, treeRef, 0, 0)
		require.NoError(t, err)
		assert.Equal(t, []string{"index.html"}, got.Comparison.Changed)
		assert.Empty(t, got.Comparison.Added)
	})
	t.Run("a file one side lacks", func(t *testing.T) {
		_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 2, Files: []InputFile{{Path: "new.html", Body: []byte("<p>n</p>\n")}}})
		require.NoError(t, err)
		got, err := svc.Diff(ctx, owner, site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "new.html"}, 2, 3)
		require.NoError(t, err)
		assert.Contains(t, got.Diff, "+<p>n</p>")
	})
	t.Run("a file neither side has", func(t *testing.T) {
		_, err := svc.Diff(ctx, owner, site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "no.html"}, 1, 2)
		require.ErrorContains(t, err, "neither release")
	})
	t.Run("the first release has nothing before it", func(t *testing.T) {
		_, err := svc.Diff(ctx, owner, homeRef, 0, 1)
		require.ErrorContains(t, err, "no earlier release")
	})
}

func TestTree(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := seeded(t)

	all, err := svc.Tree(ctx, owner, treeRef, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, 4, all.Total, "three files and the redirect map")
	assert.False(t, all.Truncated)
	assert.Equal(t, "_redirects", all.Files[0].Path)

	folder, err := svc.Tree(ctx, owner, site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "about/"}, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 1, folder.Total)
	assert.Equal(t, "about/index.html", folder.Files[0].Path)

	page1, err := svc.Tree(ctx, owner, treeRef, 1, 2)
	require.NoError(t, err)
	assert.Len(t, page1.Files, 2)
	assert.True(t, page1.Truncated)

	empty := site.Ref{Site: "marketing", Environment: "stage"}
	_, err = svc.Tree(ctx, owner, empty, 0, 0)
	require.ErrorContains(t, err, "serves no release")
}

func TestLocateTree(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := seeded(t)

	got, err := svc.LocateTree(ctx, owner, treeRef, textpatch.LocateQuery{Find: "$99"})
	require.NoError(t, err)
	require.Len(t, got.Files, 1)
	assert.Equal(t, "index.html", got.Files[0].Path)
	assert.Equal(t, 1, got.Count)
	assert.Equal(t, 3, got.FilesScanned, "the two pages and the redirect map")
	assert.Equal(t, 1, got.FilesSkipped, "the image")

	none, err := svc.LocateTree(ctx, owner, treeRef, textpatch.LocateQuery{Find: "absent words"})
	require.NoError(t, err)
	assert.NotNil(t, none.Files)
	assert.Empty(t, none.Files)

	cut, err := svc.LocateTree(ctx, owner, treeRef, textpatch.LocateQuery{Pattern: "<", Limit: 1})
	require.NoError(t, err)
	assert.True(t, cut.Truncated, "a limit that cuts the list says so")

	_, err = svc.LocateTree(ctx, owner, treeRef, textpatch.LocateQuery{Find: "x", Section: "Intro"})
	require.ErrorContains(t, err, "need one file")

	_, err = svc.LocateTree(ctx, owner, treeRef, textpatch.LocateQuery{})
	require.Error(t, err, "neither find nor pattern")
}

func TestLocateTree_AFilePastTheSearchBoundIsCountedNotRead(t *testing.T) {
	ctx := context.Background()
	svc, store, _ := seeded(t)
	store.releases[siteID][0].Files = append(store.releases[siteID][0].Files,
		site.File{Path: "zhuge.html", SHA256: "not-stored", ContentType: "text/html", Size: maxSearchBytes + 1})
	got, err := svc.LocateTree(ctx, owner, treeRef, textpatch.LocateQuery{Find: "$99"})
	require.NoError(t, err)
	assert.Equal(t, 2, got.FilesSkipped, "the image and the oversized page")
	assert.Equal(t, 1, got.Count)
}

func TestDiff_AFolderNamesOnlyItsOwnFiles(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := seeded(t)
	_, err := svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Base: 1, Files: []InputFile{
		{Path: "index.html", Body: []byte("<h1>x</h1>\n")},
		{Path: "about/index.html", Body: []byte("<p>y</p>\n")},
	}})
	require.NoError(t, err)
	got, err := svc.Diff(ctx, owner, site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "about/"}, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, []string{"about/index.html"}, got.Comparison.Changed)
}

func TestDelete_RemovesEachSharedObjectOnce(t *testing.T) {
	ctx := context.Background()
	svc, _, objects := seeded(t)
	_, err := svc.PatchFile(ctx, owner, PatchRequest{Ref: homeRef, Edits: edit("$99", "$89")})
	require.NoError(t, err)
	require.NotEmpty(t, objects.keys())
	require.NoError(t, svc.Delete(ctx, admin, "marketing"))
	assert.Empty(t, objects.keys())
}

func TestReadFailuresAreReported(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("down")
	t.Run("a deployment with no bucket", func(t *testing.T) {
		svc, _, _ := seeded(t)
		svc.Objects = nil
		_, err := svc.ReadFile(ctx, owner, homeRef)
		require.ErrorContains(t, err, "serves no sites")
	})
	t.Run("an environment the site lacks", func(t *testing.T) {
		svc, _, _ := seeded(t)
		_, err := svc.ReadFile(ctx, owner, site.Ref{Site: "marketing", Environment: "nope", Path: "a"})
		require.ErrorIs(t, err, site.ErrNoEnvironment)
	})
	t.Run("a site that does not exist", func(t *testing.T) {
		svc, _, _ := seeded(t)
		_, err := svc.ReadFile(ctx, owner, site.Ref{Site: "nope", Environment: site.EnvDev, Path: "a"})
		require.Error(t, err)
		_, err = svc.PatchFile(ctx, owner, PatchRequest{Ref: site.Ref{Site: "nope", Environment: site.EnvDev, Path: "a"}})
		require.Error(t, err)
		_, err = svc.Diff(ctx, owner, site.Ref{Site: "nope", Environment: site.EnvDev}, 0, 0)
		require.Error(t, err)
	})
	t.Run("the file list cannot be read", func(t *testing.T) {
		svc, store, _ := seeded(t)
		store.failFiles = boom
		_, err := svc.ReadFile(ctx, owner, homeRef)
		require.ErrorIs(t, err, boom)
		_, err = svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Files: []InputFile{page("a.html")}})
		require.ErrorIs(t, err, boom)
	})
	t.Run("the release a pointer names is gone", func(t *testing.T) {
		svc, store, _ := seeded(t)
		store.releases[siteID] = nil
		_, err := svc.ReadFile(ctx, owner, homeRef)
		require.ErrorIs(t, err, site.ErrNoRelease)
		_, err = svc.Publish(ctx, owner, PublishRequest{Site: "marketing", Files: []InputFile{page("a.html")}})
		require.ErrorIs(t, err, site.ErrNoRelease)
	})
	t.Run("a stored object cannot be read", func(t *testing.T) {
		svc, _, objects := seeded(t)
		for _, key := range objects.keys() {
			require.NoError(t, objects.DeleteObject(ctx, "portal", strings.TrimPrefix(key, "portal/")))
		}
		_, err := svc.ReadFile(ctx, owner, homeRef)
		require.ErrorContains(t, err, "reading index.html")
		got, err := svc.LocateTree(ctx, owner, treeRef, textpatch.LocateQuery{Find: "$99"})
		require.NoError(t, err, "one unreadable file must not lose the search")
		assert.Equal(t, 3, got.FilesSkipped, "the two pages and the image were not searched")
		assert.Equal(t, 1, got.FilesScanned, "the redirect map needs no object")
		_, err = svc.Diff(ctx, owner, homeRef, 1, 1)
		require.ErrorContains(t, err, "reading index.html")
	})
	t.Run("a binary file cannot be diffed", func(t *testing.T) {
		svc, _, _ := seeded(t)
		_, err := svc.Diff(ctx, owner, site.Ref{Site: "marketing", Environment: site.EnvDev, Path: "logo.png"}, 1, 1)
		var e *textpatch.Error
		require.ErrorAs(t, err, &e)
		assert.Equal(t, textpatch.CodeNotText, e.Code)
	})
	t.Run("a release to compare with that does not exist", func(t *testing.T) {
		svc, _, _ := seeded(t)
		_, err := svc.Diff(ctx, owner, homeRef, 7, 1)
		require.ErrorIs(t, err, site.ErrNoRelease)
		_, err = svc.Diff(ctx, owner, homeRef, 1, 9)
		require.ErrorIs(t, err, site.ErrNoRelease)
		_, err = svc.Diff(ctx, owner, site.Ref{Site: "marketing", Environment: site.EnvDev, Path: ".env"}, 1, 1)
		require.ErrorContains(t, err, "dotted segment")
	})
}
