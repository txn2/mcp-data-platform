package portal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakeResourceWriter records what the tool asked for and answers with whatever
// the test set, so a handler test can assert the arguments the tool built
// rather than only the words it printed.
type fakeResourceWriter struct {
	created  resource.NewResource
	replaced resource.RevisionUpload
	// createdContent and replacedContent are the bytes the tool handed over.
	// The writer takes them as a reader (#1631), and the real one draws it, so
	// the fake draws it too: a fake that left it unread would record a reader
	// nobody can look at twice and would hide a caller that passed a spent one.
	createdContent  []byte
	replacedContent []byte
	claims          resource.Claims
	gotID           string

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
	f.createdContent = drainContent(in.Content)
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &resource.Resource{
		ID: "res1", Scope: in.Scope, ScopeID: in.ScopeID, Path: in.Path,
		Filename: in.Filename, DisplayName: in.DisplayName, MIMEType: in.MIMEType,
		SizeBytes: int64(len(f.createdContent)),
		URI:       resource.BuildURI("mcp", in.Scope, in.ScopeID, in.Path, in.Filename),
		S3Key:     "resources/res1/" + in.Filename,
	}, nil
}

func (f *fakeResourceWriter) Replace(
	_ context.Context, id string, up resource.RevisionUpload, claims resource.Claims,
) (*resource.Resource, int, error) {
	f.gotID, f.replaced, f.claims = id, up, claims
	f.replacedContent = drainContent(up.Content)
	if f.replaceErr != nil {
		return nil, 0, f.replaceErr
	}
	res := *f.existingOrDefault()
	res.MIMEType, res.SizeBytes = up.MIMEType, int64(len(f.replacedContent))
	return &res, f.version, nil
}

// drainContent reads a write's content the way the real writer does. Nil
// content is an empty object rather than an error, which is the contract
// CreateResource and ReviseContent hold.
func drainContent(r io.Reader) []byte {
	if r == nil {
		return nil
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil
	}
	return data
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
		ID: "res1", Scope: resource.ScopeUser, ScopeID: "user1", Path: "datasets",
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
		DisplayName: "Daily Weather", Path: "datasets",
		Description: "Highs and lows", Content: "day,high\nmon,71\ntue,68\n",
		ContentType: "text/csv",
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
	assert.Equal(t, "day,high\nmon,71\ntue,68\n", string(w.createdContent))
	assert.Equal(t, "text/csv", w.created.MIMEType)
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
		{"no folder path", func(i *manageResourceInput) { i.Path = "" }, "folder chain the file is filed under"},
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

			assert.Equal(t, tc.expect, string(w.createdContent))
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

	assert.Equal(t, png, string(w.createdContent),
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
			assert.Empty(t, w.createdContent)
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
			assert.Empty(t, w.replacedContent)
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
	assert.Empty(t, w.replacedContent)
}

func TestReplaceRefusesUnusableContent(t *testing.T) {
	tk, w := resourceToolkit(t)

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1", ContentBase64: "not base64!!",
	})

	require.True(t, result.IsError)
	assert.Contains(t, errText(t, result), "not valid base64")
	assert.Empty(t, w.replacedContent, "a replacement whose bytes cannot be read reaches no writer")
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

// A create with no content_type is refused, and refused before anything is
// written. The stored type is what a viewer and an <img> act on, and it cannot
// be recovered from the bytes for the families an agent writes most, so a
// create that declares none is a file that will silently not render (#1508).
func TestCreateRequiresAContentType(t *testing.T) {
	tk, w := resourceToolkit(t)
	in := createInputFor()
	in.ContentType = "   "

	result := callResource(t, tk, in)

	require.True(t, result.IsError)
	msg := errText(t, result)
	assert.Contains(t, msg, "content_type is required")
	assert.Contains(t, msg, "image/svg+xml", "the refusal names types the caller can choose between")
	assert.Contains(t, msg, "Nothing was saved")
	assert.Empty(t, w.created.Filename, "the refusal is said instead of a write, never after one")
}

// The case from the report: an agent-written SVG with no declaration was
// stored text/plain, which nosniff makes final, so every <img> naming it was a
// broken image. Declared, the type survives the write -- detection may not
// name an active family from content, and now does not have to.
func TestCreateStoresADeclaredActiveType(t *testing.T) {
	tk, w := resourceToolkit(t)
	in := createInputFor()
	in.Filename, in.ContentType = "badge.svg", "image/svg+xml"
	in.Content = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 40"></svg>`

	require.False(t, callResource(t, tk, in).IsError)

	assert.Equal(t, "image/svg+xml", w.created.MIMEType)
}

// A replacement keeps the family the resource already carries. Re-deciding it
// from the bytes would reclassify the file under every reference to it, and
// for an SVG it would land text/plain on every refresh.
func TestReplaceKeepsTheTypeTheResourceCarries(t *testing.T) {
	tk, w := resourceToolkit(t)
	w.existing = &resource.Resource{
		ID: "res1", Scope: resource.ScopeUser, ScopeID: "user1", Path: "brand",
		Filename: "badge.svg", DisplayName: "Badge", MIMEType: "image/svg+xml",
		URI: "mcp://user/user1/brand/badge.svg",
	}

	require.False(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1",
		Content: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 40"></svg>`,
	}).IsError)

	assert.Equal(t, "image/svg+xml", w.replaced.MIMEType)
}

// Declaring a type on a replacement is changing what family the file is, which
// is a deliberate act, so the declaration wins over the stored type.
func TestReplaceHonoursADeclaredType(t *testing.T) {
	tk, w := resourceToolkit(t)

	require.False(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1",
		Content: "# Weather\n\nHighs and lows by day.\n", ContentType: "text/markdown",
	}).IsError)

	assert.Equal(t, "text/markdown", w.replaced.MIMEType,
		"the stored text/csv stands in for a declaration, it does not override one")
}

// A resource stored under a generic type is not frozen there: the stored type
// stands in for a declaration, and a generic declaration is what detection is
// allowed to replace. This is the way back for a file written before the
// declaration was required.
func TestReplaceUpgradesAGenericStoredType(t *testing.T) {
	tk, w := resourceToolkit(t)
	w.existing = &resource.Resource{
		ID: "res1", Scope: resource.ScopeUser, ScopeID: "user1", Path: "datasets",
		Filename: "weather.csv", DisplayName: "Daily Weather", MIMEType: "application/octet-stream",
		URI: "mcp://user/user1/datasets/weather.csv",
	}

	require.False(t, callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1",
		Content: "day,high\nmon,88\ntue,90\n",
	}).IsError)

	assert.Equal(t, "text/csv", w.replaced.MIMEType)
}

// The schema and the refusal both point a caller at the built-in page listing
// the types. The page is shipped by a package this one cannot import, so the
// reference is built from the shared constant; this is the gate that catches a
// schema literal that drifted from it.
func TestManageResourcePointsAtTheContentTypePage(t *testing.T) {
	ref := knowledgepage.BuiltinReference(knowledgepage.BuiltinSlugContentTypes)

	assert.Contains(t, string(manageResourceSchema), ref)
	assert.Contains(t, resourceContentTypeRequired, ref)
}

// A file stored under a type the deny list has since grown to cover is still
// replaceable: inheriting that type would refuse the write over a declaration
// its caller never made, so detection settles it exactly as it did before.
func TestReplaceDoesNotInheritADeniedStoredType(t *testing.T) {
	tk, w := resourceToolkit(t)
	w.existing = &resource.Resource{
		ID: "res1", Scope: resource.ScopeUser, ScopeID: "user1", Path: "runbooks",
		Filename: "legacy.xhtml", DisplayName: "Legacy", MIMEType: contenttype.XHTML,
		URI: "mcp://user/user1/runbooks/legacy.xhtml",
	}

	result := callResource(t, tk, manageResourceInput{
		Action: resourceActionReplace, Reference: "mcp:resource:res1",
		Content: "day,high\nmon,88\ntue,90\n",
	})

	require.False(t, result.IsError, "the refusal would name a type the caller never sent: %s", errText(t, result))
	assert.Equal(t, "text/csv", w.replaced.MIMEType)
}

// The tool description and the content_type field description are two halves of
// one contract, and an agent acts on whichever it reads first. 1.126.4 required
// the declaration in the field and left the tool description saying the type is
// detected when you do not name one, so an agent that read the tool sent no
// content_type and had its create refused with nothing written (#1519).
//
// Both halves therefore state both facts a caller acts on: a create declares
// the type, and a replacement keeps the one the resource already carries.
func TestManageResourceDescriptionAndSchemaStateTheSameContract(t *testing.T) {
	tool := advertisedTool(t, ManageResourceToolName)

	halves := map[string]string{
		"tool description":   tool.Description,
		"content_type field": advertisedFieldDescription(t, tool, "content_type"),
	}

	for name, text := range halves {
		t.Run(name, func(t *testing.T) {
			lower := strings.ToLower(text)

			assert.Contains(t, lower, "required for create",
				"a caller reading this half is not told a create is refused without the declaration")
			assert.Contains(t, lower, "not detected",
				"a caller reading this half may still expect the platform to work the type out")
			assert.Contains(t, lower, "keeps the type the resource already carries",
				"a caller reading this half is not told what a replacement does with the stored type")

			// The caveat that makes the sentence above true: a generic stored
			// type does not survive a replacement, it is detected again.
			assert.Contains(t, lower, "generic type",
				"a caller reading this half is told a stored type always survives a replacement")
			assert.Contains(t, lower, "re-detected",
				"a caller reading this half is not told what happens to a generically typed file")

			// The exact claim that shipped in 1.126.4 (#1519).
			assert.NotContains(t, lower, "detected from the content when you do not name one",
				"this half promises detection the create path refuses")
		})
	}

	// Only the tool description has to name the field: the other half is the
	// field, and a caller reading it already knows which one it is.
	assert.Contains(t, tool.Description, "content_type",
		"the tool description does not name the field a create has to send")
}
