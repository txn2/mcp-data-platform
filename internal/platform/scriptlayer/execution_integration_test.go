package scriptlayer

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptexec"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
)

// This file is the assembled-system proof for #1284. Stage one proved a draft
// crosses the middleware chain as its author; what has to be proved here is the
// half that runs with nobody present:
//
//   - a run executes the script's latest saved version, as the script
//     principal, presenting the roles its author held at the save;
//   - its output lands as a new VERSION of one stable portal asset;
//   - a delivery resolves against the deployment's configured destination set,
//     and the authorization middleware is the authority over every connection
//     the run touches;
//   - the run and every capability call it made are audited under one run id.
//
// The stack is real: the platform's own middleware in the platform's own order,
// the real run worker claiming off a queue, the real engine, the real tool. The
// substitutions are the warehouse (a trino_query handler returning the shape
// the toolkit returns) and the object store.

// memRuns is an in-memory run queue for the end-to-end test. It models the
// parts of the contract this test depends on: one claim at a time, a lease on
// every write, and outputs recorded as they land.
type memRuns struct {
	mu   sync.Mutex
	byID map[string]*script.Run
	// notify stands in for the store's pg_notify: production wakes the worker
	// the moment a run is enqueued, and a fake that left the worker to its poll
	// tick would make every test in this file wait seconds for nothing.
	notify func()
	order  []string
	// claims counts claim attempts, which is how a replica that must not claim
	// is held to it.
	claims int
}

func newMemRuns() *memRuns { return &memRuns{byID: map[string]*script.Run{}} }

func (m *memRuns) Enqueue(_ context.Context, r *script.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	r.Status, r.ScheduledFor = script.RunStatusPending, now
	if r.FireTime.IsZero() {
		r.FireTime = now
	}
	stored := *r
	m.byID[r.ID] = &stored
	m.order = append(m.order, r.ID)
	if m.notify != nil {
		m.notify()
	}
	return nil
}

// all returns every run this queue has seen, which is how a test proves that a
// refused enqueue produced no row.
func (m *memRuns) all() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.order...)
}

func (m *memRuns) claimCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claims
}

func (m *memRuns) GetRun(_ context.Context, id string) (*script.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.byID[id]
	if !ok {
		return nil, script.ErrRunNotFound
	}
	out := *r
	return &out, nil
}

func (m *memRuns) ListRuns(_ context.Context, filter script.RunFilter) ([]script.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []script.Run{}
	for i := len(m.order) - 1; i >= 0; i-- {
		r := m.byID[m.order[i]]
		if filter.ScriptID != "" && r.ScriptID != filter.ScriptID {
			continue
		}
		if filter.Status != "" && r.Status != filter.Status {
			continue
		}
		out = append(out, *r)
	}
	return out, nil
}

func (m *memRuns) Claim(_ context.Context, worker string, lease time.Duration) (*script.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.claims++
	for _, id := range m.order {
		r := m.byID[id]
		if r.Status != script.RunStatusPending {
			continue
		}
		until := time.Now().Add(lease)
		r.Status, r.LockedBy, r.LockedUntil = script.RunStatusRunning, worker, &until
		r.Attempt++
		out := *r
		return &out, nil
	}
	return nil, script.ErrNoWork
}

// held enforces the fencing token, as the real store's WHERE clause does.
func (m *memRuns) held(lease script.RunLease) (*script.Run, error) {
	r, ok := m.byID[lease.RunID]
	if !ok || r.LockedBy != lease.Worker || r.Attempt != lease.Attempt {
		return nil, script.ErrLeaseLost
	}
	return r, nil
}

func (m *memRuns) RecordOutput(_ context.Context, lease script.RunLease, out script.RunOutput) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.held(lease)
	if err != nil {
		return err
	}
	r.Outputs = append(r.Outputs, out)
	return nil
}

func (m *memRuns) Finish(_ context.Context, lease script.RunLease, res script.RunResult) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.held(lease)
	if err != nil {
		return err
	}
	finished := time.Now().UTC()
	r.Status, r.Error, r.Log, r.LogTruncated = res.Status, res.Error, res.Log, res.LogTruncated
	r.Metrics, r.FinishedAt = res.Metrics, &finished
	return nil
}

func (m *memRuns) Retry(_ context.Context, lease script.RunLease, cause string, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.held(lease)
	if err != nil {
		return err
	}
	r.Status, r.Error = script.RunStatusPending, cause
	return nil
}

func (*memRuns) PurgeRuns(context.Context, time.Duration) (int64, error) { return 0, nil }

// memAssets, memVersions and memS3 stand in for the portal persistence the
// output writer targets.
type memAssets struct {
	mu       sync.Mutex
	byKey    map[string]*portal.Asset
	inserted []portal.Asset
}

func newMemAssets() *memAssets { return &memAssets{byKey: map[string]*portal.Asset{}} }

func (m *memAssets) Insert(_ context.Context, asset portal.Asset) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.inserted = append(m.inserted, asset)
	m.byKey[asset.OwnerID+"|"+asset.IdempotencyKey] = &asset
	return nil
}

func (m *memAssets) GetByIdempotencyKey(_ context.Context, ownerID, key string) (*portal.Asset, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if asset, ok := m.byKey[ownerID+"|"+key]; ok {
		return asset, nil
	}
	return nil, errors.New("not found")
}

func (*memAssets) Get(context.Context, string) (*portal.Asset, error) {
	return nil, errors.New("not found")
}

func (*memAssets) GetByIDs(context.Context, []string) (assets map[string]*portal.Asset, err error) {
	return map[string]*portal.Asset{}, nil
}

func (*memAssets) List(context.Context, portal.AssetFilter) (assets []portal.Asset, total int, err error) {
	return nil, 0, nil
}
func (*memAssets) Update(context.Context, string, portal.AssetUpdate) error { return nil }
func (*memAssets) AppendProvenanceCapture(context.Context, string, portal.ProvenanceCapture) error {
	return nil
}

func (*memAssets) SoftDelete(context.Context, string) error { return nil }

type memVersions struct {
	mu      sync.Mutex
	created []portal.AssetVersion
	counts  map[string]int
}

func newMemVersions() *memVersions { return &memVersions{counts: map[string]int{}} }

func (m *memVersions) CreateVersion(_ context.Context, v portal.AssetVersion) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[v.AssetID]++
	m.created = append(m.created, v)
	return m.counts[v.AssetID], nil
}

func (*memVersions) ListByAsset(context.Context, string, int, int) (versions []portal.AssetVersion, total int, err error) {
	return nil, 0, nil
}

func (*memVersions) GetByVersion(context.Context, string, int) (*portal.AssetVersion, error) {
	return nil, errors.New("not found")
}

func (*memVersions) GetLatest(context.Context, string) (*portal.AssetVersion, error) {
	return nil, errors.New("not found")
}

type memS3 struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func newMemS3() *memS3 { return &memS3{objects: map[string][]byte{}} }

func (m *memS3) PutObject(_ context.Context, _, key string, data []byte, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

func (*memS3) PutObjectStream(context.Context, string, string, io.Reader, string) (size int64, err error) {
	return 0, nil
}

func (*memS3) GetObject(context.Context, string, string) (data []byte, contentType string, err error) {
	return nil, "", nil
}
func (*memS3) DeleteObject(context.Context, string, string) error { return nil }
func (*memS3) Close() error                                       { return nil }

// connectionAuthz is the authorization middleware standing in for the persona
// filter: it authorizes tools for the analyst persona but only on the
// connections that persona holds, which is the entire authorization boundary a
// run answers to.
type connectionAuthz struct {
	allowed map[string]bool
	// deniedTools are the tools this persona may not call at all, which is what
	// the persona filter answers for a tool outside a persona's allow list. It
	// is the only thing that refuses a platform.call (#1419).
	mu          sync.Mutex
	deniedTools map[string]bool
}

// deny puts one tool outside the persona, the way a persona's deny list does.
func (a *connectionAuthz) deny(tool string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.deniedTools == nil {
		a.deniedTools = map[string]bool{}
	}
	a.deniedTools[tool] = true
}

func (a *connectionAuthz) IsAuthorized(_ context.Context, _ string, _ []string, tool, connection string) (authorized bool, persona, reason string) {
	a.mu.Lock()
	denied := a.deniedTools[tool]
	a.mu.Unlock()
	if denied {
		return false, "analyst", "tool not allowed for persona: analyst"
	}
	if connection != "" && !a.allowed[connection] {
		return false, "analyst", "connection not allowed for persona: analyst"
	}
	return true, "analyst", ""
}

// executeInput mirrors the trino toolkit's trino_execute argument shape. The
// write tool is served here by a tool of the same name and arguments, because a
// script reaches it the way an agent does: one ordinary tool call.
type executeInput struct {
	SQL        string `json:"sql"`
	Connection string `json:"connection,omitempty"`
}

// recordingExecutes collects what the write tool was asked to run.
type recordingExecutes struct {
	mu   sync.Mutex
	seen []executeInput
}

func (e *recordingExecutes) record(in executeInput) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, in)
}

func (e *recordingExecutes) calls() []executeInput {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]executeInput(nil), e.seen...)
}

// invokeInput mirrors the apigateway toolkit's api_invoke_endpoint arguments:
// the automation whose input is not SQL, which is the case the closed surface
// blocked.
type invokeInput struct {
	Connection  string         `json:"connection,omitempty"`
	OperationID string         `json:"operation_id"`
	Body        map[string]any `json:"body,omitempty"`
}

// putObjectInput mirrors the s3 toolkit's s3_put_object argument shape, which
// is the tool a delivery is issued as. Delivery is not a private route to a
// bucket: it is one ordinary tool call over the run's session, so it is served
// here by a tool of the same name and the same arguments.
type putObjectInput struct {
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
	IsBase64    bool   `json:"is_base64,omitempty"`
	Connection  string `json:"connection,omitempty"`
}

// recordingPuts collects what the delivery tool was asked to write.
type recordingPuts struct {
	mu   sync.Mutex
	seen []putObjectInput
}

func (p *recordingPuts) record(in putObjectInput) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, in)
}

func (p *recordingPuts) calls() []putObjectInput {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]putObjectInput(nil), p.seen...)
}

// execHarness is the assembled system: the tool layer, the run worker, and the
// stores each of them writes to.
type execHarness struct {
	server   *mcp.Server
	handle   *Handle
	store    *memStore
	runs     *memRuns
	assets   *memAssets
	versions *memVersions
	s3       *memS3
	audit    *recordingAudit
	queries  *recordingQueries
	puts     *recordingPuts
	worker   *scriptexec.Handle
	authz    *connectionAuthz
	executes *recordingExecutes
	identity *recordingIdentity
}

// recordingIdentity captures the PlatformContext a tool handler was given, which
// is the only way to prove an identity actually crossed the middleware chain
// rather than merely being set on the way in.
type recordingIdentity struct {
	mu   sync.Mutex
	seen []middleware.PlatformContext
}

func (r *recordingIdentity) record(pc middleware.PlatformContext) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, pc)
}

func (r *recordingIdentity) calls() []middleware.PlatformContext {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]middleware.PlatformContext(nil), r.seen...)
}

// executionServer wires the whole feature: manage_script and run_script over
// the real middleware chain, a Trino-shaped query tool, and the real run worker
// executing whatever run_script enqueues. The deployment declares one bucket
// destination, acme-drop, which is the configured set delivery tests resolve
// against.
func executionServer(t *testing.T, allowedConnections ...string) execHarness {
	t.Helper()
	return execServerWithWorker(t, true, allowedConnections...)
}

// execServerWithWorker assembles the same system with the run worker on or off,
// which is the single difference between the two deployment shapes.
func execServerWithWorker(t *testing.T, workerOn bool, allowedConnections ...string) execHarness {
	t.Helper()
	store := newMemStore()
	runs := newMemRuns()
	assets, versions, s3 := newMemAssets(), newMemVersions(), newMemS3()
	audit := &recordingAudit{}
	queries := &recordingQueries{rows: []map[string]any{
		{"region": "west", "total": float64(120)},
		{"region": "east", "total": float64(80)},
	}}

	h := New(Config{Store: store, Runs: runs, AdminPersona: "admin"})
	server := mcp.NewServer(&mcp.Implementation{Name: "script-exec-integration", Version: "v0"}, nil)
	h.RegisterTool(server)

	mcp.AddTool(server, &mcp.Tool{Name: "trino_query", Description: "query"},
		func(_ context.Context, _ *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, any, error) {
			queries.record(in)
			if queries.fail != "" {
				return &mcp.CallToolResult{
					IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: queries.fail}},
				}, nil, nil
			}
			out := queryOutput{
				Columns: []queryColumn{{Name: "region", Type: "varchar"}, {Name: "total", Type: "bigint"}},
				Rows:    queries.rows, RowCount: len(queries.rows),
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, out, nil
		})

	puts := &recordingPuts{}
	mcp.AddTool(server, &mcp.Tool{Name: "s3_put_object", Description: "put"},
		func(_ context.Context, _ *mcp.CallToolRequest, in putObjectInput) (*mcp.CallToolResult, any, error) {
			puts.record(in)
			decoded, err := base64.StdEncoding.DecodeString(in.Content)
			if err != nil {
				// A tool reports its own failure as an error RESULT, not as a Go
				// error: a Go error is a transport failure, and this is the
				// caller sending something the tool cannot use.
				return &mcp.CallToolResult{ //nolint:nilerr // an MCP tool failure is an error result, never a returned error
					IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "content is not base64"}},
				}, nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
				map[string]any{"bucket": in.Bucket, "key": in.Key, "size": len(decoded)}, nil
		})

	seen := &recordingIdentity{}
	mcp.AddTool(server, &mcp.Tool{Name: "whoami", Description: "identity"},
		func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			if pc := middleware.GetPlatformContext(ctx); pc != nil {
				seen.record(*pc)
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
				map[string]any{"ok": true}, nil
		})

	executes := &recordingExecutes{}
	mcp.AddTool(server, &mcp.Tool{Name: "trino_execute", Description: "execute"},
		func(_ context.Context, _ *mcp.CallToolRequest, in executeInput) (*mcp.CallToolResult, any, error) {
			executes.record(in)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
				map[string]any{"statement": in.SQL, "rows_affected": 1}, nil
		})

	mcp.AddTool(server, &mcp.Tool{Name: "api_invoke_endpoint", Description: "invoke"},
		func(_ context.Context, _ *mcp.CallToolRequest, in invokeInput) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}},
				map[string]any{
					"status": 200,
					"body": map[string]any{
						"operation": in.OperationID,
						"periods": []any{
							map[string]any{"name": "Tonight", "temperature": 78},
							map[string]any{"name": "Thursday", "temperature": 101},
						},
					},
				}, nil
		})

	allowed := map[string]bool{}
	for _, c := range allowedConnections {
		allowed[c] = true
	}
	authz := &connectionAuthz{allowed: allowed}
	tracker := middleware.NewSessionWorkflowTracker(
		[]string{"search"}, []string{"trino_query"}, searchgate.NewMemoryStore(time.Hour), time.Hour)
	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(audit))
	server.AddReceivingMiddleware(middleware.MCPWorkflowGateMiddleware(tracker))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{
			UserID: "user-jane@example.com", Email: "jane@example.com",
			Roles: []string{"analyst"}, AuthType: middleware.AuthTypeOIDC,
		}},
		authz,
		&fakeLookup{},
		middleware.ToolCallConfig{Transport: "http", AdminPersona: "admin", WorkflowTracker: tracker},
	))

	worker := scriptexec.New(scriptexec.Config{
		Runs: runs, Scripts: store, Versions: store,
		Server: server, Audit: audit,
		Export:         scriptexec.ExportDeps{Assets: assets, Versions: versions, S3: s3, Bucket: "assets", Prefix: "portal"},
		Destinations:   []script.Destination{acmeDrop()},
		WorkerDisabled: !workerOn,
	})
	require.NotNil(t, worker)
	runs.notify = worker.Notify
	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })

	return execHarness{
		server: server, handle: h, store: store, runs: runs,
		assets: assets, versions: versions, s3: s3, audit: audit,
		queries: queries, puts: puts, worker: worker,
		authz: authz, executes: executes, identity: seen,
	}
}

// reportSource is a script that queries and writes one output.
const reportSource = `res = platform.query(
    connection = "warehouse",
    sql = "SELECT region, total FROM sales WHERE d = :day",
    params = {"day": run.params["day"]},
)
print("rows: %d" % res["row_count"])
platform.export(name="daily-sales", rows=res["rows"], format="csv")
`

// authorScript creates a script through the real tool. Saving is all it takes
// for the script to run: the version create wrote is the version a run
// executes.
func authorScript(t *testing.T, h execHarness, source string) {
	t.Helper()
	res := call(t, h.handle, authorCtx(), manageScriptInput{
		Command: cmdCreate, Name: "daily", DisplayName: "Daily", Source: source,
		Params: []script.Param{{Name: "day", Type: script.ParamTypeString, Required: true}},
	})
	require.False(t, res.IsError, resultText(res))
}

// runScript calls the run_script tool over a real client session.
func runScript(ctx context.Context, t *testing.T, session *mcp.ClientSession, args map[string]any) (fields map[string]any, isErr bool) {
	t.Helper()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: ToolNameRunScript, Arguments: args})
	require.NoError(t, err)
	text := resultText(res)
	if res.IsError {
		return map[string]any{"error": text}, true
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	return out, false
}

// TestIntegration_RunWritesAnAssetVersion is the #1284 definition of done:
// save a script, call run_script, and get a real query and a real portal asset
// version, executed by the worker as the script principal.
func TestIntegration_RunWritesAnAssetVersion(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, reportSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	assert.Contains(t, out["log"], "rows: 2")

	outputs, ok := out["outputs"].([]any)
	require.True(t, ok, out["outputs"])
	require.Len(t, outputs, 1)
	first, ok := outputs[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "daily-sales", first["name"])
	assert.Equal(t, float64(1), first["asset_version"])

	require.Len(t, h.assets.inserted, 1)
	assert.Equal(t, "script:daily", h.assets.inserted[0].OwnerID)
	require.Len(t, h.versions.created, 1)
	require.Len(t, h.s3.objects, 1)
	for _, data := range h.s3.objects {
		assert.True(t, strings.HasPrefix(string(data), "region,total"), string(data))
	}

	// The query the script issued crossed the real chain, as the script.
	calls := h.queries.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "warehouse", calls[0].Connection)
	assert.Contains(t, calls[0].SQL, "'2026-08-12'", "the parameter was bound, not spliced")
}

// TestIntegration_SecondRunIsANewVersionOfTheSameAsset is the stable-identity
// property a recurring report depends on.
func TestIntegration_SecondRunIsANewVersionOfTheSameAsset(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, reportSource)
	session := connectAgent(ctx, t, h.server)

	for i := 1; i <= 2; i++ {
		out, isErr := runScript(ctx, t, session, map[string]any{
			"name": "daily", "args": map[string]any{"day": "2026-08-12"},
		})
		require.False(t, isErr, out["error"])
		require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	}

	assert.Len(t, h.assets.inserted, 1, "one output name means one asset, however often it runs")
	require.Len(t, h.versions.created, 2, "each run is a new version of that asset")
	assert.Len(t, h.s3.objects, 2, "each version keeps its own object")
}

// TestIntegration_RunExecutesTheLatestSavedVersion pins the versioning rule
// end to end: a run executes the version sc.Version names — the latest saved
// one — so an edit is what the very next run executes.
func TestIntegration_RunExecutesTheLatestSavedVersion(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, reportSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	assert.EqualValues(t, 1, out["version"])
	assert.Contains(t, out["log"], "rows: 2")

	edited := strings.Replace(reportSource,
		`print("rows: %d" % res["row_count"])`,
		`print("v2 rows: %d" % res["row_count"])`, 1)
	res := call(t, h.handle, authorCtx(), manageScriptInput{
		Command: cmdUpdate, Name: "daily", Source: edited,
	})
	require.False(t, res.IsError, resultText(res))

	out, isErr = runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	assert.EqualValues(t, 2, out["version"], "the run records the version it executed")
	assert.Contains(t, out["log"], "v2 rows: 2", "the edited source is what ran")
}

// deliverySource is a script that computes one result and both refreshes its
// portal asset and delivers the same rows to an external system — the shape the
// destination axis exists for.
const deliverySource = `res = platform.query(
    connection = "warehouse",
    sql = "SELECT region, total FROM sales WHERE d = :day",
    params = {"day": run.params["day"]},
)
platform.export(name="daily-sales", rows=res["rows"], format="csv")
platform.export(
    name = "daily-sales",
    rows = res["rows"],
    format = "csv",
    destination = "acme-drop",
    key = "2026/08/sales.csv",
)
`

// acmeDrop is the configured external destination: a named platform
// connection, a bucket, and the prefix everything written here sits under.
func acmeDrop() script.Destination {
	return script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}
}

// TestIntegration_RunDeliversToAConfiguredBucket is external delivery end to
// end: one computed result becomes a new version of the script's portal asset
// AND an object in the bucket the deployment's configuration names, written as
// an ordinary platform tool call under the script's own principal.
func TestIntegration_RunDeliversToAConfiguredBucket(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse", "acme-s3")
	authorScript(t, h, deliverySource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)

	// The object landed under the configured prefix, at the key the script
	// chose beneath it, over the connection the destination names.
	puts := h.puts.calls()
	require.Len(t, puts, 1)
	assert.Equal(t, "acme-s3", puts[0].Connection)
	assert.Equal(t, "acme-exports", puts[0].Bucket)
	assert.Equal(t, "weekly/2026/08/sales.csv", puts[0].Key)
	assert.Equal(t, "text/csv", puts[0].ContentType)
	require.True(t, puts[0].IsBase64)
	decoded, err := base64.StdEncoding.DecodeString(puts[0].Content)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(decoded), "region,total"), string(decoded))

	// The same result also refreshed the portal asset: one name, two places.
	require.Len(t, h.versions.created, 1)

	outputs, ok := out["outputs"].([]any)
	require.True(t, ok, out["outputs"])
	require.Len(t, outputs, 2, "one output name written to two destinations is two records")
	delivered, ok := outputs[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "daily-sales", delivered["name"])
	assert.Equal(t, "acme-drop", delivered["destination"])
	assert.Equal(t, "acme-exports", delivered["bucket"])
	assert.Equal(t, "weekly/2026/08/sales.csv", delivered["key"])
	assert.Positive(t, delivered["bytes"])

	// The delivery is audited like every other capability call: under the
	// script principal, on the connection it wrote over, in the run's session.
	runID, _ := out["run_id"].(string)
	require.NotEmpty(t, runID)
	put := h.audit.waitFor(t, "s3_put_object")
	assert.Equal(t, "script:daily", put.UserID)
	assert.Equal(t, middleware.SourceScript, put.Source)
	assert.Equal(t, "acme-s3", put.Connection)
	assert.Equal(t, runID, put.SessionID)
}

// TestIntegration_DeliveryUsesTheOutputNameWhenTheScriptNamesNoKey covers the
// simpler arrangement: a consumer reading one fixed path, refreshed each run.
func TestIntegration_DeliveryUsesTheOutputNameWhenTheScriptNamesNoKey(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse", "acme-s3")
	source := strings.Replace(deliverySource, "    key = \"2026/08/sales.csv\",\n", "", 1)
	authorScript(t, h, source)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)

	puts := h.puts.calls()
	require.Len(t, puts, 1)
	assert.Equal(t, "weekly/daily-sales.csv", puts[0].Key)
}

// TestIntegration_UnconfiguredDestinationIsRefused pins destination
// resolution: a name the deployment's configuration does not declare is
// refused inside the host, naming the configured set, and nothing leaves the
// platform.
func TestIntegration_UnconfiguredDestinationIsRefused(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse", "acme-s3")
	source := strings.Replace(deliverySource, `destination = "acme-drop"`, `destination = "nowhere"`, 1)
	authorScript(t, h, source)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	assert.Equal(t, script.RunStatusFailed, out["status"], out)
	assert.Contains(t, out["error"], `destination "nowhere" is not configured`)
	assert.Contains(t, out["error"], "acme-drop", "the refusal names the configured set")
	assert.Empty(t, h.puts.calls(), "nothing left the platform")
}

// TestIntegration_MiddlewareRefusesAConnectionThePersonaLacks pins where
// authorization lives: the run presents its author's roles, the middleware
// resolves the persona those roles hold, and a connection outside that
// persona's reach is refused before the tool runs — for a delivery and for a
// query alike.
func TestIntegration_MiddlewareRefusesAConnectionThePersonaLacks(t *testing.T) {
	ctx := context.Background()

	t.Run("a delivery over a connection the persona does not hold", func(t *testing.T) {
		// acme-drop is configured, but the authorizer does not allow acme-s3.
		h := executionServer(t, "warehouse")
		authorScript(t, h, deliverySource)
		session := connectAgent(ctx, t, h.server)

		out, isErr := runScript(ctx, t, session, map[string]any{
			"name": "daily", "args": map[string]any{"day": "2026-08-12"},
		})
		require.False(t, isErr, out["error"])
		assert.Equal(t, script.RunStatusFailed, out["status"], out)
		assert.Contains(t, out["error"], "not authorized",
			"the authorization middleware is the authority of record")
		assert.Empty(t, h.puts.calls(), "the refusal happened before the tool ran")
	})

	t.Run("a query over a connection the persona does not hold", func(t *testing.T) {
		h := executionServer(t) // no connections allowed by the authorizer
		authorScript(t, h, reportSource)
		session := connectAgent(ctx, t, h.server)

		out, isErr := runScript(ctx, t, session, map[string]any{
			"name": "daily", "args": map[string]any{"day": "2026-08-12"},
		})
		require.False(t, isErr, out["error"])
		assert.Equal(t, script.RunStatusFailed, out["status"], out)
		assert.Contains(t, out["error"], "not authorized")
		assert.Empty(t, h.queries.calls(), "the call never reached the warehouse")
	})
}

// TestIntegration_RunIsAuditedUnderTheScriptPrincipal pins the attribution: the
// capability call and the run lifecycle event both name the script principal
// and share the run id as their session.
func TestIntegration_RunIsAuditedUnderTheScriptPrincipal(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, reportSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	runID, ok := out["run_id"].(string)
	require.True(t, ok)

	query := h.audit.waitFor(t, "trino_query")
	assert.Equal(t, "script:daily", query.UserID, "the capability call is attributed to the script")
	assert.Equal(t, "jane@example.com", query.UserEmail, "with its owner alongside for accountability")
	assert.Equal(t, middleware.SourceScript, query.Source)
	assert.Equal(t, runID, query.SessionID, "one run is one session")

	lifecycle := h.audit.waitFor(t, "run_script")
	assert.Equal(t, "script_run", lifecycle.EventKind)
	assert.Equal(t, runID, lifecycle.SessionID)
	assert.True(t, lifecycle.Success)
	assert.Equal(t, "daily", lifecycle.Parameters["script"])
	assert.Equal(t, "jane@example.com", lifecycle.Parameters["requested_by"])
}

// TestIntegration_RunHistoryIsReadableThroughTheTool covers the follow-up path
// a caller uses when a run outlives its wait.
func TestIntegration_RunHistoryIsReadableThroughTheTool(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, reportSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	runID, ok := out["run_id"].(string)
	require.True(t, ok)

	listed, isErr := callTool(ctx, t, session, map[string]any{"command": cmdRuns, "name": "daily"})
	require.False(t, isErr, listed)
	runsList, ok := listed["runs"].([]any)
	require.True(t, ok)
	require.Len(t, runsList, 1)

	single, isErr := callTool(ctx, t, session, map[string]any{"command": cmdGetRun, "run_id": runID})
	require.False(t, isErr, single)
	assert.Equal(t, script.RunStatusSucceeded, single["status"])
	assert.Contains(t, single["log"], "rows: 2")
	assert.Equal(t, "jane@example.com", single["requested_by"])
}

// TestIntegration_ScriptFailureIsReportedAndNotRetried pins the determinism
// rule end to end: a failing script fails its run once, with the backtrace.
func TestIntegration_ScriptFailureIsReportedAndNotRetried(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, `fail("this report is broken")`)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	assert.Equal(t, script.RunStatusFailed, out["status"], out)
	assert.Contains(t, out["error"], "this report is broken")
	assert.Equal(t, false, out["retryable"])
	assert.Equal(t, float64(1), out["attempt"], "a deterministic failure is not retried")
}

// TestIntegration_WaitTimeoutHandsBackTheRunID covers the bounded window: the
// run keeps going and the caller is told how to follow it.
func TestIntegration_WaitTimeoutHandsBackTheRunID(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, reportSource)

	// Stop the worker so nothing can claim the run inside the wait.
	require.NoError(t, h.worker.Stop(ctx))
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"}, "wait_seconds": -1,
	})
	require.False(t, isErr, out["error"])
	assert.Equal(t, script.RunStatusPending, out["status"])
	assert.Contains(t, out["message"], "get_run")
	assert.NotEmpty(t, out["run_id"])
}

// TestIntegration_SplitDeploymentEnqueuesWithoutExecuting covers the serving
// half of the split deployment end to end: run_script is registered, the run is
// accepted and queued under the caller's identity, and the wait returns it
// still pending because nothing on this replica ever claims. What executes it
// is a worker deployment reading the same queue.
func TestIntegration_SplitDeploymentEnqueuesWithoutExecuting(t *testing.T) {
	ctx := context.Background()
	h := execServerWithWorker(t, false, "warehouse")
	authorScript(t, h, reportSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"}, "wait_seconds": 1,
	})
	require.False(t, isErr, out["error"])
	assert.Equal(t, script.RunStatusPending, out["status"])
	assert.NotEmpty(t, out["run_id"])
	assert.Zero(t, h.runs.claimCount(), "a worker-off replica must never claim from the queue")
	assert.Empty(t, h.assets.inserted, "and must not have executed the run it queued")
}

// TestIntegration_RunScriptIsUnavailableWithoutAQueue covers the deployment
// shape that can author scripts but not execute them: the tool is not
// registered at all, rather than registered and failing.
func TestIntegration_RunScriptIsUnavailableWithoutAQueue(t *testing.T) {
	ctx := context.Background()
	h, _ := newHandle()
	server := mcp.NewServer(&mcp.Implementation{Name: "no-queue", Version: "v0"}, nil)
	h.RegisterTool(server)
	session := connectAgent(ctx, t, server)

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	assert.Contains(t, names, ToolNameManageScript)
	assert.NotContains(t, names, ToolNameRunScript,
		fmt.Sprintf("a deployment with no run queue authors scripts and runs none: %v", names))
}

// etlSource is the automation the closed capability list blocked: fetch from an
// external API server-side, then land the result in the warehouse. Both halves
// are platform.call.
const etlSource = `resp = platform.call("api_invoke_endpoint", {
    "connection": "util",
    "operation_id": "fetch_forecast",
    "body": {"office": "PSR"},
})
periods = resp["body"]["periods"]
print("periods: %d" % len(periods))
# The statement's text is the script's own: trino_execute takes no bound
# parameters, so a value from outside never becomes part of one.
platform.call("trino_execute", {
    "connection": "warehouse",
    "sql": "REFRESH MATERIALIZED VIEW forecast",
})
platform.export(name="forecast", rows=periods, format="json")
`

// TestIntegration_ScriptCallsTheToolsItsAuthorCanCall is #1419's definition of
// done, proved against the assembled system: a saved script reaches
// api_invoke_endpoint and trino_execute through platform.call, the API response
// arrives inside the script, and the statement reaches the connection the
// script named.
func TestIntegration_ScriptCallsTheToolsItsAuthorCanCall(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse", "util")
	authorScript(t, h, etlSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	assert.Contains(t, out["log"], "periods: 2",
		"the tool's structured result reached the script as ordinary data")

	executes := h.executes.calls()
	require.Len(t, executes, 1, "the write reached the write tool")
	assert.Equal(t, "warehouse", executes[0].Connection)
	assert.Equal(t, "REFRESH MATERIALIZED VIEW forecast", executes[0].SQL,
		"the statement the script wrote is the statement that arrived")

	// The call was an ordinary tool call and is audited as one, under the
	// script principal like every other call a run makes.
	written := h.audit.waitFor(t, "trino_execute")
	assert.Equal(t, "script:daily", written.UserID)
	assert.Equal(t, middleware.SourceScript, written.Source)
}

// TestIntegration_MiddlewareRefusesAToolThePersonaLacks is the authorization
// contract for the open surface: the persona filter is the only refusal, it is
// evaluated at run time, and its own words reach the run record. Nothing in the
// script layer decides this.
func TestIntegration_MiddlewareRefusesAToolThePersonaLacks(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse", "util")
	h.authz.deny("trino_execute")
	authorScript(t, h, etlSource)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	assert.Equal(t, script.RunStatusFailed, out["status"], out)
	assert.Contains(t, out["error"], "tool not allowed for persona",
		"the persona filter's own refusal reaches the run record")
	assert.Empty(t, h.executes.calls(), "the refusal happened before the tool ran")
	assert.Empty(t, h.assets.inserted, "and the run failed rather than exporting half a pipeline")
}

// TestIntegration_AScriptCannotStartARun pins the runaway-work guard. It is not
// authorization — run_script is a tool the caller's own persona allows — it is
// that a worker executes one run at a time per replica, so a script waiting on
// a run it queued would wait on the worker running it.
func TestIntegration_AScriptCannotStartARun(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, `platform.call("run_script", {"name": "daily"})`)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	assert.Equal(t, script.RunStatusFailed, out["status"], out)
	assert.Contains(t, out["error"], "cannot be called from inside a script run")

	// Exactly one run exists: the one the agent asked for. The refusal is what
	// keeps the second from being queued at all.
	assert.Len(t, h.runs.all(), 1)
}

// TestIntegration_AScriptCannotDraftRunAScript is the same guard on the other
// entry point, which would nest an interpreter inside an interpreter.
func TestIntegration_AScriptCannotDraftRunAScript(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, `platform.call("manage_script", {"command": "run_draft", "name": "daily"})`)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	assert.Equal(t, script.RunStatusFailed, out["status"], out)
	assert.Contains(t, out["error"], "cannot be called from inside a script run")
}

// TestIntegration_ARunActsForItsVersionAuthor is the wiring proof behind every
// ownership check a script crosses (#1419).
//
// A run authenticates as script:<name>, a principal that owns nothing a person
// owns, so an ownership check judged on that id alone refuses a script the very
// assets its own author can edit — a refusal that is not the persona filter's.
// The run therefore carries the address of the person it acts for, and this
// asserts that the address REACHES a tool handler through the real chain, which
// is the half a unit test on the predicate cannot prove.
//
// It is the author, not the script's owner: the run presents the author's
// roles, so pairing them with anyone else's ownership would give a run a
// combination no person has.
func TestIntegration_ARunActsForItsVersionAuthor(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, `platform.call("whoami")`)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)

	calls := h.identity.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "script:daily", calls[0].UserID,
		"the run still authenticates as the script principal, which is what audit and its outputs are attributed to")
	assert.Equal(t, "jane@example.com", calls[0].OnBehalfOfEmail,
		"and carries the address of the author whose roles it presents, so ownership follows the same person")
	assert.Equal(t, middleware.SourceScript, calls[0].Source)
}

// TestIntegration_ADraftActsAsItselfAndNeedsNoSecondIdentity covers the other
// run kind. A draft authenticates as the caller, a real person with a real user
// id, so ownership already resolves on the id and no address is carried: a
// second matching path for a caller who already matches would be surface with
// nothing to do.
func TestIntegration_ADraftActsAsItselfAndNeedsNoSecondIdentity(t *testing.T) {
	h := executionServer(t, "warehouse")
	authorScript(t, h, `platform.call("whoami")`)

	res := call(t, h.handle, authorCtx(), manageScriptInput{
		Command: cmdRunDraft, Name: "daily",
		Args: map[string]any{"day": "2026-08-12"},
	})
	require.False(t, res.IsError, resultText(res))

	calls := h.identity.calls()
	require.Len(t, calls, 1)
	assert.Equal(t, "user-jane@example.com", calls[0].UserID,
		"a draft is its caller, so the id is the person's own")
	assert.Empty(t, calls[0].OnBehalfOfEmail,
		"and nobody is being acted for")
}

// TestIntegration_AScriptCannotAuthorOrScheduleAScript closes the door beside
// the one refuseReentrantRun locks.
//
// A run that could create a script and set a cadence starts unbounded work
// across runs — the cycle the re-entrancy guard exists to stop. A run that
// could PATCH a script is worse: inside a run the caller identity is the
// script's own, so the new version would capture the roles the run is executing
// with, under the owner's address, making a role set captured once for one save
// permanent and attributed to somebody who never held it.
func TestIntegration_AScriptCannotAuthorOrScheduleAScript(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		args string
	}{
		{"create", `{"command": "create", "name": "spawned", "source": "print(1)"}`},
		{"patch", `{"command": "patch", "name": "daily", "edits": [{"find": "a", "replace": "b"}]}`},
		{"update", `{"command": "update", "name": "daily", "source": "print(2)"}`},
		{"delete", `{"command": "delete", "name": "daily"}`},
		{"schedule_set", `{"command": "schedule_set", "name": "daily", "cron": "* * * * *"}`},
		{"schedule_enable", `{"command": "schedule_enable", "name": "daily"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := executionServer(t, "warehouse")
			authorScript(t, h, `platform.call("manage_script", `+tc.args+`)`)
			session := connectAgent(ctx, t, h.server)

			out, isErr := runScript(ctx, t, session, map[string]any{
				"name": "daily", "args": map[string]any{"day": "2026-08-12"},
			})
			require.False(t, isErr, out["error"])
			assert.Equal(t, script.RunStatusFailed, out["status"], out)
			assert.Contains(t, out["error"], "cannot be called from inside a script run")

			// The script is untouched: one version, the one that was authored.
			sc, err := h.store.GetByName(ctx, "jane@example.com", "daily")
			require.NoError(t, err)
			require.NotNil(t, sc)
			assert.Equal(t, 1, sc.Version, "no version was written by the run")
		})
	}
}

// TestIntegration_AScriptMayStillReadTheScriptSurface is the other half: the
// guard is about changing what exists, not about looking. A run that could not
// describe the automation it belongs to would be worse off for no gain.
func TestIntegration_AScriptMayStillReadTheScriptSurface(t *testing.T) {
	ctx := context.Background()
	h := executionServer(t, "warehouse")
	authorScript(t, h, `
res = platform.call("manage_script", {"command": "get", "name": "daily"})
print("read %s" % res["name"])
`)
	session := connectAgent(ctx, t, h.server)

	out, isErr := runScript(ctx, t, session, map[string]any{
		"name": "daily", "args": map[string]any{"day": "2026-08-12"},
	})
	require.False(t, isErr, out["error"])
	require.Equal(t, script.RunStatusSucceeded, out["status"], out)
	assert.Contains(t, out["log"], "read daily")
}
