package portal

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// fakeExtractor records the request it was handed and answers what the test
// set.
type fakeExtractor struct {
	got    ArchiveExtraction
	claims resource.Claims
	out    *ExtractedArchive
	err    error
}

func (f *fakeExtractor) ExtractArchive(
	_ context.Context, req ArchiveExtraction, claims resource.Claims,
) (*ExtractedArchive, error) {
	f.got, f.claims = req, claims
	return f.out, f.err
}

func extractToolkit(t *testing.T) (*Toolkit, *fakeExtractor) {
	t.Helper()
	tk, _ := resourceToolkit(t)
	x := &fakeExtractor{}
	tk.SetResourceExtractor(x)
	return tk, x
}

func landed(member, uri string, version int, created bool, changes ...string) ExtractedMember {
	return ExtractedMember{Member: member, ResourceLanding: toolkit.ResourceLanding{
		ResourceID: "r-" + member, Reference: "mcp:resource:r-" + member, URI: uri,
		Version: version, Created: created, TableChanges: changes,
	}}
}

func extractInput() manageResourceInput {
	return manageResourceInput{
		Action: resourceActionExtract, Reference: "mcp:resource:arch1", Path: "pipelines/staging",
		Members: "*.csv", Filename: "delivery.csv", IfExists: "replace",
		Description: "Monthly feed", Tags: []string{"feed"}, ChangeSummary: "October delivery",
	}
}

func TestExtractHandsTheCallToTheExtractor(t *testing.T) {
	tk, x := extractToolkit(t)
	x.out = &ExtractedArchive{
		Format:  "zip",
		Members: []ExtractedMember{landed("export.csv", "mcp://user/u/pipelines/staging/delivery.csv", 2, false, "Table t followed onto version 2.")},
		Skipped: []string{"link"},
	}

	result := callResource(t, tk, extractInput())

	require.False(t, result.IsError, errText(t, result))
	assert.Equal(t, ArchiveExtraction{
		ArchiveID: "arch1", Path: "pipelines/staging", Members: "*.csv", Filename: "delivery.csv",
		Replace: true, Description: "Monthly feed", Tags: []string{"feed"}, ChangeSummary: "October delivery",
	}, x.got)
	assert.NotEmpty(t, x.claims.Sub, "the call's own identity is what the extraction acts under")

	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out extractOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	assert.Equal(t, "mcp:resource:arch1", out.Archive)
	assert.Equal(t, "zip", out.Format)
	require.Len(t, out.Members, 1)
	assert.Equal(t, "mcp:resource:r-export.csv", out.Members[0].Reference)
	assert.Equal(t, 2, out.Members[0].Version)
	assert.Equal(t, []string{"Table t followed onto version 2."}, out.Members[0].TableChanges)
	assert.Contains(t, out.Message, "Extracted 1 files from the zip archive: 0 created, 1 recorded as the next version")
	assert.Contains(t, out.Message, "Table t followed onto version 2.")
	assert.Contains(t, out.Message, "links or devices rather than files: link")
}

func TestExtractValidatesItsArguments(t *testing.T) {
	tk, x := extractToolkit(t)
	for name, tc := range map[string]struct {
		mutate func(*manageResourceInput)
		want   string
	}{
		"no reference":      {func(in *manageResourceInput) { in.Reference = "" }, "reference is required for extract"},
		"not a reference":   {func(in *manageResourceInput) { in.Reference = "delivery.zip" }, "not a reference this platform issues"},
		"an asset":          {func(in *manageResourceInput) { in.Reference = "mcp:asset:a1" }, "acts on managed resources only"},
		"no path":           {func(in *manageResourceInput) { in.Path = " " }, "path is required for extract"},
		"a bad if_exists":   {func(in *manageResourceInput) { in.IfExists = "overwrite" }, "is not one this tool takes"},
		"a failing extract": {func(*manageResourceInput) { x.err = errors.New("the archive is corrupt") }, "Nothing was written."},
	} {
		t.Run(name, func(t *testing.T) {
			x.err = nil
			in := extractInput()
			tc.mutate(&in)
			result := callResource(t, tk, in)
			require.True(t, result.IsError)
			assert.Contains(t, errText(t, result), tc.want)
		})
	}
}

// TestAPartialExtractionNamesWhatItWrote holds the result to the ticket's
// line: no written file goes unreported when an extraction stops part way.
func TestAPartialExtractionNamesWhatItWrote(t *testing.T) {
	tk, x := extractToolkit(t)
	x.out = &ExtractedArchive{Format: "zip", Members: []ExtractedMember{
		landed("a.csv", "mcp://user/u/staging/a.csv", 1, true),
	}}
	x.err = errors.New(`extracting mcp://user/u/pipelines/d.zip: corrupt archive: member "b.csv": zip: checksum error`)

	result := callResource(t, tk, extractInput())

	require.True(t, result.IsError)
	text := errText(t, result)
	assert.Contains(t, text, "zip: checksum error. The extraction stopped there")
	assert.Contains(t, text, "these 1 were written before it: a.csv as mcp://user/u/staging/a.csv (mcp:resource:r-a.csv, version 1)")
}

func TestExtractWithoutAnExtractor(t *testing.T) {
	tk, _ := resourceToolkit(t)
	result := callResource(t, tk, extractInput())
	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "cannot extract archives")
}

func TestExtractCreatesAndDefaultsToFail(t *testing.T) {
	tk, x := extractToolkit(t)
	x.out = &ExtractedArchive{Format: "tar.gz", Members: []ExtractedMember{
		landed("a.csv", "mcp://user/u/staging/a.csv", 1, true),
		landed("b.csv", "mcp://user/u/staging/b.csv", 1, true),
	}}
	in := extractInput()
	in.IfExists, in.Filename = "", ""
	result := callResource(t, tk, in)
	require.False(t, result.IsError, errText(t, result))
	assert.False(t, x.got.Replace)
	assert.Contains(t, errTextOrText(t, result), "Extracted 2 files from the tar.gz archive: 2 created, 0 recorded")
}

func errTextOrText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}
