//go:build integration

package provenance_test

// End-to-end provenance capture over a real PostgreSQL audit store (#1320).
//
// Every claim this feature makes is about the join between three things a unit
// test can stub apart: the id minted before a call runs, the audit row written
// after it returns, and the asset written later that cites it. These tests run
// the real assembled server — tool-call middleware, the purpose resolver, the
// audit writer, the call-reference stamp, the portal toolkit — against a real
// database, and a second server instance over the same database for the
// multi-replica claim.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/provenance"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/audit"
	auditpostgres "github.com/txn2/mcp-data-platform/pkg/audit/postgres"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

const (
	capUserID   = "550e8400-e29b-41d4-a716-446655440333"
	capEmail    = "analyst@example.com"
	capBucket   = "portal-assets"
	failingSQL  = "SELECT * FROM nope"
	goodSQL     = "SELECT region, revenue FROM sales"
	queryReason = "Sizing Q3 revenue by region for the board deck."
	invokeGoal  = "Pulling the CRM account list the report is keyed on."
)

// --- fakes the assembled server needs -------------------------------------

type memS3 struct{ objects map[string][]byte }

func newMemS3() *memS3 { return &memS3{objects: map[string][]byte{}} }

func (m *memS3) PutObject(_ context.Context, bucket, key string, body []byte, _ string) error {
	m.objects[bucket+"/"+key] = body
	return nil
}

func (m *memS3) PutObjectStream(_ context.Context, bucket, key string, body io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("reading body: %w", err)
	}
	m.objects[bucket+"/"+key] = data
	return int64(len(data)), nil
}

func (m *memS3) GetObject(_ context.Context, bucket, key string) ([]byte, string, error) {
	body, ok := m.objects[bucket+"/"+key]
	if !ok {
		return nil, "", errors.New("no such object")
	}
	return body, "text/markdown", nil
}

func (m *memS3) DeleteObject(_ context.Context, bucket, key string) error {
	delete(m.objects, bucket+"/"+key)
	return nil
}

func (*memS3) Close() error { return nil }

type fixedAuthn struct{ user *middleware.UserInfo }

func (f *fixedAuthn) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return f.user, nil
}

type allowAuthz struct{}

func (allowAuthz) IsAuthorized(context.Context, string, []string, string, string) (bool, string, string) {
	return true, "analyst", ""
}

// toolkitLookup maps the test's tools onto the toolkit kinds the platform
// classifies them by, which is what decides a call's provenance kind.
type toolkitLookup struct{}

func (toolkitLookup) GetToolkitForTool(tool string) registry.ToolkitMatch {
	switch tool {
	case "trino_query":
		return registry.ToolkitMatch{Kind: "trino", Name: "prod", Connection: "warehouse", Found: true}
	case "api_invoke_endpoint":
		return registry.ToolkitMatch{Kind: "api", Name: "gateway", Connection: "crm", Found: true}
	case portalkit.SaveToolName, portalkit.ManageToolName:
		return registry.ToolkitMatch{Kind: "portal", Name: "portal", Found: true}
	default:
		return registry.ToolkitMatch{}
	}
}

// --- harness ---------------------------------------------------------------

// replica is one server instance: its own audit writer and MCP server over a
// shared database, exactly as two processes of the platform would be.
type replica struct {
	server     *mcp.Server
	auditStore *auditpostgres.Store
	writer     *audit.AsyncWriter
}

// newReplica assembles a server over db: the portal toolkit with provenance
// capture wired, two fake data tools, and the real middleware chain.
func newReplica(t *testing.T, db *sql.DB, sessions pkgsession.Store, s3 *memS3) *replica {
	t.Helper()

	auditStore := auditpostgres.New(db, auditpostgres.Config{RetentionDays: 30})
	writer := audit.NewAsyncWriter(auditStore)
	t.Cleanup(func() { _ = writer.Close(context.Background()) })
	logger := middleware.NewAuditStoreAdapter(writer)

	flusher, ok := logger.(provenance.Flusher)
	require.True(t, ok, "the audit adapter must expose the flush a capture waits on")
	capturer := provenance.New(auditStore, flusher)

	toolkit := portalkit.New(portalkit.Config{
		Name:              "portal",
		AssetStore:        portal.NewPostgresAssetStore(db, nil),
		VersionStore:      portal.NewPostgresVersionStore(db, nil, nil, nil),
		S3Client:          s3,
		S3Bucket:          capBucket,
		S3Prefix:          "assets/",
		CaptureProvenance: capturer.Capture,
	})

	server := mcp.NewServer(&mcp.Implementation{Name: "provenance-test", Version: "v0"}, nil)
	toolkit.RegisterTools(server)

	// trino_query fails on the sentinel statement and otherwise returns rows;
	// api_invoke_endpoint always answers.
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "Run a query",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"},"connection":{"type":"string"}}}`),
	}, func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var args struct {
			SQL string `json:"sql"`
		}
		_ = json.Unmarshal(req.Params.Arguments, &args)
		if args.SQL == failingSQL {
			return &mcp.CallToolResult{
				IsError: true,
				Content: []mcp.Content{&mcp.TextContent{Text: "TABLE_NOT_FOUND: nope"}},
			}, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"rows":[["west",10]]}`}}}, nil
	})
	server.AddTool(&mcp.Tool{
		Name:        "api_invoke_endpoint",
		Description: "Invoke an endpoint",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"connection":{"type":"string"},"method":{"type":"string"},"path":{"type":"string"}}}`),
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"status":200}`}}}, nil
	})

	// Innermost added first: the call reference and audit both read the
	// PlatformContext the tool-call middleware writes.
	server.AddReceivingMiddleware(middleware.MCPCallReferenceMiddleware(provenance.SourceToolkitKinds(), nil))
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(logger))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fixedAuthn{user: &middleware.UserInfo{
			UserID: capUserID, Email: capEmail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
		}},
		allowAuthz{},
		toolkitLookup{},
		middleware.ToolCallConfig{
			Transport:    "http",
			AdminPersona: "admin",
			SessionResolver: middleware.NewSessionResolver(sessions, middleware.SessionResolverConfig{
				Enabled: true, TTL: time.Hour, InitTool: "platform_info",
			}),
			PurposeResolver: middleware.NewPurposeResolver(middleware.PurposeConfig{
				Enabled: true, Lookup: toolkitLookup{},
			}),
		},
	))

	return &replica{server: server, auditStore: auditStore, writer: writer}
}

// connect opens an in-memory MCP client session against a replica.
func connect(ctx context.Context, t *testing.T, r *replica) *mcp.ClientSession {
	t.Helper()
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	go func() { _, _ = r.server.Connect(ctx, serverTransport, nil) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// mintHandle creates the session row an agent would receive from platform_info.
func mintHandle(ctx context.Context, t *testing.T, sessions pkgsession.Store) string {
	t.Helper()
	handle, err := pkgsession.GenerateHandle()
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, sessions.Create(ctx, &pkgsession.Session{
		ID: handle, UserID: capUserID, CreatedAt: now, LastActiveAt: now,
		ExpiresAt: now.Add(time.Hour),
		State:     map[string]any{pkgsession.StateKeyMintedBy: pkgsession.MintedByPlatformInfo},
	}))
	return handle
}

// call invokes a tool and fails the test on a transport error.
func call(ctx context.Context, t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	return res
}

// runQuery runs a query under the session handle, waits for its audit row, and
// returns the call id the result cited.
func runQuery(ctx context.Context, t *testing.T, r *replica, sess *mcp.ClientSession, handle, sqlText string) string {
	t.Helper()
	res := call(ctx, t, sess, "trino_query", map[string]any{
		"sql": sqlText, "connection": "warehouse", "session_id": handle, "purpose": queryReason,
	})
	require.False(t, res.IsError, "query must succeed: %v", res.Content)
	flush(ctx, t, r)
	return callID(t, res)
}

// callID reads the identifier the platform stamped on a data call's result.
func callID(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		tc, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		var block map[string]middleware.CallReference
		if err := json.Unmarshal([]byte(tc.Text), &block); err != nil {
			continue
		}
		if ref, present := block[middleware.CallReferenceKey]; present && ref.CallID != "" {
			return ref.CallID
		}
	}
	t.Fatal("the result carried no call reference")
	return ""
}

// flush waits for the replica's queued audit rows to reach the database.
func flush(ctx context.Context, t *testing.T, r *replica) {
	t.Helper()
	require.NoError(t, r.writer.Flush(ctx))
}

// saveAsset saves an asset and returns its id.
func saveAsset(ctx context.Context, t *testing.T, sess *mcp.ClientSession, handle, name string, sources []string) string {
	t.Helper()
	args := map[string]any{
		"name": name, "content": "# " + name, "content_type": "text/markdown", "session_id": handle,
	}
	if sources != nil {
		args["sources"] = sources
	}
	res := call(ctx, t, sess, portalkit.SaveToolName, args)
	require.False(t, res.IsError, "save_asset must succeed: %v", res.Content)

	var out struct {
		AssetID string `json:"asset_id"`
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	require.NotEmpty(t, out.AssetID)
	return out.AssetID
}

// storedProvenance reads an asset's provenance straight from the row.
func storedProvenance(ctx context.Context, t *testing.T, db *sql.DB, assetID string) portal.Provenance {
	t.Helper()
	var raw []byte
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT provenance FROM portal_assets WHERE id = $1`, assetID).Scan(&raw))
	var prov portal.Provenance
	require.NoError(t, json.Unmarshal(raw, &prov))
	return prov
}

func kinds(capture portal.ProvenanceCapture) []string {
	out := make([]string, 0, len(capture.Calls))
	for _, c := range capture.Calls {
		out = append(out, c.Kind)
	}
	return out
}

func outcomes(capture portal.ProvenanceCapture) []string {
	out := make([]string, 0, len(capture.Calls))
	for _, c := range capture.Calls {
		out = append(out, c.Outcome)
	}
	return out
}

// --- the acceptance criteria ----------------------------------------------

// A session that ran a failed query, a good query, and an API invoke, then
// saved an asset: the asset records all three, by reference and by snapshot.
func TestProvenanceCaptureOnSave_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3())
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions)

	failed := call(ctx, t, sess, "trino_query", map[string]any{
		"sql": failingSQL, "connection": "warehouse", "session_id": handle, "purpose": queryReason,
	})
	require.True(t, failed.IsError, "the first query is meant to fail")
	good := runQuery(ctx, t, r, sess, handle, goodSQL)
	invoke := call(ctx, t, sess, "api_invoke_endpoint", map[string]any{
		"connection": "crm", "method": "GET", "path": "/v1/accounts",
		"session_id": handle, "purpose": invokeGoal,
	})
	require.False(t, invoke.IsError)
	flush(ctx, t, r)

	assetID := saveAsset(ctx, t, sess, handle, "Q3 revenue", nil)
	prov := storedProvenance(ctx, t, db, assetID)

	require.Len(t, prov.Captures, 1)
	capture := prov.Captures[0]
	require.Len(t, capture.Calls, 3, "the failed query is part of how the answer was reached")
	assert.Equal(t, []string{portal.ProvenanceKindSQL, portal.ProvenanceKindSQL, portal.ProvenanceKindAPI}, kinds(capture))
	assert.Equal(t, []string{
		portal.ProvenanceOutcomeError, portal.ProvenanceOutcomeSuccess, portal.ProvenanceOutcomeSuccess,
	}, outcomes(capture))
	assert.Equal(t, queryReason, capture.Calls[0].Purpose)
	assert.Equal(t, invokeGoal, capture.Calls[2].Purpose)
	assert.Equal(t, failingSQL, capture.Calls[0].Statement)
	assert.Equal(t, goodSQL, capture.Calls[1].Statement)
	assert.Equal(t, "warehouse", capture.Calls[1].Connection)
	assert.Equal(t, "GET", capture.Calls[2].Method)
	assert.Equal(t, "/v1/accounts", capture.Calls[2].Path)
	assert.Contains(t, capture.Calls[0].Error, "TABLE_NOT_FOUND")
	assert.Equal(t, handle, capture.SessionID)
	assert.Equal(t, portalkit.SaveToolName, capture.Tool)

	// The id the good query handed the agent is the id the capture recorded.
	assert.Equal(t, good, capture.Calls[1].EventID)

	// Every recorded id resolves to the audit row it names.
	require.Len(t, capture.EventIDs, 3)
	rows, err := r.auditStore.Query(ctx, audit.QueryFilter{IDs: capture.EventIDs, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, rows, 3, "each captured event id must resolve to a stored call")
	for _, row := range rows {
		assert.Equal(t, handle, row.SessionID)
		assert.Equal(t, capUserID, row.UserID)
	}
}

// A second save in the same session records the calls made since the first,
// not the ones the first asset already accounts for.
func TestProvenanceSecondSaveCapturesOnlyNewCalls_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3())
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions)

	first := runQuery(ctx, t, r, sess, handle, goodSQL)
	firstAsset := saveAsset(ctx, t, sess, handle, "First", nil)
	flush(ctx, t, r)

	second := runQuery(ctx, t, r, sess, handle, "SELECT 2")
	secondAsset := saveAsset(ctx, t, sess, handle, "Second", nil)

	firstProv := storedProvenance(ctx, t, db, firstAsset)
	require.Len(t, firstProv.Captures, 1)
	assert.Equal(t, []string{first}, firstProv.Captures[0].EventIDs)

	secondProv := storedProvenance(ctx, t, db, secondAsset)
	require.Len(t, secondProv.Captures, 1)
	assert.Equal(t, []string{second}, secondProv.Captures[0].EventIDs,
		"the second asset must not re-record the first asset's query")
}

// Updating an asset's content appends a capture: the asset ends up saying what
// fed each of its versions.
func TestProvenanceUpdateAppendsACapture_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3())
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions)

	first := runQuery(ctx, t, r, sess, handle, goodSQL)
	assetID := saveAsset(ctx, t, sess, handle, "Revenue", nil)
	flush(ctx, t, r)

	revision := runQuery(ctx, t, r, sess, handle, "SELECT revenue FROM sales WHERE region = 'west'")
	res := call(ctx, t, sess, portalkit.ManageToolName, map[string]any{
		"action": "update", "asset_id": assetID, "content": "# Revenue, revised",
		"session_id": handle,
	})
	require.False(t, res.IsError, "update must succeed: %v", res.Content)

	prov := storedProvenance(ctx, t, db, assetID)
	require.Len(t, prov.Captures, 2, "the update appends rather than replacing")
	assert.Equal(t, []string{first}, prov.Captures[0].EventIDs)
	assert.Equal(t, portalkit.SaveToolName, prov.Captures[0].Tool)
	assert.Equal(t, []string{revision}, prov.Captures[1].EventIDs)
	assert.Equal(t, portalkit.ManageToolName, prov.Captures[1].Tool)
	assert.Equal(t, 2, prov.Captures[1].Version, "the capture names the version it produced")
}

// An agent that knows which call produced the asset says so, and exactly those
// calls are recorded.
func TestProvenanceExplicitSources_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3())
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions)

	wanted := runQuery(ctx, t, r, sess, handle, goodSQL)
	_ = runQuery(ctx, t, r, sess, handle, "SELECT 999 -- an exploratory query the asset is not built on")

	assetID := saveAsset(ctx, t, sess, handle, "Cited", []string{"mcp:call:" + wanted})
	prov := storedProvenance(ctx, t, db, assetID)

	require.Len(t, prov.Captures, 1)
	assert.True(t, prov.Captures[0].Explicit)
	assert.Equal(t, []string{wanted}, prov.Captures[0].EventIDs)
	require.Len(t, prov.Captures[0].Calls, 1)
	assert.Equal(t, goodSQL, prov.Captures[0].Calls[0].Statement)
}

// One replica serves the queries and another serves the save: the asset still
// records what the session did, because the record is the audit log and not a
// buffer in one process.
func TestProvenanceAcrossReplicas_RealDB(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	s3 := newMemS3()

	first := newReplica(t, db, sessions, s3)
	second := newReplica(t, db, sessions, s3)
	handle := mintHandle(ctx, t, sessions)

	querySession := connect(ctx, t, first)
	queryID := runQuery(ctx, t, first, querySession, handle, goodSQL)

	saveSession := connect(ctx, t, second)
	assetID := saveAsset(ctx, t, saveSession, handle, "Cross replica", nil)

	prov := storedProvenance(ctx, t, db, assetID)
	require.Len(t, prov.Captures, 1)
	assert.Equal(t, []string{queryID}, prov.Captures[0].EventIDs,
		fmt.Sprintf("the save on the second replica must record the query the first served (%s)", queryID))
}
