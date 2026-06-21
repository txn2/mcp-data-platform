package knowledgesearch

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// These tests assemble the real read path end to end: the knowledge_search tool
// handler -> knowledge.Router -> the real memory/insights/assets provider
// adapters -> stores. The stores are fakes, but they enforce the same per-owner
// scoping the real Postgres stores do (filtering on CreatedBy / CapturedBy /
// OwnerID), so the test proves the tool resolves the caller from the platform
// context and the router actually carries that identity through each adapter to
// the store. This is the wiring CLAUDE.md requires an integration test to
// cover, not just per-function unit tests.

// scopedMemoryStore returns only records whose CreatedBy matches the query.
type scopedMemoryStore struct {
	records []memory.Record
}

func (s *scopedMemoryStore) HybridSearch(_ context.Context, q memory.HybridQuery) ([]memory.ScoredRecord, error) {
	return s.scoped(q.CreatedBy), nil
}

func (s *scopedMemoryStore) LexicalSearch(_ context.Context, q memory.LexicalQuery) ([]memory.ScoredRecord, error) {
	return s.scoped(q.CreatedBy), nil
}

func (s *scopedMemoryStore) scoped(owner string) []memory.ScoredRecord {
	var out []memory.ScoredRecord
	for _, r := range s.records {
		if r.CreatedBy == owner {
			out = append(out, memory.ScoredRecord{Record: r, Score: 0.5})
		}
	}
	return out
}

// scopedInsightStore returns only insights whose CapturedBy matches the query.
type scopedInsightStore struct {
	insights []knowledgekit.Insight
}

func (s *scopedInsightStore) Search(_ context.Context, q knowledgekit.InsightSearchQuery) ([]knowledgekit.ScoredInsight, error) {
	var out []knowledgekit.ScoredInsight
	for _, in := range s.insights {
		if in.CapturedBy == q.CapturedBy {
			out = append(out, knowledgekit.ScoredInsight{Insight: in, Score: 0.5})
		}
	}
	return out, nil
}

// scopedAssetStore returns only assets whose OwnerID matches the query.
type scopedAssetStore struct {
	assets []portal.Asset
}

func (s *scopedAssetStore) SearchAssets(_ context.Context, q portal.AssetSearchQuery) ([]portal.ScoredAsset, error) {
	var out []portal.ScoredAsset
	for _, a := range s.assets {
		if a.OwnerID == q.OwnerID {
			out = append(out, portal.ScoredAsset{Asset: a, Score: 0.5})
		}
	}
	return out, nil
}

const (
	userAEmail = "alice@example.com"
	userAID    = "uuid-alice"
	userBEmail = "bob@example.com"
	userBID    = "uuid-bob"
)

// assembledToolkit builds the toolkit over the real router and adapters with
// data owned by user A in every store.
func assembledToolkit() *Toolkit {
	mem := &scopedMemoryStore{records: []memory.Record{
		{ID: "m-alice", CreatedBy: userAEmail, Content: "alice memory", Dimension: memory.DimensionPreference},
	}}
	ins := &scopedInsightStore{insights: []knowledgekit.Insight{
		{ID: "i-alice", CapturedBy: userAEmail, InsightText: "alice insight"},
	}}
	assets := &scopedAssetStore{assets: []portal.Asset{
		{ID: "a-alice", OwnerID: userAID, Name: "alice asset"},
	}}

	router := knowledge.NewRouter(nil,
		knowledge.NewMemoryProvider(mem),
		knowledge.NewInsightsProvider(ins),
		knowledge.NewAssetsProvider(assets),
	)
	return New("default", router)
}

func callSearch(ctx context.Context, t *testing.T, tk *Toolkit, intent string) searchOutput {
	t.Helper()
	res, _, err := tk.handleSearch(ctx, &mcp.CallToolRequest{}, searchInput{Intent: intent})
	if err != nil {
		t.Fatalf("handleSearch returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool reported error: %v", res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	var out searchOutput
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("decoding output: %v", err)
	}
	return out
}

func ctxFor(userID, email string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID:    userID,
		UserEmail: email,
	})
}

// AC1: a single knowledge_search returns fused results from every provider,
// each tagged with its source.
func TestAC1_FusedAndSourceTagged(t *testing.T) {
	tk := assembledToolkit()
	out := callSearch(ctxFor(userAID, userAEmail), t, tk, "alice")

	if out.Count != 3 {
		t.Fatalf("expected 3 hits across providers, got %d: %+v", out.Count, out.Hits)
	}
	got := map[string]bool{}
	for _, h := range out.Hits {
		if h.Source == "" {
			t.Errorf("hit missing source: %+v", h)
		}
		if h.Ref == "" || h.Text == "" {
			t.Errorf("hit missing ref/text: %+v", h)
		}
		got[h.Source] = true
	}
	for _, src := range []string{knowledge.SourceMemory, knowledge.SourceInsights, knowledge.SourceAssets} {
		if !got[src] {
			t.Errorf("missing hit from source %q (sources seen: %v)", src, got)
		}
	}
	if out.Ranking != "lexical" {
		t.Errorf("ranking = %q, want lexical (no embedder)", out.Ranking)
	}
}

// AC2: user B's search never surfaces user A's per-user records. Proven with two
// distinct identities against the same assembled system.
func TestAC2_PerUserIsolationBetweenIdentities(t *testing.T) {
	tk := assembledToolkit()

	// User A owns data in every store and sees all three hits.
	aOut := callSearch(ctxFor(userAID, userAEmail), t, tk, "anything")
	if aOut.Count != 3 {
		t.Fatalf("user A should see 3 own hits, got %d", aOut.Count)
	}

	// User B owns nothing; none of A's records may appear.
	bOut := callSearch(ctxFor(userBID, userBEmail), t, tk, "anything")
	if bOut.Count != 0 {
		t.Fatalf("user B must see no hits (would be a cross-user leak), got %d: %+v", bOut.Count, bOut.Hits)
	}
	for _, h := range bOut.Hits {
		if h.Ref == "m-alice" || h.Ref == "i-alice" || h.Ref == "a-alice" {
			t.Errorf("LEAK: user B received user A's record %q from %q", h.Ref, h.Source)
		}
	}
}

// An anonymous caller (no platform context identity) gets no per-user results
// rather than an error or a cross-user search.
func TestAnonymousCallerGetsNothing(t *testing.T) {
	tk := assembledToolkit()
	out := callSearch(context.Background(), t, tk, "anything")
	if out.Count != 0 {
		t.Fatalf("anonymous caller should see no per-user hits, got %d", out.Count)
	}
}

func TestHandleSearch_EmptyIntentErrors(t *testing.T) {
	tk := assembledToolkit()
	res, _, err := tk.handleSearch(ctxFor(userAID, userAEmail), &mcp.CallToolRequest{}, searchInput{Intent: "   "})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result for empty intent")
	}
}

func TestToolkit_RegistersTool(t *testing.T) {
	tk := assembledToolkit()
	if tk.Kind() != "knowledge_search" {
		t.Errorf("Kind = %q", tk.Kind())
	}
	if tools := tk.Tools(); len(tools) != 1 || tools[0] != toolName {
		t.Errorf("Tools = %v", tools)
	}
	// Registration should not panic against a real server.
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tk.RegisterTools(srv)
}
