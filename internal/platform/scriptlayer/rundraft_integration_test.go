package scriptlayer

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/toolratelimit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/searchgate"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
)

// This file is the assembled-system proof for #1283. A unit test that hands the
// engine a hand-written Caller proves the interpreter works; it proves nothing
// about whether a script's platform call actually crosses the middleware chain,
// carries the author's identity, and is audited. So the stack here is real:
// mcp.Server with the platform's own middleware wired in the platform's own
// order, the manage_script tool registered by the real Handle, and a client
// session driving tools/call over an in-memory transport.
//
// The one substitution is the warehouse. trino_query is registered as a handler
// that returns the shape the real Trino toolkit returns (columns, rows,
// row_count) rather than reaching a cluster. Everything between the script and
// that handler — host binding, in-memory session, auth, authz, session gate,
// search-first gate, audit — is the production code path.

// queryOutput mirrors the structured result the Trino toolkit returns, so the
// host binding is exercised against the real shape.
type queryOutput struct {
	Columns  []queryColumn    `json:"columns"`
	Rows     []map[string]any `json:"rows"`
	RowCount int              `json:"row_count"`
}

type queryColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type queryInput struct {
	SQL        string `json:"sql"`
	Limit      int    `json:"limit,omitempty"`
	Connection string `json:"connection,omitempty"`
}

// recordingAudit collects audit events the chain emits. Audit is written off
// the request path, so reads wait for it.
type recordingAudit struct {
	mu     sync.Mutex
	events []middleware.AuditEvent
}

func (a *recordingAudit) Log(_ context.Context, e middleware.AuditEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, e)
	return nil
}

// waitFor returns the first audit event for a tool, or fails.
func (a *recordingAudit) waitFor(t *testing.T, tool string) middleware.AuditEvent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		a.mu.Lock()
		for _, e := range a.events {
			if e.ToolName == tool {
				a.mu.Unlock()
				return e
			}
		}
		a.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no audit event for %s", tool)
	return middleware.AuditEvent{}
}

// fakeAuthn authenticates every call as one fixed user, standing in for the
// real authenticators.
type fakeAuthn struct{ user *middleware.UserInfo }

func (f *fakeAuthn) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return f.user, nil
}

// fakeAuthz authorizes every call and resolves one persona.
type fakeAuthz struct{ persona string }

func (f *fakeAuthz) IsAuthorized(context.Context, string, []string, string, string) (authorized bool, persona, reason string) {
	return true, f.persona, ""
}

// harness is the assembled server plus the pieces a test asserts against.
type harness struct {
	server  *mcp.Server
	handle  *Handle
	store   *memStore
	audit   *recordingAudit
	queries *recordingQueries
}

// recordingQueries records what the trino_query handler was actually asked, so
// the test can assert on the SQL that crossed the chain.
type recordingQueries struct {
	mu   sync.Mutex
	seen []queryInput
	rows []map[string]any
	fail string
}

func (q *recordingQueries) record(in queryInput) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.seen = append(q.seen, in)
}

func (q *recordingQueries) calls() []queryInput {
	q.mu.Lock()
	defer q.mu.Unlock()
	return append([]queryInput(nil), q.seen...)
}

// assembledServer wires the real middleware chain in the platform's own order
// (innermost added first, so the last added runs first) and registers both
// manage_script and a Trino-shaped query tool.
func assembledServer(t *testing.T) harness {
	t.Helper()
	return assembledServerWith(t, nil)
}

// assembledServerWith is assembledServer over a declared destination set, for
// the tests that assert what validate and run_draft say about a destination,
// and over any further middleware a test places where the platform places its
// limiter: inner to the gates, outer to audit.
func assembledServerWith(t *testing.T, destinations []script.Destination, inner ...mcp.Middleware) harness {
	t.Helper()
	const authorID = "user-jane@example.com"

	store := newMemStore()
	h := New(Config{Store: store, AdminPersona: "admin", Destinations: destinations})
	audit := &recordingAudit{}
	queries := &recordingQueries{rows: []map[string]any{
		{"region": "west", "total": float64(120)},
		{"region": "east", "total": float64(80)},
	}}

	server := mcp.NewServer(&mcp.Implementation{Name: "script-integration", Version: "v0"}, nil)
	h.RegisterTool(server)

	mcp.AddTool(server, &mcp.Tool{Name: "trino_query", Description: "query"},
		func(_ context.Context, _ *mcp.CallToolRequest, in queryInput) (*mcp.CallToolResult, any, error) {
			queries.record(in)
			if queries.fail != "" {
				return &mcp.CallToolResult{
					IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: queries.fail}},
				}, nil, nil
			}
			out := queryOutput{
				Columns:  []queryColumn{{Name: "region", Type: "varchar"}, {Name: "total", Type: "bigint"}},
				Rows:     queries.rows,
				RowCount: len(queries.rows),
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, out, nil
		})

	tracker := middleware.NewSessionWorkflowTracker(
		[]string{"search"}, []string{"trino_query"}, searchgate.NewMemoryStore(time.Hour), time.Hour)

	server.AddReceivingMiddleware(middleware.MCPAuditMiddleware(audit))
	server.AddReceivingMiddleware(inner...)
	server.AddReceivingMiddleware(middleware.MCPWorkflowGateMiddleware(tracker))
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		&fakeAuthn{user: &middleware.UserInfo{
			UserID: authorID, Email: "jane@example.com",
			Roles: []string{"analyst"}, AuthType: middleware.AuthTypeOIDC,
		}},
		&fakeAuthz{persona: "analyst"},
		&fakeLookup{},
		middleware.ToolCallConfig{Transport: "http", AdminPersona: "admin", WorkflowTracker: tracker},
	))

	return harness{server: server, handle: h, store: store, audit: audit, queries: queries}
}

// fakeLookup reports no toolkit for any tool, which is what the registry does
// for platform-level tools.
type fakeLookup struct{}

func (fakeLookup) GetToolkitForTool(string) registry.ToolkitMatch {
	return registry.ToolkitMatch{}
}

// connectAgent opens a client session the way a real MCP agent would.
func connectAgent(ctx context.Context, t *testing.T, server *mcp.Server) *mcp.ClientSession {
	t.Helper()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "v0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool runs one manage_script command over the real session and decodes the
// JSON result.
func callTool(ctx context.Context, t *testing.T, session *mcp.ClientSession, args map[string]any) (fields map[string]any, isErr bool) {
	t.Helper()
	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: ToolNameManageScript, Arguments: args})
	require.NoError(t, err)
	text := resultText(res)
	if res.IsError {
		return map[string]any{"error": text}, true
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(text), &out), text)
	return out, false
}

// TestIntegration_AuthorValidateRunDraft is the #1283 definition of done: an
// agent authors a script through the real tool, validate reports the
// capabilities and connections it references, and run_draft executes it through
// the real middleware chain under the author's identity, returning rows and the
// bounded log.
func TestIntegration_AuthorValidateRunDraft(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	const source = `report_date = date.add_days(date.of(run.fire_time), -1)
print("reporting on " + report_date)
res = platform.query(
    connection = "warehouse",
    sql = "SELECT region, total FROM sales WHERE d = :day AND region IN :regions",
    params = {"day": report_date, "regions": ["west", "east"]},
)
for row in res["rows"]:
    print("%s %d" % (row["region"], row["total"]))
platform.export(name = "daily-sales", rows = res["rows"], format = "csv")
`

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "daily-sales", "source": source,
		"params": []map[string]any{{"name": "day", "type": "date", "required": true}},
	})
	require.False(t, isErr, created)
	assert.Equal(t, "created", created["status"])

	validated, isErr := callTool(ctx, t, session, map[string]any{
		"command": "validate", "name": "daily-sales",
	})
	require.False(t, isErr, validated)
	assert.Equal(t, true, validated["ok"])
	assert.ElementsMatch(t, []any{"platform.query", "platform.export"}, validated["capabilities"])
	assert.Equal(t, []any{"warehouse"}, validated["connections"])
	// A script that neither reads nor saves state says so (#1545).
	assert.Equal(t, false, validated["reads_state"])
	assert.Equal(t, false, validated["saves_state"])

	stateful, isErr := callTool(ctx, t, session, map[string]any{
		"command": "validate",
		"source":  "since = run.state.get(\"synced_through\", \"never\")\nplatform.save_state({\"synced_through\": run.fire_time})\n",
	})
	require.False(t, isErr, stateful)
	assert.Equal(t, true, stateful["reads_state"], "validate over MCP must report run.state reads (#1545)")
	assert.Equal(t, true, stateful["saves_state"], "validate over MCP must report platform.save_state (#1545)")

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "daily-sales",
		"args": map[string]any{"day": "2026-08-13"},
	})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran["error"])
	assert.EqualValues(t, 1, ran["queries"])

	log, _ := ran["log"].(string)
	assert.Contains(t, log, "reporting on ")
	assert.Contains(t, log, "west 120")
	assert.Contains(t, log, "east 80", "the rows the tool returned reached the script")

	exports, _ := ran["exports"].([]any)
	require.Len(t, exports, 1, "the export was previewed")
	preview, _ := exports[0].(map[string]any)
	assert.Equal(t, "daily-sales", preview["name"])
	assert.EqualValues(t, 2, preview["row_count"])

	// The SQL that crossed the chain carries bound literals, not the
	// placeholders the author wrote, and carries the row cap.
	calls := h.queries.calls()
	require.Len(t, calls, 1)
	assert.Contains(t, calls[0].SQL, "region IN ('west', 'east')")
	assert.NotContains(t, calls[0].SQL, ":day")
	assert.Equal(t, "warehouse", calls[0].Connection)
	assert.Positive(t, calls[0].Limit)

	// The draft run persisted nothing: the version a platform run would
	// execute is still the one create wrote.
	for _, sc := range h.store.scripts {
		assert.Equal(t, 1, sc.Version)
		v, err := h.store.GetVersion(ctx, sc.ID, sc.Version)
		require.NoError(t, err)
		require.NotNil(t, v)
		assert.Equal(t, script.VersionStatusApplied, v.Status)
	}
}

// TestIntegration_DraftRunCarriesTheAuthorIdentityAndIsAudited is the "no new
// authority" claim stated as an assertion: the query a script issues is
// attributed to the person who ran the draft, with their persona, and is
// recorded — it is not an anonymous or elevated call.
func TestIntegration_DraftRunCarriesTheAuthorIdentityAndIsAudited(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	_, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "one-query",
		"source": "platform.query(sql = \"SELECT 1\")\n",
	})
	require.False(t, isErr)

	ran, isErr := callTool(ctx, t, session, map[string]any{"command": "run_draft", "name": "one-query"})
	require.False(t, isErr, ran)
	require.Equal(t, "succeeded", ran["status"], ran["error"])

	ev := h.audit.waitFor(t, "trino_query")
	assert.Equal(t, "user-jane@example.com", ev.UserID, "the query runs as the author, not as a service principal")
	assert.Equal(t, "jane@example.com", ev.UserEmail)
	assert.Equal(t, "analyst", ev.Persona, "the author's persona is resolved by the same authorizer")
	assert.True(t, ev.Success)
	assert.True(t, ev.Authorized)
	assert.Equal(t, middleware.SourceScript, ev.Source, "the call is attributable as a script run")

	// The run is isolated from the author's own session state: it keys on a
	// minted script-run session id, never on the agent session that asked for it.
	assert.NotEmpty(t, ev.SessionID)
	assert.Equal(t, pkgsession.ScriptSessionPrefix, ev.SessionID[:len(pkgsession.ScriptSessionPrefix)])

	manage := h.audit.waitFor(t, ToolNameManageScript)
	assert.NotEqual(t, manage.SessionID, ev.SessionID,
		"the script run must not share the calling agent's session identity")
}

// TestIntegration_ScriptQueryIsNotBlockedByTheSearchFirstGate covers the
// SourceScript exemption end to end: the agent session never called search, and
// a script's query still executes.
func TestIntegration_ScriptQueryIsNotBlockedByTheSearchFirstGate(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	// The same query, called directly by the agent, is gated.
	direct, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "trino_query", Arguments: map[string]any{"sql": "SELECT 1"},
	})
	require.NoError(t, err)
	require.True(t, direct.IsError, "the search-first gate must still hold for the agent itself")

	_, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "gated", "source": "platform.query(sql = \"SELECT 1\")\n",
	})
	require.False(t, isErr)

	ran, isErr := callTool(ctx, t, session, map[string]any{"command": "run_draft", "name": "gated"})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran["error"])
}

// TestIntegration_RunDraftReportsAFailedRunWithItsLog proves a failure is
// legible rather than a bare error: the log up to the failure is returned, and
// the response says plainly that retrying is pointless.
func TestIntegration_RunDraftReportsAFailedRunWithItsLog(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	h.queries.fail = "Query failed: table sales does not exist"
	session := connectAgent(ctx, t, h.server)

	_, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "broken-query",
		"source": "print(\"starting\")\nplatform.query(sql = \"SELECT 1 FROM sales\")\n",
	})
	require.False(t, isErr)

	ran, isErr := callTool(ctx, t, session, map[string]any{"command": "run_draft", "name": "broken-query"})
	require.False(t, isErr, ran)
	assert.Equal(t, "failed", ran["status"])
	assert.Contains(t, ran["error"], "table sales does not exist")
	assert.Equal(t, false, ran["retryable"])
	assert.Contains(t, ran["log"], "starting", "the log up to the failure is what the author needs")
}

// TestIntegration_RunDraftChecksParametersBeforeRunning keeps a typo from
// reaching the interpreter as a missing key.
func TestIntegration_RunDraftChecksParametersBeforeRunning(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	_, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "needs-params", "source": "print(run.params[\"day\"])\n",
		"params": []map[string]any{{"name": "day", "type": "date", "required": true}},
	})
	require.False(t, isErr)

	res, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "needs-params", "args": map[string]any{"dya": "2026-08-13"},
	})
	require.True(t, isErr)
	assert.Contains(t, res["error"], "unknown parameter")
	assert.Empty(t, h.queries.calls())
}

// TestIntegration_RunDraftNeedsAnAuthenticatedCaller covers the refusal that
// makes the identity property structural: with no caller identity there is
// nobody to run as, so the draft does not run at all.
func TestIntegration_RunDraftNeedsAnAuthenticatedCaller(t *testing.T) {
	h, store := newHandle()
	server := mcp.NewServer(&mcp.Implementation{Name: "t", Version: "v0"}, nil)
	h.RegisterTool(server)
	require.NoError(t, store.Create(context.Background(), &script.Script{
		Name: "x", OwnerEmail: "jane@example.com",
		Source: "print(1)", Enabled: true,
	}, script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}))

	res := call(t, h, authorCtxWithoutUserID(), manageScriptInput{Command: cmdRunDraft, Name: "x"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "authenticated caller")
}

// authorCtxWithoutUserID carries an email but no user id, the shape an
// unauthenticated call produces.
func authorCtxWithoutUserID() context.Context {
	pc := middleware.NewPlatformContext("req")
	pc.UserEmail = "jane@example.com"
	return middleware.WithPlatformContext(context.Background(), pc)
}

// TestIntegration_RunDraftWithoutAServerIsRefused covers the no-server
// deployment shape rather than panicking on a nil server.
func TestIntegration_RunDraftWithoutAServerIsRefused(t *testing.T) {
	h, store := newHandle()
	require.NoError(t, store.Create(context.Background(), &script.Script{
		Name: "x", OwnerEmail: "jane@example.com",
		Source: "print(1)", Enabled: true,
	}, script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}))

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdRunDraft, Name: "x"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "unavailable")
}

// TestIntegration_OneRunIsOneSession pins the correlation the run id claims: the
// id handed back to the author is the session id every platform call in that run
// records, so a run that issues several queries is one grouping in audit rather
// than several unrelated ones.
func TestIntegration_OneRunIsOneSession(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	_, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "three-queries",
		"source": "for n in range(3):\n    platform.query(sql = \"SELECT 1\")\n",
	})
	require.False(t, isErr)

	ran, isErr := callTool(ctx, t, session, map[string]any{"command": "run_draft", "name": "three-queries"})
	require.False(t, isErr, ran)
	require.Equal(t, "succeeded", ran["status"], ran["error"])
	require.EqualValues(t, 3, ran["queries"])

	runID, _ := ran["run_id"].(string)
	require.NotEmpty(t, runID)

	h.audit.waitFor(t, "trino_query")
	h.audit.mu.Lock()
	defer h.audit.mu.Unlock()
	queryEvents := 0
	for _, ev := range h.audit.events {
		if ev.ToolName != "trino_query" {
			continue
		}
		queryEvents++
		assert.Equal(t, runID, ev.SessionID,
			"every call in a run records the run id the author was given")
	}
	assert.Equal(t, 3, queryEvents)
}

// TestIntegration_DisabledScriptDoesNotRun: run_draft is the only execution path
// that exists, so without this check "disabled" would disable nothing.
func TestIntegration_DisabledScriptDoesNotRun(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	_, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "off", "source": "platform.query(sql = \"SELECT 1\")\n",
	})
	require.False(t, isErr)
	_, isErr = callTool(ctx, t, session, map[string]any{
		"command": "update", "name": "off", "enabled": false,
	})
	require.False(t, isErr)

	res, isErr := callTool(ctx, t, session, map[string]any{"command": "run_draft", "name": "off"})
	require.True(t, isErr)
	assert.Contains(t, res["error"], "disabled")
	assert.Empty(t, h.queries.calls())
}

// TestIntegration_RunDraftExecutesTheSourceItWasGiven is #1413. run_draft
// exists so an author can execute an edit that has not been saved; before this
// it read the record and ran the stored version, reported success, and handed
// back a log the submitted source never produced.
func TestIntegration_RunDraftExecutesTheSourceItWasGiven(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "probe", "source": "print(\"saved\")\n",
	})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "probe",
		"source": "print(\"submitted\")\n",
	})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran["error"])

	log, _ := ran["log"].(string)
	assert.Equal(t, "submitted\n", log, "the source sent with the call is what ran")
	assert.NotContains(t, log, "saved")

	// The stored version is untouched: a draft is not a save.
	for _, sc := range h.store.scripts {
		assert.Equal(t, "print(\"saved\")\n", sc.Source)
		assert.Equal(t, 1, sc.Version)
	}
}

// TestIntegration_RunDraftWithNoSourceRunsTheSavedVersion is the other half of
// the same rule, and the behavior every existing case relied on: dry-running a
// script nobody has edited needs no source.
func TestIntegration_RunDraftWithNoSourceRunsTheSavedVersion(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "probe", "source": "print(\"saved\")\n",
	})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "probe",
	})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran["error"])
	assert.Equal(t, "saved\n", ran["log"])
}

// TestIntegration_RunDraftRefusesASubmittedSourceThatDoesNotValidate keeps the
// static read in front of the interpreter on this surface too, which is where
// the portal editor already had it.
func TestIntegration_RunDraftRefusesASubmittedSourceThatDoesNotValidate(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "probe", "source": "print(\"saved\")\n",
	})
	require.False(t, isErr, created)

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "probe", "source": "def f(:\n",
	})
	require.True(t, isErr, ran)
	assert.Contains(t, ran["error"], "was not run")
}

// TestIntegration_ValidateRefusesAnUndeclaredDestination is #1415: on a
// deployment declaring no bucket destinations, a script naming one reported
// ok and failed at the export, after its queries had already run.
func TestIntegration_ValidateRefusesAnUndeclaredDestination(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	const source = `res = platform.query(connection = "warehouse", sql = "SELECT region, total FROM sales")
platform.export("top-stores", res["rows"], "csv", destination = "drop", key = "top-stores.csv")
`
	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "top-stores", "source": source,
	})
	require.False(t, isErr, created, "the source is storable; only this deployment cannot serve it")

	validated, isErr := callTool(ctx, t, session, map[string]any{
		"command": "validate", "name": "top-stores",
	})
	require.False(t, isErr, validated)
	assert.Equal(t, false, validated["ok"])

	findings, _ := validated["findings"].([]any)
	require.Len(t, findings, 1)
	finding, _ := findings[0].(map[string]any)
	assert.Equal(t, "error", finding["severity"])
	assert.Contains(t, finding["message"], `destination "drop" is not configured`)

	// And the refusal arrives before the queries, not after them.
	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "top-stores",
	})
	require.True(t, isErr, ran)
	assert.Contains(t, ran["error"], `destination "drop" is not configured`)
	assert.Empty(t, h.queries.calls(), "nothing was queried on the way to a known refusal")
}

// TestIntegration_ValidateAcceptsADeclaredDestination is the same script on the
// deployment that declares the destination: validate must not invent a stricter
// set than the run resolves against.
func TestIntegration_ValidateAcceptsADeclaredDestination(t *testing.T) {
	ctx := context.Background()
	h := assembledServerWith(t, []script.Destination{{
		Name: "drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}})
	session := connectAgent(ctx, t, h.server)

	const source = `platform.export("top-stores", [], "csv", destination = "drop", key = "top-stores.csv")` + "\n"
	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "top-stores", "source": source,
	})
	require.False(t, isErr, created)

	validated, isErr := callTool(ctx, t, session, map[string]any{
		"command": "validate", "name": "top-stores",
	})
	require.False(t, isErr, validated)
	assert.Equal(t, true, validated["ok"], validated["findings"])
	assert.Equal(t, []any{"drop"}, validated["destinations"])
}

// TestIntegration_ValidateChecksAnInlineSourcesDestinations covers the shape an
// agent actually uses while iterating: validate carrying the edit, with no
// stored script involved.
func TestIntegration_ValidateChecksAnInlineSourcesDestinations(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	validated, isErr := callTool(ctx, t, session, map[string]any{
		"command": "validate",
		"source":  `platform.export("x", [], "csv", destination = "drop", key = "x.csv")` + "\n",
	})
	require.False(t, isErr, validated)
	assert.Equal(t, false, validated["ok"])
	assert.NotEmpty(t, validated["help"])
}

// TestIntegration_SumIsAvailableToAScript is #1414: the contract prescribes
// sum() as the fix for a DECIMAL column arriving as a string, and a script
// written from the platform's own documentation has to run.
func TestIntegration_SumIsAvailableToAScript(t *testing.T) {
	ctx := context.Background()
	h := assembledServer(t)
	session := connectAgent(ctx, t, h.server)

	const source = `res = platform.query(connection = "warehouse", sql = "SELECT region, total FROM sales")
print("total %d" % sum([float(r["total"]) for r in res["rows"]]))
`
	created, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "totals", "source": source,
	})
	require.False(t, isErr, created)

	validated, isErr := callTool(ctx, t, session, map[string]any{
		"command": "validate", "name": "totals",
	})
	require.False(t, isErr, validated)
	assert.Equal(t, true, validated["ok"], validated["findings"])

	ran, isErr := callTool(ctx, t, session, map[string]any{
		"command": "run_draft", "name": "totals",
	})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran["error"])
	assert.Contains(t, ran["log"], "total 200")
}

// TestIntegration_RunDraftPacesARateLimitedCall is the draft half of the #1533
// acceptance: a draft shares its author's bucket with the agent driving it, and
// a call the limiter refuses is waited out under DraftTimeout rather than
// failing the draft. With one token a second and a burst of three, create and
// run_draft spend two, the first query spends the third, and the second query
// is refused once and admitted a second later.
func TestIntegration_RunDraftPacesARateLimitedCall(t *testing.T) {
	ctx := context.Background()
	limiter := toolratelimit.New(60, 3, nil, nil)
	t.Cleanup(limiter.Close)
	h := assembledServerWith(t, nil, limiter.Middleware())
	session := connectAgent(ctx, t, h.server)

	_, isErr := callTool(ctx, t, session, map[string]any{
		"command": "create", "name": "two-queries",
		"source": "a = platform.query(sql = \"SELECT 1\")\nprint(len(a[\"rows\"]))\nb = platform.query(sql = \"SELECT 2\")\nprint(len(b[\"rows\"]))\n",
	})
	require.False(t, isErr)

	ran, isErr := callTool(ctx, t, session, map[string]any{"command": "run_draft", "name": "two-queries"})
	require.False(t, isErr, ran)
	assert.Equal(t, "succeeded", ran["status"], ran)
	assert.Len(t, h.queries.calls(), 2, "both queries were served, the second after a wait")
	assert.Equal(t, "2\nrate limit: trino_query was refused; waited 1s and retried\n2\n", ran["log"])
}
