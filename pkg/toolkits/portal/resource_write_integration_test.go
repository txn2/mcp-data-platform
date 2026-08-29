package portal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/internal/portal/assetrefs"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// resourceRows is the managed-resource half of the assembled system: the record
// store and its content-revision trail, which a Postgres deployment has as one
// object satisfying both interfaces.
type resourceRows struct {
	mu       sync.Mutex
	rows     map[string]*resource.Resource
	versions map[string][]resource.Version
}

func newResourceRows() *resourceRows {
	return &resourceRows{rows: map[string]*resource.Resource{}, versions: map[string][]resource.Version{}}
}

func (s *resourceRows) Insert(_ context.Context, r resource.Resource) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rows[r.ID] = &r
	return nil
}

func (s *resourceRows) Get(_ context.Context, id string) (*resource.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.find(func(r *resource.Resource) bool { return r.ID == id })
}

func (s *resourceRows) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.find(func(r *resource.Resource) bool { return r.URI == uri })
}

// find returns a copy of the first row matching, so a caller mutating what it
// read cannot reach into the store.
func (s *resourceRows) find(match func(*resource.Resource) bool) (*resource.Resource, error) {
	for _, r := range s.rows {
		if match(r) {
			copied := *r
			return &copied, nil
		}
	}
	return nil, fmt.Errorf("resource not found: %w", sql.ErrNoRows)
}

func (s *resourceRows) GetByIDs(_ context.Context, ids []string) (map[string]*resource.Resource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]*resource.Resource{}
	for _, id := range ids {
		if r, ok := s.rows[id]; ok {
			copied := *r
			out[id] = &copied
		}
	}
	return out, nil
}

func (*resourceRows) List(context.Context, resource.Filter) ([]resource.Resource, int, error) {
	return nil, 0, nil
}

func (*resourceRows) Update(context.Context, string, resource.Update) error { return nil }
func (*resourceRows) Move(context.Context, []resource.Move) error {
	return errors.New("resourceRows does not move resources")
}

func (*resourceRows) Delete(context.Context, string) error { return nil }

func (s *resourceRows) AddRevision(_ context.Context, rev resource.Revision) (*resource.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.rows[rev.ResourceID]
	if !ok {
		return nil, fmt.Errorf("resource not found: %w", sql.ErrNoRows)
	}
	v := resource.Version{
		ResourceID: rev.ResourceID, Version: len(s.versions[rev.ResourceID]) + 1,
		MIMEType: rev.MIMEType, SizeBytes: rev.SizeBytes, S3Key: rev.S3Key,
		UploaderSub: rev.UploaderSub, UploaderEmail: rev.UploaderEmail, ChangeSummary: rev.ChangeSummary,
	}
	s.versions[rev.ResourceID] = append(s.versions[rev.ResourceID], v)
	// The head moves with the trail, as it does inside the store's transaction.
	row.S3Key, row.MIMEType, row.SizeBytes = rev.S3Key, rev.MIMEType, rev.SizeBytes
	return &v, nil
}

func (s *resourceRows) ListVersions(_ context.Context, id string) ([]resource.Version, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.versions[id], nil
}

func (*resourceRows) GetVersion(context.Context, string, int) (*resource.Version, error) {
	return nil, fmt.Errorf("version not found: %w", sql.ErrNoRows)
}

func (*resourceRows) PruneVersions(context.Context, string, int) ([]resource.Version, error) {
	return nil, nil
}

var (
	_ resource.Store        = (*resourceRows)(nil)
	_ resource.VersionStore = (*resourceRows)(nil)
	_ assetrefs.Resources   = (*resourceRows)(nil)
)

// writeSystem is the assembled system under test: the real writer over one set
// of resource rows and one blob backend, the real portal toolkit reached
// through a real MCP session, and the real portal HTTP handler serving the
// reference route a rendered asset's <img> hits.
type writeSystem struct {
	session   *mcp.ClientSession
	handler   *portal.Handler
	rows      *resourceRows
	blobs     *sharedS3
	announced []*resource.Resource
}

func newWriteSystem(t *testing.T) *writeSystem {
	t.Helper()
	assets := newInMemoryAssetStore()
	versions := newInMemoryVersionStore()
	versions.assets = assets
	refs := newRefStoreStub()
	blobs := newSharedS3()
	rows := newResourceRows()
	sys := &writeSystem{handler: nil, rows: rows, blobs: blobs}

	tk := New(Config{
		Name: "test", AssetStore: assets, VersionStore: versions,
		S3Client: blobs, S3Bucket: intAssetBucket, BaseURL: intPortalBase, MaxContentSize: 1 << 20,
	})
	declarer := assetrefs.NewDeclarer(refs, assets)
	declarer.BindResources(rows, "")
	tk.SetContentRefs(declarer)
	tk.SetResourceWriter(resourcewrite.New(resourcewrite.Deps{
		Store: rows, Blobs: blobs, Bucket: intResBucket, URIScheme: "mcp",
		Registered: func(r *resource.Resource) { sys.announced = append(sys.announced, r) },
	}))

	server := mcp.NewServer(&mcp.Implementation{Name: "platform", Version: "v0"}, nil)
	tk.RegisterTools(server)
	server.AddReceivingMiddleware(agentIdentityMiddleware)
	sys.session = connectWriteAgent(t, server)

	sys.handler = portal.NewHandler(portal.Deps{
		AssetStore:       assets,
		VersionStore:     versions,
		ShareStore:       portal.NewNoopShareStore(),
		S3Client:         blobs,
		S3Bucket:         intAssetBucket,
		PublicBaseURL:    intPortalBase,
		RateLimit:        portal.RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		ContentRefs:      refs,
		ResourceReader:   rows,
		ResourceBlobs:    blobs,
		ResourceS3Bucket: intResBucket,
	}, portalUserMiddleware("user1"))

	return sys
}

// agentIdentityMiddleware puts the signed-in identity on the request context,
// which is what the platform's auth middleware leaves behind for every tool.
func agentIdentityMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		return next(middleware.WithPlatformContext(ctx, &middleware.PlatformContext{
			UserID: "user1", UserEmail: refAuthor, SessionID: "sess1",
		}), method, req)
	}
}

func connectWriteAgent(t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	session, err := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "v0"}, nil).
		Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// call runs one tool over the real session, exactly as an agent would.
func (s *writeSystem) call(t *testing.T, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := s.session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	require.NoError(t, err)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	if res.IsError {
		return map[string]any{"error": tc.Text}, true
	}
	out := map[string]any{}
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out), tc.Text)
	return out, false
}

func (s *writeSystem) mustCall(t *testing.T, tool string, args map[string]any) map[string]any {
	t.Helper()
	out, isErr := s.call(t, tool, args)
	require.False(t, isErr, "%s failed: %v", tool, out["error"])
	return out
}

func (s *writeSystem) fetch(t *testing.T, path string) (status int, body string) {
	t.Helper()
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody))
	return rec.Code, rec.Body.String()
}

// trimBase turns the absolute URL a rendered page fetches into the path the
// handler is asked for.
func trimBase(url string) string { return strings.TrimPrefix(url, intPortalBase) }

// TestAgentWritesTheFileItsAssetReads is the end-to-end acceptance test for
// #1487, run over the real writer, the real toolkit reached through a real MCP
// session, and the real portal handler sharing one set of stores.
//
// It asserts, in one pass, every claim the feature makes: an agent creates a
// managed resource, an asset references it, the agent replaces its content, and
// the URL a rendered asset fetches serves the new bytes with the asset never
// re-saved -- plus the version trail that makes the change auditable and
// revertible.
//
// Each half is unit-tested in isolation elsewhere. What this adds is that the
// tool's write actually reaches the serving route: a unit test that hands the
// handler a hand-built resource row would pass whether or not manage_resource
// ever wrote one.
func TestAgentWritesTheFileItsAssetReads(t *testing.T) {
	sys := newWriteSystem(t)

	// 1. The agent files the data as a managed resource.
	created := sys.mustCall(t, ManageResourceToolName, map[string]any{
		"action": "create", "filename": "weather.csv", "display_name": "Daily Weather",
		"path": "datasets", "description": "Highs and lows by day",
		"content": "day,high\nmon,71\ntue,68\n", "content_type": "text/csv",
	})
	uri, _ := created["uri"].(string)
	reference, _ := created["reference"].(string)
	assert.Equal(t, "mcp://user/user1/datasets/weather.csv", uri)
	assert.Equal(t, "text/csv", created["content_type"])
	require.Len(t, sys.announced, 1, "a created resource is announced to connected clients")

	// 2. A report references it by URI, storing the reference and not the file.
	saved := sys.mustCall(t, SaveToolName, map[string]any{
		"name": "Weather Report", "content_type": "text/html",
		"content":    fmt.Sprintf(`<h1>Weather</h1><script>fetch(%q)</script>`, uri),
		"references": []any{uri},
	})
	assetID, _ := saved["asset_id"].(string)
	require.NotEmpty(t, assetID)
	assert.Equal(t, float64(1), saved["references_declared"])

	// 3. The URL the rendered report fetches serves the original bytes.
	view := sys.mustGetContent(t, assetID)
	refURL := extractRefURL(t, view)
	code, body := sys.fetch(t, trimBase(refURL))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "day,high\nmon,71\ntue,68\n", body)

	// 4. The agent replaces the file's content. Nothing about the report changes.
	replaced := sys.mustCall(t, ManageResourceToolName, map[string]any{
		"action": "replace_content", "reference": reference,
		"content": "day,high\nmon,88\ntue,90\n", "change_summary": "hourly refresh",
	})
	assert.Equal(t, created["resource_id"], replaced["resource_id"], "the id every reference is keyed on")
	assert.Equal(t, uri, replaced["uri"], "the URI the asset's reference resolves through")
	assert.Equal(t, "weather.csv", replaced["filename"])
	assert.Equal(t, float64(2), replaced["version"])

	// 5. The same URL, with the asset never re-saved, now serves the new bytes.
	afterURL := extractRefURL(t, sys.mustGetContent(t, assetID))
	assert.Equal(t, refURL, afterURL, "a replacement does not move the URL a reader's open page holds")
	code, body = sys.fetch(t, trimBase(afterURL))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "day,high\nmon,88\ntue,90\n", body)

	// 6. The change is in the version history, with its author, and the version
	//    before it is still readable where it was written.
	resourceID, _ := created["resource_id"].(string)
	history, err := sys.rows.ListVersions(t.Context(), resourceID)
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, refAuthor, history[1].UploaderEmail)
	assert.Equal(t, "hourly refresh", history[1].ChangeSummary)
	prior, _, err := sys.blobs.GetObject(t.Context(), intResBucket, history[0].S3Key)
	require.NoError(t, err)
	assert.Equal(t, "day,high\nmon,71\ntue,68\n", string(prior), "the prior version is restorable")
}

// TestAgentIsRefusedAScopeItMayNotWrite is the other half of the acceptance:
// the refusal an ordinary caller meets, over the same real session.
func TestAgentIsRefusedAScopeItMayNotWrite(t *testing.T) {
	sys := newWriteSystem(t)

	out, isErr := sys.call(t, ManageResourceToolName, map[string]any{
		"action": "create", "filename": "policy.md", "display_name": "Policy",
		"path": "runbooks", "description": "Retention policy", "content": "# Policy",
		"content_type": "text/markdown", "scope": "global",
	})

	require.True(t, isErr)
	message, _ := out["error"].(string)
	assert.Contains(t, message, "global scope")
	assert.NotContains(t, message, "policy.md", "the refusal names the scope, not the file")
	assert.Empty(t, sys.announced, "a refused create announces nothing")
}

// mustGetContent reads the asset's served content, which is where a reference
// is rewritten into the URL a rendered page fetches.
func (s *writeSystem) mustGetContent(t *testing.T, assetID string) string {
	t.Helper()
	code, body := s.fetch(t, "/api/v1/portal/assets/"+assetID+"/content")
	require.Equal(t, http.StatusOK, code)
	return body
}
