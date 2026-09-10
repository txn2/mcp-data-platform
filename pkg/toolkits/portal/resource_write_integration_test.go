package portal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"sort"
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

// List applies the filter the way the Postgres store's WHERE clause does: the
// visible libraries, then the folder prefix. A fake that answered every row
// would let the listing test pass while the caller was being shown another
// library's files.
func (s *resourceRows) List(_ context.Context, f resource.Filter) ([]resource.Resource, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]resource.Resource, 0, len(s.rows))
	for _, r := range s.rows {
		if !f.AllScopes && !inAnyLibrary(f.Scopes, r) {
			continue
		}
		if !resource.PathUnder(r.Path, f.Path) {
			continue
		}
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Filename < out[j].Filename })
	return out, len(out), nil
}

// inAnyLibrary reports whether a resource is in one of the libraries a listing
// was narrowed to.
func inAnyLibrary(scopes []resource.ScopeFilter, r *resource.Resource) bool {
	for _, lib := range scopes {
		if lib.Scope != r.Scope {
			continue
		}
		if lib.Scope == resource.ScopeGlobal || lib.ScopeID == r.ScopeID {
			return true
		}
	}
	return false
}

func (*resourceRows) Update(context.Context, string, resource.Update) error { return nil }
func (*resourceRows) Move(context.Context, []resource.Move) error {
	return errors.New("resourceRows does not move resources")
}

// Delete removes the row and the version trail beneath it, which is what the
// Postgres store does through ON DELETE CASCADE. A fake that kept either would
// let the delete test pass while the address it emptied still resolved.
func (s *resourceRows) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rows, id)
	delete(s.versions, id)
	return nil
}

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
	refs      *refStoreStub
	announced []*resource.Resource
	// unregistered records the URIs a delete took out of the MCP resource
	// list, which is what stops a client that has already listed from going on
	// offering a file that is gone.
	unregistered []string
}

func newWriteSystem(t *testing.T) *writeSystem {
	t.Helper()
	assets := newInMemoryAssetStore()
	versions := newInMemoryVersionStore()
	versions.assets = assets
	refs := newRefStoreStub()
	blobs := newSharedS3()
	rows := newResourceRows()
	sys := &writeSystem{handler: nil, rows: rows, blobs: blobs, refs: refs}

	tk := New(Config{
		Name: "test", AssetStore: assets, VersionStore: versions,
		S3Client: blobs, S3Bucket: intAssetBucket, BaseURL: intPortalBase, MaxContentSize: 1 << 20,
	})
	declarer := assetrefs.NewDeclarer(refs, assets)
	declarer.BindResources(rows, "")
	tk.SetContentRefs(declarer)
	tk.SetResourceWriter(resourcewrite.New(resourcewrite.Deps{
		Store: rows, Blobs: blobs, Bucket: intResBucket, URIScheme: "mcp",
		Registered:   func(r *resource.Resource) { sys.announced = append(sys.announced, r) },
		Unregistered: func(uri string) { sys.unregistered = append(sys.unregistered, uri) },
	}))
	// The delete's warning path over the real reference store the declarer
	// writes into (#1665), so an asset reference made through save_asset is the
	// same row the delete is refused by.
	tk.SetResourceHolds(referencingAssets{refs: refs})

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

// referencingAssets counts the assets whose content references a file, over the
// same reference store save_asset writes into.
//
// It stands in for the composition root's adapter, which spans three reverse
// lookups in three layers and cannot be imported here without a cycle. The row
// it counts is the real one: an asset saved through the tool in this test is
// what makes the count non-zero.
type referencingAssets struct {
	refs *refStoreStub
}

func (r referencingAssets) ResourceHolds(ctx context.Context, resourceID string) (ResourceHolds, error) {
	rows, err := r.refs.ListByTarget(ctx, assetrefs.TargetResource, resourceID, 50)
	if err != nil {
		return ResourceHolds{}, err
	}
	return ResourceHolds{Assets: len(rows)}, nil
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

// Folders is not exercised here: this fake stands in for the read paths a
// resourceRows uses, and none of them lists a library's folders.
func (*resourceRows) Folders(_ context.Context, _ resource.Filter) ([]resource.Folder, error) {
	return nil, nil
}

// Tags is not exercised here: this fake stands in for the read paths a
// resourceRows uses, and none of them lists a library's tags.
func (*resourceRows) Tags(_ context.Context, _ resource.Filter) ([]string, error) {
	return nil, nil
}

// The capture routes are not exercised here: this fake stands in for the read
// paths a resourceRows uses, and none of them captures or lists a thumbnail.
func (*resourceRows) SetThumbnail(_ context.Context, _ string, _ resource.ThumbnailCapture) error {
	return nil
}

func (*resourceRows) ClearThumbnail(_ context.Context, _, _ string) error { return nil }

func (*resourceRows) PendingThumbnails(_ context.Context, _ resource.Filter, _ int) ([]resource.Resource, error) {
	return nil, nil
}

// TestAgentFindsRefreshesAndRemovesAFileByItsPath is the end-to-end test for
// #1665, over the same real session, real writer and real portal handler.
//
// It follows one file through the whole lifecycle an agent that keeps no id
// has: it looks the address up before anything is there, lands a file with one
// idempotent call, lands it again at the same address and gets the next version
// of the same file, finds it in a folder listing, is refused a delete while a
// saved asset still reads it, and then deletes it with force -- after which the
// URL the report fetches stops serving.
//
// What this adds over the unit tests is that the address is the same address at
// every layer: the one the create writes, the one the lookup resolves, the one
// the portal serves through, and the one the delete empties.
func TestAgentFindsRefreshesAndRemovesAFileByItsPath(t *testing.T) {
	sys := newWriteSystem(t)
	address := map[string]any{"path": "datasets", "filename": "weather.csv"}

	// 1. Nothing is filed there yet, and the lookup says so rather than failing.
	empty := sys.mustCall(t, ManageResourceToolName, withAction("get", address))
	assert.Equal(t, false, empty["found"])
	assert.Equal(t, "mcp://user/user1/datasets/weather.csv", empty["uri"],
		"the address is named even when it holds nothing")

	// 2. One idempotent call lands the file, and the caller keeps no id.
	created := sys.mustCall(t, ManageResourceToolName, withAction("create", map[string]any{
		"path": "datasets", "filename": "weather.csv", "display_name": "Daily Weather",
		"description": "Highs and lows by day", "content": "day,high\nmon,71\n",
		"content_type": "text/csv", "if_exists": "replace",
	}))
	uri, _ := created["uri"].(string)
	resourceID, _ := created["resource_id"].(string)
	require.NotEmpty(t, resourceID)

	// 3. The same call again is the NEXT VERSION of that same file, not a second one.
	again := sys.mustCall(t, ManageResourceToolName, withAction("create", map[string]any{
		"path": "datasets", "filename": "weather.csv", "display_name": "Daily Weather",
		"description": "Highs and lows by day", "content": "day,high\nmon,88\n",
		"content_type": "text/csv", "if_exists": "replace", "change_summary": "second pull",
	}))
	assert.Equal(t, resourceID, again["resource_id"], "the address is the identity, not a remembered id")
	assert.Equal(t, uri, again["uri"])
	assert.Equal(t, float64(2), again["version"])

	// 4. The lookup now finds it, and the folder listing holds it.
	found := sys.mustCall(t, ManageResourceToolName, withAction("get", address))
	assert.Equal(t, true, found["found"])
	record, _ := found["resource"].(map[string]any)
	require.NotNil(t, record)
	assert.Equal(t, resourceID, record["resource_id"])
	assert.Equal(t, "Daily Weather", record["display_name"])

	listed := sys.mustCall(t, ManageResourceToolName, withAction("list", map[string]any{"path": "datasets"}))
	assert.Equal(t, float64(1), listed["total"])

	// 5. A report references the file, and the delete is refused while it does.
	saved := sys.mustCall(t, SaveToolName, map[string]any{
		"name": "Weather Report", "content_type": "text/html",
		"content":    fmt.Sprintf(`<h1>Weather</h1><script>fetch(%q)</script>`, uri),
		"references": []any{uri},
	})
	assetID, _ := saved["asset_id"].(string)
	refURL := extractRefURL(t, sys.mustGetContent(t, assetID))
	code, body := sys.fetch(t, trimBase(refURL))
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, "day,high\nmon,88\n", body)

	refused := sys.mustCall(t, ManageResourceToolName, withAction("delete", address))
	assert.Equal(t, false, refused["deleted"])
	message, _ := refused["message"].(string)
	assert.Contains(t, message, "1 asset references this file")
	assert.Contains(t, message, "force=true")
	stillThere := sys.mustCall(t, ManageResourceToolName, withAction("get", address))
	assert.Equal(t, true, stillThere["found"], "a refused delete leaves the file where it was")

	// 6. Forced, the file goes, and everything that reached it stops reaching it.
	deleted := sys.mustCall(t, ManageResourceToolName,
		withAction("delete", map[string]any{"path": "datasets", "filename": "weather.csv", "force": true}))
	assert.Equal(t, true, deleted["deleted"])
	assert.Equal(t, []string{uri}, sys.unregistered,
		"a client that has already listed must stop being offered a file that is gone")

	gone := sys.mustCall(t, ManageResourceToolName, withAction("get", address))
	assert.Equal(t, false, gone["found"], "the address the file lived at is empty again")

	code, _ = sys.fetch(t, trimBase(refURL))
	assert.NotEqual(t, http.StatusOK, code, "the URL the report fetches no longer serves the deleted file")
}

// withAction is one manage_resource call's arguments: the action plus the
// address or options it acts on.
func withAction(action string, args map[string]any) map[string]any {
	out := map[string]any{"action": action}
	maps.Copy(out, args)
	return out
}
