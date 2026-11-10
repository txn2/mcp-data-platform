package sitetool

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

const (
	contentSite = "catalog-site"
	contentHome = "<h1>From $99</h1>\n"
	homeTarget  = site.RefPrefix + contentSite + "/default/index.html"
	treeTarget  = site.RefPrefix + contentSite + "/default"
)

// seededContent is a site with one release of a page, an image and a redirect.
func seededContent(t *testing.T) (*fixture, *Content) {
	t.Helper()
	f := newFixture()
	call(t, f, admin(), Input{Command: CmdCreate, Name: contentSite})
	call(t, f, admin(), Input{Command: CmdPublish, Site: contentSite, Files: []File{
		{Path: indexName, Content: new(contentHome)},
		{Path: "logo.bin", Content: new("AAEC"), Encoding: "base64"},
		{Path: "_redirects", Content: new("/a /b 301\n")},
	}})
	return f, &Content{Service: f.tool.Service, ToolAllowed: func(string) bool { return true }}
}

func editsOf(t *testing.T, find, replace string) []textpatch.Edit {
	t.Helper()
	var e textpatch.Edit
	raw, err := json.Marshal(map[string]string{"find": find, "replace": replace})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &e))
	return []textpatch.Edit{e}
}

func contentCallOK(t *testing.T, c *Content, ctx context.Context, in portalkit.SiteContentInput) map[string]any { //nolint:revive // the fixture leads
	t.Helper()
	res, _, err := c.Handle(ctx, in)
	require.NoError(t, err)
	body := text(t, res)
	require.False(t, res.IsError, body)
	out := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(body), &out), body)
	return out
}

func contentRefusal(t *testing.T, c *Content, ctx context.Context, in portalkit.SiteContentInput) string { //nolint:revive // the fixture leads
	t.Helper()
	res, _, err := c.Handle(ctx, in)
	require.NoError(t, err, "a refusal is a result, never a protocol error")
	require.True(t, res.IsError, "expected a refusal, got %s", text(t, res))
	return text(t, res)
}

func TestContent_ReadsAFileWithAnAssetsFields(t *testing.T) {
	_, c := seededContent(t)
	got := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionGetContent, Target: homeTarget})
	assert.Equal(t, contentHome, got[textpatch.FieldContent])
	assert.EqualValues(t, 1, got[fieldVersion])
	assert.Equal(t, homeTarget, got[fieldRef], "the answer names what was read")
	assert.Equal(t, "text/html; charset=utf-8", got[fieldType])

	for _, action := range []string{actionOutline, actionStats} {
		fields := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: action, Target: homeTarget})
		assert.EqualValues(t, 1, fields[fieldVersion], action)
	}
	found := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionLocate, Target: homeTarget, Find: "$99"})
	assert.EqualValues(t, 1, found[textpatch.FieldCount])
}

func TestContent_ReadsATree(t *testing.T) {
	_, c := seededContent(t)
	list := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionOutline, Target: treeTarget})
	assert.EqualValues(t, 3, list["total"])
	assert.Len(t, list["files"], 3)

	stats := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionStats, Target: treeTarget})
	assert.EqualValues(t, 3, stats["total"])

	found := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionLocate, Target: treeTarget, Find: "$99"})
	assert.Len(t, found["files"], 1)
	assert.EqualValues(t, 2, found["files_scanned"])
	assert.EqualValues(t, 1, found["files_skipped"])

	msg := contentRefusal(t, c, admin(), portalkit.SiteContentInput{Action: actionGetContent, Target: treeTarget})
	assert.Contains(t, msg, "outline")
}

func TestContent_PatchAndDiff(t *testing.T) {
	f, c := seededContent(t)
	dry := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionPatch, Target: homeTarget, Edits: editsOf(t, "$99", "$89"), DryRun: true})
	assert.Equal(t, true, dry["dry_run"])
	assert.EqualValues(t, 1, dry[fieldVersion])

	done := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionPatch, Target: homeTarget, Edits: editsOf(t, "$99", "$89")})
	assert.EqualValues(t, 2, done[fieldVersion])
	assert.Equal(t, "1 edit via patch", done["change_summary"], "a patch with no summary still reads as a change")
	assert.NotContains(t, done, textpatch.FieldContent, "the body does not cross the wire")
	st, err := f.tool.Service.Find(admin(), contentSite)
	require.NoError(t, err)
	assert.Equal(t, "1 edit via patch", f.store.releases[st.ID][0].Note)

	same := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionPatch, Target: homeTarget, Edits: editsOf(t, "$89", "$89"), ChangeSummary: "none"})
	assert.Equal(t, true, same["unchanged"])

	fileDiff := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionDiff, Target: homeTarget})
	assert.Contains(t, fileDiff[textpatch.FieldDiff], "+<h1>From $89</h1>")
	assert.EqualValues(t, 1, fileDiff["from_version"])

	treeDiff := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionDiff, Target: treeTarget, FromVersion: 1, ToVersion: 2})
	assert.Equal(t, []any{indexName}, treeDiff["changed"])
}

func TestContent_Refusals(t *testing.T) {
	f, c := seededContent(t)
	stranger := ctxAs("stranger@example.com", false, "")

	assert.Contains(t, contentRefusal(t, c, admin(), portalkit.SiteContentInput{Action: "share", Target: homeTarget}), "does not apply to a site file")
	assert.Contains(t, contentRefusal(t, c, admin(), portalkit.SiteContentInput{Action: actionGetContent, Target: site.RefPrefix + contentSite}), "<environment>")

	t.Run("no access", func(t *testing.T) {
		msg := contentRefusal(t, c, stranger, portalkit.SiteContentInput{Action: actionGetContent, Target: homeTarget})
		assert.NotContains(t, msg, "$99")
	})
	t.Run("a stale base keeps the edit grammar's code", func(t *testing.T) {
		msg := contentRefusal(t, c, admin(), portalkit.SiteContentInput{Action: actionPatch, Target: homeTarget, BaseVersion: 9, Edits: editsOf(t, "$99", "$1")})
		assert.Contains(t, msg, textpatch.CodeStaleBase)
	})
	t.Run("losing a race reads the same as a stale base", func(t *testing.T) {
		res := fail(site.ErrStale)
		assert.True(t, res.IsError)
		assert.Contains(t, text(t, res), textpatch.CodeStaleBase)
	})
	t.Run("a persona without manage_site cannot write, and can still read", func(t *testing.T) {
		denied := &Content{Service: f.tool.Service, ToolAllowed: func(string) bool { return false }}
		assert.Contains(t, contentRefusal(t, denied, admin(), portalkit.SiteContentInput{Action: actionPatch, Target: homeTarget, Edits: editsOf(t, "$99", "$1")}), "manage_site")
		contentCallOK(t, denied, admin(), portalkit.SiteContentInput{Action: actionGetContent, Target: homeTarget})
	})
	t.Run("with no persona check bound a write is refused", func(t *testing.T) {
		unbound := &Content{Service: f.tool.Service}
		assert.Contains(t, contentRefusal(t, unbound, admin(), portalkit.SiteContentInput{Action: actionPatch, Target: homeTarget, Edits: editsOf(t, "$99", "$1")}), "manage_site")
	})
	t.Run("sites turned off", func(t *testing.T) {
		off := false
		f.tool.Service.Config.Enabled = &off
		defer func() { f.tool.Service.Config.Enabled = nil }()
		assert.Contains(t, contentRefusal(t, c, admin(), portalkit.SiteContentInput{Action: actionGetContent, Target: homeTarget}), "turned off")
	})
}

// A read of a tree that cannot be served reports why, for every action a tree
// answers.
func TestContent_ATreeThatCannotBeRead(t *testing.T) {
	_, c := seededContent(t)
	stranger := ctxAs("stranger@example.com", false, "")
	for _, action := range []string{actionLocate, actionOutline, actionStats, actionDiff} {
		msg := contentRefusal(t, c, stranger, portalkit.SiteContentInput{Action: action, Target: treeTarget, Find: "x"})
		assert.NotEmpty(t, msg, action)
	}
	assert.NotEmpty(t, contentRefusal(t, c, admin(), portalkit.SiteContentInput{Action: actionLocate, Target: homeTarget, Section: "No such section", Find: "x"}))
	assert.NotEmpty(t, contentRefusal(t, c, admin(), portalkit.SiteContentInput{Action: actionDiff, Target: treeTarget, FromVersion: 8, ToVersion: 9}))
}

func TestPublish_WithABaseOverlaysAndDeletes(t *testing.T) {
	f, c := seededContent(t)
	call(t, f, admin(), Input{Command: CmdPublish, Site: contentSite, Base: 1, Files: []File{
		{Path: "added.html", Content: new("<p>added</p>\n")},
		{Path: "logo.bin", Delete: true},
	}})
	list := contentCallOK(t, c, admin(), portalkit.SiteContentInput{Action: actionOutline, Target: treeTarget})
	assert.EqualValues(t, 3, list["total"], "the page, the new file and the redirect map")
}

func TestPublish_AFileEntryMustSayWhatItIs(t *testing.T) {
	f, _ := seededContent(t)
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdPublish, Site: contentSite, Base: 1, Files: []File{{Path: "a.html"}}}), "content is required")
	assert.Contains(t, refusal(t, f, admin(), Input{Command: CmdPublish, Site: contentSite, Base: 1, Files: []File{{Path: "a.html", Content: new("x"), Delete: true}}}), "not both")
}
