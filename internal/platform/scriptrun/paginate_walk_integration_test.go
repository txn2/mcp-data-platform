package scriptrun

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	apigateway "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

type walkAssetStore struct{ inserted int }

func (s *walkAssetStore) InsertExportAsset(context.Context, apigateway.ExportAsset) error {
	s.inserted++
	return nil
}

func (*walkAssetStore) GetByIdempotencyKey(context.Context, string, string) (*apigateway.ExportAssetRef, error) {
	return nil, io.EOF
}

type walkVersionStore struct{}

func (*walkVersionStore) CreateExportVersion(context.Context, apigateway.ExportVersion) (int, error) {
	return 1, nil
}

type walkS3 struct{ items int }

func (s *walkS3) PutObjectStream(_ context.Context, _, _ string, body io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("walkS3: read stream: %w", err)
	}
	var items []any
	if err := json.Unmarshal(data, &items); err != nil {
		return 0, fmt.Errorf("walkS3: asset is not a JSON array: %w", err)
	}
	s.items = len(items)
	return int64(len(data)), nil
}

// TestIntegration_AScriptWalksAPaginatedAPIInOneCall is the #1535
// acceptance for a managed script: through the real chain (the limiter,
// the tool-call middleware, the real api-gateway toolkit), one
// platform.call("api_export", {paginate}) against a 40-page upstream
// returns asset metadata with the page count, the asset holds every item,
// and the limiter admitted the one call on a burst of one token, so the
// script paid one token for the whole collection and paced nothing.
func TestIntegration_AScriptWalksAPaginatedAPIInOneCall(t *testing.T) {
	const pages = 40
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if c := r.URL.Query().Get("cursor"); c != "" {
			page, _ = strconv.Atoi(c)
		}
		body := map[string]any{"data": []map[string]any{{"id": page}}}
		if page < pages {
			body["next_cursor"] = strconv.Itoa(page + 1)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(upstream.Close)

	tk := apigateway.New("api")
	require.NoError(t, tk.AddConnection("vendor", map[string]any{"base_url": upstream.URL}))
	store, s3 := &walkAssetStore{}, &walkS3{}
	tk.SetExportDeps(apigateway.ExportDeps{
		AssetStore: store, VersionStore: &walkVersionStore{}, S3Client: s3, S3Bucket: "exports",
		GetUserContext: func(context.Context) *apigateway.ExportUserContext {
			return &apigateway.ExportUserContext{UserID: "script:changelog", UserEmail: "paced@example.com"}
		},
	})
	server := mcp.NewServer(&mcp.Implementation{Name: "walk", Version: "v0"}, nil)
	tk.RegisterTools(server)
	limiter := toolratelimit.New(60, 1, nil, nil)
	t.Cleanup(limiter.Close)
	server.AddReceivingMiddleware(limiter.Middleware())
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(limitedAuthn{middleware.AuthTypeScript}, limitedAuthz{}, limitedLookup{},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"}))
	caller, cleanup, err := Connect(context.Background(), server, "script-run")
	require.NoError(t, err)
	t.Cleanup(cleanup)

	opts := RunLimits()
	opts.Source = strings.Join([]string{
		`out = platform.call("api_export", {`,
		`    "connection": "vendor", "method": "GET", "path": "/v1/changelog", "name": "changelog.json",`,
		`    "paginate": {"items": "data", "cursor_param": "cursor", "max_pages": 500},`,
		`})`,
		`print(out["pages_fetched"], out["items_merged"], out["stopped_by"], out["asset_id"] != "")`,
	}, "\n")
	opts.Name, opts.RunID, opts.FireTime, opts.Caller = "changelog", "run_1", fireTime, caller
	result, err := Run(context.Background(), opts)
	require.NoError(t, err)

	assert.Equal(t, "40 40 end True\n", result.Log, "one call returned the asset metadata with the walk")
	assert.Equal(t, pages, s3.items, "the asset holds every page's items")
	assert.Equal(t, 1, store.inserted)
	assert.NotContains(t, result.Log, "rate limit:", "one call is one token; nothing was paced")
}
