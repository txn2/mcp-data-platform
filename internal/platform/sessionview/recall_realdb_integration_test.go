//go:build integration

package sessionview_test

// Agent recall of a session, end to end over a real PostgreSQL database
// (#1322).
//
// What this proves cannot be proved by stubbing the pieces apart: the calls an
// agent makes are written by the audit pipeline, the session is derived from
// those rows by the read model, the outcome of each call is derived from the
// catalog the same pipeline wrote, and the agent gets all of it back through
// the search and fetch tools it actually calls. Every one of those hand-offs is
// a place the session could come back empty, unannotated, or — the one that
// matters — belonging to somebody else.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/auditwiring"
	"github.com/txn2/mcp-data-platform/internal/platform/callrecord"
	"github.com/txn2/mcp-data-platform/internal/platform/sessionview"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	pkgsession "github.com/txn2/mcp-data-platform/pkg/session"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
	searchkit "github.com/txn2/mcp-data-platform/pkg/toolkits/search"
)

const (
	analystID   = "550e8400-e29b-41d4-a716-446655440111"
	analystMail = "analyst@example.com"
	strangerID  = "550e8400-e29b-41d4-a716-446655440222"
	strangerML  = "stranger@example.com"

	revenueSQL   = "SELECT region, SUM(amount) FROM iceberg.sales.orders GROUP BY region"
	priorYearSQL = "SELECT region, SUM(amount) FROM iceberg.sales.orders_2025 GROUP BY region"
	strangerSQL  = "SELECT sku, qty FROM iceberg.sales.inventory"

	revenuePurpose   = "Sizing Q3 revenue by region for the board deck."
	priorYearPurpose = "Adding the prior-year comparison to the board deck."
	strangerPurpose  = "Checking warehouse inventory levels for the restock run."
)

// --- the assembled server --------------------------------------------------

type fixedAuthn struct{ user *middleware.UserInfo }

func (f *fixedAuthn) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return f.user, nil
}

type allowAuthz struct{}

func (allowAuthz) IsAuthorized(context.Context, string, []string, string, string) (bool, string, string) {
	return true, "analyst", ""
}

type toolkitLookup struct{}

func (toolkitLookup) GetToolkitForTool(tool string) registry.ToolkitMatch {
	if tool == "trino_query" {
		return registry.ToolkitMatch{Kind: "trino", Name: "prod", Connection: "warehouse", Found: true}
	}
	return registry.ToolkitMatch{}
}

// replica is one server instance over the shared database, serving one caller:
// its own audit layer (with the call-catalog decorator inside the writer) and
// its own MCP server carrying the query tool and the discovery tools.
type replica struct {
	server *mcp.Server
	layer  *auditwiring.Layer
}

// newReplica assembles the server one caller talks to. The search federation is
// the real one: a router over the sessions provider, wired to the same call
// catalog the audit writer feeds, which is what lets a recalled timeline say
// what each call was and what came of it.
func newReplica(t *testing.T, db *sql.DB, sessions pkgsession.Store, user *middleware.UserInfo) *replica {
	t.Helper()

	layer := auditwiring.Assemble(auditwiring.Config{
		DB:            db,
		RetentionDays: 30,
		BuildURN: func(_, catalog, schema, table string) string {
			return fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:trino,%s.%s.%s,PROD)", catalog, schema, table)
		},
	})

	provider := knowledge.NewSessionsProvider(sessionview.ReaderFor(db))
	provider.SetCalls(layer.Calls())
	discovery := searchkit.New("default", knowledge.NewRouter(nil, nil, provider))

	server := mcp.NewServer(&mcp.Implementation{Name: "recall-test", Version: "v0"}, nil)
	discovery.RegisterTools(server)
	registerQueryTool(server)

	// Innermost added first: the call reference and the audit writer both read
	// the PlatformContext the tool-call middleware writes.
	server.AddReceivingMiddleware(middleware.MCPCallReferenceMiddleware([]string{"trino"}))
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

	return &replica{server: server, layer: layer}
}

func registerQueryTool(server *mcp.Server) {
	server.AddTool(&mcp.Tool{
		Name:        "trino_query",
		Description: "Run a query",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"sql":{"type":"string"},"connection":{"type":"string"}}}`),
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"rows":[["west",10]]}`}}}, nil
	})
}

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

// mintHandle creates the session row an agent receives from platform_info.
func mintHandle(ctx context.Context, t *testing.T, sessions pkgsession.Store, userID string) string {
	t.Helper()
	handle, err := pkgsession.GenerateHandle()
	require.NoError(t, err)
	now := time.Now()
	require.NoError(t, sessions.Create(ctx, &pkgsession.Session{
		ID: handle, UserID: userID, CreatedAt: now, LastActiveAt: now,
		ExpiresAt: now.Add(time.Hour),
		State: map[string]any{
			pkgsession.StateKeyMintedBy: pkgsession.MintedByPlatformInfo,
			pkgsession.StateKeyPersona:  "analyst",
		},
	}))
	return handle
}

func call(ctx context.Context, t *testing.T, sess *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "%s failed: %s", name, resultText(res))
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

// runQuery runs one query under a session handle, then drains the audit writer
// so the row it produced, and the record derived from it, are readable.
func runQuery(ctx context.Context, t *testing.T, r *replica, sess *mcp.ClientSession, handle, sqlText, purpose string) {
	t.Helper()
	call(ctx, t, sess, "trino_query", map[string]any{
		"sql": sqlText, "connection": "warehouse", "session_id": handle, "purpose": purpose,
	})
	flush(ctx, t, r)
}

// flush drains the async audit writer, so what a call just did is readable.
func flush(ctx context.Context, t *testing.T, r *replica) {
	t.Helper()
	f := auditwiring.AsFlusher(r.layer.Logger())
	require.NotNil(t, f, "the async writer must expose the barrier a read waits on")
	require.NoError(t, f.Flush(ctx))
}

// --- reading the tools' answers -------------------------------------------

type searchEnvelope struct {
	Groups []struct {
		Source string          `json:"source"`
		Hits   []knowledge.Hit `json:"hits"`
	} `json:"groups"`
	Count int `json:"count"`
}

type fetchEnvelope struct {
	Found     bool   `json:"found"`
	Reference string `json:"reference"`
	Document  *struct {
		Reference  string                  `json:"reference"`
		Source     string                  `json:"source"`
		Title      string                  `json:"title"`
		Content    json.RawMessage         `json:"content"`
		References []knowledge.DocumentRef `json:"references"`
	} `json:"document"`
}

func decode[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	var out T
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out), "response: %s", resultText(res))
	return out
}

// sessionHits returns the hits the sessions source contributed.
func sessionHits(t *testing.T, res *mcp.CallToolResult) []knowledge.Hit {
	t.Helper()
	for _, g := range decode[searchEnvelope](t, res).Groups {
		if g.Source == knowledge.SourceSessions {
			return g.Hits
		}
	}
	return nil
}

// --- the story recall tells ------------------------------------------------

// The whole of #1322 in one run: an agent searches for what it was doing, gets
// its session back, opens it, and finds the calls it made — each with the
// reference that fetches its record — and the asset and insight it left behind.
func TestSessionRecallRealDBSearchesAndOpensItsOwnSession(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)

	analyst := newReplica(t, db, sessions, &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	agent := connect(ctx, t, analyst)
	handle := mintHandle(ctx, t, sessions, analystID)

	runQuery(ctx, t, analyst, agent, handle, revenueSQL, revenuePurpose)
	runQuery(ctx, t, analyst, agent, handle, priorYearSQL, priorYearPurpose)
	saveAsset(ctx, t, db, handle, "Q3 revenue by region")
	captureInsight(ctx, t, db, handle, "revenue.amount excludes returns.")

	// A phrase from what the agent said it was doing finds the session.
	found := sessionHits(t, call(ctx, t, agent, "search", map[string]any{
		"intent": "revenue for the board deck", "session_id": handle,
	}))
	require.Len(t, found, 1, "one session, however many of its calls matched")
	assert.Equal(t, "mcp:session:"+handle, found[0].Reference)
	assert.Contains(t, found[0].Text, revenuePurpose)
	assert.Contains(t, found[0].Text, "Saved: Q3 revenue by region")

	// Searching under the handle is itself a call the session made, so drain
	// the writer before reading the timeline it now belongs to.
	flush(ctx, t, analyst)

	// And the reference it handed back opens it.
	fetched := decode[fetchEnvelope](t, call(ctx, t, agent, "fetch", map[string]any{
		"reference": found[0].Reference, "session_id": handle,
	}))
	require.True(t, fetched.Found, "the session the search just returned must be fetchable")
	require.NotNil(t, fetched.Document)
	assert.Equal(t, knowledge.SourceSessions, fetched.Document.Source)
	assert.Equal(t, revenuePurpose, fetched.Document.Title)

	var recall knowledge.SessionRecall
	require.NoError(t, json.Unmarshal(fetched.Document.Content, &recall))
	assert.Equal(t, handle, recall.SessionID)
	assert.Equal(t, string(sessionview.KindAgent), recall.Kind)
	assert.Equal(t, "analyst", recall.Persona)
	assert.Equal(t, 3, recall.CallCount, "the two queries and the search that found them")
	assert.Equal(t, 0, recall.FailureCount)

	require.Len(t, recall.Timeline, 3, "the full timeline, oldest first")
	assert.Equal(t, revenuePurpose, recall.Timeline[0].Purpose)
	assert.Equal(t, priorYearPurpose, recall.Timeline[1].Purpose)
	for i, c := range recall.Timeline[:2] {
		assert.NotEmpty(t, c.Reference, "call %d was cataloged, so it must carry the reference that fetches it", i)
		assert.Equal(t, callrecord.KindSQL, c.Kind)
		assert.Equal(t, callrecord.OutcomeRan, c.Outcome,
			"nothing has cited these calls, which is an outcome and not an absence")
	}

	// The search is on the timeline too — it is part of what the session did —
	// but the catalog records data access, so it carries nothing to fetch.
	discovery := recall.Timeline[2]
	assert.Equal(t, "search", discovery.ToolName)
	assert.Empty(t, discovery.Reference, "a call with no record must not offer a reference that resolves to nothing")
	assert.Empty(t, discovery.Outcome)

	// What the session left behind comes back as references, which is the
	// answer to the session id an asset used to carry and nobody could follow.
	require.Len(t, recall.Assets, 1)
	assert.Equal(t, "Q3 revenue by region", recall.Assets[0].Name)
	assert.Equal(t, "mcp:asset:"+recall.Assets[0].ID, recall.Assets[0].Reference)
	require.Len(t, recall.Insights, 1)
	assert.Equal(t, "mcp:insight:"+recall.Insights[0].ID, recall.Insights[0].Reference)
	assert.Len(t, fetched.Document.References, 2, "the asset and the insight are the session's outbound links")

	// The call reference the timeline carries is a real one: it resolves to the
	// record of that call.
	rec, err := analyst.layer.Calls().GetByEventID(ctx, recall.Timeline[0].EventID, analystID)
	require.NoError(t, err, "the timeline must not offer a reference that resolves to nothing")
	assert.Equal(t, revenueSQL, rec.Statement)
}

// A session belongs to the caller who ran it. Another caller searching the same
// words finds nothing, and fetching the reference itself — which is guessable,
// being the session id — is answered exactly as a session that never existed.
func TestSessionRecallRealDBRefusesAnotherCallersSession(t *testing.T) {
	ctx := context.Background()
	db := testdb.New(t)
	sessions := pkgsession.NewMemoryStore(time.Hour)

	analyst := newReplica(t, db, sessions, &middleware.UserInfo{
		UserID: analystID, Email: analystMail, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	agentA := connect(ctx, t, analyst)
	handleA := mintHandle(ctx, t, sessions, analystID)
	runQuery(ctx, t, analyst, agentA, handleA, revenueSQL, revenuePurpose)
	saveAsset(ctx, t, db, handleA, "Q3 revenue by region")

	stranger := newReplica(t, db, sessions, &middleware.UserInfo{
		UserID: strangerID, Email: strangerML, Roles: []string{"analyst"}, AuthType: middleware.AuthTypeAPIKey,
	})
	agentB := connect(ctx, t, stranger)
	handleB := mintHandle(ctx, t, sessions, strangerID)
	runQuery(ctx, t, stranger, agentB, handleB, strangerSQL, strangerPurpose)

	// B searching A's words finds B's sessions, which is none of them.
	assert.Empty(t, sessionHits(t, call(ctx, t, agentB, "search", map[string]any{
		"intent": "revenue for the board deck", "session_id": handleB,
	})), "another caller's session is not theirs to find")

	// And naming A's session outright answers as an id that never ran.
	refused := decode[fetchEnvelope](t, call(ctx, t, agentB, "fetch", map[string]any{
		"reference": "mcp:session:" + handleA, "session_id": handleB,
	}))
	assert.False(t, refused.Found, "a session id is guessable; the read must be scoped, not the reference")
	assert.Nil(t, refused.Document)

	never := decode[fetchEnvelope](t, call(ctx, t, agentB, "fetch", map[string]any{
		"reference": "mcp:session:dps_neverran", "session_id": handleB,
	}))
	assert.Equal(t, refused, fetchEnvelope{
		Found: never.Found, Reference: refused.Reference, Document: never.Document,
	}, "the two must be indistinguishable apart from the id echoed back")

	// B's own session is still B's to recall, so the refusal above is the scope
	// and not recall being broken.
	mine := sessionHits(t, call(ctx, t, agentB, "search", map[string]any{
		"intent": "warehouse inventory restock", "session_id": handleB,
	}))
	require.Len(t, mine, 1)
	assert.Equal(t, "mcp:session:"+handleB, mine[0].Reference)
}

// saveAsset writes an asset the session produced, through the store the portal
// toolkit writes through.
func saveAsset(ctx context.Context, t *testing.T, db *sql.DB, handle, name string) {
	t.Helper()
	require.NoError(t, portalstore.NewPostgresAssetStore(db).Insert(ctx, portaldomain.Asset{
		ID:          "ast_" + name[:2] + handle[4:12],
		OwnerID:     analystID,
		OwnerEmail:  analystMail,
		Name:        name,
		ContentType: "text/csv",
		S3Bucket:    "portal-assets",
		S3Key:       "assets/" + handle + "/content.csv",
		SessionID:   handle,
	}))
}

// captureInsight writes an insight the session captured, through the adapter
// memory_capture writes through.
func captureInsight(ctx context.Context, t *testing.T, db *sql.DB, handle, text string) {
	t.Helper()
	insights := knowledgekit.NewMemoryInsightAdapter(memory.NewPostgresStore(db))
	require.NoError(t, insights.Insert(ctx, knowledgekit.Insight{
		ID:          "ins_" + handle[4:12],
		SessionID:   handle,
		CapturedBy:  analystMail,
		Persona:     "analyst",
		Source:      "user",
		Category:    "correction",
		InsightText: text,
		Confidence:  "high",
		Status:      "pending",
	}))
}
