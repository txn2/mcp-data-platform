package portal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeResourceWriter records what the tool asked for and answers with whatever
// the test set, so a handler test can assert the arguments the tool built
// rather than only the words it printed.
type fakeResourceWriter struct {
	created  resource.NewResource
	replaced resource.RevisionUpload
	claims   resource.Claims
	gotID    string

	createErr  error
	replaceErr error
	getErr     error
	existing   *resource.Resource
	version    int
}

func (f *fakeResourceWriter) Create(
	_ context.Context, in resource.NewResource, claims resource.Claims,
) (*resource.Resource, error) {
	f.created, f.claims = in, claims
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &resource.Resource{
		ID: "res1", Scope: in.Scope, ScopeID: in.ScopeID, Category: in.Category,
		Filename: in.Filename, DisplayName: in.DisplayName, MIMEType: in.MIMEType,
		SizeBytes: int64(len(in.Data)),
		URI:       resource.BuildURI("mcp", in.Scope, in.ScopeID, in.Category, in.Filename),
		S3Key:     "resources/res1/" + in.Filename,
	}, nil
}

func (f *fakeResourceWriter) Replace(
	_ context.Context, id string, up resource.RevisionUpload, claims resource.Claims,
) (*resource.Resource, int, error) {
	f.gotID, f.replaced, f.claims = id, up, claims
	if f.replaceErr != nil {
		return nil, 0, f.replaceErr
	}
	res := *f.existingOrDefault()
	res.MIMEType, res.SizeBytes = up.MIMEType, int64(len(up.Data))
	return &res, f.version, nil
}

func (f *fakeResourceWriter) Get(_ context.Context, id string, _ resource.Claims) (*resource.Resource, error) {
	f.gotID = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.existingOrDefault(), nil
}

func (f *fakeResourceWriter) existingOrDefault() *resource.Resource {
	if f.existing != nil {
		return f.existing
	}
	return &resource.Resource{
		ID: "res1", Scope: resource.ScopeUser, ScopeID: "user1", Category: "datasets",
		Filename: "weather.csv", DisplayName: "Daily Weather", MIMEType: "text/csv",
		URI: "mcp://user/user1/datasets/weather.csv", S3Key: "resources/res1/weather.csv",
	}
}

var _ ResourceWriter = (*fakeResourceWriter)(nil)

// resourceToolkit builds a toolkit with a bound writer, plus the writer itself.
func resourceToolkit(t *testing.T) (*Toolkit, *fakeResourceWriter) {
	t.Helper()
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "b", MaxContentSize: 1 << 20})
	w := &fakeResourceWriter{version: 2}
	tk.SetResourceWriter(w)
	return tk, w
}

// callResource runs one manage_resource action.
func callResource(t *testing.T, tk *Toolkit, input manageResourceInput) *mcp.CallToolResult {
	t.Helper()
	result, _, err := tk.handleManageResource(refCtx(""), nil, input)
	require.NoError(t, err)
	return result
}

// createInputFor is a valid create, which each test varies one field of.
func createInputFor() manageResourceInput {
	return manageResourceInput{
		Action: resourceActionCreate, Filename: "Weather Daily.CSV",
		DisplayName: "Daily Weather", Category: "datasets",
		Description: "Highs and lows", Content: "day,high\nmon,71\ntue,68\n",
	}
}

func decodeResourceOutput(t *testing.T, result *mcp.CallToolResult) resourceOutput {
	t.Helper()
	require.False(t, result.IsError, "call failed: %s", errText(t, result))
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out resourceOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

func TestManageResourceWithoutALayer(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "b"})

	result := callResource(t, tk, createInputFor())

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "no managed-resource library")
	assert.Contains(t, errText(t, result), "Nothing was saved",
		"a deployment that cannot write must not leave the caller thinking it did")
}

func TestManageResourceWithoutAnIdentity(t *testing.T) {
	tk, _ := resourceToolkit(t)

	result, _, err := tk.handleManageResource(context.Background(), nil, createInputFor())

	require.NoError(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "signed-in identity")
}

func TestManageResourceUnknownAction(t *testing.T) {
	tk, _ := resourceToolkit(t)

	result := callResource(t, tk, manageResourceInput{Action: "delete"})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "create, replace_content")
}

func TestCreateBuildsTheResourceFromTheCall(t *testing.T) {
	tk, w := resourceToolkit(t)

	out := decodeResourceOutput(t, callResource(t, tk, createInputFor()))

	assert.Equal(t, "weather-daily.csv", w.created.Filename, "the filename is normalized before it reaches the URI")
	assert.Equal(t, resource.ScopeUser, w.created.Scope)
	assert.Equal(t, "user1", w.created.ScopeID, "an unnamed scope is the caller's own")
	assert.Equal(t, "day,high\nmon,71\ntue,68\n", string(w.created.Data))
	assert.Equal(t, "text/csv", w.created.MIMEType, "the type is detected when the call names none")
	assert.Equal(t, []string{}, w.created.Tags)
	assert.Equal(t, "user1", w.claims.Sub, "the write acts as the caller, not as the platform")

	assert.Equal(t, "res1", out.ResourceID)
	assert.Equal(t, "mcp:resource:res1", out.Reference)
	assert.Equal(t, "mcp://user/user1/datasets/weather-daily.csv", out.URI)
	assert.Zero(t, out.Version, "a create reports no version number")
	assert.Contains(t, out.Message, "save_asset")
}

func TestCreateHonoursANamedScope(t *testing.T) {
	tk, w := resourceToolkit(t)
	in := createInputFor()
	in.Scope, in.ScopeID = "persona", "finance"

	require.False(t, callResource(t, tk, in).IsError)

	assert.Equal(t, resource.ScopePersona, w.created.Scope)
	assert.Equal(t, "finance", w.created.ScopeID)
}

func TestCreateClearsScopeIDForGlobal(t *testing.T) {
	tk, w := resourceToolkit(t)
	in := createInputFor()
	in.Scope, in.ScopeID = "global", "ignored"

	require.False(t, callResource(t, tk, in).IsError)

	assert.Empty(t, w.created.ScopeID, "a global resource has no scope id, so one offered is dropped not refused")
}

// scriptRunCtx is the identity a managed-script run carries: a principal that
// owns nothing, the script owner's address, and the version author's address as
// the person the run acts for (#1419).
func scriptRunCtx(author string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "script:weekly-refresh", UserEmail: author, SessionID: "run1",
		OnBehalfOfEmail: author,
	})
}

// TestCreateFromAScriptFilesItInItsAuthorsLibrary is the acceptance for the
// script half: a scheduled run writing with no scope named must not file the
// resource under its own principal, which is a library its author's Resources
// page does not show.
func TestCreateFromAScriptFilesItInItsAuthorsLibrary(t *testing.T) {
	tk, w := resourceToolkit(t)

	result, _, err := tk.handleManageResource(scriptRunCtx("author@example.com"), nil, createInputFor())
	require.NoError(t, err)
	require.False(t, result.IsError, "%s", errText(t, result))

	assert.Equal(t, resource.ScopeUser, w.created.Scope)
	assert.Equal(t, "author@example.com", w.created.ScopeID,
		"the default is the person the run acts for, not the script principal")
	assert.Equal(t, "author@example.com", w.claims.OnBehalfOf,
		"the writer is told who the run acts for, so its own checks reach what the author reaches")
}

func TestReplaceFromAScriptActsForItsAuthor(t *testing.T) {
	tk, w := resourceToolkit(t)

	result, _, err := tk.handleManageResource(scriptRunCtx("author@example.com"), nil, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", Content: "day,high\nmon,88\ntue,90\n",
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "%s", errText(t, result))

	assert.Equal(t, "author@example.com", w.claims.OnBehalfOf)
}

func TestCreateValidatesItsPlacement(t *testing.T) {
	tests := []struct {
		name  string
		mutfn func(*manageResourceInput)
		says  string
	}{
		{"no filename", func(i *manageResourceInput) { i.Filename = "" }, "filename is required"},
		{"a refused extension", func(i *manageResourceInput) { i.Filename = "run.sh" }, "extension"},
		{"no category", func(i *manageResourceInput) { i.Category = "" }, "shelf the file sits on"},
		{"no display name", func(i *manageResourceInput) { i.DisplayName = "" }, "display_name is required"},
		{"an unknown scope", func(i *manageResourceInput) { i.Scope = "team" }, "unknown scope"},
		{"a persona scope with no id", func(i *manageResourceInput) { i.Scope = "persona" }, "scope_id is required"},
		{"a description that is too long", func(i *manageResourceInput) {
			i.Description = strings.Repeat("x", resource.MaxDescriptionLen+1)
		}, "description"},
		{"a malformed tag", func(i *manageResourceInput) { i.Tags = []string{"Not A Tag"} }, "tag"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tk, w := resourceToolkit(t)
			in := createInputFor()
			tc.mutfn(&in)

			result := callResource(t, tk, in)

			require.True(t, result.IsError)
			assert.Contains(t, errText(t, result), tc.says)
			assert.Empty(t, w.created.Filename, "a refused create reaches no writer")
		})
	}
}

func TestCreateReportsTheWritersRefusal(t *testing.T) {
	tk, w := resourceToolkit(t)
	w.createErr = errors.New("you cannot write to the global scope, which is administrators only: managed-resource write refused")

	result := callResource(t, tk, createInputFor())

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "global scope")
}

func TestContentArrivesAsTextOrAsBytes(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 24)
	tests := []struct {
		name   string
		mutfn  func(*manageResourceInput)
		expect string
	}{
		{"text", func(i *manageResourceInput) { i.Content = "a,b\n1,2\n" }, "a,b\n1,2\n"},
		{"base64", func(i *manageResourceInput) {
			i.Content = ""
			i.Filename = "logo.png"
			i.ContentBase64 = base64.StdEncoding.EncodeToString([]byte(png))
		}, png},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tk, w := resourceToolkit(t)
			in := createInputFor()
			tc.mutfn(&in)

			require.False(t, callResource(t, tk, in).IsError)

			assert.Equal(t, tc.expect, string(w.created.Data))
		})
	}
}

func TestUnpaddedBase64IsAccepted(t *testing.T) {
	png := "\x89PNG\r\n\x1a\n" + strings.Repeat("\x00", 23)
	padded := base64.StdEncoding.EncodeToString([]byte(png))
	require.Contains(t, padded, "=", "the fixture must actually need padding for this to test anything")

	tk, w := resourceToolkit(t)
	in := createInputFor()
	in.Content, in.Filename = "", "logo.png"
	in.ContentBase64 = strings.TrimRight(padded, "=")

	require.False(t, callResource(t, tk, in).IsError)

	assert.Equal(t, png, string(w.created.Data),
		"a model that emits unpadded base64 must not have its file refused for the spelling")
}

func TestADeclaredTypeSurvivesDetection(t *testing.T) {
	tk, w := resourceToolkit(t)
	in := createInputFor()
	// Two rows is too little for the content alone to read as a CSV, which is
	// exactly when naming the type is worth it.
	in.Content, in.ContentType = "day,high\nmon,71\n", "text/csv"

	require.False(t, callResource(t, tk, in).IsError)

	assert.Equal(t, "text/csv", w.created.MIMEType)
	assert.Equal(t, "text/csv", w.created.DeclaredMIMEType,
		"what the caller declared is carried through, so a swap can be told from an agreement")
}

func TestContentRefusals(t *testing.T) {
	tests := []struct {
		name  string
		mutfn func(*manageResourceInput)
		says  string
	}{
		{"neither field", func(i *manageResourceInput) { i.Content = "" }, "content is required"},
		{"both fields", func(i *manageResourceInput) { i.ContentBase64 = "aGk=" }, "not both"},
		{"malformed base64", func(i *manageResourceInput) {
			i.Content = ""
			i.ContentBase64 = "not base64!!"
		}, "not valid base64"},
		{"a denied type", func(i *manageResourceInput) { i.ContentType = "application/x-shellscript" }, "not allowed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tk, w := resourceToolkit(t)
			in := createInputFor()
			tc.mutfn(&in)

			result := callResource(t, tk, in)

			require.True(t, result.IsError)
			assert.Contains(t, errText(t, result), tc.says)
			assert.Empty(t, w.created.Data)
		})
	}
}

func TestContentIsCappedAtTheSameSizeAnAssetIs(t *testing.T) {
	tk := New(Config{Name: "test", AssetStore: newInMemoryAssetStore(), S3Bucket: "b", MaxContentSize: 8})
	w := &fakeResourceWriter{version: 2}
	tk.SetResourceWriter(w)
	in := createInputFor()
	in.Content = strings.Repeat("x", 9)

	result := callResource(t, tk, in)

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "exceeds maximum 8 bytes")
	assert.Contains(t, errText(t, result), "resource library",
		"the refusal says where a file this large does go")
}

func TestReplaceKeepsTheStoredIdentity(t *testing.T) {
	tk, w := resourceToolkit(t)

	out := decodeResourceOutput(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1",
		Content: "day,high\nmon,88\ntue,90\n", ChangeSummary: "hourly refresh",
	}))

	assert.Equal(t, "res1", w.gotID)
	assert.Equal(t, "hourly refresh", w.replaced.ChangeSummary)
	assert.Equal(t, "text/csv", w.replaced.MIMEType,
		"detection is given the stored filename, so a CSV stays a CSV across a replacement")
	assert.Equal(t, "mcp://user/user1/datasets/weather.csv", out.URI)
	assert.Equal(t, "weather.csv", out.Filename)
	assert.Equal(t, 2, out.Version)
	assert.Contains(t, out.Message, "version 2")
	assert.Contains(t, out.Message, "without being re-saved")
}

func TestReplaceLabelsAnUnexplainedRevision(t *testing.T) {
	tk, w := resourceToolkit(t)

	require.False(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", Content: "x",
	}).IsError)

	assert.Equal(t, defaultResourceChangeSummary, w.replaced.ChangeSummary)
}

func TestReplaceIgnoresAnOfferedFilename(t *testing.T) {
	tk, w := resourceToolkit(t)

	out := decodeResourceOutput(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1",
		Filename: "renamed.json", Content: "day,high\nmon,88\ntue,90\n",
	}))

	assert.Equal(t, "weather.csv", out.Filename)
	assert.Equal(t, "text/csv", w.replaced.MIMEType,
		"a rename would change the URI, so the offered name reaches neither the record nor detection")
}

func TestReplaceReferenceRefusals(t *testing.T) {
	tests := []struct {
		name      string
		reference string
		says      string
	}{
		{"absent", "  ", "reference is required"},
		{"not a platform reference", "just-an-id", "not a reference this platform issues"},
		{"another kind", "mcp:asset:a1", "names a target of type"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tk, w := resourceToolkit(t)

			result := callResource(t, tk, manageResourceInput{
				Action: resourceActionReplace, Reference: tc.reference, Content: "x",
			})

			require.True(t, result.IsError)
			assert.Contains(t, errText(t, result), tc.says)
			assert.Empty(t, w.replaced.Data)
		})
	}
}

func TestReplaceResolvesTheFileBeforeReadingThePayload(t *testing.T) {
	tk, w := resourceToolkit(t)
	w.getErr = errors.New(`there is no managed resource "res1" you can see: no such managed resource`)

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", Content: "x",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "no managed resource")
	assert.Empty(t, w.replaced.Data)
}

func TestReplaceRefusesUnusableContent(t *testing.T) {
	tk, w := resourceToolkit(t)

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", ContentBase64: "not base64!!",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "not valid base64")
	assert.Empty(t, w.replaced.Data, "a replacement whose bytes cannot be read reaches no writer")
}

func TestReplaceReportsTheWritersRefusal(t *testing.T) {
	tk, w := resourceToolkit(t)
	w.replaceErr = errors.New("you cannot replace the content of a file in the global scope: managed-resource write refused")

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", Content: "x",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "global scope")
}
