//go:build integration

package callrecord_test

// End-to-end call catalog over a real PostgreSQL database (#1321).
//
// Every claim this feature makes is about a join a unit test can stub apart:
// the record written from an audit event, the asset or capture that later cites
// it, the session that re-ran it, and the outcome derived from all three on
// read. These tests run the real assembled server — tool-call middleware, the
// purpose resolver, the audit writer with the catalog decorator inside it, the
// portal and memory toolkits — against a real database, and read the catalog
// back through the same store the surfaces use.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/auditwiring"
	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	memstore "github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
	memorykit "github.com/txn2/mcp-data-platform/pkg/toolkits/memory"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

const (
	analystID   = "550e8400-e29b-41d4-a716-446655440111"
	analystMail = "analyst@example.com"
	strangerID  = "550e8400-e29b-41d4-a716-446655440222"
	strangerML  = "stranger@example.com"
	bucket      = "portal-assets"

	failingSQL   = "SELECT * FROM iceberg.sales.nope"
	revenueSQL   = "SELECT region, SUM(amount) FROM iceberg.sales.orders GROUP BY region"
	inventorySQL = "SELECT sku, qty FROM iceberg.sales.inventory"
	deleteSQL    = "DELETE FROM iceberg.sales.orders WHERE region = 'west'"
	queryPurpose = "Sizing Q3 revenue by region for the board deck."
	invokePurpos = "Pulling the CRM account list the report is keyed on."

	ordersURN = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.sales.orders,PROD)"
)

// --- fakes the assembled server needs -------------------------------------

type memS3 struct{ objects map[string][]byte }

func newMemS3() *memS3 { return &memS3{objects: map[string][]byte{}} }

func (m *memS3) PutObject(_ context.Context, b, key string, body []byte, _ string) error {
	m.objects[b+"/"+key] = body
	return nil
}

func (m *memS3) PutObjectStream(_ context.Context, b, key string, body io.Reader, _ string) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("reading body: %w", err)
	}
	m.objects[b+"/"+key] = data
	return int64(len(data)), nil
}

func (m *memS3) GetObject(_ context.Context, b, key string) ([]byte, string, error) {
	body, ok := m.objects[b+"/"+key]
	if !ok {
		return nil, "", errors.New("no such object")
	}
	return body, "text/markdown", nil
}

func (m *memS3) DeleteObject(_ context.Context, b, key string) error {
	delete(m.objects, b+"/"+key)
	return nil
}

func (*memS3) Close() error { return nil }

// fixedAuthn authenticates every request as one person, which is how a replica
// serving one caller behaves.
type fixedAuthn struct{ user *middleware.UserInfo }

func (f *fixedAuthn) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return f.user, nil
}

type allowAuthz struct{}

func (allowAuthz) IsAuthorized(context.Context, string, []string, string, string) (bool, string, string) {
	return true, "analyst", ""
}

// toolkitLookup maps the test's tools onto the toolkit kinds the platform
// classifies them by.
type toolkitLookup struct{}

func (toolkitLookup) GetToolkitForTool(tool string) registry.ToolkitMatch {
	switch tool {
	case "trino_query":
		return registry.ToolkitMatch{Kind: "trino", Name: "prod", Connection: "warehouse", Found: true}
	case "api_invoke_endpoint":
		return registry.ToolkitMatch{Kind: "api", Name: "gateway", Connection: "crm", Found: true}
	case portalkit.SaveToolName, portalkit.ManageToolName:
		return registry.ToolkitMatch{Kind: "portal", Name: "portal", Found: true}
	case "memory_capture":
		return registry.ToolkitMatch{Kind: "memory", Name: "memory", Found: true}
	default:
		return registry.ToolkitMatch{}
	}
}

// curatedQueries records what a promotion wrote to the catalog. It models the
// DataHub client's contract: a set of dataset URNs and the statement.
type curatedQueries struct {
	datasetURNs []string
	name        string
	statement   string
}

func (c *curatedQueries) CreateCuratedQuery(_ context.Context, datasetURNs []string, name, sqlText, _ string) (string, error) {
	c.datasetURNs, c.name, c.statement = datasetURNs, name, sqlText
	return "urn:li:query:promoted-1", nil
}

// --- harness ---------------------------------------------------------------

// replica is one server instance over the shared database: its own audit layer
// (with the catalog decorator inside the writer) and its own MCP server.
type replica struct {
	server *mcp.Server
	layer  *auditwiring.Layer
	calls  *callrecord.PostgresStore
}

// newReplica assembles a server for one caller over db.
func newReplica(t *testing.T, db *sql.DB, sessions pkgsession.Store, s3 *memS3, user *middleware.UserInfo) *replica {
	t.Helper()

	layer := auditwiring.Assemble(auditwiring.Config{
		DB:            db,
		RetentionDays: 30,
		// The platform segment is the connection KIND the audit event carried,
		// so a target names the platform the statement ran against rather than
		// one of several a shared connection name could mean (#1384).
		BuildURN: func(kind, _, catalog, schema, table string) string {
			return fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:%s,%s.%s.%s,PROD)", kind, catalog, schema, table)
		},
	})

	toolkit := portalkit.New(portalkit.Config{
		Name:              "portal",
		AssetStore:        portal.NewPostgresAssetStore(db),
		VersionStore:      portal.NewPostgresVersionStore(db),
		S3Client:          s3,
		S3Bucket:          bucket,
		S3Prefix:          "assets/",
		CaptureProvenance: layer.Capturer().Capture,
	})

	memories, err := memorykit.New("memory", memstore.NewPostgresStore(db), nil)
	require.NoError(t, err)

	server := mcp.NewServer(&mcp.Implementation{Name: "callrecord-test", Version: "v0"}, nil)
	toolkit.RegisterTools(server)
	memories.RegisterTools(server)
	registerDataTools(server)

	// Innermost added first: the call reference and audit both read the
	// PlatformContext the tool-call middleware writes.
	server.AddReceivingMiddleware(middleware.MCPCallReferenceMiddleware([]string{"trino", "api"}))
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(layer.Logger()))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fixedAuthn{user: user},
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

	return &replica{server: server, layer: layer, calls: layer.Calls()}
}

// registerDataTools adds the two data tools the catalog records: a query that
// fails on a sentinel statement, and an API invocation that always answers.
func registerDataTools(server *mcp.Server) {
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
		InputSchema: json.RawMessage(`{"type":"object","properties":{"connection":{"type":"string"},"method":{"type":"string"},"path":{"type":"string"},"operation_id":{"type":"string"},"path_params":{"type":"object"}}}`),
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"status":200}`}}}, nil
	})
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
func mintHandle(ctx context.Context, t *testing.T, sessions pkgsession.Store, userID string) string {
	t.Helper()
	handle, err := pkgsession.GenerateHandle()
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, sessions.Create(ctx, &pkgsession.Session{
		ID: handle, UserID: userID, CreatedAt: now, LastActiveAt: now,
		ExpiresAt: now.Add(time.Hour),
		State:     map[string]any{pkgsession.StateKeyMintedBy: pkgsession.MintedByPlatformInfo},
	}))
	return handle
}

// resultText renders a tool result's text content, for a failure message that
// says what went wrong rather than printing a pointer.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

// call invokes a tool and fails the test on a transport error.
func call(ctx context.Context, t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	return res
}

// flush drains the audit writer so the row a call just produced, and the record
// derived from it, are readable.
func flush(ctx context.Context, t *testing.T, r *replica) {
	t.Helper()
	f := auditwiring.AsFlusher(r.layer.Logger())
	require.NotNil(t, f, "the async writer must expose the barrier a read waits on")
	require.NoError(t, f.Flush(ctx))
}

// runQuery runs a query under a session handle and returns the call reference
// its result cited.
func runQuery(ctx context.Context, t *testing.T, r *replica, sess *mcp.ClientSession, handle, sqlText string) string {
	t.Helper()
	res := call(ctx, t, sess, "trino_query", map[string]any{
		"sql": sqlText, "connection": "warehouse", "session_id": handle, "purpose": queryPurpose,
	})
	flush(ctx, t, r)
	return reference(t, res)
}

// invoke calls the API gateway tool under a session handle and returns the call
// reference its result cited. path_params are passed as the real gateway takes
// them: values substituted into the operation's own path template.
func invoke(ctx context.Context, t *testing.T, r *replica, sess *mcp.ClientSession, handle, method, path, operationID string, pathParams map[string]any) string {
	t.Helper()
	args := map[string]any{
		"connection": "crm", "method": method, "operation_id": operationID,
		"session_id": handle, "purpose": invokePurpos,
	}
	if path != "" {
		args["path"] = path
	}
	if pathParams != nil {
		args["path_params"] = pathParams
	}
	res := call(ctx, t, sess, "api_invoke_endpoint", args)
	flush(ctx, t, r)
	return reference(t, res)
}

// reference reads the mcp:call:<id> reference the platform stamped on a data
// call's result, or "" when it stamped none.
func reference(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		text, ok := c.(*mcp.TextContent)
		if !ok {
			continue
		}
		var block map[string]middleware.CallReference
		if err := json.Unmarshal([]byte(text.Text), &block); err != nil {
			continue
		}
		if ref, ok := block[middleware.CallReferenceKey]; ok && ref.Reference != "" {
			return ref.Reference
		}
	}
	return ""
}

// recordFor reads the cataloged record behind a call reference, as the caller.
func recordFor(ctx context.Context, t *testing.T, r *replica, ref, userID string) *callrecord.Record {
	t.Helper()
	eventID, ok := portal.ParseCallReference(ref)
	require.True(t, ok, "a data call must hand back a reference: %q", ref)
	rec, err := r.calls.GetByEventID(ctx, eventID, userID)
	require.NoError(t, err, "record for %s", ref)
	return rec
}

// --- the story the catalog tells ------------------------------------------

// A session that fails, corrects itself, invokes an API, and saves an asset
// citing both good calls: the failure reads failed and both cited calls read
// satisfied. This is the shape #1321 exists to make legible.
func TestCallCatalogRealDBDerivesOutcomesFromWhatCitesACall(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	failed := call(ctx, t, sess, "trino_query", map[string]any{
		"sql": failingSQL, "connection": "warehouse", "session_id": handle, "purpose": queryPurpose,
	})
	require.True(t, failed.IsError, "the sentinel statement must fail")
	flush(ctx, t, r)
	// A failed call is stamped with no reference, so its record is found the
	// way the catalog finds it: by the session that ran it.
	assert.Empty(t, reference(t, failed), "a failed result is not a place to hand back a citation")

	corrected := runQuery(ctx, t, r, sess, handle, revenueSQL)
	invoked := call(ctx, t, sess, "api_invoke_endpoint", map[string]any{
		"connection": "crm", "method": "GET", "path": "/v1/accounts",
		"operation_id": "listAccounts", "session_id": handle, "purpose": invokePurpos,
	})
	flush(ctx, t, r)
	invokeRef := reference(t, invoked)
	require.NotEmpty(t, invokeRef)

	saved := call(ctx, t, sess, portalkit.SaveToolName, map[string]any{
		"name": "Q3 revenue", "content": "region,amount\nwest,10\n", "content_type": "text/csv",
		"sources": []any{corrected, invokeRef}, "session_id": handle,
	})
	require.False(t, saved.IsError, "save must succeed: %s", resultText(saved))
	flush(ctx, t, r)

	// Both cited calls are satisfied, by the asset that names them.
	for _, ref := range []string{corrected, invokeRef} {
		rec := recordFor(ctx, t, r, ref, analystID)
		assert.Equal(t, callrecord.OutcomeSatisfied, rec.Outcome, "record %s", ref)
		assert.Equal(t, callrecord.SatisfiedByAsset, rec.SatisfiedBy)
		require.NotEmpty(t, rec.Artifacts, "a satisfied record names what was built from it")
		assert.Equal(t, "Q3 revenue", rec.Artifacts[0].Name)
	}

	// The failed one is failed, whatever came after it.
	failedRecords, err := r.calls.List(ctx, callrecord.Filter{
		UserID: analystID, SessionID: handle, Outcome: callrecord.OutcomeFailed,
	})
	require.NoError(t, err)
	require.Len(t, failedRecords, 1)
	assert.Equal(t, failingSQL, failedRecords[0].Statement)
	assert.Contains(t, failedRecords[0].ErrorMessage, "TABLE_NOT_FOUND")

	// The corrected query's targets are the datasets it read, which is what
	// promotion carries into the catalog and what the per-table enrichment
	// keys on.
	rec := recordFor(ctx, t, r, corrected, analystID)
	assert.Equal(t, []string{ordersURN}, rec.Targets)
}

// A query nothing was built from, re-run later in the same session over the
// same tables, is a draft the agent replaced.
func TestCallCatalogRealDBSupersedesADraft(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	first := runQuery(ctx, t, r, sess, handle, revenueSQL)
	// The same statement again, later in the same session.
	second := runQuery(ctx, t, r, sess, handle, revenueSQL)

	assert.Equal(t, callrecord.OutcomeSuperseded, recordFor(ctx, t, r, first, analystID).Outcome,
		"the earlier run was replaced and nothing was built from it")
	assert.Equal(t, callrecord.OutcomeRan, recordFor(ctx, t, r, second, analystID).Outcome,
		"the last run has nothing after it")

	// A cited call is never a draft, even when a later run followed it.
	saved := call(ctx, t, sess, portalkit.SaveToolName, map[string]any{
		"name": "Revenue", "content": "x", "content_type": "text/csv",
		"sources": []any{first}, "session_id": handle,
	})
	require.False(t, saved.IsError, "save must succeed: %s", resultText(saved))
	flush(ctx, t, r)
	assert.Equal(t, callrecord.OutcomeSatisfied, recordFor(ctx, t, r, first, analystID).Outcome,
		"a call something was built from outranks supersession")
}

// Supersession is a read-shaped idea. Two runs of the same read over the same
// tables are two answers to one question, and the earlier is a draft; two
// mutations against the same resource are two mutations, and neither replaces
// the other (#1352).
func TestCallCatalogRealDBSupersedesOnlyReads(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	// A read, twice: the earlier one is the draft the agent replaced.
	firstRead := runQuery(ctx, t, r, sess, handle, revenueSQL)
	runQuery(ctx, t, r, sess, handle, revenueSQL)
	assert.Equal(t, callrecord.OutcomeSuperseded, recordFor(ctx, t, r, firstRead, analystID).Outcome,
		"a later read of the same tables is a better answer to the same question")

	// A mutation, twice, over the very same table the extractor resolves: two
	// deletions are two deletions.
	firstWrite := runQuery(ctx, t, r, sess, handle, deleteSQL)
	secondWrite := runQuery(ctx, t, r, sess, handle, deleteSQL)
	for _, ref := range []string{firstWrite, secondWrite} {
		rec := recordFor(ctx, t, r, ref, analystID)
		require.Equal(t, []string{ordersURN}, rec.Targets,
			"the test only means something if both mutations resolved the same target")
		assert.Equal(t, callrecord.OutcomeRan, rec.Outcome,
			"a mutation is not a better version of an earlier mutation")
	}

	// The same rule on the API side, where the method is the evidence.
	firstGet := invoke(ctx, t, r, sess, handle, "GET", "/v1/accounts", "listAccounts", nil)
	invoke(ctx, t, r, sess, handle, "GET", "/v1/accounts", "listAccounts", nil)
	assert.Equal(t, callrecord.OutcomeSuperseded, recordFor(ctx, t, r, firstGet, analystID).Outcome,
		"re-reading one endpoint is the API shape of a corrected draft")

	firstPost := invoke(ctx, t, r, sess, handle, "POST", "/v1/accounts", "createAccount", nil)
	secondPost := invoke(ctx, t, r, sess, handle, "POST", "/v1/accounts", "createAccount", nil)
	for _, ref := range []string{firstPost, secondPost} {
		assert.Equal(t, callrecord.OutcomeRan, recordFor(ctx, t, r, ref, analystID).Outcome,
			"creating two accounts is two accounts")
	}
}

// One endpoint template, two resources: the path parameters are part of the
// target, so approving one script is not reported as having been replaced by
// approving another (#1352).
func TestCallCatalogRealDBDistinguishesResourcesThroughOneEndpoint(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	const readOne = "GET /admin/scripts/{id}"
	scriptA := invoke(ctx, t, r, sess, handle, "GET", "", readOne, map[string]any{"id": "script-a"})
	scriptB := invoke(ctx, t, r, sess, handle, "GET", "", readOne, map[string]any{"id": "script-b"})

	recA := recordFor(ctx, t, r, scriptA, analystID)
	recB := recordFor(ctx, t, r, scriptB, analystID)
	assert.Equal(t, []string{"api:crm:GET /admin/scripts/script-a"}, recA.Targets)
	assert.Equal(t, []string{"api:crm:GET /admin/scripts/script-b"}, recB.Targets)
	assert.Equal(t, callrecord.OutcomeRan, recA.Outcome,
		"reading one script is not replaced by reading a different one")

	// The same resource twice is the case supersession is for, so the rule has
	// not simply been switched off.
	again := invoke(ctx, t, r, sess, handle, "GET", "", readOne, map[string]any{"id": "script-a"})
	require.NotEmpty(t, again)
	assert.Equal(t, callrecord.OutcomeSuperseded, recordFor(ctx, t, r, scriptA, analystID).Outcome,
		"re-reading the same script is the later answer to the same question")

	// A call whose template no argument resolved names a template, not a
	// resource, and a target that cannot distinguish it is no target at all.
	unresolved := invoke(ctx, t, r, sess, handle, "GET", "", readOne, nil)
	assert.Empty(t, recordFor(ctx, t, r, unresolved, analystID).Targets)
}

// A save names its sources or it does not. The default window is the record of
// what the session did, and is not evidence that any given call in it answered
// anything: a session that read a notification history and looked up a user
// before saving an asset had both captured, and both read satisfied (#1353).
func TestCallCatalogRealDBSatisfiesOnlyTheCallsAnAssetNamed(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	unrelated := invoke(ctx, t, r, sess, handle, "GET", "/v1/notifications", "listNotifications", nil)
	answering := runQuery(ctx, t, r, sess, handle, revenueSQL)

	// A save with no sources: the window still records both calls on the asset.
	saved := call(ctx, t, sess, portalkit.SaveToolName, map[string]any{
		"name": "Windowed", "content": "x", "content_type": "text/csv", "session_id": handle,
	})
	require.False(t, saved.IsError, "save must succeed: %s", resultText(saved))
	flush(ctx, t, r)

	for _, ref := range []string{unrelated, answering} {
		rec := recordFor(ctx, t, r, ref, analystID)
		assert.Equal(t, callrecord.OutcomeRan, rec.Outcome,
			"a call the window swept up has not been shown to answer anything: %s", ref)
		assert.Empty(t, rec.SatisfiedBy)
		assert.Empty(t, rec.Artifacts,
			"an asset that did not name the call is not an artifact of it")
	}
	queue, err := r.calls.List(ctx, callrecord.Filter{UserID: analystID, PromotableOnly: true})
	require.NoError(t, err)
	assert.Empty(t, queue, "a windowed save offers nothing for review")

	// The provenance panel is unaffected: the asset still records the session's
	// work, which is what makes the write checkable.
	assets, _, err := portal.NewPostgresAssetStore(db).List(ctx, portal.AssetFilter{OwnerID: analystID})
	require.NoError(t, err)
	require.Len(t, assets, 1)
	require.Len(t, assets[0].Provenance.Captures, 1)
	assert.Len(t, assets[0].Provenance.Captures[0].EventIDs, 2,
		"narrowing what counts as evidence must not narrow what is recorded")

	// Naming a source is what makes the record read satisfied.
	named := call(ctx, t, sess, portalkit.SaveToolName, map[string]any{
		"name": "Named", "content": "x", "content_type": "text/csv",
		"sources": []any{answering}, "session_id": handle,
	})
	require.False(t, named.IsError, "save must succeed: %s", resultText(named))
	flush(ctx, t, r)

	rec := recordFor(ctx, t, r, answering, analystID)
	assert.Equal(t, callrecord.OutcomeSatisfied, rec.Outcome)
	assert.Equal(t, callrecord.SatisfiedByAsset, rec.SatisfiedBy)
	require.Len(t, rec.Artifacts, 1, "only the asset that named it")
	assert.Equal(t, "Named", rec.Artifacts[0].Name)
	assert.Equal(t, callrecord.OutcomeRan, recordFor(ctx, t, r, unrelated, analystID).Outcome,
		"the unrelated read is still unrelated")
}

// An export names one call without being asked to: the statement it streamed
// into the asset. That call is the content, so it reads satisfied while the
// window captured around it does not (#1353).
func TestCallCatalogRealDBExportCitesTheStatementItStreamed(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	r := newReplica(t, db, pkgsession.NewMemoryStore(time.Hour), newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})

	const (
		streamed  = "evt-export-own"
		inScope   = "evt-export-window"
		sessionID = "dps_export"
	)
	for _, id := range []string{streamed, inScope} {
		require.NoError(t, r.calls.Insert(ctx, callrecord.Record{
			EventID: id, Kind: callrecord.KindSQL, ToolName: "trino_export",
			Connection: "warehouse", Statement: revenueSQL, UserID: analystID,
			UserEmail: analystMail, SessionID: sessionID, Success: true,
			CreatedAt: time.Now().UTC(),
		}))
	}

	// The capture an export takes: the window it swept up, then its own call,
	// which appendOwn marks as cited (see the provenance package's tests).
	require.NoError(t, portal.NewPostgresAssetStore(db).Insert(ctx, portal.Asset{
		ID: "ast-export-1", OwnerID: analystID, OwnerEmail: analystMail,
		Name: "Exported revenue", ContentType: "text/csv",
		S3Bucket: bucket, S3Key: "assets/ast-export-1.csv", SessionID: sessionID,
		Provenance: portal.Provenance{
			UserID: analystID, SessionID: sessionID,
			Captures: []portal.ProvenanceCapture{{
				Tool: "trino_export", CapturedAt: time.Now().UTC(), Version: 1,
				SessionID: sessionID, EventIDs: []string{inScope, streamed},
				Calls: []portal.ProvenanceCall{
					{EventID: inScope, Kind: portal.ProvenanceKindSQL, Tool: "trino_query", Outcome: portal.ProvenanceOutcomeSuccess},
					{EventID: streamed, Kind: portal.ProvenanceKindSQL, Tool: "trino_export", Outcome: portal.ProvenanceOutcomeSuccess, Cited: true},
				},
			}},
		},
	}))

	own, err := r.calls.GetByEventID(ctx, streamed, analystID)
	require.NoError(t, err)
	assert.Equal(t, callrecord.OutcomeSatisfied, own.Outcome)
	assert.Equal(t, callrecord.SatisfiedByExport, own.SatisfiedBy,
		"the export route is the export naming what it streamed")
	require.Len(t, own.Artifacts, 1)
	assert.Equal(t, "export", own.Artifacts[0].Kind)

	windowed, err := r.calls.GetByEventID(ctx, inScope, analystID)
	require.NoError(t, err)
	assert.Equal(t, callrecord.OutcomeRan, windowed.Outcome,
		"a call the export's window swept up did not produce the file")
	assert.Empty(t, windowed.Artifacts)
}

// The agent's own verdict: a query answered in conversation only, confirmed by
// a capture that names it. That capture is what puts it in the review queue.
func TestCallCatalogRealDBCaptureSatisfiesAQuery(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	answered := runQuery(ctx, t, r, sess, handle, revenueSQL)
	uncaptured := runQuery(ctx, t, r, sess, handle, inventorySQL)

	captured := call(ctx, t, sess, "memory_capture", map[string]any{
		"type":    "business_knowledge",
		"content": "Revenue by region excludes canceled orders; filter on status <> 'canceled'.",
		"sources": []any{answered},
	})
	require.False(t, captured.IsError, "capture must succeed: %v", captured.Content)
	flush(ctx, t, r)

	rec := recordFor(ctx, t, r, answered, analystID)
	assert.Equal(t, callrecord.OutcomeSatisfied, rec.Outcome)
	assert.Equal(t, callrecord.SatisfiedByCapture, rec.SatisfiedBy,
		"a capture that names the call is the agent's own verdict on it")
	require.NotEmpty(t, rec.Artifacts)
	assert.Contains(t, rec.Artifacts[0].Name, "canceled orders")

	// The same query with no capture stays a run that came to nothing.
	assert.Equal(t, callrecord.OutcomeRan, recordFor(ctx, t, r, uncaptured, analystID).Outcome)

	// And the confirmed one is what the review queue offers, ordered by reuse.
	queue, err := r.calls.List(ctx, callrecord.Filter{UserID: analystID, PromotableOnly: true})
	require.NoError(t, err)
	require.Len(t, queue, 1)
	assert.Equal(t, rec.ID, queue[0].ID)
}

// Reuse is a stranger's confirmation: a later session that found the record and
// then ran what it holds. Running the same statement without having read the
// record is not reuse, and neither is the author re-running their own.
func TestCallCatalogRealDBCreditsOnlyAFetchedRerun(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	s3 := newMemS3()

	author := newReplica(t, db, sessions, s3, &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	authorSess := connect(ctx, t, author)
	authorHandle := mintHandle(ctx, t, sessions, analystID)
	ref := runQuery(ctx, t, author, authorSess, authorHandle, revenueSQL)
	rec := recordFor(ctx, t, author, ref, analystID)

	// The author re-running their own query credits nothing.
	_ = runQuery(ctx, t, author, authorSess, authorHandle, revenueSQL)
	assert.Zero(t, recordFor(ctx, t, author, ref, analystID).ReuseCount)

	// A second person, in a second session, who runs the same statement
	// without having read the record: still not reuse, because nothing says
	// the record led to it.
	stranger := newReplica(t, db, sessions, s3, &middleware.UserInfo{
		UserID: strangerID, Email: strangerML, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	strangerSess := connect(ctx, t, stranger)
	blindHandle := mintHandle(ctx, t, sessions, strangerID)
	_ = runQuery(ctx, t, stranger, strangerSess, blindHandle, revenueSQL)
	assert.Zero(t, recordFor(ctx, t, author, ref, analystID).ReuseCount,
		"an identical query written independently is not reuse")

	// Now the same person reads the record first, then runs it. That is reuse.
	readingHandle := mintHandle(ctx, t, sessions, strangerID)
	require.NoError(t, stranger.calls.RecordFetch(ctx, rec.ID, callrecord.Fetcher{
		SessionID: readingHandle, UserID: strangerID,
	}))
	_ = runQuery(ctx, t, stranger, strangerSess, readingHandle, revenueSQL)

	assert.Equal(t, 1, recordFor(ctx, t, author, ref, analystID).ReuseCount)

	// Crediting is per session, so the same session running it again does not
	// count twice.
	_ = runQuery(ctx, t, stranger, strangerSess, readingHandle, revenueSQL)
	assert.Equal(t, 1, recordFor(ctx, t, author, ref, analystID).ReuseCount)
}

// A record belongs to the caller who made it. Another caller's id is answered
// exactly as an id that was never used, and the operator surface reads both.
func TestCallCatalogRealDBScopesReadsToTheCaller(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	s3 := newMemS3()

	author := newReplica(t, db, sessions, s3, &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	authorSess := connect(ctx, t, author)
	ref := runQuery(ctx, t, author, authorSess, mintHandle(ctx, t, sessions, analystID), revenueSQL)
	rec := recordFor(ctx, t, author, ref, analystID)

	stranger := newReplica(t, db, sessions, s3, &middleware.UserInfo{
		UserID: strangerID, Email: strangerML, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	strangerSess := connect(ctx, t, stranger)
	_ = runQuery(ctx, t, stranger, strangerSess, mintHandle(ctx, t, sessions, strangerID), inventorySQL)

	// Each list holds only its own caller's records.
	mine, err := author.calls.List(ctx, callrecord.Filter{UserID: analystID})
	require.NoError(t, err)
	for _, r := range mine {
		assert.Equal(t, analystID, r.UserID)
	}
	theirs, err := author.calls.List(ctx, callrecord.Filter{UserID: strangerID})
	require.NoError(t, err)
	require.NotEmpty(t, theirs)
	for _, r := range theirs {
		assert.Equal(t, strangerID, r.UserID)
	}

	// Another caller's record id and an id that was never used are the same
	// answer, so nothing about someone else's work leaks through the read.
	_, otherErr := author.calls.Get(ctx, callrecord.Scope{ID: rec.ID, UserID: strangerID})
	_, unknownErr := author.calls.Get(ctx, callrecord.Scope{
		ID: "00000000-0000-0000-0000-000000000000", UserID: strangerID,
	})
	assert.ErrorIs(t, otherErr, callrecord.ErrNotFound)
	assert.ErrorIs(t, unknownErr, callrecord.ErrNotFound)

	// The operator surface is unrestricted by design.
	adminRead, err := author.calls.Get(ctx, callrecord.Scope{ID: rec.ID})
	require.NoError(t, err)
	assert.Equal(t, rec.ID, adminRead.ID)
}

// Promotion writes the record to the catalog with every dataset it read, and
// the record then carries what it became.
func TestCallCatalogRealDBPromotesWithEveryTargetItRead(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	joined := "SELECT o.region FROM iceberg.sales.orders o JOIN iceberg.sales.regions g ON o.region_id = g.id"
	ref := runQuery(ctx, t, r, sess, handle, joined)
	saved := call(ctx, t, sess, portalkit.SaveToolName, map[string]any{
		"name": "Regions", "content": "x", "content_type": "text/csv",
		"sources": []any{ref}, "session_id": handle,
	})
	require.False(t, saved.IsError, "save must succeed: %s", resultText(saved))
	flush(ctx, t, r)

	rec := recordFor(ctx, t, r, ref, analystID)
	require.Equal(t, callrecord.OutcomeSatisfied, rec.Outcome)

	writer := &curatedQueries{}
	promoter := callrecord.NewPromoter(r.calls, writer, nil)
	promoted, err := promoter.Promote(ctx, callrecord.Scope{ID: rec.ID, UserID: analystID}, "reviewer@example.com")
	require.NoError(t, err)

	// A query that joins two tables belongs to both.
	assert.ElementsMatch(t, rec.Targets, writer.datasetURNs)
	assert.Len(t, writer.datasetURNs, 2)
	assert.Equal(t, queryPurpose, writer.name, "the stated purpose names the catalog entry")
	assert.Equal(t, joined, writer.statement)

	assert.Equal(t, "urn:li:query:promoted-1", promoted.PromotedURN)
	assert.Equal(t, "reviewer@example.com", promoted.PromotedBy)
	assert.False(t, promoted.Promotable(), "a promoted record is no longer offered")

	// And it is gone from the queue it was offered in.
	queue, err := r.calls.List(ctx, callrecord.Filter{UserID: analystID, PromotableOnly: true})
	require.NoError(t, err)
	for _, q := range queue {
		assert.NotEqual(t, rec.ID, q.ID)
	}
}

// A satisfied query is findable by what its caller said it was for, which is
// the only sentence about it a person wrote.
func TestCallCatalogRealDBSearchFindsAQueryByItsPurpose(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	ref := runQuery(ctx, t, r, sess, handle, revenueSQL)
	saved := call(ctx, t, sess, portalkit.SaveToolName, map[string]any{
		"name": "Revenue", "content": "x", "content_type": "text/csv",
		"sources": []any{ref}, "session_id": handle,
	})
	require.False(t, saved.IsError, "save must succeed: %s", resultText(saved))
	flush(ctx, t, r)

	hits, err := r.calls.Search(ctx, callrecord.SearchQuery{Text: "revenue by region", UserID: analystID})
	require.NoError(t, err)
	require.NotEmpty(t, hits, "the query must be findable by the words its caller wrote")
	assert.Equal(t, revenueSQL, hits[0].Record.Statement)
	assert.Equal(t, callrecord.OutcomeSatisfied, hits[0].Record.Outcome,
		"a hit carries its standing so an agent can prefer a query that answered something")

	// A search with no caller returns nothing rather than everyone's calls.
	anonymous, err := r.calls.Search(ctx, callrecord.SearchQuery{Text: "revenue"})
	require.NoError(t, err)
	assert.Empty(t, anonymous)

	// The record is also what a table's describe would show beside it.
	proven, err := r.calls.ForTargets(ctx, []string{ordersURN}, analystID, 3)
	require.NoError(t, err)
	require.NotEmpty(t, proven)
	assert.Equal(t, revenueSQL, proven[0].Statement)
}

// The catalog is swept by what a record came to, not by its age alone. This is
// the claim that keeps it from growing without bound AND the claim that keeps
// the sweep from deleting the evidence the feature exists to keep, so it is
// asserted against a real database rather than against the statement text.
func TestCallCatalogRealDBSweepsOnlyWhatCameToNothing(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)
	r := newReplica(t, db, sessions, newMemS3(), &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	sess := connect(ctx, t, r)
	handle := mintHandle(ctx, t, sessions, analystID)

	// Four records, one of each fate: one nothing came of, one an asset cites,
	// one a reviewer declined, and one another session re-ran.
	unused := runQuery(ctx, t, r, sess, handle, inventorySQL)
	cited := runQuery(ctx, t, r, sess, handle, revenueSQL)
	declined := runQuery(ctx, t, r, sess, handle, "SELECT sku FROM iceberg.sales.skus")
	reused := runQuery(ctx, t, r, sess, handle, "SELECT id FROM iceberg.sales.customers")

	saved := call(ctx, t, sess, portalkit.SaveToolName, map[string]any{
		"name": "Revenue", "content": "x", "content_type": "text/csv",
		"sources": []any{cited}, "session_id": handle,
	})
	require.False(t, saved.IsError, "save must succeed: %s", resultText(saved))
	flush(ctx, t, r)

	declinedRec := recordFor(ctx, t, r, declined, analystID)
	require.NoError(t, r.calls.Reject(ctx, declinedRec.ID, callrecord.Rejection{
		Actor: "reviewer@example.com", Note: "Superseded by the sku view.",
	}))

	reusedRec := recordFor(ctx, t, r, reused, analystID)
	readingHandle := mintHandle(ctx, t, sessions, strangerID)
	require.NoError(t, r.calls.RecordFetch(ctx, reusedRec.ID, callrecord.Fetcher{
		SessionID: readingHandle, UserID: strangerID,
	}))
	credited, err := r.calls.CreditReuse(ctx, callrecord.Record{
		Success: true, SessionID: readingHandle, UserID: strangerID,
		Kind: callrecord.KindSQL, Connection: "warehouse",
		Statement: "SELECT id FROM iceberg.sales.customers", CreatedAt: time.Now().UTC(),
	})
	require.NoError(t, err)
	require.Equal(t, 1, credited)

	// Age every record past the window, so what survives is decided by what
	// came of it rather than by when it ran.
	_, err = db.ExecContext(ctx, `UPDATE call_records SET created_at = NOW() - INTERVAL '400 days'`)
	require.NoError(t, err)

	removed, err := r.calls.Cleanup(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), removed, "only the record nothing came of is swept")

	_, err = r.calls.GetByEventID(ctx, eventIDOf(t, unused), analystID)
	assert.ErrorIs(t, err, callrecord.ErrNotFound, "the unused draft is gone")

	for name, ref := range map[string]string{
		"cited by an asset": cited,
		"declined":          declined,
		"re-run by another": reused,
	} {
		if _, err := r.calls.GetByEventID(ctx, eventIDOf(t, ref), analystID); err != nil {
			t.Errorf("%s: record must survive the sweep, got %v", name, err)
		}
	}

	// And a promoted record survives too, whatever its age.
	promoted, err := callrecord.NewPromoter(r.calls, &curatedQueries{}, nil).
		Promote(ctx, callrecord.Scope{ID: recordFor(ctx, t, r, cited, analystID).ID}, "reviewer@example.com")
	require.NoError(t, err)
	require.NotEmpty(t, promoted.PromotedURN)
	removed, err = r.calls.Cleanup(ctx)
	require.NoError(t, err)
	assert.Zero(t, removed, "nothing is left that came to nothing")
}

// eventIDOf reads the event id out of a call reference.
func eventIDOf(t *testing.T, ref string) string {
	t.Helper()
	id, ok := portal.ParseCallReference(ref)
	require.True(t, ok, "reference %q", ref)
	return id
}
