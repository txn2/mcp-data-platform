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
)

type fakePrefixExtractor struct {
	got    PrefixExtraction
	claims resource.Claims
	out    *ExtractedArchive
	err    error
}

func (f *fakePrefixExtractor) ExtractPrefix(_ context.Context, req PrefixExtraction, claims resource.Claims) (*ExtractedArchive, error) {
	f.got, f.claims = req, claims
	return f.out, f.err
}

func prefixInput() manageResourceInput {
	return manageResourceInput{
		Action: resourceActionExtract, Path: "sites/media", IfExists: "replace", Description: "Site media",
		FromBucket: &BucketSource{Connection: "assets", Bucket: "web-media", Prefix: "drop/"},
	}
}

// extract with from_bucket writes a bucket prefix out as managed resources
// (#2593).
func TestExtractFromABucketPrefix(t *testing.T) {
	tk, _ := resourceToolkit(t)
	x := &fakePrefixExtractor{out: &ExtractedArchive{Format: "bucket prefix", Members: []ExtractedMember{
		landed("logo.svg", "mcp://user/u/sites/media/logo.svg", 1, true),
		landed("2026/chart.csv", "mcp://user/u/sites/media/2026/chart.csv", 1, true),
	}}}
	tk.SetPrefixExtractor(x)

	result := callResource(t, tk, prefixInput())
	require.False(t, result.IsError, errText(t, result))
	assert.Equal(t, PrefixExtraction{
		Source: BucketSource{Connection: "assets", Bucket: "web-media", Prefix: "drop/"},
		Path:   "sites/media", Replace: true, Description: "Site media", Tags: []string{},
	}, x.got)
	assert.NotEmpty(t, x.claims.Sub, "the call's own identity is what the extraction acts under")
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out prefixExtractOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	assert.Equal(t, "s3://web-media/drop/ on connection assets", out.Source)
	assert.Equal(t, 2, out.Count)
	assert.Contains(t, out.Message, "Extracted 2 files from s3://web-media/drop/ on connection assets: 2 created")
}

func TestExtractFromABucketPrefixRefusals(t *testing.T) {
	tk, _ := resourceToolkit(t)
	assert.Contains(t, errText(t, callResource(t, tk, prefixInput())), "cannot extract from a bucket", "no extractor bound")

	x := &fakePrefixExtractor{}
	tk.SetPrefixExtractor(x)
	for name, change := range map[string]func(*manageResourceInput){
		"an archive too":   func(in *manageResourceInput) { in.Reference = "mcp:resource:a" },
		"members":          func(in *manageResourceInput) { in.Members = "*.csv" },
		"no folder":        func(in *manageResourceInput) { in.Path = "" },
		"a bad if_exists":  func(in *manageResourceInput) { in.IfExists = "sometimes" },
		"an unknown index": func(in *manageResourceInput) { in.Index = "partly" },
	} {
		t.Run(name, func(t *testing.T) {
			in := prefixInput()
			change(&in)
			result := callResource(t, tk, in)
			assert.True(t, result.IsError, name)
		})
	}
	x.err = errors.New(`connection "assets" is not one your persona may use`)
	result := callResource(t, tk, prefixInput())
	assert.Contains(t, errText(t, result), "not one your persona may use")
	assert.Contains(t, errText(t, result), "Nothing was written")
}
