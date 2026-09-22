package exportrefs_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/syntax"

	"github.com/txn2/mcp-data-platform/internal/platform/exportrefs"
)

func TestParse(t *testing.T) {
	t.Parallel()
	refs, err := exportrefs.Parse([]any{" mcp://a/b.png ", "mcp:asset:x1"})
	require.NoError(t, err)
	assert.Equal(t, []string{"mcp://a/b.png", "mcp:asset:x1"}, refs)

	refs, err = exportrefs.Parse([]any{})
	require.NoError(t, err)
	assert.Equal(t, []string{}, refs, "an empty list is a decision, returned as one")

	_, err = exportrefs.Parse("mcp://a/b.png")
	require.ErrorContains(t, err, "references must be a list of reference strings")
	_, err = exportrefs.Parse([]any{3})
	require.ErrorContains(t, err, "each reference is a managed resource's mcp:// URI")
	_, err = exportrefs.Parse([]any{"  "})
	require.ErrorContains(t, err, "each reference is a managed resource's mcp:// URI")
}

func TestArgs(t *testing.T) {
	t.Parallel()
	assert.Equal(t, map[string]any{"action": "update", "asset_id": "a1", "references": []any{"mcp://a/b.png"}},
		exportrefs.Args("a1", []string{"mcp://a/b.png"}))
	assert.Equal(t, []any{}, exportrefs.Args("a1", []string{})["references"], "a clearing is sent as an empty array")
	assert.Equal(t, "manage_asset action=update", exportrefs.Call)
}

func TestLogLine(t *testing.T) {
	t.Parallel()
	assert.Equal(t,
		"undeclared_references: report: mcp://a/b.png, mcp:asset:x1 (not declared by this export; unless the asset declares them some other way they are served as written and resolve to nothing)",
		exportrefs.LogLine("report", []string{"mcp://a/b.png", "mcp:asset:x1"}))
}

func parse(t *testing.T, source string) *syntax.File {
	t.Helper()
	file, err := (&syntax.FileOptions{Set: true, While: true, TopLevelControl: true, GlobalReassign: true}).Parse("s", source, 0)
	require.NoError(t, err)
	return file
}

func TestInSource(t *testing.T) {
	t.Parallel()
	file := parse(t, `LOGO = "mcp://global/brand/logo.svg"
html = "<img src='" + LOGO + "'><script src='mcp:asset:ast_7c1e'></script><img src='mcp://global/brand/logo.svg'>"
platform.export(name="r", rows=html, format="html", references=["mcp:asset:ast_7c1e"])`)
	got := exportrefs.InSource(file, "export", "call")
	require.Equal(t, []exportrefs.Literal{{URI: "mcp://global/brand/logo.svg", Line: 1}}, got,
		"each undeclared reference once, at its first line; the declared one not at all")
	assert.Contains(t, got[0].Message(), `"mcp://global/brand/logo.svg" is not declared`)
	assert.Contains(t, got[0].Hint(), `references=["mcp://global/brand/logo.svg"]`)
}

func TestInSourceStaysQuietWhereItWouldGuess(t *testing.T) {
	t.Parallel()
	tests := []struct{ name, source string }{
		{"no export", `note = "mcp://global/brand/logo.svg"`},
		{"a computed declaration", `refs = ["mcp://global/brand/logo.svg"]
platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", references=refs)`},
		{"a name reassigned", `u = "mcp://global/brand/logo.svg"
u = "mcp://global/brand/other.svg"
platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", references=[u])`},
		{"a name bound to a call", `u = str(1)
platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", references=[u])`},
		{"a non-string element", `platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", references=[1])`},
		{"a spread export", `kw = {"references": ["mcp://global/brand/logo.svg"]}
platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", **kw)`},
		{"a tool argument", `platform.call("manage_resource", {"action": "get", "uri": "mcp://global/brand/logo.svg"})
platform.export(name="r", rows="<p>ok</p>", format="html")`},
		{"declared in a tuple", `platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", references=("mcp://global/brand/logo.svg",))`},
		{"declared in parentheses", `platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", references=(["mcp://global/brand/logo.svg"]))`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, exportrefs.InSource(parse(t, tt.source), "export", "call"))
		})
	}
}

func TestInSourceReadsOnlyThePlatformModule(t *testing.T) {
	t.Parallel()
	file := parse(t, `other.call("x", "mcp://a/b.png")
f = lambda: 1
platform.export(name="r", rows="<p/>", format="html", key=3)`)
	assert.Equal(t, []exportrefs.Literal{{URI: "mcp://a/b.png", Line: 1}}, exportrefs.InSource(file, "export", "call"),
		"a call on another module is not the platform's call member")
}

func TestDeclares(t *testing.T) {
	t.Parallel()
	with := parse(t, `platform.export(name="r", rows="<p/>", format="html", references=[])`)
	without := parse(t, `platform.export(name="r", rows="<p/>", format="html")`)
	call := func(f *syntax.File) *syntax.CallExpr {
		stmt, ok := f.Stmts[0].(*syntax.ExprStmt)
		require.True(t, ok)
		c, ok := stmt.X.(*syntax.CallExpr)
		require.True(t, ok)
		return c
	}
	assert.True(t, exportrefs.Declares(call(with)))
	assert.False(t, exportrefs.Declares(call(without)))
}

// TestInSourceReadsANamedConstant is the shape a report script is written in:
// the file named once, used in the markup and in the declaration.
func TestInSourceReadsANamedConstant(t *testing.T) {
	t.Parallel()
	file := parse(t, `LOGO = "mcp://global/brand/logo.svg"
html = "<img src='" + LOGO + "'><img src='mcp://global/brand/mark.png'>"
platform.export(name="r", rows=html, format="html", references=[LOGO])`)
	assert.Equal(t, []exportrefs.Literal{{URI: "mcp://global/brand/mark.png", Line: 2}},
		exportrefs.InSource(file, "export", "call"))
}
