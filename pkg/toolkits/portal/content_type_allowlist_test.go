package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// contentTypeDoorCase is one declaration put to every door that accepts a
// caller-declared content type for content carried as a string.
type contentTypeDoorCase struct {
	name        string
	contentType string
	content     string
	accept      bool
}

// contentTypeDoorCases covers the families the doors take, an alias that has to
// normalize before the lookup, and the declarations that must not get through:
// the scriptable document family a browser runs natively, a binary type that
// cannot survive a JSON string, an executable, and an invented text/x-* type
// that a text/* prefix rule would have admitted.
var contentTypeDoorCases = []contentTypeDoorCase{
	{name: "markdown", contentType: "text/markdown", content: "# Report", accept: true},
	{name: "json", contentType: "application/json", content: `{"rows":[{"id":1}]}`, accept: true},
	{name: "html", contentType: "text/html", content: "<h1>Report</h1>", accept: true},
	{name: "svg", contentType: "image/svg+xml", content: `<svg xmlns="http://www.w3.org/2000/svg"/>`, accept: true},
	{name: "xml alias normalizes", contentType: "text/xml", content: "<report><row/></report>", accept: true},
	{name: "csv with parameter", contentType: "text/csv; charset=utf-8", content: "a,b\n1,2\n3,4\n", accept: true},
	{name: "xhtml refused", contentType: "application/xhtml+xml", content: "<html><body/></html>", accept: false},
	{name: "pdf refused", contentType: "application/pdf", content: "not really a pdf", accept: false},
	{name: "executable refused", contentType: "application/x-msdownload", content: "MZ payload", accept: false},
	{name: "invented text subtype refused", contentType: "text/x-shellscript", content: "echo hi", accept: false},
}

// TestContentTypeAllowlistGovernsEveryStringContentDoor is the parity gate for
// issue #1069: the REST inline-create endpoint, save_asset and the manage_asset
// content update must accept and refuse the same declarations. Each door is
// driven for real — an HTTP request through the portal handler, and the two
// tool handlers — because the finding was not that the check was wrong but that
// two of the three doors did not have one.
func TestContentTypeAllowlistGovernsEveryStringContentDoor(t *testing.T) {
	for _, tc := range contentTypeDoorCases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.accept, restCreateAccepts(t, tc), "REST inline create")
			assert.Equal(t, tc.accept, saveAssetAccepts(t, tc), "save_asset")
			assert.Equal(t, tc.accept, manageUpdateAccepts(t, tc), "manage_asset update")
		})
	}
}

// TestSaveAssetRefusesXHTMLWithActionableError pins the error a refused
// declaration produces: naming the accepted types is what lets an agent correct
// the call instead of retrying the same one.
func TestSaveAssetRefusesXHTMLWithActionableError(t *testing.T) {
	tk, ctx := newAllowlistToolkit(t)
	result, _, err := tk.handleSaveAsset(ctx, nil, saveAssetInput{
		Name:        "report",
		Content:     "<html><body/></html>",
		ContentType: "application/xhtml+xml",
	})
	require.NoError(t, err)
	require.True(t, result.IsError)

	msg := errText(t, result)
	assert.Contains(t, msg, `application/xhtml+xml`)
	assert.Contains(t, msg, "text/markdown")
	assert.Contains(t, msg, "text/html")
}

// TestManageUpdateKeepsAnExistingOffAllowlistType covers the asset an export
// wrote from an upstream response, whose type no door would accept today. An
// edit that keeps that type has not declared anything, and refusing it would
// make the asset uneditable.
func TestManageUpdateKeepsAnExistingOffAllowlistType(t *testing.T) {
	assets := newInMemoryAssetStore()
	tk, ctx := newAllowlistToolkitOver(t, assets)
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID:          "legacy",
		OwnerID:     "user1",
		Name:        "upstream export",
		ContentType: "text/x-log",
	}))

	result, _, err := tk.handleUpdate(ctx, manageAssetInput{
		Action:  "update",
		AssetID: "legacy",
		Content: "line one\nline two\n",
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "keeping the asset's own type must stay editable: %s", errText(t, result))

	stored, getErr := assets.Get(context.Background(), "legacy")
	require.NoError(t, getErr)
	assert.Equal(t, "text/x-log", stored.ContentType, "the update must not reclassify the asset")
}

// TestManageUpdateRefusesAChangeToAnOffAllowlistType is the other half: the
// exemption above is for the type the asset already carries, not a license to
// declare a new one.
func TestManageUpdateRefusesAChangeToAnOffAllowlistType(t *testing.T) {
	assets := newInMemoryAssetStore()
	tk, ctx := newAllowlistToolkitOver(t, assets)
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID:          "legacy",
		OwnerID:     "user1",
		Name:        "upstream export",
		ContentType: "text/x-log",
	}))

	result, _, err := tk.handleUpdate(ctx, manageAssetInput{
		Action:      "update",
		AssetID:     "legacy",
		Content:     "<html><body/></html>",
		ContentType: "application/xhtml+xml",
	})
	require.NoError(t, err)
	assert.True(t, result.IsError)

	stored, getErr := assets.Get(context.Background(), "legacy")
	require.NoError(t, getErr)
	assert.Equal(t, "text/x-log", stored.ContentType)
}

// TestSaveAssetSchemaAdvertisesTheEnforcedTypes keeps the advertised schema and
// the enforced set in step. An agent picks its declaration from the schema, so a
// type listed but refused sends it into a failure it cannot diagnose, and a type
// accepted but unlisted is a capability nothing will use.
func TestSaveAssetSchemaAdvertisesTheEnforcedTypes(t *testing.T) {
	var schema struct {
		Properties struct {
			ContentType struct {
				Description string `json:"description"`
			} `json:"content_type"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(saveAssetSchema, &schema))

	described := schema.Properties.ContentType.Description
	for _, ct := range contenttype.StorableTextTypes() {
		assert.Contains(t, described, ct, "the schema must advertise every accepted type")
	}
	assert.NotContains(t, described, contenttype.XHTML, "the schema must not advertise a refused type")
}

// --- door drivers ---

// restCreateAccepts posts the declaration to the real portal REST handler and
// reports whether the asset was created.
func restCreateAccepts(t *testing.T, tc contentTypeDoorCase) bool {
	t.Helper()

	assets := newInMemoryAssetStore()
	h := portal.NewHandler(portal.Deps{
		AssetStore:    assets,
		VersionStore:  newLinkedVersionStore(assets),
		S3Client:      &mockS3Client{},
		S3Bucket:      "bucket",
		PublicBaseURL: "https://example.com",
		RateLimit:     portal.RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, allowlistTestAuth)

	body, err := json.Marshal(map[string]string{
		"name":         "report",
		"content_type": tc.contentType,
		"content":      tc.content,
	})
	require.NoError(t, err)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/portal/assets", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code == http.StatusCreated {
		return true
	}
	require.Equal(t, http.StatusUnsupportedMediaType, w.Code,
		"a refused declaration must be 415, got %d: %s", w.Code, w.Body.String())
	return false
}

// saveAssetAccepts runs the declaration through the save_asset handler.
func saveAssetAccepts(t *testing.T, tc contentTypeDoorCase) bool {
	t.Helper()

	tk, ctx := newAllowlistToolkit(t)
	result, _, err := tk.handleSaveAsset(ctx, nil, saveAssetInput{
		Name:        "report",
		Content:     tc.content,
		ContentType: tc.contentType,
	})
	require.NoError(t, err)
	return !result.IsError
}

// manageUpdateAccepts replaces the content of a markdown asset with the
// declaration under test, which is the type-changing case the door gates.
func manageUpdateAccepts(t *testing.T, tc contentTypeDoorCase) bool {
	t.Helper()

	assets := newInMemoryAssetStore()
	tk, ctx := newAllowlistToolkitOver(t, assets)
	require.NoError(t, assets.Insert(context.Background(), portal.Asset{
		ID:          "asset1",
		OwnerID:     "user1",
		Name:        "report",
		ContentType: "text/markdown",
	}))

	result, _, err := tk.handleUpdate(ctx, manageAssetInput{
		Action:      "update",
		AssetID:     "asset1",
		Content:     tc.content,
		ContentType: tc.contentType,
	})
	require.NoError(t, err)
	return !result.IsError
}

// --- fixtures ---

// allowlistTestAuth is the portal handler's auth middleware for these tests:
// the same user the tool handlers run as, so all three doors are exercised by
// one caller.
func allowlistTestAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := portal.ContextWithUser(r.Context(), &portal.User{UserID: "user1", Email: "user1@example.com"})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newAllowlistToolkit(t *testing.T) (*Toolkit, context.Context) {
	t.Helper()
	return newAllowlistToolkitOver(t, newInMemoryAssetStore())
}

func newAllowlistToolkitOver(t *testing.T, assets *inMemoryAssetStore) (*Toolkit, context.Context) {
	t.Helper()

	tk := New(Config{
		Name:         "test",
		AssetStore:   assets,
		VersionStore: newLinkedVersionStore(assets),
		S3Client:     &mockS3Client{},
		S3Bucket:     "bucket",
		S3Prefix:     "assets/",
	})
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "user1", UserEmail: "user1@example.com", SessionID: "sess1",
	})
	return tk, ctx
}
