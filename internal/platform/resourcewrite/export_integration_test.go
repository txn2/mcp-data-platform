package resourcewrite_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// This is the assembled proof for #1663: a real api_export call, through a real
// MCP server and the real tool-call middleware, against a real upstream, lands
// the response as a managed resource in the real writer and the real store, and
// a second identical call records the next version of that same file.
//
// Every unit test around this feature hands some layer its input directly. What
// none of them can prove is the trip the capability actually depends on: the
// caller's identity traveling from the authenticator, through the
// PlatformContext the middleware puts on the context, into the claims the
// landing is authorized by, and the late-bound holder the toolkit was wired with
// resolving to a lander that only exists later. That is the whole of what breaks
// silently, so it is what this exercises end to end.

// landingAuthn authenticates every call as one analyst.
type landingAuthn struct{}

func (landingAuthn) Authenticate(_ context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{UserID: "user-7", Email: "analyst@example.com", Roles: []string{"analyst"}}, nil
}

// landingAuthz authorizes every call as the analyst persona.
type landingAuthz struct{}

func (landingAuthz) IsAuthorized(
	_ context.Context, _ string, _ []string, _, _ string,
) (allowed bool, persona, reason string) {
	return true, "analyst", ""
}

// landingLookup names the toolkit a tool belongs to, as the registry does.
type landingLookup struct{}

func (landingLookup) GetToolkitForTool(_ string) registry.ToolkitMatch {
	return registry.ToolkitMatch{Kind: apigateway.Kind, Name: "apigateway", Connection: "crm", Found: true}
}

func TestAPIExportLandsAManagedResource_Integration(t *testing.T) {
	var hits int
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		// text/plain on purpose: a CSV has no content signature, and an upstream
		// that labels one this way must still produce a file registerable as a
		// table. The destination's filename is what settles that.
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("id,total\n1,10\n"))
	}))
	defer upstream.Close()

	store, blobs := newMemStore(), newMemBlobs()
	writer := resourcewrite.New(resourcewrite.Deps{
		Store: store, Blobs: blobs, Bucket: testBucket, URIScheme: testScheme,
	})
	require.NotNil(t, writer)

	// The holder is what the toolkit is wired with, and the lander is bound onto
	// it afterwards, which is the sequencing production has: the export tools are
	// assembled with the portal layer and the managed-resource layer is built
	// after them.
	landing := &resourcewrite.Ref{}
	var followed []int
	tk := apigateway.NewMulti(apigateway.MultiConfig{
		DefaultName: "crm",
		Instances: map[string]apigateway.Config{"crm": {
			BaseURL:          upstream.URL,
			AuthMode:         apigateway.AuthModeNone,
			ConnectTimeout:   2 * time.Second,
			CallTimeout:      5 * time.Second,
			MaxResponseBytes: 1 << 20,
			TrustLevel:       apigateway.TrustLevelTrusted,
		}},
	})
	defer func() { _ = tk.Close() }()
	tk.SetExportDeps(apigateway.ExportDeps{
		AssetStore:     unusedAssetStore{},
		VersionStore:   unusedAssetStore{},
		ResourceLander: landing,
		S3Bucket:       "assets",
		GetUserContext: func(ctx context.Context) *apigateway.ExportUserContext {
			pc := middleware.GetPlatformContext(ctx)
			if pc == nil {
				return nil
			}
			return &apigateway.ExportUserContext{UserID: pc.UserID, UserEmail: pc.UserEmail, SessionID: pc.SessionID}
		},
	})

	server := mcp.NewServer(&mcp.Implementation{Name: "landing", Version: "v0"}, nil)
	tk.RegisterTools(server)
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		landingAuthn{}, landingAuthz{}, landingLookup{},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))

	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, st, nil)
	require.NoError(t, err)
	sess, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil).Connect(ctx, ct, nil)
	require.NoError(t, err)
	defer func() { _ = sess.Close() }()

	// Before the library exists the tool answers the reason rather than failing
	// in a way that reads as a fault.
	refused := exportCall(t, sess)
	require.True(t, refused.IsError)
	assert.Contains(t, resultText(t, refused), "managed-resource library")
	assert.Equal(t, 0, hits, "a destination with nothing behind it did not call the upstream")

	lander := resourcewrite.NewLander(resourcewrite.LanderDeps{Writer: writer})
	lander.SetTableFollower(func(_ context.Context, _ string, version int) []string {
		followed = append(followed, version)
		return []string{"scratch.orders followed onto version " + strconv.Itoa(version) + "."}
	})
	landing.Bind(lander)

	first := landedResource(t, exportCall(t, sess))
	assert.True(t, first.Created)
	assert.Equal(t, 1, first.Version)
	// The identity came from the authenticator, through the middleware, into the
	// claims: the file is in the caller's own library, addressed by their subject.
	assert.Equal(t, "mcp://user/user-7/datasets/orders.csv", first.URI)
	assert.Equal(t, "mcp:resource:"+first.ResourceID, first.Reference)
	// The filename settled the type the declaration could not.
	assert.Equal(t, "text/csv", first.ContentType)

	stored, err := store.GetByURI(ctx, first.URI)
	require.NoError(t, err)
	assert.Equal(t, "analyst@example.com", stored.UploaderEmail)
	assert.Equal(t, resource.ScopeUser, stored.Scope)
	body, _, err := blobs.GetObject(ctx, testBucket, stored.S3Key)
	require.NoError(t, err)
	assert.Equal(t, "id,total\n1,10\n", string(body), "the upstream body reached storage")

	second := landedResource(t, exportCall(t, sess))
	assert.Equal(t, first.ResourceID, second.ResourceID, "the same path is the same file")
	assert.Equal(t, first.URI, second.URI)
	assert.False(t, second.Created)
	assert.Equal(t, 2, second.Version)
	assert.Equal(t, []int{2}, followed, "the tables over the file followed the version written")
	require.Len(t, second.Tables, 1)
	assert.Contains(t, second.Tables[0], "version 2")

	versions, err := store.ListVersions(ctx, second.ResourceID)
	require.NoError(t, err)
	assert.Len(t, versions, 2, "the file's history holds both landings")
	assert.Equal(t, 2, hits, "each landing made exactly one upstream call")
}

// exportCall drives one api_export at the managed-resource destination through
// the real client session.
func exportCall(t *testing.T, sess *mcp.ClientSession) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "api_export",
		Arguments: map[string]any{
			"connection": "crm", "method": http.MethodGet, "path": "/v1/orders",
			"name":     "ACME orders",
			"resource": map[string]any{"path": "datasets", "filename": "orders.csv"},
		},
	})
	require.NoError(t, err)
	return res
}

// landedResource reads the resource half of an api_export result.
func landedResource(t *testing.T, res *mcp.CallToolResult) struct {
	ResourceID  string   `json:"resource_id"`
	Reference   string   `json:"reference"`
	URI         string   `json:"uri"`
	ContentType string   `json:"content_type"`
	Version     int      `json:"version"`
	Created     bool     `json:"created"`
	Tables      []string `json:"table_changes"`
} {
	t.Helper()
	require.False(t, res.IsError, "api_export refused the call: %s", resultText(t, res))
	var out struct {
		Resource struct {
			ResourceID  string   `json:"resource_id"`
			Reference   string   `json:"reference"`
			URI         string   `json:"uri"`
			ContentType string   `json:"content_type"`
			Version     int      `json:"version"`
			Created     bool     `json:"created"`
			Tables      []string `json:"table_changes"`
		} `json:"resource"`
		AssetID string `json:"asset_id"`
	}
	require.NoError(t, json.Unmarshal([]byte(resultText(t, res)), &out))
	require.NotEmpty(t, out.Resource.ResourceID, "the result carries no resource: %s", resultText(t, res))
	assert.Empty(t, out.AssetID, "a resource export wrote an asset too")
	return out.Resource
}

func resultText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	text, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected text content, got %T", res.Content[0])
	return text.Text
}

// unusedAssetStore satisfies the asset half of the export deps, which a resource
// destination never reaches. A method that is called is a test failure, which is
// the assertion it carries.
type unusedAssetStore struct{}

func (unusedAssetStore) InsertExportAsset(context.Context, apigateway.ExportAsset) error {
	panic("a resource destination wrote a portal asset")
}

func (unusedAssetStore) GetByIdempotencyKey(context.Context, string, string) (*apigateway.ExportAssetRef, error) {
	panic("a resource destination looked up an asset idempotency key")
}

func (unusedAssetStore) CreateExportVersion(context.Context, apigateway.ExportVersion) (int, error) {
	panic("a resource destination wrote a portal asset version")
}
