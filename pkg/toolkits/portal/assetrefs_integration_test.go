package portal

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// sharedS3 is one in-memory object store standing in for the blob backend the
// toolkit writes to and the portal handler reads from.
//
// It is deliberately one store rather than two fixtures: the point of the test
// below is that what the save actually wrote is what the viewing surface
// actually serves, and two mocks agreeing by construction would prove nothing.
type sharedS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newSharedS3() *sharedS3 { return &sharedS3{objects: map[string][]byte{}} }

func (s *sharedS3) PutObject(_ context.Context, bucket, key string, data []byte, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[bucket+"/"+key] = data
	return nil
}

func (s *sharedS3) PutObjectStream(_ context.Context, bucket, key string, body io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("reading stream: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[bucket+"/"+key] = data
	return int64(len(data)), nil
}

func (s *sharedS3) GetObject(_ context.Context, bucket, key string) (body []byte, contentType string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, ok := s.objects[bucket+"/"+key]
	if !ok {
		return nil, "", notFoundError{}
	}
	return data, "", nil
}

func (s *sharedS3) DeleteObject(_ context.Context, bucket, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.objects, bucket+"/"+key)
	return nil
}

func (*sharedS3) Close() error { return nil }

var _ portal.S3Client = (*sharedS3)(nil)

// refSystem is the assembled system under test: one asset store, one version
// store, one reference store and one blob backend, with the MCP toolkit on one
// side and the portal's HTTP handler on the other.
type refSystem struct {
	toolkit *Toolkit
	handler *portal.Handler
	blobs   *sharedS3
}

const (
	intAssetBody   = `<h1>Q4 Revenue</h1><img src="` + refLogoURI + `" alt="logo">`
	intLogoBytes   = "\x89PNG-logo-bytes"
	intPortalBase  = "https://platform.example.com"
	intAssetBucket = "portal-assets"
	intResBucket   = "managed-resources"
)

func newRefSystem(t *testing.T) *refSystem {
	t.Helper()
	assets := newInMemoryAssetStore()
	versions := newInMemoryVersionStore()
	versions.assets = assets
	refs := newRefStoreStub()
	blobs := newSharedS3()
	require.NoError(t, blobs.PutObject(context.Background(), intResBucket, "resources/global/logo.png",
		[]byte(intLogoBytes), "image/png"))

	tk := New(Config{
		Name: "test", AssetStore: assets, VersionStore: versions,
		S3Client: blobs, S3Bucket: intAssetBucket, BaseURL: intPortalBase,
	})
	tk.SetResourceRefs(assetrefs.NewDeclarer(refs, resourceStub{}, ""))

	h := portal.NewHandler(portal.Deps{
		AssetStore:       assets,
		VersionStore:     versions,
		ShareStore:       portal.NewNoopShareStore(),
		S3Client:         blobs,
		S3Bucket:         intAssetBucket,
		PublicBaseURL:    intPortalBase,
		RateLimit:        portal.RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		ResourceRefs:     refs,
		ResourceReader:   resourceStub{},
		ResourceBlobs:    blobs,
		ResourceS3Bucket: intResBucket,
	}, portalUserMiddleware("user1"))

	return &refSystem{toolkit: tk, handler: h, blobs: blobs}
}

// portalUserMiddleware puts an authenticated portal user on every request, the
// state the real authentication chain leaves behind.
func portalUserMiddleware(userID string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := portal.ContextWithUser(r.Context(), &portal.User{UserID: userID, Email: refAuthor})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (s *refSystem) get(t *testing.T, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec
}

// TestReferenceSurvivesSaveViewAndPatch is the end-to-end acceptance test for
// #1474, run over the real toolkit and the real HTTP handler sharing one set of
// stores. It asserts, in one pass, every claim the feature makes:
//
//   - a save that declares a reference stores the mcp:// URI, not the file;
//   - the portal's content read serves an absolute URL in its place;
//   - that URL, fetched with no session at all -- which is what an <img> inside
//     a sandboxed blob: frame sends -- returns the resource's bytes;
//   - the agent's own read still sees the URI it wrote, and a patch round trip
//     through it leaves the reference intact.
//
// Each of those is unit-tested in isolation elsewhere. What this adds is that
// the toolkit's output actually reaches the handler: a unit test that hands the
// handler a hand-built reference row would pass whether or not a save ever
// wrote one.
func TestReferenceSurvivesSaveViewAndPatch(t *testing.T) {
	sys := newRefSystem(t)

	// 1. The agent saves a report that names the logo by its URI.
	saved := decodeSave(t, mustSaveBody(t, sys.toolkit, intAssetBody, []string{refLogoURI}))
	require.Equal(t, 1, saved.ResourcesReferenced)
	assetID := saved.AssetID

	// The stored object carries the URI and none of the file's bytes.
	stored, _, err := sys.blobs.GetObject(t.Context(), intAssetBucket, storedKey(t, sys, assetID))
	require.NoError(t, err)
	assert.Contains(t, string(stored), refLogoURI)
	assert.NotContains(t, string(stored), intLogoBytes)

	// 2. Opening the asset in the portal yields a working absolute URL.
	view := sys.get(t, "/api/v1/portal/assets/"+assetID+"/content")
	require.Equal(t, http.StatusOK, view.Code)
	refURL := extractRefURL(t, view.Body.String())
	assert.True(t, strings.HasPrefix(refURL, intPortalBase+"/portal/refs/"+assetID+"/"),
		"a blob: document cannot resolve a relative path, so the URL must be absolute: %s", refURL)
	assert.NotContains(t, view.Body.String(), refLogoURI)

	// 3. That URL returns the resource's bytes to a caller with no session.
	blob := sys.get(t, strings.TrimPrefix(refURL, intPortalBase))
	require.Equal(t, http.StatusOK, blob.Code)
	assert.Equal(t, intLogoBytes, blob.Body.String())

	// 4. The agent's own read is not rewritten, so its next patch cannot write
	// a platform-internal path back into the asset.
	body := decodeMap(t, mustAction(t, sys.toolkit, manageAssetInput{
		Action: "get_content", AssetID: assetID,
	}))
	assert.Contains(t, body["content"], refLogoURI)
	assert.NotContains(t, body["content"], "/portal/refs/")

	// 5. A patch round trip leaves the reference intact and still resolving.
	mustAction(t, sys.toolkit, manageAssetInput{
		Action: "patch", AssetID: assetID,
		Edits: []textpatch.Edit{{Op: textpatch.OpInsertAfter, Find: "</h1>", Text: "<p>up 12%</p>"}},
	})

	after := sys.get(t, "/api/v1/portal/assets/"+assetID+"/content")
	require.Equal(t, http.StatusOK, after.Code)
	assert.Contains(t, after.Body.String(), "up 12%")
	assert.Equal(t, refURL, extractRefURL(t, after.Body.String()),
		"a surviving reference keeps its URL, so a reader's open page does not break on every save")
}

// storedKey returns the object key the asset's current content lives at.
func storedKey(t *testing.T, sys *refSystem, assetID string) string {
	t.Helper()
	asset := decodeMap(t, mustAction(t, sys.toolkit, manageAssetInput{Action: "get", AssetID: assetID}))
	key, ok := asset["s3_key"].(string)
	require.True(t, ok, "the get action must report the object key")
	return key
}

// extractRefURL pulls the single rewritten reference URL out of served content.
func extractRefURL(t *testing.T, content string) string {
	t.Helper()
	const marker = intPortalBase + "/portal/refs/"
	start := strings.Index(content, marker)
	require.GreaterOrEqual(t, start, 0, "no rewritten reference URL in: %s", content)
	rest := content[start:]
	end := strings.IndexAny(rest, `"' <>`)
	require.Greater(t, end, 0)
	return rest[:end]
}

// mustSaveBody saves one HTML asset with the given body and declaration.
func mustSaveBody(t *testing.T, tk *Toolkit, body string, uris []string) *mcp.CallToolResult {
	t.Helper()
	result, _, err := tk.handleSaveAsset(refCtx(""), nil, saveAssetInput{
		Name: "Q4 Report", Content: body, ContentType: "text/html", Resources: uris,
	})
	require.NoError(t, err)
	require.False(t, result.IsError, "save failed: %s", errText(t, result))
	return result
}

// mustAction runs one manage_asset action and requires it to succeed.
func mustAction(t *testing.T, tk *Toolkit, input manageAssetInput) *mcp.CallToolResult {
	t.Helper()
	result, _, err := tk.handleManageAsset(refCtx(""), nil, input)
	require.NoError(t, err)
	require.False(t, result.IsError, "%s failed: %s", input.Action, errText(t, result))
	return result
}
