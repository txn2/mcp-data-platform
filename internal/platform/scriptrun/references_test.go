package scriptrun

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const logoURI = "mcp://global/brand/logo.svg"

const referencesSource = `LOGO = "mcp://global/brand/logo.svg"
html = "<html><body><img src='" + LOGO + "'><img src=\"mcp://global/brand/mark.png\"></body></html>"
out = platform.export(name="report", rows=html, format="html", references=[LOGO])
print(out["references"], out["undeclared_references"])
`

// TestExport_ReferencesAreDeclaredOnTheWrittenAsset is #1834's one call: the
// version is written, then its references are declared through manage_asset
// update on the asset the write reported, so the author-can-read check is the
// one an agent's save gets.
func TestExport_ReferencesAreDeclaredOnTheWrittenAsset(t *testing.T) {
	caller := &registeringCaller{}
	exporter := &recordingExporter{}
	result, err := registerRun(t, referencesSource, caller, exporter, WritesMade)
	require.NoError(t, err)

	require.Len(t, exporter.requests, 1)
	assert.Equal(t, []string{logoURI}, exporter.requests[0].References)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, "manage_asset", caller.calls[0].name)
	assert.Equal(t, map[string]any{
		"action": "update", "asset_id": "asset_1", "references": []any{logoURI},
	}, caller.calls[0].args)

	require.Len(t, result.Exports, 1)
	assert.Equal(t, []string{logoURI}, result.Exports[0].References)
	assert.Equal(t, []string{"mcp://global/brand/mark.png"}, result.Exports[0].UndeclaredReferences)
	assert.Contains(t, result.Log, `["mcp://global/brand/logo.svg"] ["mcp://global/brand/mark.png"]`)
	assert.Contains(t, result.Log, "undeclared_references: report: mcp://global/brand/mark.png",
		"the run log says which reference will not load, whether or not the script prints the result")
	assert.Empty(t, result.Writes, "a platform run records no write list")
}

// TestExport_ReferencesInAWriteReportingDraftAreListed: a draft allowed to
// write declares them and lists the call beside its other writes.
func TestExport_ReferencesInAWriteReportingDraftAreListed(t *testing.T) {
	result, err := registerRun(t, referencesSource, &registeringCaller{}, &recordingExporter{}, WritesReported)
	require.NoError(t, err)
	assert.Equal(t, []WriteRecord{{Tool: "manage_asset", Call: "manage_asset action=update"}}, result.Writes)
}

// TestExport_ReferencesInAPreviewAreReportedNotDeclared: a draft that wrote
// nothing has no asset to declare them on, and makes no call.
func TestExport_ReferencesInAPreviewAreReportedNotDeclared(t *testing.T) {
	caller := &registeringCaller{}
	result, err := registerRun(t, referencesSource, caller, nil, WritesRefused)
	require.NoError(t, err)
	assert.Empty(t, caller.calls)
	require.Len(t, result.Exports, 1)
	assert.True(t, result.Exports[0].Preview)
	assert.Equal(t, []string{logoURI}, result.Exports[0].References)
	assert.Equal(t, []string{"mcp://global/brand/mark.png"}, result.Exports[0].UndeclaredReferences)
}

// TestExport_EmptyReferencesClearThem: an empty list is a decision, sent as
// one, and reported as an empty list rather than as nothing.
func TestExport_EmptyReferencesClearThem(t *testing.T) {
	caller := &registeringCaller{}
	result, err := registerRun(t,
		`out = platform.export(name="r", rows="<p>plain</p>", format="html", references=[])
print("refs", out["references"])`,
		caller, &recordingExporter{}, WritesMade)
	require.NoError(t, err)
	require.Len(t, caller.calls, 1)
	assert.Equal(t, []any{}, caller.calls[0].args["references"])
	assert.Equal(t, []string{}, result.Exports[0].References)
	assert.Contains(t, result.Log, "refs []")
}

// TestExport_NoReferencesArgumentLeavesTheAssetAlone: an export that passed
// no references= makes no declaration, so a reference declared on the asset
// another way survives the run; the body is still read and the note made.
func TestExport_NoReferencesArgumentLeavesTheAssetAlone(t *testing.T) {
	caller := &registeringCaller{}
	result, err := registerRun(t,
		`out = platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html")
print("refs" in out, out["undeclared_references"])`,
		caller, &recordingExporter{}, WritesMade)
	require.NoError(t, err)
	assert.Empty(t, caller.calls)
	assert.Nil(t, result.Exports[0].References)
	assert.Contains(t, result.Log, `False ["mcp://global/brand/logo.svg"]`)
}

// TestExport_ReferenceRefusalFailsTheRunNamingTheOutput: the author cannot
// read what they declared, and the run stops where they read why.
func TestExport_ReferenceRefusalFailsTheRunNamingTheOutput(t *testing.T) {
	caller := &registeringCaller{err: errors.New(`you cannot read the resource at "mcp://global/brand/logo.svg"`)}
	_, err := registerRun(t, referencesSource, caller, &recordingExporter{}, WritesMade)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `output "report" was written, and declaring its references failed`)
	assert.Contains(t, err.Error(), "you cannot read the resource")
}

// TestExport_ReferenceRefusalsPrecedeTheWrite: what references= cannot be is
// refused before anything is written.
func TestExport_ReferenceRefusalsPrecedeTheWrite(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{
			"a tabular output",
			`platform.export(name="o", rows=[{"a": 1}], format="csv", references=["mcp://a/b.png"])`,
			"references are declared on a portal document",
		},
		{
			"a bucket destination",
			`platform.export(name="o", rows="<p/>", format="html", destination="acme-drop", references=["mcp://a/b.png"])`,
			"references are declared on a portal document",
		},
		{
			"not a list",
			`platform.export(name="o", rows="<p/>", format="html", references="mcp://a/b.png")`,
			"references must be a list of reference strings",
		},
		{
			"a non-string entry",
			`platform.export(name="o", rows="<p/>", format="html", references=[3])`,
			"each reference is a managed resource's mcp:// URI",
		},
		{
			"a blank entry",
			`platform.export(name="o", rows="<p/>", format="html", references=["  "])`,
			"each reference is a managed resource's mcp:// URI",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := &recordingExporter{}
			caller := &registeringCaller{}
			_, err := registerRun(t, tt.source, caller, exporter, WritesMade)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, exporter.requests, "the refusal precedes the write")
			assert.Empty(t, caller.calls)
		})
	}
}

// TestExport_ReferenceEntriesAreTrimmed: the URI is what the rewrite matches,
// so a padded entry is declared as the string the markup holds.
func TestExport_ReferenceEntriesAreTrimmed(t *testing.T) {
	caller := &registeringCaller{}
	_, err := registerRun(t,
		`platform.export(name="r", rows="<img src='mcp://a/b.png'>", format="html", references=["  mcp://a/b.png "])`,
		caller, &recordingExporter{}, WritesMade)
	require.NoError(t, err)
	assert.Equal(t, []any{"mcp://a/b.png"}, caller.calls[0].args["references"])
}

// TestValidate_ReportsUndeclaredReferences: a literal reference string no
// export declares is a warning with the export to fix, and the declaration is
// a manage_asset call the report names.
func TestValidate_ReportsUndeclaredReferences(t *testing.T) {
	report := Validate(`LOGO = "mcp://global/brand/logo.svg"
html = "<img src='" + LOGO + "'><script src='mcp:asset:ast_7c1e'></script>"
platform.export(name="r", rows=html, format="html", references=["mcp:asset:ast_7c1e"])`)
	assert.True(t, report.OK, "a warning does not stop the script from running")
	assert.Equal(t, []string{"manage_asset"}, report.Tools)
	require.Len(t, report.Findings, 1)
	f := report.Findings[0]
	assert.Equal(t, SeverityWarning, f.Severity)
	assert.Equal(t, 1, f.Line)
	assert.Contains(t, f.Message, `"mcp://global/brand/logo.svg" is not declared`)
	assert.Contains(t, f.Hint, `references=["mcp://global/brand/logo.svg"]`)
}

// TestValidate_UndeclaredReferenceWarningsStayQuietWhereTheyWouldGuess: no
// export, a computed declaration, and a tool argument are not documents.
func TestValidate_UndeclaredReferenceWarningsStayQuietWhereTheyWouldGuess(t *testing.T) {
	tests := []struct{ name, source string }{
		{"no export", `platform.call("manage_asset", {"action": "get", "asset_id": "x"})
note = "mcp://global/brand/logo.svg"`},
		{"a computed declaration", `refs = ["mcp://global/brand/logo.svg"]
platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", references=refs)`},
		{"a spread export", `kw = {"references": ["mcp://global/brand/logo.svg"]}
platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", **kw)`},
		{"a tool argument", `platform.call("manage_resource", {"action": "get", "uri": "mcp://global/brand/logo.svg"})
platform.export(name="r", rows="<p>ok</p>", format="html")`},
		{"declared", `platform.export(name="r", rows="<img src='mcp://global/brand/logo.svg'>", format="html", references=("mcp://global/brand/logo.svg",))`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := Validate(tt.source)
			for _, f := range report.Findings {
				assert.NotContains(t, f.Message, "is not declared", f)
			}
		})
	}
}
