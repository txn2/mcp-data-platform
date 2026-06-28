package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/memory"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	knowledgekit "github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

// These tests assemble the real read path end to end: the search tool
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

func (s *scopedMemoryStore) EntityLookup(_ context.Context, urn, _, createdBy string) ([]memory.Record, error) {
	var out []memory.Record
	for _, r := range s.records {
		// slices.Contains mirrors the entity_urns JSONB containment the real
		// stores filter on.
		if r.CreatedBy == createdBy && slices.Contains(r.EntityURNs, urn) {
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

// Get reads a record by id regardless of owner (the MemoryProvider applies the
// ownership scope), modeling the real store's by-id read; a missing id is the
// store's memory.ErrRecordNotFound sentinel (the real store does NOT surface
// sql.ErrNoRows).
func (s *scopedMemoryStore) Get(_ context.Context, id string) (*memory.Record, error) {
	for i := range s.records {
		if s.records[i].ID == id {
			r := s.records[i]
			return &r, nil
		}
	}
	return nil, fmt.Errorf("memory record not found: %s: %w", id, memory.ErrRecordNotFound)
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

// List is the entity-keyed path: insights owned by the caller and linked to the
// requested URN, mirroring the real adapter's owner + entity_urns filter.
func (s *scopedInsightStore) List(_ context.Context, f knowledgekit.InsightFilter) ([]knowledgekit.Insight, int, error) {
	var out []knowledgekit.Insight
	for _, in := range s.insights {
		if in.CapturedBy != f.CapturedBy {
			continue
		}
		if f.EntityURN != "" && !slices.Contains(in.EntityURNs, f.EntityURN) {
			continue
		}
		out = append(out, in)
	}
	return out, len(out), nil
}

// Get reads an insight by id regardless of owner (the InsightsProvider applies the
// ownership scope), modeling the memory-backed adapter; a missing id surfaces the
// wrapped memory.ErrRecordNotFound the adapter passes through.
func (s *scopedInsightStore) Get(_ context.Context, id string) (*knowledgekit.Insight, error) {
	for i := range s.insights {
		if s.insights[i].ID == id {
			in := s.insights[i]
			return &in, nil
		}
	}
	return nil, fmt.Errorf("getting insight record: %w", memory.ErrRecordNotFound)
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

// Get reads an asset by id regardless of owner (the AssetsProvider applies the
// ownership scope). It reports a missing id as a wrapped sql.ErrNoRows, exactly as
// the real postgres store does, so the provider's not-found mapping is exercised.
func (s *scopedAssetStore) Get(_ context.Context, id string) (*portal.Asset, error) {
	for i := range s.assets {
		if s.assets[i].ID == id {
			a := s.assets[i]
			return &a, nil
		}
	}
	return nil, fmt.Errorf("querying asset: %w", sql.ErrNoRows)
}

// globalCatalog is a shared datahub fake: it returns the same catalog hit for
// every caller, modeling global (non-per-user) knowledge.
type globalCatalog struct{}

func (globalCatalog) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return []semantic.TableSearchResult{{URN: "g-catalog", Name: "global table"}}, nil
}

// GetTableContext is the entity-keyed path: it resolves only the known demo
// dataset, returning an empty (URN-less) context for any other table so a
// non-existent URN yields no catalog hit.
func (globalCatalog) GetTableContext(_ context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	if table.Table != "os_acme_transactions" {
		return &semantic.TableContext{}, nil
	}
	return &semantic.TableContext{
		URN:         "urn:li:dataset:(urn:li:dataPlatform:trino," + table.String() + ",PROD)",
		Description: "acme transactions catalog entry",
	}, nil
}

// globalPrompts is a shared prompts fake returning one global prompt for every
// caller.
type globalPrompts struct{}

func (globalPrompts) Search(_ context.Context, _ prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	return []prompt.ScoredPrompt{{Prompt: prompt.Prompt{ID: globalPromptID, Name: "g-prompt", DisplayName: "global prompt"}, Score: 0.5}}, nil
}

// globalPromptID is the UUID the global prompt is referenced by (prompt references
// carry the prompt's UUID id).
const globalPromptID = "11111111-1111-1111-1111-111111111111"

// GetByID returns the global prompt for its id, the read half of search.
func (globalPrompts) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if id != globalPromptID {
		return nil, nil //nolint:nilnil // mirrors prompt.Store.GetByID: (nil,nil) means not-found
	}
	return &prompt.Prompt{ID: globalPromptID, Name: "g-prompt", DisplayName: "global prompt", Content: "global prompt body", Scope: prompt.ScopeGlobal, Status: prompt.StatusApproved, Enabled: true}, nil
}

// globalKnowledgePages is a shared knowledge-page fake returning one canonical
// page for every caller, modeling org-shared (non-per-user) knowledge.
type globalKnowledgePages struct{}

func (globalKnowledgePages) Search(_ context.Context, _ knowledgepage.SearchQuery) ([]knowledgepage.ScoredPage, error) {
	return []knowledgepage.ScoredPage{{Page: knowledgepage.Page{ID: "g-page", Title: "global page"}, Score: 0.5}}, nil
}

func (globalKnowledgePages) ListPagesReferencing(_ context.Context, _ knowledgepage.EntityRef) ([]knowledgepage.PageRef, error) {
	return nil, nil
}

// Get returns the canonical page's full body for its id, the read half of search.
func (globalKnowledgePages) Get(_ context.Context, id string) (*knowledgepage.Page, error) {
	if id != "g-page" {
		return nil, knowledgepage.ErrNotFound
	}
	return &knowledgepage.Page{ID: "g-page", Title: "global page", Body: "the full canonical page body"}, nil
}

// List enumerates the canonical pages for browse, honoring offset/limit over a
// fixed two-page corpus so a browse round-trip can assert pagination and total.
func (globalKnowledgePages) List(_ context.Context, filter knowledgepage.Filter) ([]knowledgepage.Page, int, error) {
	all := []knowledgepage.Page{
		{ID: "g-page", Title: "global page", Body: "the full canonical page body"},
		{ID: "g-page-2", Title: "second page", Body: "another body"},
	}
	start := min(filter.Offset, len(all))
	end := start + filter.Limit
	if filter.Limit <= 0 || end > len(all) {
		end = len(all)
	}
	return all[start:end], len(all), nil
}

func (globalKnowledgePages) ListEntityRefs(_ context.Context, _ string) ([]knowledgepage.EntityRef, error) {
	return nil, nil
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
		{ID: "m-alice", CreatedBy: userAEmail, Content: "alice memory", Dimension: memory.DimensionPreference, Status: memory.StatusActive},
	}}
	ins := &scopedInsightStore{insights: []knowledgekit.Insight{
		{ID: "i-alice", CapturedBy: userAEmail, InsightText: "alice insight", Status: knowledgekit.StatusApproved},
	}}
	assets := &scopedAssetStore{assets: []portal.Asset{
		{ID: "a-alice", OwnerID: userAID, Name: "alice asset"},
	}}

	router := knowledge.NewRouter(nil, nil,
		knowledge.NewMemoryProvider(mem),
		knowledge.NewInsightsProvider(ins),
		knowledge.NewCatalogProvider(globalCatalog{}),
		knowledge.NewPromptsProvider(globalPrompts{}),
		knowledge.NewAssetsProvider(assets),
		knowledge.NewKnowledgePagesProvider(globalKnowledgePages{}),
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

func callBrowse(ctx context.Context, t *testing.T, tk *Toolkit, input searchInput) (*mcp.CallToolResult, browseOutput) {
	t.Helper()
	res, _, err := tk.handleSearch(ctx, &mcp.CallToolRequest{}, input)
	if err != nil {
		t.Fatalf("handleSearch returned transport error: %v", err)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	var out browseOutput
	if !res.IsError {
		if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
			t.Fatalf("decoding browse output: %v", err)
		}
	}
	return res, out
}

// AC1+AC2: an agent can page the complete set of a source with a total count, and
// no relevance threshold drops members (browse returns the whole corpus).
func TestBrowse_EnumeratesKnowledgePagesWithPagination(t *testing.T) {
	tk := assembledToolkit()
	ctx := ctxFor(userAID, userAEmail)

	// Page 1: offset 0, limit 1, over a two-page corpus.
	_, p1 := callBrowse(ctx, t, tk, searchInput{Sources: []string{"knowledge_pages"}, Offset: 0, Limit: 1})
	if p1.Source != "knowledge_pages" || p1.Total != 2 {
		t.Fatalf("page1 meta = %+v, want source knowledge_pages total 2", p1)
	}
	if p1.Count != 1 || len(p1.Items) != 1 || p1.Items[0].Reference != "mcp:knowledge_page:g-page" {
		t.Fatalf("page1 items = %+v", p1.Items)
	}

	// Page 2: offset 1 returns the rest; together the two pages enumerate the corpus.
	_, p2 := callBrowse(ctx, t, tk, searchInput{Sources: []string{"knowledge_pages"}, Offset: 1, Limit: 1})
	if p2.Offset != 1 || p2.Count != 1 || p2.Items[0].Reference != "mcp:knowledge_page:g-page-2" {
		t.Fatalf("page2 = %+v", p2)
	}
}

// AC3: persona scope is respected. The browsable sources here are global, so any
// caller (including anonymous) may enumerate them, exactly as search exposes them;
// a per-user source is not browsable for an anonymous caller (covered in the router
// test). An unknown or non-browsable source is reported distinctly, not as data.
func TestBrowse_RejectsBadSourceSelection(t *testing.T) {
	tk := assembledToolkit()
	ctx := ctxFor(userAID, userAEmail)

	// Zero sources with no intent: ambiguous, a tool error explaining both modes.
	res, _ := callBrowse(ctx, t, tk, searchInput{Offset: 0})
	if !res.IsError {
		t.Error("a no-intent call with no single source should be a tool error")
	}

	// Two sources: browse enumerates one source at a time.
	res, _ = callBrowse(ctx, t, tk, searchInput{Sources: []string{"knowledge_pages", "context_documents"}})
	if !res.IsError {
		t.Error("browsing two sources at once should be a tool error")
	}

	// A real but non-browsable source.
	res, _ = callBrowse(ctx, t, tk, searchInput{Sources: []string{"memory"}})
	if !res.IsError {
		t.Error("browsing a non-browsable source should be a tool error")
	}

	// An unknown source name.
	res, _ = callBrowse(ctx, t, tk, searchInput{Sources: []string{"no_such_source"}})
	if !res.IsError {
		t.Error("an unknown source should be a tool error")
	}
}

func callFetch(ctx context.Context, t *testing.T, tk *Toolkit, ref string) fetchOutput {
	t.Helper()
	res, _, err := tk.handleFetch(ctx, &mcp.CallToolRequest{}, fetchInput{Reference: ref})
	if err != nil {
		t.Fatalf("handleFetch returned transport error: %v", err)
	}
	if res.IsError {
		t.Fatalf("fetch reported a tool error: %v", res.Content)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	var out fetchOutput
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("decoding fetch output: %v", err)
	}
	return out
}

// referenceFor returns the reference of the first hit from the named source, or
// fails the test: a fetch round-trip starts from a reference search actually
// emitted, not a hand-assembled one.
func referenceFor(t *testing.T, out searchOutput, source string) string {
	t.Helper()
	for _, h := range hitsOf(out) {
		if h.Source == source {
			if h.Reference == "" {
				t.Fatalf("hit from %q carried no reference: %+v", source, h)
			}
			return h.Reference
		}
	}
	t.Fatalf("no hit from source %q in %+v", source, out)
	return ""
}

// AC2: a search result round-trips through fetch. Take the reference search
// returned for a knowledge page and fetch its full body.
func TestFetch_KnowledgePageRoundTrip(t *testing.T) {
	tk := assembledToolkit()
	ctx := ctxFor(userAID, userAEmail)

	out := callSearch(ctx, t, tk, "global page")
	ref := referenceFor(t, out, "knowledge_pages")
	if ref != "mcp:knowledge_page:g-page" {
		t.Fatalf("reference = %q, want mcp:knowledge_page:g-page", ref)
	}

	got := callFetch(ctx, t, tk, ref)
	if !got.Found {
		t.Fatalf("fetch found=false for a live reference: %+v", got)
	}
	if got.Document == nil || got.Document.Body != "the full canonical page body" {
		t.Errorf("fetch did not return the full page body: %+v", got.Document)
	}
	if got.Document.Source != "knowledge_pages" || got.Document.Reference != ref {
		t.Errorf("document provenance wrong: %+v", got.Document)
	}
}

// AC3: a stale or unknown reference is a structured not-found, not a tool error.
func TestFetch_StaleReferenceIsStructuredNotFound(t *testing.T) {
	tk := assembledToolkit()
	ctx := ctxFor(userAID, userAEmail)

	for _, ref := range []string{
		"mcp:knowledge_page:does-not-exist", // owned form, missing record
		"mcp:bogus:whatever",                // unknown form
		"not a reference at all",            // unparseable
	} {
		got := callFetch(ctx, t, tk, ref)
		if got.Found {
			t.Errorf("ref %q: found=true, want a structured not-found", ref)
		}
		if got.Message == "" {
			t.Errorf("ref %q: not-found should carry an explanatory message", ref)
		}
	}
}

// AC4: persona/per-user scope is respected. One user cannot fetch another user's
// asset by reference, even though the reference form is valid.
func TestFetch_PerUserScopeRespected(t *testing.T) {
	tk := assembledToolkit()

	// User A owns a-alice; A can fetch it.
	aRef := referenceFor(t, callSearch(ctxFor(userAID, userAEmail), t, tk, "alice asset"), "assets")
	if got := callFetch(ctxFor(userAID, userAEmail), t, tk, aRef); !got.Found {
		t.Fatalf("owner could not fetch their own asset: %+v", got)
	}

	// User B fetching A's reference gets a structured not-found: fetch never reads
	// content B could not have searched.
	if got := callFetch(ctxFor(userBID, userBEmail), t, tk, aRef); got.Found {
		t.Errorf("user B fetched user A's asset: %+v", got.Document)
	}

	// An anonymous caller likewise cannot fetch a per-user reference.
	if got := callFetch(context.Background(), t, tk, aRef); got.Found {
		t.Errorf("anonymous caller fetched a per-user asset: %+v", got.Document)
	}
}

// hitsOf flattens the grouped display set into one slice, for assertions that
// only care about which hits surfaced rather than their grouping.
func hitsOf(out searchOutput) []knowledge.Hit {
	var hits []knowledge.Hit
	for _, g := range out.Groups {
		hits = append(hits, g.Hits...)
	}
	return hits
}

func ctxFor(userID, email string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID:    userID,
		UserEmail: email,
	})
}

// AC1: a single search returns results from every provider, grouped and each
// tagged with its source.
func TestAC1_FusedAndSourceTagged(t *testing.T) {
	tk := assembledToolkit()
	out := callSearch(ctxFor(userAID, userAEmail), t, tk, "alice")

	got := map[string]bool{}
	for _, h := range hitsOf(out) {
		if h.Source == "" || h.Ref == "" || h.Text == "" {
			t.Errorf("hit missing source/ref/text: %+v", h)
		}
		got[h.Source] = true
	}
	for _, src := range []string{
		knowledge.SourceMemory, knowledge.SourceInsights, knowledge.SourceAssets,
		knowledge.SourceCatalog, knowledge.SourcePrompts, knowledge.SourceKnowledgePages,
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
	for _, h := range hitsOf(bOut) {
		if h.Ref == "m-alice" || h.Ref == "i-alice" || h.Ref == "a-alice" {
			t.Errorf("LEAK: user B received user A's record %q from %q", h.Ref, h.Source)
		}
	}
	// User B still sees the shared catalog/prompt/knowledge-page content.
	bRefs := refSet(bOut)
	if !bRefs["g-catalog"] || !bRefs["g-prompt"] || !bRefs["g-page"] {
		t.Errorf("user B should see shared content; got refs %v", bRefs)
	}
}

// TestEntityPathScopedToCaller proves the entity-keyed path also honors per-user
// scope: user B gets nothing from user A's entity-linked memory.
func TestEntityPathScopedToCaller(t *testing.T) {
	mem := &scopedMemoryStore{records: []memory.Record{
		{ID: "e-alice", CreatedBy: userAEmail, Content: "alice orders note", Dimension: memory.DimensionEntity, EntityURNs: []string{"urn:li:dataset:orders"}},
	}}
	router := knowledge.NewRouter(nil, nil, knowledge.NewMemoryProvider(mem))
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
		t.Fatalf("entity lookup leaked across users: %+v", hitsOf(out))
	}
}

// callEntitySearch runs an entity-only search (no intent) and decodes the output.
func callEntitySearch(ctx context.Context, t *testing.T, tk *Toolkit, urns []string) searchOutput {
	t.Helper()
	res, _, err := tk.handleSearch(ctx, &mcp.CallToolRequest{}, searchInput{EntityURNs: urns})
	if err != nil {
		t.Fatalf("transport error: %v", err)
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
		t.Fatalf("decode: %v", err)
	}
	return out
}

// TestEntityFanoutUnionsAllSources is the issue #654 acceptance test: an
// entity-only search over an exact dataset URN must fan out across every
// entity-keyed source, returning the catalog entity, the URN-linked insights,
// and the URN-linked memory grouped by source, rather than the old near-empty
// result.
func TestEntityFanoutUnionsAllSources(t *testing.T) {
	const urn = "urn:li:dataset:(urn:li:dataPlatform:trino,opensearch.default.os_acme_transactions,PROD)"
	mem := &scopedMemoryStore{records: []memory.Record{
		{ID: "m1", CreatedBy: userAEmail, Content: "alice note", Dimension: memory.DimensionEntity, EntityURNs: []string{urn}},
	}}
	ins := &scopedInsightStore{insights: []knowledgekit.Insight{
		{ID: "i1", CapturedBy: userAEmail, InsightText: "amount is gross margin", Status: knowledgekit.StatusApproved, EntityURNs: []string{urn}},
	}}
	router := knowledge.NewRouter(nil, nil,
		knowledge.NewMemoryProvider(mem),
		knowledge.NewInsightsProvider(ins),
		knowledge.NewCatalogProvider(globalCatalog{}),
	)
	tk := New("default", router)

	out := callEntitySearch(ctxFor(userAID, userAEmail), t, tk, []string{urn})
	if out.Ranking != "entity" {
		t.Errorf("ranking = %q, want entity", out.Ranking)
	}
	bySource := map[string]bool{}
	for _, h := range hitsOf(out) {
		bySource[h.Source] = true
	}
	for _, src := range []string{knowledge.SourceMemory, knowledge.SourceInsights, knowledge.SourceCatalog} {
		if !bySource[src] {
			t.Errorf("entity fanout missing %q; sources seen: %v", src, bySource)
		}
	}
}

// TestEntityFanoutNonexistentURNReturnsZero proves a URN that no source has
// linked content for returns nothing (no false matches), acceptance criterion 4.
func TestEntityFanoutNonexistentURNReturnsZero(t *testing.T) {
	const linked = "urn:li:dataset:(urn:li:dataPlatform:trino,opensearch.default.os_acme_transactions,PROD)"
	const missing = "urn:li:dataset:(urn:li:dataPlatform:trino,opensearch.default.nope,PROD)"
	mem := &scopedMemoryStore{records: []memory.Record{
		{ID: "m1", CreatedBy: userAEmail, Content: "alice note", Dimension: memory.DimensionEntity, EntityURNs: []string{linked}},
	}}
	ins := &scopedInsightStore{insights: []knowledgekit.Insight{
		{ID: "i1", CapturedBy: userAEmail, InsightText: "linked", Status: knowledgekit.StatusApproved, EntityURNs: []string{linked}},
	}}
	router := knowledge.NewRouter(nil, nil,
		knowledge.NewMemoryProvider(mem),
		knowledge.NewInsightsProvider(ins),
		knowledge.NewCatalogProvider(globalCatalog{}),
	)
	tk := New("default", router)

	out := callEntitySearch(ctxFor(userAID, userAEmail), t, tk, []string{missing})
	if out.Count != 0 {
		t.Fatalf("non-existent URN produced false matches: %+v", hitsOf(out))
	}
}

func refSet(out searchOutput) map[string]bool {
	hits := hitsOf(out)
	m := make(map[string]bool, len(hits))
	for _, h := range hits {
		m[h.Ref] = true
	}
	return m
}

// An anonymous caller still gets shared content but no per-user results.
func TestAnonymousCallerSeesOnlyShared(t *testing.T) {
	tk := assembledToolkit()
	out := callSearch(context.Background(), t, tk, "anything")
	for _, h := range hitsOf(out) {
		if h.Source == knowledge.SourceMemory || h.Source == knowledge.SourceInsights || h.Source == knowledge.SourceAssets {
			t.Errorf("anonymous caller got per-user hit from %q: %+v", h.Source, h)
		}
	}
}

// stubConnLister is a knowledge.ConnectionLister for the grouped-output test.
type stubConnLister struct{}

func (stubConnLister) Connections() []knowledge.ConnectionInfo {
	return []knowledge.ConnectionInfo{
		{Name: "warehouse", Kind: "trino", Description: "analytics orders tables"},
		{Name: "stripe", Kind: "api", Description: "payments"},
	}
}

// TestGroupedOutputAndCoverage proves the tool surfaces results grouped by
// source with a coverage summary (the anti-tunnel contract): a query that
// matches both the catalog and a connection produces a group and a coverage
// entry for each, and Count equals the total shown.
func TestGroupedOutputAndCoverage(t *testing.T) {
	router := knowledge.NewRouter(nil, nil,
		knowledge.NewCatalogProvider(globalCatalog{}),
		knowledge.NewConnectionsProvider(stubConnLister{}),
	)
	tk := New("default", router)
	out := callSearch(context.Background(), t, tk, "orders")

	sources := map[string]bool{}
	shown := 0
	for _, g := range out.Groups {
		sources[g.Source] = true
		shown += len(g.Hits)
	}
	if !sources[knowledge.SourceCatalog] || !sources[knowledge.SourceConnections] {
		t.Errorf("expected datahub and connections groups, got %v", sources)
	}
	if out.Count != shown {
		t.Errorf("Count %d != total hits shown %d", out.Count, shown)
	}
	if len(out.Coverage) == 0 {
		t.Fatal("coverage summary must be populated")
	}
	for _, c := range out.Coverage {
		if c.Matched < c.Shown {
			t.Errorf("coverage matched (%d) must be >= shown (%d) for %q", c.Matched, c.Shown, c.Source)
		}
	}
}

// TestSourcesNarrowsThroughTool proves the sources filter reaches the router
// from the tool input.
func TestSourcesNarrowsThroughTool(t *testing.T) {
	router := knowledge.NewRouter(nil, nil,
		knowledge.NewCatalogProvider(globalCatalog{}),
		knowledge.NewConnectionsProvider(stubConnLister{}),
	)
	tk := New("default", router)
	res, _, err := tk.handleSearch(context.Background(), &mcp.CallToolRequest{},
		searchInput{Intent: "orders", Sources: []string{knowledge.SourceConnections}})
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
	for _, g := range out.Groups {
		if g.Source == knowledge.SourceCatalog {
			t.Errorf("sources=[connections] should exclude datahub, got group %q", g.Source)
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
	if tk.Kind() != "search" {
		t.Errorf("Kind = %q", tk.Kind())
	}
	if tools := tk.Tools(); len(tools) != 2 || tools[0] != toolName || tools[1] != fetchToolName {
		t.Errorf("Tools = %v, want [search fetch]", tools)
	}
	srv := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	tk.RegisterTools(srv)
}

// erroringFetcher is a provider that owns a reference but fails its fetch with a
// genuine backend error (not a not-found), to exercise the fetch handler's
// real-error path (distinct from a structured found=false).
type erroringFetcher struct{}

func (erroringFetcher) Name() string           { return "boom" }
func (erroringFetcher) Scope() knowledge.Scope { return knowledge.ScopeShared }
func (erroringFetcher) Search(context.Context, knowledge.Query) ([]knowledge.Hit, error) {
	return nil, nil
}

func (erroringFetcher) Fetch(context.Context, string, knowledge.Caller) (*knowledge.Document, bool, error) {
	return nil, true, errors.New("store down")
}

func TestFetch_BackendErrorIsToolError(t *testing.T) {
	router := knowledge.NewRouter(nil, nil, erroringFetcher{})
	tk := New("default", router)
	res, _, err := tk.handleFetch(ctxFor(userAID, userAEmail), &mcp.CallToolRequest{},
		fetchInput{Reference: "anything"})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	// A real backend failure is a tool error, NOT a structured found=false.
	if !res.IsError {
		t.Errorf("a backend failure should be a tool error, got %+v", res.Content)
	}
}

// erroringBrowser is a browsable provider whose Browse fails with a generic error,
// to exercise the browse handler's "browse failed" path (distinct from the typed
// unknown-source / not-browsable errors).
type erroringBrowser struct{}

func (erroringBrowser) Name() string           { return "knowledge_pages" }
func (erroringBrowser) Scope() knowledge.Scope { return knowledge.ScopeShared }
func (erroringBrowser) Search(context.Context, knowledge.Query) ([]knowledge.Hit, error) {
	return nil, nil
}

func (erroringBrowser) Browse(context.Context, knowledge.BrowseQuery) (knowledge.BrowsePage, error) {
	return knowledge.BrowsePage{}, errors.New("catalog down")
}

func TestBrowse_BackendErrorIsToolError(t *testing.T) {
	router := knowledge.NewRouter(nil, nil, erroringBrowser{})
	tk := New("default", router)
	res, _ := callBrowse(ctxFor(userAID, userAEmail), t, tk,
		searchInput{Sources: []string{"knowledge_pages"}, Offset: 0})
	if !res.IsError {
		t.Errorf("a backend failure should be a tool error, got %+v", res.Content)
	}
}

// emptyBrowser is a browsable provider that returns an empty page (nil hits), to
// prove the handler renders items as an empty array rather than null.
type emptyBrowser struct{}

func (emptyBrowser) Name() string           { return "knowledge_pages" }
func (emptyBrowser) Scope() knowledge.Scope { return knowledge.ScopeShared }
func (emptyBrowser) Search(context.Context, knowledge.Query) ([]knowledge.Hit, error) {
	return nil, nil
}

func (emptyBrowser) Browse(context.Context, knowledge.BrowseQuery) (knowledge.BrowsePage, error) {
	return knowledge.BrowsePage{Hits: nil, Total: 0}, nil
}

func TestBrowse_EmptyPageRendersEmptyArray(t *testing.T) {
	tk := New("default", knowledge.NewRouter(nil, nil, emptyBrowser{}))
	res, out := callBrowse(ctxFor(userAID, userAEmail), t, tk, searchInput{Sources: []string{"knowledge_pages"}})
	if res.IsError {
		t.Fatalf("an empty browse should succeed: %v", res.Content)
	}
	if out.Count != 0 || out.Total != 0 {
		t.Errorf("empty page = %+v, want count 0 total 0", out)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("unexpected content type %T", res.Content[0])
	}
	if !strings.Contains(tc.Text, "\"items\": []") {
		t.Errorf("items should serialize as an empty array, got: %s", tc.Text)
	}
}

func TestFetch_EmptyReferenceIsToolError(t *testing.T) {
	tk := assembledToolkit()
	// An empty reference is a malformed call (the agent must pass one), distinct
	// from a well-formed reference that resolves to nothing: it is a tool error,
	// not a structured not-found.
	res, _, err := tk.handleFetch(ctxFor(userAID, userAEmail), &mcp.CallToolRequest{}, fetchInput{Reference: "   "})
	if err != nil {
		t.Fatalf("transport error: %v", err)
	}
	if !res.IsError {
		t.Errorf("empty reference should be a tool error, got %+v", res.Content)
	}
}

// AC (#699): memory and insight search hits now carry a fetchable reference, and a
// fetch on that reference returns the full record scoped to the caller.
func TestFetch_MemoryAndInsightRoundTrip(t *testing.T) {
	tk := assembledToolkit()
	aCtx := ctxFor(userAID, userAEmail)

	// search surfaces alice's memory and insight, each now carrying a reference.
	out := callSearch(aCtx, t, tk, "alice")
	memRef := referenceFor(t, out, "memory")
	insRef := referenceFor(t, out, "insights")
	if memRef != "mcp:memory:m-alice" {
		t.Fatalf("memory reference = %q, want mcp:memory:m-alice", memRef)
	}
	if insRef != "mcp:insight:i-alice" {
		t.Fatalf("insight reference = %q, want mcp:insight:i-alice", insRef)
	}

	// The owner fetches both in full.
	if got := callFetch(aCtx, t, tk, memRef); !got.Found || got.Document.Body != "alice memory" {
		t.Errorf("owner memory fetch = %+v", got)
	}
	if got := callFetch(aCtx, t, tk, insRef); !got.Found || got.Document.Body != "alice insight" {
		t.Errorf("owner insight fetch = %+v", got)
	}

	// Another user fetching alice's references gets a structured not-found: fetch
	// never reads memory or insights the caller could not have searched.
	bCtx := ctxFor(userBID, userBEmail)
	if got := callFetch(bCtx, t, tk, memRef); got.Found {
		t.Errorf("user B fetched user A's memory: %+v", got.Document)
	}
	if got := callFetch(bCtx, t, tk, insRef); got.Found {
		t.Errorf("user B fetched user A's insight: %+v", got.Document)
	}

	// An anonymous caller likewise cannot fetch a per-user reference.
	if got := callFetch(context.Background(), t, tk, memRef); got.Found {
		t.Errorf("anonymous caller fetched a memory record: %+v", got.Document)
	}
}
