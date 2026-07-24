package portal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// keyedS3 stores objects by bucket/key so a patch round-trips through the same
// path the real client takes: the write lands under a new key and the next read
// must see it. A single fixed body would hide every key-routing bug.
type keyedS3 struct {
	objects map[string][]byte
	types   map[string]string
	getErr  error
}

func newKeyedS3() *keyedS3 {
	return &keyedS3{objects: map[string][]byte{}, types: map[string]string{}}
}

func (*keyedS3) ref(bucket, key string) string { return bucket + "/" + key }

func (s *keyedS3) PutObject(_ context.Context, bucket, key string, body []byte, ct string) error {
	s.objects[s.ref(bucket, key)] = append([]byte(nil), body...)
	s.types[s.ref(bucket, key)] = ct
	return nil
}

func (s *keyedS3) PutObjectStream(ctx context.Context, bucket, key string, body io.Reader, ct string) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("reading stream: %w", err)
	}
	return int64(len(data)), s.PutObject(ctx, bucket, key, data, ct)
}

func (s *keyedS3) GetObject(_ context.Context, bucket, key string) (body []byte, contentType string, err error) {
	if s.getErr != nil {
		return nil, "", s.getErr
	}
	data, ok := s.objects[s.ref(bucket, key)]
	if !ok {
		return nil, "", notFoundError{}
	}
	return data, s.types[s.ref(bucket, key)], nil
}

func (s *keyedS3) DeleteObject(_ context.Context, bucket, key string) error {
	delete(s.objects, s.ref(bucket, key))
	return nil
}

func (*keyedS3) Close() error { return nil }

var _ portal.S3Client = (*keyedS3)(nil)

// patchReport is the multi-section document the patch tests edit.
const patchReport = `# Quarterly Report

## Findings

Revenue grew 12% year over year.

## Methodology

We sampled 100 accounts.

## Appendix

See the table above.
`

// patchFixture is a toolkit wired to in-memory stores holding one text asset.
type patchFixture struct {
	tk      *Toolkit
	assets  *inMemoryAssetStore
	s3      *keyedS3
	assetID string
	ctx     context.Context
}

// newPatchFixture seeds an owned asset with body and returns the wired toolkit.
func newPatchFixture(t *testing.T, body, contentType string) *patchFixture {
	t.Helper()
	assets := newInMemoryAssetStore()
	versions := newLinkedVersionStore(assets)
	s3 := newKeyedS3()
	tk := New(Config{
		Name: "test", AssetStore: assets, VersionStore: versions,
		S3Client: s3, S3Bucket: "bucket", S3Prefix: "assets/",
	})

	const assetID = "asset-1"
	key := "assets/user1/asset-1/content.md"
	require.NoError(t, s3.PutObject(context.Background(), "bucket", key, []byte(body), contentType))
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID: assetID, OwnerID: "user1", OwnerEmail: "user1@example.com",
		Name: "Report", ContentType: contentType,
		S3Bucket: "bucket", S3Key: key, SizeBytes: int64(len(body)), CurrentVersion: 1,
	}))
	_, err := versions.CreateVersion(context.Background(), portal.AssetVersion{
		ID: "v1", AssetID: assetID, S3Key: key, S3Bucket: "bucket",
		ContentType: contentType, SizeBytes: int64(len(body)), ChangeSummary: "Initial version",
	})
	require.NoError(t, err)

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", UserEmail: "user1@example.com",
	})
	return &patchFixture{tk: tk, assets: assets, s3: s3, assetID: assetID, ctx: ctx}
}

// call runs a manage_asset action and decodes the JSON result.
func (f *patchFixture) call(t *testing.T, input manageAssetInput) (map[string]any, *mcp.CallToolResult) {
	t.Helper()
	if input.AssetID == "" {
		input.AssetID = f.assetID
	}
	result, _, err := f.tk.handleManageAsset(f.ctx, nil, input)
	require.NoError(t, err)
	require.NotNil(t, result)

	var decoded map[string]any
	if !result.IsError && len(result.Content) > 0 {
		tc, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok)
		require.NoError(t, json.Unmarshal([]byte(tc.Text), &decoded))
	}
	return decoded, result
}

// storedBody returns the asset's current bytes as the toolkit would read them.
func (f *patchFixture) storedBody(t *testing.T) string {
	t.Helper()
	asset, err := f.assets.Get(context.Background(), f.assetID)
	require.NoError(t, err)
	data, _, err := f.s3.GetObject(context.Background(), asset.S3Bucket, asset.S3Key)
	require.NoError(t, err)
	return string(data)
}

// errorText joins the text blocks of a tool result.
func errorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	var sb strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

func TestManageAssetOutline(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	got, result := f.call(t, manageAssetInput{Action: actionOutline})
	require.False(t, result.IsError, errorText(t, result))

	sections, ok := got["sections"].([]any)
	require.True(t, ok)
	require.Len(t, sections, 4)
	first, ok := sections[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "# Quarterly Report", first["heading"])
	assert.Equal(t, float64(1), got["version"])
	assert.Equal(t, float64(len(patchReport)), got["size_bytes"])
}

func TestManageAssetStatsCarriesNoBody(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	got, result := f.call(t, manageAssetInput{Action: actionStats})
	require.False(t, result.IsError, errorText(t, result))

	assert.Equal(t, float64(len(patchReport)), got["size_bytes"])
	assert.Equal(t, textpatch.DocStats(patchReport).Hash, got["hash"])
	assert.NotContains(t, got, "content")
}

func TestManageAssetGetContentSpans(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	whole, _ := f.call(t, manageAssetInput{Action: actionGetContent})
	assert.Equal(t, patchReport, whole["content"])

	section, _ := f.call(t, manageAssetInput{Action: actionGetContent, Section: "## Methodology"})
	assert.Equal(t, "## Methodology\n\nWe sampled 100 accounts.\n\n", section["content"])
	assert.NotNil(t, section["section"])

	lines, _ := f.call(t, manageAssetInput{Action: actionGetContent, LineStart: 3, LineEnd: 3})
	assert.Equal(t, "## Findings\n", lines["content"])

	_, result := f.call(t, manageAssetInput{Action: actionGetContent, Section: "Nowhere"})
	assert.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), textpatch.CodeSectionNotFound)
}

func TestManageAssetLocate(t *testing.T) {
	f := newPatchFixture(t, "## A\n\nsee it\n\n## B\n\nsee it\n", "text/markdown")

	got, result := f.call(t, manageAssetInput{Action: actionLocate, Find: "see it"})
	require.False(t, result.IsError, errorText(t, result))
	assert.Equal(t, float64(2), got["count"])

	matches, ok := got["matches"].([]any)
	require.True(t, ok)
	require.Len(t, matches, 2)
	first, ok := matches[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(3), first["line"])
	assert.Equal(t, "## A", first["section"])

	scoped, _ := f.call(t, manageAssetInput{Action: actionLocate, Find: "see it", Section: "## B"})
	assert.Equal(t, float64(1), scoped["count"])
}

func TestManageAssetPatchWritesANewVersion(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	got, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits: []textpatch.Edit{{
			Find:    "Revenue grew 12% year over year",
			Replace: "Revenue grew 14% year over year",
		}},
		ChangeSummary: "correct the YoY figure",
	})
	require.False(t, result.IsError, errorText(t, result))

	assert.Equal(t, float64(2), got["version"])
	assert.NotContains(t, got, "content", "the response never echoes the new body")
	assert.Contains(t, got["diff"], "+Revenue grew 14% year over year.")
	diff, isString := got["diff"].(string)
	require.True(t, isString)
	assert.Less(t, len(diff), 1024)

	stored := f.storedBody(t)
	assert.Equal(t, strings.Replace(patchReport,
		"Revenue grew 12% year over year", "Revenue grew 14% year over year", 1), stored,
		"no other part of the document is altered")

	versions, _, err := f.tk.versionStore.ListByAsset(context.Background(), f.assetID, 10, 0)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, "correct the YoY figure", versions[1].ChangeSummary)
}

// jsxDashboardAsset is a headingless JSX dashboard, the #1039 target that a
// markdown heading grammar cannot address.
const jsxDashboardAsset = `<Dashboard>
  <Card data-region="revenue"><h3>Revenue</h3><Value>$1.2M</Value></Card>
  <Card data-region="users"><h3>Users</h3><Value>18,204</Value></Card>
</Dashboard>`

// TestManageAssetPatchJSXCardBySelector is the #1039 acceptance criterion driven
// through the real manage_asset dispatch: rewriting one card addressed by
// selector changes only that element's stored bytes and leaves its sibling
// byte-for-byte intact, verified against the stored version.
func TestManageAssetPatchJSXCardBySelector(t *testing.T) {
	f := newPatchFixture(t, jsxDashboardAsset, "text/jsx")

	got, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits: []textpatch.Edit{{
			Op:       textpatch.OpReplaceSection,
			Selector: `[data-region="users"]`,
			Text:     `<Card data-region="users"><h3>Active Users</h3><Value>19,001</Value></Card>`,
		}},
	})
	require.False(t, result.IsError, errorText(t, result))
	assert.Equal(t, float64(2), got["version"])

	stored := f.storedBody(t)
	assert.Contains(t, stored, "Active Users")
	assert.Contains(t, stored, "19,001")
	assert.Contains(t, stored, `<Card data-region="revenue"><h3>Revenue</h3><Value>$1.2M</Value></Card>`,
		"the sibling card is untouched")
	assert.NotContains(t, stored, "18,204")

	// A revert restores the exact prior bytes, proving the patch is an ordinary
	// version.
	_, result = f.call(t, manageAssetInput{Action: actionRevert, Version: 1})
	require.False(t, result.IsError, errorText(t, result))
	assert.Equal(t, jsxDashboardAsset, f.storedBody(t))
}

// TestManageAssetOutlineJSXReportsLandmarks: outline on a headingless JSX asset
// returns a non-empty landmark list through the real dispatch.
func TestManageAssetOutlineJSXReportsLandmarks(t *testing.T) {
	f := newPatchFixture(t, jsxDashboardAsset, "text/jsx")

	got, result := f.call(t, manageAssetInput{Action: actionOutline})
	require.False(t, result.IsError, errorText(t, result))

	landmarks, ok := got["landmarks"].([]any)
	require.True(t, ok, "an HTML/JSX outline reports landmarks")
	require.NotEmpty(t, landmarks)
	first, ok := landmarks[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "Card", first["tag"])
	assert.Equal(t, `Card[data-region="revenue"]`, first["selector"])
}

// TestManageAssetSelectorRefusedOnMarkdown: a selector against a markdown asset
// is refused, naming the section alternative, through the real dispatch.
func TestManageAssetSelectorRefusedOnMarkdown(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	_, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits:  []textpatch.Edit{{Op: textpatch.OpReplaceSection, Selector: ".card", Text: "x"}},
	})
	require.True(t, result.IsError)
	body := errorText(t, result)
	assert.Contains(t, body, textpatch.CodeBadEdit)
	assert.Contains(t, body, "section")
	assert.Equal(t, patchReport, f.storedBody(t), "nothing is written")
}

func TestManageAssetPatchGeneratesAChangeSummary(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	got, result := f.call(t, manageAssetInput{Action: actionPatch, Edits: []textpatch.Edit{
		{Find: "We sampled 100 accounts.", Replace: "We sampled 250 accounts."},
		{Op: textpatch.OpAppend, Text: "\nRevised.\n"},
	}})
	require.False(t, result.IsError, errorText(t, result))
	assert.Equal(t, "2 edits via patch", got["change_summary"])

	single, _ := f.call(t, manageAssetInput{Action: actionPatch, Edits: []textpatch.Edit{
		{Find: "Revised.", Replace: "Revised twice."},
	}})
	assert.Equal(t, "1 edit via patch", single["change_summary"])
}

func TestManageAssetPatchDryRunWritesNothing(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	got, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		DryRun: true,
		Edits:  []textpatch.Edit{{Find: "We sampled 100 accounts.", Replace: "We sampled 250 accounts."}},
	})
	require.False(t, result.IsError, errorText(t, result))

	assert.Equal(t, true, got["dry_run"])
	assert.Equal(t, float64(1), got["version"], "the current version is unchanged")
	assert.Contains(t, got["diff"], "+We sampled 250 accounts.")
	assert.Equal(t, patchReport, f.storedBody(t))

	versions, _, err := f.tk.versionStore.ListByAsset(context.Background(), f.assetID, 10, 0)
	require.NoError(t, err)
	assert.Len(t, versions, 1, "a dry run creates no version")
}

func TestManageAssetPatchDryRunReportsTheSameAsARealRun(t *testing.T) {
	edits := []textpatch.Edit{{Find: "See the table above.", Replace: "See Table 1."}}

	dry := newPatchFixture(t, patchReport, "text/markdown")
	dryGot, _ := dry.call(t, manageAssetInput{Action: actionPatch, DryRun: true, Edits: edits})

	live := newPatchFixture(t, patchReport, "text/markdown")
	liveGot, _ := live.call(t, manageAssetInput{Action: actionPatch, Edits: edits})

	assert.Equal(t, dryGot["diff"], liveGot["diff"])
	assert.Equal(t, dryGot["edits"], liveGot["edits"])
	assert.Equal(t, dryGot["size_bytes"], liveGot["size_bytes"])
}

func TestManageAssetPatchRefusesAmbiguousAnchor(t *testing.T) {
	f := newPatchFixture(t, "alpha x beta x\n", "text/markdown")

	_, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits:  []textpatch.Edit{{Find: "x", Replace: "y"}},
	})
	require.True(t, result.IsError)
	text := errorText(t, result)
	assert.Contains(t, text, textpatch.CodeAmbiguous)
	assert.Contains(t, text, "matches 2 spans")
	assert.Equal(t, "alpha x beta x\n", f.storedBody(t), "nothing is written")
}

func TestManageAssetPatchRefusesStaleBase(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	_, result := f.call(t, manageAssetInput{
		Action:      actionPatch,
		BaseVersion: 5,
		Edits:       []textpatch.Edit{{Find: "See the table above.", Replace: "See Table 1."}},
	})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), textpatch.CodeStaleBase)
	assert.Equal(t, patchReport, f.storedBody(t))

	// The matching base version is accepted.
	_, ok := f.call(t, manageAssetInput{
		Action:      actionPatch,
		BaseVersion: 1,
		Edits:       []textpatch.Edit{{Find: "See the table above.", Replace: "See Table 1."}},
	})
	assert.False(t, ok.IsError, errorText(t, ok))
}

func TestManageAssetTextVerbsRefuseBinaryContent(t *testing.T) {
	f := newPatchFixture(t, "%PDF-1.7 binary bytes", "application/pdf")

	for _, action := range []string{actionPatch, actionOutline, actionStats, actionGetContent, actionLocate} {
		_, result := f.call(t, manageAssetInput{
			Action: action,
			Find:   "binary",
			Edits:  []textpatch.Edit{{Find: "binary", Replace: "text"}},
		})
		require.True(t, result.IsError, action)
		assert.Contains(t, errorText(t, result), textpatch.CodeNotText, action)
	}
	assert.Equal(t, "%PDF-1.7 binary bytes", f.storedBody(t), "the stored bytes are unchanged")
}

func TestManageAssetPatchRequiresOwnership(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	f.ctx = middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "intruder", UserEmail: "intruder@example.com",
	})

	_, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits:  []textpatch.Edit{{Find: "See the table above.", Replace: "See Table 1."}},
	})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), "access")
	assert.Equal(t, patchReport, f.storedBody(t))
}

// TestManageAssetPatchAllowsAdmin is the #1042 asset-side regression: an admin
// is unrestricted by design and patches an asset they do not own, matching the
// admin-wide access the platform grants everywhere else.
func TestManageAssetPatchAllowsAdmin(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	f.ctx = middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "operator", UserEmail: "operator@example.com", IsAdmin: true,
	})

	_, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits:  []textpatch.Edit{{Find: "See the table above.", Replace: "See Table 1."}},
	})
	require.False(t, result.IsError, errorText(t, result))
	assert.Contains(t, f.storedBody(t), "See Table 1.", "admin's patch landed on another user's asset")
}

// TestManageAssetReadVerbsAllowAdmin: an admin reads another user's asset
// through the content verbs (canReadAsset admits the admin).
func TestManageAssetReadVerbsAllowAdmin(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	f.ctx = middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "operator", UserEmail: "operator@example.com", IsAdmin: true,
	})

	got, result := f.call(t, manageAssetInput{Action: actionGetContent})
	require.False(t, result.IsError, errorText(t, result))
	assert.Equal(t, patchReport, got["content"])
}

func TestManageAssetContentVerbsRequireAnAssetID(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	for _, action := range []string{actionPatch, actionOutline, actionStats, actionGetContent, actionLocate, actionDiff} {
		result, _, err := f.tk.handleManageAsset(f.ctx, nil, manageAssetInput{Action: action})
		require.NoError(t, err)
		require.True(t, result.IsError, action)
		assert.Contains(t, errorText(t, result), middleware.CodeMissingParameter, action)
	}
}

func TestManageAssetDiffBetweenVersions(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	_, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits:  []textpatch.Edit{{Find: "We sampled 100 accounts.", Replace: "We sampled 250 accounts."}},
	})
	require.False(t, result.IsError, errorText(t, result))

	got, result := f.call(t, manageAssetInput{Action: actionDiff})
	require.False(t, result.IsError, errorText(t, result))
	assert.Equal(t, float64(1), got["from_version"])
	assert.Equal(t, float64(2), got["to_version"])
	diff, ok := got["diff"].(string)
	require.True(t, ok)
	assert.Contains(t, diff, "--- v1")
	assert.Contains(t, diff, "-We sampled 100 accounts.")
	assert.Contains(t, diff, "+We sampled 250 accounts.")
	assert.NotContains(t, diff, "Quarterly Report", "only the changed hunk is reported")
}

func TestManageAssetDiffNeedsAnEarlierVersion(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	_, result := f.call(t, manageAssetInput{Action: actionDiff})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), "no earlier version")

	_, result = f.call(t, manageAssetInput{Action: actionDiff, FromVersion: 1, ToVersion: 9})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), "version 9 not found")
}

func TestManageAssetPatchThenRevertRestoresTheBody(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	_, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits:  []textpatch.Edit{{Op: textpatch.OpReplaceSection, Section: "## Appendix", Text: "## Appendix\n\nRewritten.\n"}},
	})
	require.False(t, result.IsError, errorText(t, result))
	assert.Contains(t, f.storedBody(t), "Rewritten.")

	_, result = f.call(t, manageAssetInput{Action: actionRevert, Version: 1})
	require.False(t, result.IsError, errorText(t, result))
	assert.Equal(t, patchReport, f.storedBody(t), "patch produces an ordinary version, so revert still works")
}

func TestManageAssetPatchRespectsMaxContentSize(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	f.tk.maxContentSize = len(patchReport) + 5

	_, result := f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits:  []textpatch.Edit{{Op: textpatch.OpAppend, Text: strings.Repeat("x", 100)}},
	})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), textpatch.CodeTooLarge)
	assert.Equal(t, patchReport, f.storedBody(t))
}

func TestManageAssetContentVerbsReportAMissingObject(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	f.s3.getErr = notFoundError{}

	_, result := f.call(t, manageAssetInput{Action: actionOutline})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), "failed to read asset content")
}

func TestManageAssetLocateReportsABadQuery(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")

	_, result := f.call(t, manageAssetInput{Action: actionLocate})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), textpatch.CodeBadEdit)

	_, result = f.call(t, manageAssetInput{Action: actionLocate, Pattern: "("})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), textpatch.CodeBadPattern)
}

func TestManageAssetContentVerbsRefuseADeletedAsset(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	require.NoError(t, f.assets.SoftDelete(context.Background(), f.assetID))

	_, result := f.call(t, manageAssetInput{Action: actionOutline})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), "deleted")
}

func TestManageAssetContentVerbsFollowShareGrants(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	f.tk.shareStore = &grantingShareStore{}
	f.ctx = middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "viewer", UserEmail: "viewer@example.com",
	})

	got, result := f.call(t, manageAssetInput{Action: actionGetContent})
	require.False(t, result.IsError, errorText(t, result))
	assert.Equal(t, patchReport, got["content"], "a share grant is enough to read")

	// Reading is not writing: the patch still requires ownership.
	_, result = f.call(t, manageAssetInput{
		Action: actionPatch,
		Edits:  []textpatch.Edit{{Find: "See the table above.", Replace: "See Table 1."}},
	})
	assert.True(t, result.IsError)
}

func TestManageAssetContentVerbsRequireObjectStorage(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	f.tk.s3Client = nil

	_, result := f.call(t, manageAssetInput{Action: actionStats})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), "content storage not configured")
}

func TestManageAssetDiffRefusesANonTextVersion(t *testing.T) {
	f := newPatchFixture(t, "%PDF-1.7", "application/pdf")
	_, err := f.tk.versionStore.CreateVersion(context.Background(), portal.AssetVersion{
		ID: "v2", AssetID: f.assetID, S3Key: "assets/user1/asset-1/v2/content.pdf",
		S3Bucket: "bucket", ContentType: "application/pdf",
	})
	require.NoError(t, err)

	_, result := f.call(t, manageAssetInput{Action: actionDiff, FromVersion: 1, ToVersion: 2})
	require.True(t, result.IsError)
	assert.Contains(t, errorText(t, result), textpatch.CodeNotText)
}

// grantingShareStore grants a direct viewer share on every asset, which is the
// state a shared-with-me asset is in.
type grantingShareStore struct {
	portal.ShareStore
}

func (grantingShareStore) GetActiveShareForTarget(_ context.Context, _, targetID, _, _ string) (*portal.Share, error) {
	return &portal.Share{AssetID: targetID, Permission: portal.PermissionViewer}, nil
}

func TestManageAssetSchemaAdvertisesTheSharedGrammar(t *testing.T) {
	var schema map[string]any
	require.NoError(t, json.Unmarshal(manageAssetSchema, &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)

	for name, shared := range textpatch.PropertiesMap() {
		got, present := props[name]
		require.True(t, present, "manage_asset must advertise %q", name)
		if name == "section" || name == "find" || name == "pattern" {
			continue // names the toolkit may also define are compared loosely
		}
		assert.Equal(t, shared, got, "property %q must be the shared grammar", name)
	}
}

func TestWithPatchPropertiesRefusesToRedefineTheSharedGrammar(t *testing.T) {
	// The grammar must read identically on every tool, so a toolkit that
	// redefines one of its names fails the build rather than shipping a tool
	// whose "shared" argument means something else.
	base := []byte(`{"type":"object","properties":{"find":{"type":"string","description":"toolkit wording"}}}`)
	assert.PanicsWithValue(t,
		`textpatch: tool schema redefines shared property "find"`,
		func() { withPatchProperties(base) })
}

func TestWithPatchPropertiesMergesIntoADisjointSchema(t *testing.T) {
	base := []byte(`{"type":"object","properties":{"asset_id":{"type":"string"}}}`)
	merged := withPatchProperties(base)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(merged, &schema))
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)

	assert.Contains(t, props, "asset_id", "the toolkit's own arguments survive")
	assert.Contains(t, props, "edits")
	assert.Contains(t, props, "find")
}

func TestUploadContentUpdateDefaultsTheChangeSummary(t *testing.T) {
	f := newPatchFixture(t, patchReport, "text/markdown")
	asset, err := f.assets.Get(context.Background(), f.assetID)
	require.NoError(t, err)

	version, err := f.tk.uploadContentUpdate(f.ctx, asset, "new body", "", "")
	require.NoError(t, err)
	assert.Equal(t, 2, version)

	versions, _, err := f.tk.versionStore.ListByAsset(context.Background(), f.assetID, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, defaultChangeSummary, versions[1].ChangeSummary)
}
