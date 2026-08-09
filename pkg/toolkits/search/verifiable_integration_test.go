package search

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/tableavail"
	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// These tests exercise the delivery of a checkable insight (#1220) through the
// real assembled server: the search toolkit's tools registered on an mcp.Server
// behind the real tool-call middleware, driven by a client over an in-memory
// transport, over the real knowledge.Router and InsightsProvider. Only the
// insight store and the query provider are fakes.
//
// What must hold: an insight whose entity resolves to an available table arrives
// naming that table on both delivery surfaces (the search hit and the fetched
// record), and an insight whose entity does not resolve — or any insight at all
// when no verifier is wired — arrives exactly as it did before.

const (
	verifyURN      = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.orders,PROD)"
	verifyGoneURN  = "urn:li:dataset:(urn:li:dataPlatform:trino,iceberg.retail.gone,PROD)"
	verifyTable    = "iceberg.retail.orders"
	verifyConn     = "prod-trino"
	verifyInsight  = "i-checkable"
	verifyUnlinked = "i-unresolvable"
)

// verifyQueryProvider reports one table as available and nothing else, so a test
// can tell a resolved entity from an unresolved one.
type verifyQueryProvider struct {
	query.NoopProvider
}

func (*verifyQueryProvider) GetTableAvailability(_ context.Context, urn string) (*query.TableAvailability, error) {
	if urn != verifyURN {
		return &query.TableAvailability{Available: false, Error: "not found"}, nil
	}
	return &query.TableAvailability{
		Available:  true,
		QueryTable: verifyTable,
		Connection: verifyConn,
	}, nil
}

// verifiableInsightToolkit assembles the search toolkit over the real router and
// the real insights provider, holding two applied insights: one about an entity
// the query provider can see and one about an entity it cannot. verifier nil
// models a deployment with no query provider (or the operator opt-out).
func verifiableInsightToolkit(verifier knowledge.EntityVerifier) *Toolkit {
	ins := &scopedInsightStore{insights: []knowledgekit.Insight{
		{
			ID:          verifyInsight,
			CapturedBy:  userAEmail,
			InsightText: "The orders table holds 1140 rows.",
			Status:      knowledgekit.StatusApplied,
			EntityURNs:  []string{verifyURN},
		},
		{
			ID:          verifyUnlinked,
			CapturedBy:  userAEmail,
			InsightText: "The retired orders extract holds 1140 rows.",
			Status:      knowledgekit.StatusApplied,
			EntityURNs:  []string{verifyGoneURN},
		},
	}}
	insights := knowledge.NewInsightsProvider(ins)
	insights.SetVerifier(verifier)
	return New("default", knowledge.NewRouter(nil, nil, insights))
}

// verifySession registers the toolkit's tools on a real server behind the real
// tool-call middleware (which is what puts the caller identity the per-user
// insights provider scopes on into the handler's context) and connects a client
// over an in-memory transport.
func verifySession(ctx context.Context, t *testing.T, tk *Toolkit) *mcp.ClientSession {
	t.Helper()

	server := mcp.NewServer(&mcp.Implementation{Name: "test-platform", Version: "v0.0.1"}, nil)
	tk.RegisterTools(server)
	server.AddReceivingMiddleware(middleware.MCPToolCallMiddleware(
		verifyAuthenticator{}, verifyAuthorizer{}, verifyToolkitLookup{},
		middleware.ToolCallConfig{Transport: "stdio", AdminPersona: "admin"},
	))

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("connecting server: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// verifyAuthenticator resolves every request to user A, the capturer the fake
// insight store scopes on.
type verifyAuthenticator struct{}

func (verifyAuthenticator) Authenticate(context.Context) (*middleware.UserInfo, error) {
	return &middleware.UserInfo{UserID: userAID, Email: userAEmail, Roles: []string{"analyst"}}, nil
}

type verifyAuthorizer struct{}

func (verifyAuthorizer) IsAuthorized(
	context.Context, string, []string, string, string,
) (authorized bool, persona, reason string) {
	return true, "analyst", ""
}

type verifyToolkitLookup struct{}

func (verifyToolkitLookup) GetToolkitForTool(string) registry.ToolkitMatch {
	return registry.ToolkitMatch{Found: true, Kind: "search", Name: "default"}
}

// toolCall is one tool invocation over a session: which tool, with what
// arguments, decoded into what.
type toolCall struct {
	session *mcp.ClientSession
	name    string
	args    map[string]any
	out     any
}

// callToolJSON invokes a tool over the session and decodes the first text
// content block into call.out.
func callToolJSON(ctx context.Context, t *testing.T, call toolCall) {
	t.Helper()

	name, out := call.name, call.out
	res, err := call.session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: call.args})
	if err != nil {
		t.Fatalf("calling %s: %v", name, err)
	}
	if res.IsError {
		t.Fatalf("%s returned an error result: %v", name, res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	if err := json.Unmarshal([]byte(tc.Text), out); err != nil {
		t.Fatalf("decoding %s output: %v", name, err)
	}
}

// insightHitsByRef indexes the insight hits of a search result by insight id.
func insightHitsByRef(out searchOutput) map[string]knowledge.Hit {
	byRef := make(map[string]knowledge.Hit)
	for _, g := range out.Groups {
		if g.Source != knowledge.SourceInsights {
			continue
		}
		for _, h := range g.Hits {
			byRef[h.Ref] = h
		}
	}
	return byRef
}

// A delivered insight the platform can query for itself arrives naming the table
// one query would settle it against — on the search hit and on the full record
// fetch returns — while an insight about an entity that does not resolve arrives
// unchanged.
func TestVerifiableInsight_SearchAndFetchNameTheQueryableTable(t *testing.T) {
	ctx := context.Background()
	session := verifySession(ctx, t, verifiableInsightToolkit(tableavail.New(&verifyQueryProvider{})))

	var out searchOutput
	callToolJSON(ctx, t, toolCall{
		session: session,
		name:    toolName,
		args:    map[string]any{"intent": "orders rows"},
		out:     &out,
	})

	hits := insightHitsByRef(out)
	checkable, ok := hits[verifyInsight]
	if !ok {
		t.Fatalf("the checkable insight was not delivered; groups = %+v", out.Groups)
	}
	if checkable.Verifiable == nil {
		t.Fatal("a search hit whose entity resolves must name the table that settles its claim")
	}
	if checkable.Verifiable.QueryTable != verifyTable || checkable.Verifiable.Connection != verifyConn {
		t.Errorf("hit verifiable = %+v, want table %q on connection %q",
			checkable.Verifiable, verifyTable, verifyConn)
	}
	if checkable.Verifiable.URN != verifyURN {
		t.Errorf("hit verifiable URN = %q, want %q", checkable.Verifiable.URN, verifyURN)
	}

	unresolvable, ok := hits[verifyUnlinked]
	if !ok {
		t.Fatalf("the unresolvable insight was not delivered; groups = %+v", out.Groups)
	}
	if unresolvable.Verifiable != nil {
		t.Errorf("an insight whose entity does not resolve must be unchanged, got %+v", unresolvable.Verifiable)
	}

	var fetched fetchOutput
	callToolJSON(ctx, t, toolCall{
		session: session,
		name:    fetchToolName,
		args:    map[string]any{"reference": "mcp:insight:" + verifyInsight},
		out:     &fetched,
	})

	if !fetched.Found || fetched.Document == nil {
		t.Fatalf("fetch did not resolve the insight: %+v", fetched)
	}
	if fetched.Document.Verifiable == nil {
		t.Fatal("the fetched record must carry the same marker its search hit did")
	}
	if fetched.Document.Verifiable.QueryTable != verifyTable {
		t.Errorf("document verifiable = %+v, want table %q", fetched.Document.Verifiable, verifyTable)
	}
}

// With no verifier wired — no query provider configured, or the operator opted
// out — every delivery surface serves the payload it always did.
func TestVerifiableInsight_NoVerifierLeavesPayloadsUnchanged(t *testing.T) {
	ctx := context.Background()
	session := verifySession(ctx, t, verifiableInsightToolkit(nil))

	var out searchOutput
	callToolJSON(ctx, t, toolCall{
		session: session,
		name:    toolName,
		args:    map[string]any{"intent": "orders rows"},
		out:     &out,
	})
	for ref, h := range insightHitsByRef(out) {
		if h.Verifiable != nil {
			t.Errorf("hit %s carries a verification marker with no verifier wired: %+v", ref, h.Verifiable)
		}
	}

	var fetched fetchOutput
	callToolJSON(ctx, t, toolCall{
		session: session,
		name:    fetchToolName,
		args:    map[string]any{"reference": "mcp:insight:" + verifyInsight},
		out:     &fetched,
	})
	if !fetched.Found || fetched.Document == nil {
		t.Fatalf("fetch did not resolve the insight: %+v", fetched)
	}
	if fetched.Document.Verifiable != nil {
		t.Errorf("fetched record carries a marker with no verifier wired: %+v", fetched.Document.Verifiable)
	}
}

// A noop query provider resolves nothing, so it is indistinguishable from no
// verifier at all on the wire: the marker is present where it can be checked and
// absent everywhere else, never an empty block.
func TestVerifiableInsight_NoopProviderResolvesNothing(t *testing.T) {
	ctx := context.Background()
	session := verifySession(ctx, t, verifiableInsightToolkit(tableavail.New(query.NewNoopProvider())))

	var out searchOutput
	callToolJSON(ctx, t, toolCall{
		session: session,
		name:    toolName,
		args:    map[string]any{"intent": "orders rows"},
		out:     &out,
	})

	hits := insightHitsByRef(out)
	if len(hits) == 0 {
		t.Fatal("the insights were not delivered at all")
	}
	for ref, h := range hits {
		if h.Verifiable != nil {
			t.Errorf("hit %s carries a marker a noop provider cannot support: %+v", ref, h.Verifiable)
		}
	}
}
