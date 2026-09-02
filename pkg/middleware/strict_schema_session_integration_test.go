package middleware_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
)

// strictSchemaTools are the api_* tools, whose input schemas are closed to
// unknown top-level arguments (issue #1057).
var strictSchemaTools = []string{
	apigateway.ToolInvokeEndpoint,
	apigateway.ToolDiscover,
	"api_export",
}

// strictSchemaArgs is a minimal valid argument set per tool: enough to satisfy
// each schema's required properties so the only thing that can fail validation
// is an argument the schema does not publish.
func strictSchemaArgs(tool string) map[string]any {
	switch tool {
	case apigateway.ToolInvokeEndpoint:
		return map[string]any{"connection": "crm", "method": "GET", "path": "/v1/things"}
	case apigateway.ToolDiscover:
		return map[string]any{"connection": "crm", "operation_id": "getThings"}
	case "api_export":
		return map[string]any{"connection": "crm", "name": "things", "method": "GET", "path": "/v1/things"}
	default:
		return map[string]any{"connection": "crm"}
	}
}

// strictSchemaSessionServer assembles the REAL chain a closed schema has to
// survive in production: the session-handle resolver inside MCPToolCallMiddleware
// (which strips the platform-injected session_id argument) plus the tools/list
// schema decorator (which advertises it), with the real api_* tools registered.
//
// ExportDeps are wired empty on purpose — api_export's handler cannot succeed
// here, but every assertion in this file is about the schema boundary, which the
// SDK enforces before any handler runs.
func strictSchemaSessionServer(t *testing.T) *mcp.Server {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(upstream.Close)

	store := pkgsession.NewMemoryStore(time.Hour)
	server := mcp.NewServer(&mcp.Implementation{Name: "strict-schema-session", Version: "v0"}, nil)

	mcp.AddTool(server, &mcp.Tool{Name: shInitTool, Description: "init"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			handle, err := pkgsession.GenerateHandle()
			if err != nil {
				return nil, nil, fmt.Errorf("mint handle: %w", err)
			}
			uid := ""
			if pc := middleware.GetPlatformContext(ctx); pc != nil {
				uid = pc.UserID
			}
			now := time.Now()
			if cerr := store.Create(ctx, &pkgsession.Session{
				ID: handle, UserID: uid, CreatedAt: now, LastActiveAt: now,
				ExpiresAt: now.Add(time.Hour),
				State:     map[string]any{pkgsession.StateKeyMintedBy: pkgsession.MintedByPlatformInfo},
			}); cerr != nil {
				return nil, nil, fmt.Errorf("persist handle: %w", cerr)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: handle}}}, nil, nil
		})

	tk := apigateway.New("primary")
	require.NoError(t, tk.AddConnection("crm", map[string]any{"base_url": upstream.URL}))
	tk.SetExportDeps(apigateway.ExportDeps{})
	tk.RegisterTools(server)

	// Innermost first: the error contract normalizes the SDK's input-validation
	// failure into the platform's {code, category, message, hint} envelope, so
	// the assertions below see the message an agent actually receives.
	server.AddReceivingMiddleware(middleware.MCPErrorContractMiddleware())
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{UserID: "user-1", Email: "analyst@example.com", Roles: []string{"analyst"}}},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{kind: "api", name: "primary", conn: "crm"},
		middleware.ToolCallConfig{
			Transport:    "http",
			AdminPersona: "admin",
			SessionResolver: middleware.NewSessionResolver(store, middleware.SessionResolverConfig{
				Enabled:  true,
				Require:  true,
				TTL:      time.Hour,
				InitTool: shInitTool,
			}),
		},
	))
	server.AddReceivingMiddleware(middleware.MCPSessionHandleSchemaMiddleware(shInitTool))
	return server
}

// resultText joins a result's text content.
func resultText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// TestIntegration_StrictSchema_SessionHandleStillPasses is the ordering proof
// for issue #1057: the platform injects session_id into the tools/list view of
// every api_* tool, and the resolver strips it from the arguments BEFORE the SDK
// validates them against the now-closed schema. A handle-carrying call must
// therefore not be refused as an unknown property.
func TestIntegration_StrictSchema_SessionHandleStillPasses(t *testing.T) {
	ctx := context.Background()
	server := strictSchemaSessionServer(t)
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	handle := mintViaPlatformInfo(ctx, t, sess)

	for _, tool := range strictSchemaTools {
		t.Run(tool, func(t *testing.T) {
			args := strictSchemaArgs(tool)
			args["session_id"] = handle
			res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
			require.NoError(t, err)
			assert.NotContains(t, resultText(res), "additional properties",
				"%s refused the platform-injected session_id; the resolver must strip it before validation", tool)
			if tool == apigateway.ToolInvokeEndpoint {
				// The one tool that can complete against the test upstream: proof
				// the call reached the handler rather than merely dodging one
				// validation message.
				assert.False(t, res.IsError, "handle-carrying invoke must execute: %s", resultText(res))
			}
		})
	}
}

// TestIntegration_StrictSchema_ListAdvertisesSessionHandle proves the schema the
// model READS still carries session_id after closure: the list decorator adds
// the property to a closed schema rather than being defeated by it.
func TestIntegration_StrictSchema_ListAdvertisesSessionHandle(t *testing.T) {
	ctx := context.Background()
	server := strictSchemaSessionServer(t)
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	listed, err := sess.ListTools(ctx, nil)
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, tool := range listed.Tools {
		schema, ok := tool.InputSchema.(map[string]any)
		if !ok {
			continue
		}
		props, _ := schema["properties"].(map[string]any)
		if tool.Name == shInitTool {
			assert.NotContains(t, props, "session_id", "the init tool must not advertise session_id")
			continue
		}
		seen[tool.Name] = true
		assert.Contains(t, props, "session_id", "%s must advertise the injected session handle", tool.Name)
		assert.Equal(t, false, schema["additionalProperties"],
			"%s must stay closed to unknown arguments after schema injection", tool.Name)
	}
	for _, tool := range strictSchemaTools {
		assert.True(t, seen[tool], "%s missing from tools/list", tool)
	}
}

// TestIntegration_StrictSchema_UnknownArgumentRefusedThroughChain proves the
// refusal survives the full middleware chain: a handle-carrying call with a
// misnamed argument is still refused by name, so the strictness the model sees
// in tools/list is the strictness it gets at call time.
func TestIntegration_StrictSchema_UnknownArgumentRefusedThroughChain(t *testing.T) {
	ctx := context.Background()
	server := strictSchemaSessionServer(t)
	sess := mustConnect(ctx, t, server)
	defer func() { _ = sess.Close() }()

	handle := mintViaPlatformInfo(ctx, t, sess)

	args := strictSchemaArgs(apigateway.ToolInvokeEndpoint)
	args["session_id"] = handle
	args["parameters"] = map[string]any{"limit": 1}

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{
		Name: apigateway.ToolInvokeEndpoint, Arguments: args,
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "misnamed argument must be refused through the chain")
	text := resultText(res)
	assert.Contains(t, text, "parameters", "the refusal must name the offending property")
	assert.NotContains(t, text, "session_id", "the stripped handle must not appear as an offender")

	// The refusal is caller-correctable, and says so: an agent that reads the
	// category knows to fix the argument rather than retry the same call.
	envelope, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok, "error result carries a structured envelope: %#v", res.StructuredContent)
	payload, ok := envelope["error"].(map[string]any)
	require.True(t, ok, "structured envelope carries an error object: %#v", envelope)
	assert.Equal(t, "invalid_arguments", payload["code"], "the agent-facing code for a schema rejection")
	assert.Equal(t, middleware.ErrCategoryClientInput, payload["category"])
	assert.NotEmpty(t, payload["hint"], "an input fault must carry a corrective hint")
}
