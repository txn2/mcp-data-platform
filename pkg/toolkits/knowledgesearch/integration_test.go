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
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// These tests assemble the real read path end to end: the knowledge_search tool
// handler -> knowledge.Router -> the real memory/insights/assets/datahub/prompts
// provider adapters -> stores. The stores are fakes, but the per-user ones
// enforce the same per-owner scoping the real Postgres stores do (CreatedBy /
// CapturedBy / OwnerID), so the test proves the tool resolves the caller from
// the platform context and the router carries that identity through each adapter.
// The datahub and prompts fakes are shared (global content), so they also prove
// shared sinks reach every caller without leaking per-user records.

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

func (s *scopedMemoryStore) EntityLookup(_ context.Context, _, _, createdBy string) ([]memory.Record, error) {
	var out []memory.Record
	for _, r := range s.records {
		if r.CreatedBy == createdBy {
			out = append(out, r)
		}
	}
	return out, nil
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

// globalCatalog is a shared datahub fake: it returns the same catalog hit for
// every caller, modeling global (non-per-user) knowledge.
type globalCatalog struct{}

func (globalCatalog) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return []semantic.TableSearchResult{{URN: "g-catalog", Name: "global table"}}, nil
}

// globalPrompts is a shared prompts fake returning one global prompt for every
// caller.
type globalPrompts struct{}

func (globalPrompts) Search(_ context.Context, _ prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	return []prompt.ScoredPrompt{{Prompt: prompt.Prompt{Name: "g-prompt", DisplayName: "global prompt"}, Score: 0.5}}, nil
}

const (
	userAEmail = "alice@example.com"
	userAID    = "uuid-alice"
	userBEmail = "bob@example.com"
	userBID    = "uuid-bob"
)

// assembledToolkit builds the toolkit over the real router and all five provider
// adapters, with per-user data owned by user A and shared catalog/prompt data.
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
		knowledge.NewMemoryProvider(mem, nil),
		knowledge.NewInsightsProvider(ins),
		knowledge.NewDatahubProvider(globalCatalog{}),
		knowledge.NewPromptsProvider(globalPrompts{}),
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

	got := map[string]bool{}
	for _, h := range out.Hits {
		if h.Source == "" || h.Ref == "" || h.Text == "" {
			t.Errorf("hit missing source/ref/text: %+v", h)
		}
		got[h.Source] = true
	}
	for _, src := range []string{
		knowledge.SourceMemory, knowledge.SourceInsights, knowledge.SourceAssets,
		knowledge.SourceDatahub, knowledge.SourcePrompts,
	} {
		if !got[src] {
			t.Errorf("missing hit from source %q (sources seen: %v)", src, got)
		}
	}
	if out.Ranking != "lexical" {
		t.Errorf("ranking = %q, want lexical (no embedder)", out.Ranking)
	}
}

// AC2: user B's search never surfaces user A's per-user records, even though
// shared providers (catalog, prompts) return global content to both.
func TestAC2_PerUserIsolationBetweenIdentities(t *testing.T) {
	tk := assembledToolkit()

	aOut := callSearch(ctxFor(userAID, userAEmail), t, tk, "anything")
	aRefs := refSet(aOut)
	for _, ref := range []string{"m-alice", "i-alice", "a-alice"} {
		if !aRefs[ref] {
			t.Fatalf("user A should see own record %q; got refs %v", ref, aRefs)
		}
	}

	bOut := callSearch(ctxFor(userBID, userBEmail), t, tk, "anything")
	for _, h := range bOut.Hits {
		if h.Ref == "m-alice" || h.Ref == "i-alice" || h.Ref == "a-alice" {
			t.Errorf("LEAK: user B received user A's record %q from %q", h.Ref, h.Source)
		}
	}
	// User B still sees the shared catalog/prompt content.
	bRefs := refSet(bOut)
	if !bRefs["g-catalog"] || !bRefs["g-prompt"] {
		t.Errorf("user B should see shared content; got refs %v", bRefs)
	}
}

// TestEntityPathScopedToCaller proves the entity-keyed path also honors per-user
// scope: user B gets nothing from user A's entity-linked memory.
func TestEntityPathScopedToCaller(t *testing.T) {
	mem := &scopedMemoryStore{records: []memory.Record{
		{ID: "e-alice", CreatedBy: userAEmail, Content: "alice orders note", Dimension: memory.DimensionEntity, EntityURNs: []string{"urn:li:dataset:orders"}},
	}}
	router := knowledge.NewRouter(nil, knowledge.NewMemoryProvider(mem, nil))
	tk := New("default", router)

	// Entity-only query (no intent) by user B must not return alice's record.
	res, _, err := tk.handleSearch(ctxFor(userBID, userBEmail), &mcp.CallToolRequest{}, searchInput{EntityURNs: []string{"urn:li:dataset:orders"}})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	var out searchOutput
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Count != 0 {
		t.Fatalf("entity lookup leaked across users: %+v", out.Hits)
	}
}

func refSet(out searchOutput) map[string]bool {
	m := make(map[string]bool, len(out.Hits))
	for _, h := range out.Hits {
		m[h.Ref] = true
	}
	return m
}

// An anonymous caller still gets shared content but no per-user results.
func TestAnonymousCallerSeesOnlyShared(t *testing.T) {
	tk := assembledToolkit()
	out := callSearch(context.Background(), t, tk, "anything")
	for _, h := range out.Hits {
		if h.Source == knowledge.SourceMemory || h.Source == knowledge.SourceInsights || h.Source == knowledge.SourceAssets {
			t.Errorf("anonymous caller got per-user hit from %q: %+v", h.Source, h)
		}
	}
}

func TestHandleSearch_RequiresIntentOrEntities(t *testing.T) {
	tk := assembledToolkit()
	res, _, err := tk.handleSearch(ctxFor(userAID, userAEmail), &mcp.CallToolRequest{}, searchInput{Intent: "   "})
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an error result when neither intent nor entity_urns is given")
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
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tk.RegisterTools(srv)
}
