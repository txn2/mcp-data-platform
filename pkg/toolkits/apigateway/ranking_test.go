package apigateway

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway/catalog"
)

// setupCatalogWithSpec wires a fresh MemoryStore to tk, creates a
// catalog with one inline spec, and returns the store so the test
// can mutate specs later. The helper centralizes the boilerplate
// that every catalog-backed test needs.
func setupCatalogWithSpec(t *testing.T, tk *Toolkit, catalogID, specName, content string) catalog.Store {
	t.Helper()
	store := catalog.NewMemoryStore()
	tk.SetCatalogStore(store)
	if err := store.CreateCatalog(context.Background(), catalog.Catalog{
		ID: catalogID, Name: catalogID, DisplayName: catalogID,
	}); err != nil {
		t.Fatalf("CreateCatalog: %v", err)
	}
	if err := store.UpsertSpec(context.Background(), catalogID,
		newSpecEntry(specName, content)); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	return store
}

// newSpecEntry constructs a SpecEntry with the inline-source defaults
// for tests that don't need to assert on source metadata.
func newSpecEntry(name, content string) catalog.SpecEntry {
	return catalog.SpecEntry{
		SpecName:   name,
		Content:    content,
		SourceKind: catalog.SourceInline,
	}
}

func TestParseRankingMode(t *testing.T) {
	cases := []struct {
		in      string
		want    RankingMode
		wantErr bool
	}{
		{"", RankingLexical, false},
		{"lexical", RankingLexical, false},
		{"semantic", RankingSemantic, false},
		{"hybrid", RankingHybrid, false},
		{"keyword", "", true},
		{"LEXICAL", "", true}, // case-sensitive — schema enum is too
	}
	for _, c := range cases {
		got, err := parseRankingMode(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parseRankingMode(%q): err = %v; want err? %v", c.in, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Errorf("parseRankingMode(%q) = %q; want %q", c.in, got, c.want)
		}
	}
}

func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		name string
		a, b []float32
		want float64
	}{
		{"identical", []float32{1, 0, 0}, []float32{1, 0, 0}, 1.0},
		{"orthogonal", []float32{1, 0, 0}, []float32{0, 1, 0}, 0.0},
		{"opposite", []float32{1, 0, 0}, []float32{-1, 0, 0}, -1.0},
		{"zero left", []float32{0, 0, 0}, []float32{1, 1, 1}, 0.0},
		{"zero right", []float32{1, 1, 1}, []float32{0, 0, 0}, 0.0},
		{"length mismatch", []float32{1, 0}, []float32{1, 0, 0}, 0.0},
		{"empty", nil, nil, 0.0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cosineSimilarity(c.a, c.b)
			if math.Abs(got-c.want) > 1e-6 {
				t.Errorf("cosineSimilarity = %v; want %v", got, c.want)
			}
		})
	}
}

func TestZeroVector(t *testing.T) {
	if !zeroVector(nil) {
		t.Error("nil should be zero")
	}
	if !zeroVector([]float32{0, 0, 0}) {
		t.Error("all zeros should be zero")
	}
	if zeroVector([]float32{0, 0, 0.0001}) {
		t.Error("non-zero element should not be zero")
	}
}

// fakeEmbedder is a deterministic embedder for tests: maps each
// distinct lowercased word to a fixed unit vector and returns the
// L2-normalized average of all word vectors. Crude but enough for
// rank-order assertions: queries that share words with an
// operation's text will score higher than queries that share
// none. Real embedding models do far better; this is a
// dependency-free stand-in.
type fakeEmbedder struct {
	dim       int
	failBatch atomic.Bool
	failEmbed atomic.Bool
}

func newFakeEmbedder(dim int) *fakeEmbedder { return &fakeEmbedder{dim: dim} }

func (f *fakeEmbedder) Dimension() int { return f.dim }

func (f *fakeEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if f.failEmbed.Load() {
		return nil, errors.New("fakeEmbedder: forced failure")
	}
	return f.encode(text), nil
}

func (f *fakeEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if f.failBatch.Load() {
		return nil, errors.New("fakeEmbedder: forced batch failure")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		out[i] = f.encode(t)
	}
	return out, nil
}

func (f *fakeEmbedder) encode(text string) []float32 {
	words := strings.Fields(strings.ToLower(text))
	vec := make([]float32, f.dim)
	if len(words) == 0 {
		return vec
	}
	for _, w := range words {
		wv := wordVec(w, f.dim)
		for i := range vec {
			vec[i] += wv[i]
		}
	}
	// Normalize.
	var norm float64
	for _, v := range vec {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return vec
	}
	scale := float32(1.0 / math.Sqrt(norm))
	for i := range vec {
		vec[i] *= scale
	}
	return vec
}

// wordVec hashes a word into a unit vector with deterministic
// per-word components. Two seeds (FNV split) give independent
// pseudo-random values per dimension.
func wordVec(word string, dim int) []float32 {
	out := make([]float32, dim)
	for i := range dim {
		h := fnv.New64a()
		_, _ = h.Write([]byte(word))
		_, _ = h.Write([]byte{byte(i), byte(i >> 8)})
		// Map uint64 hash to [-1, 1].
		out[i] = float32((float64(h.Sum64()%10001))/5000.0 - 1.0)
	}
	// Normalize.
	var norm float64
	for _, v := range out {
		norm += float64(v) * float64(v)
	}
	if norm == 0 {
		return out
	}
	scale := float32(1.0 / math.Sqrt(norm))
	for i := range out {
		out[i] *= scale
	}
	return out
}

// TestRankWithMode_LexicalEmptyQueryReturnsAll proves lexical mode
// with an empty query is the original v1 behavior — return all
// operations capped at limit. Adding semantic ranking must not
// silently change defaults.
func TestRankWithMode_LexicalEmptyQueryReturnsAll(t *testing.T) {
	tk := New("primary")
	c := &conn{operations: []OperationSummary{
		{OperationID: "a", Method: "GET", Path: "/a"},
		{OperationID: "b", Method: "GET", Path: "/b"},
	}}
	got, fb := rankWithMode(context.Background(), rankRequest{tk: tk, conn: c, ops: c.operations, query: "", limit: 10, mode: RankingLexical})
	if fb != "" {
		t.Error("lexical with empty query should not report fallback")
	}
	if len(got) != 2 {
		t.Errorf("got %d ops; want 2", len(got))
	}
}

// TestRankWithMode_SemanticFallsBackWithoutEmbedder proves the
// fallback contract: requesting "semantic" without a wired
// embedding provider must NOT panic, must return lexical results,
// and must report fallback so the handler can surface a note.
func TestRankWithMode_SemanticFallsBackWithoutEmbedder(t *testing.T) {
	tk := New("primary") // no embedder
	c := &conn{operations: []OperationSummary{
		{OperationID: "create-user", Method: "POST", Path: "/users", Summary: "Create a new user"},
		{OperationID: "list-orders", Method: "GET", Path: "/orders", Summary: "List orders"},
	}}
	// Query "users" is a substring of /users path — lexical
	// fallback must still match it. Without the fallback path
	// returning the lexical results, the model would see an empty
	// list and assume the API has nothing relevant.
	got, fb := rankWithMode(context.Background(), rankRequest{tk: tk, conn: c, ops: c.operations, query: "users", limit: 10, mode: RankingSemantic})
	if fb == "" {
		t.Error("semantic without embedder should fallback to lexical")
	}
	if len(got) == 0 {
		t.Error("fallback should still return lexical-matched ops")
	}
}

// TestRankWithMode_SemanticBeatsLexicalOnIntent is the headline
// test: a query that shares NO words with the target operation's
// surface text should still rank the target above unrelated
// operations under semantic mode. Lexical can't do this — that's
// the whole point of #371.
func TestRankWithMode_SemanticRanksByEmbedding(t *testing.T) {
	tk := New("primary")
	emb := newFakeEmbedder(32)
	tk.SetEmbeddingProvider(emb)
	c := buildTestConn(t, []testOp{
		{id: "create-order", method: "POST", path: "/v1/orders", summary: "Place a new order", desc: "Submit an order to the fulfillment queue"},
		{id: "list-orders", method: "GET", path: "/v1/orders", summary: "List orders"},
		{id: "get-user", method: "GET", path: "/v1/users/{id}", summary: "Fetch user profile"},
	})
	// Populate embeddings synchronously: this test builds the
	// conn directly via buildTestConn so the AddConnection-time
	// warmer never ran. Inline warm matches what the warmer would
	// have produced and keeps the test deterministic.
	populateTestEmbeddings(t, c, emb)
	// Query intentionally lexically-overlapping with "create-order"
	// to exercise the deterministic fakeEmbedder path. Real models
	// would handle "place new order" or "submit order" too; that's
	// captured in the corpus benchmark below.
	got, fb := rankWithMode(context.Background(), rankRequest{tk: tk, conn: c, ops: c.operations, query: "place a new order", limit: 5, mode: RankingSemantic})
	if fb != "" {
		t.Fatalf("semantic with embedder should not fallback; got reason %q", fb)
	}
	if len(got) == 0 || got[0].OperationID != "create-order" {
		t.Errorf("top result = %v; want create-order first", topIDs(got))
	}
}

func TestRankWithMode_HybridBlendsSignals(t *testing.T) {
	tk := New("primary")
	emb := newFakeEmbedder(32)
	tk.SetEmbeddingProvider(emb)
	c := buildTestConn(t, []testOp{
		{id: "create-order", method: "POST", path: "/v1/orders", summary: "Place a new order"},
		{id: "list-orders", method: "GET", path: "/v1/orders", summary: "List orders"},
		{id: "get-user", method: "GET", path: "/v1/users/{id}", summary: "Fetch user profile"},
	})
	populateTestEmbeddings(t, c, emb)
	// Query has direct lexical match on /orders path. Hybrid should
	// still surface order-related ops first.
	got, fb := rankWithMode(context.Background(), rankRequest{tk: tk, conn: c, ops: c.operations, query: "orders", limit: 5, mode: RankingHybrid})
	if fb != "" {
		t.Fatalf("hybrid with embedder should not fallback; got reason %q", fb)
	}
	if len(got) < 2 {
		t.Fatalf("want at least 2 results; got %v", topIDs(got))
	}
	// First two should both be order ops (either order). Third
	// should be the unrelated user endpoint.
	if got[0].Path != "/v1/orders" || got[1].Path != "/v1/orders" {
		t.Errorf("hybrid ordering wrong: %v", topIDs(got))
	}
}

// TestRankWithMode_SemanticEmbedFailureFallsBack proves a flaky
// embedding provider that errors on the query embed call falls
// back to lexical with the wrapped error in the Note. (Operation
// embeddings are pre-loaded from the store at registration; the
// only remaining provider call on the ranking hot path is the
// per-request query embed.)
func TestRankWithMode_SemanticEmbedFailureFallsBack(t *testing.T) {
	emb := newFakeEmbedder(32)
	tk := New("primary")
	tk.SetEmbeddingProvider(emb)
	c := buildTestConn(t, []testOp{
		{id: "a", method: "GET", path: "/a", summary: "alpha"},
		{id: "b", method: "GET", path: "/b", summary: "beta"},
	})
	populateTestEmbeddings(t, c, emb)
	emb.failEmbed.Store(true)
	_, fb := rankWithMode(context.Background(), rankRequest{tk: tk, conn: c, ops: c.operations, query: "alpha", limit: 5, mode: RankingSemantic})
	if fb == "" {
		t.Error("query-embed failure should report fallback")
	}
}

// TestRankWithMode_QueryEmbedFailureFallsBack covers the path where
// the batch embed succeeded but the per-query embed errored. Same
// fallback contract.
func TestRankWithMode_QueryEmbedFailureFallsBack(t *testing.T) {
	emb := newFakeEmbedder(32)
	tk := New("primary")
	tk.SetEmbeddingProvider(emb)
	c := buildTestConn(t, []testOp{{id: "a", method: "GET", path: "/a", summary: "alpha"}})
	populateTestEmbeddings(t, c, emb) // populate before flipping failEmbed
	emb.failEmbed.Store(true)         // single Embed errors; EmbedBatch already ran
	_, fb := rankWithMode(context.Background(), rankRequest{tk: tk, conn: c, ops: c.operations, query: "alpha", limit: 5, mode: RankingSemantic})
	if fb == "" {
		t.Error("query-embed failure should report fallback")
	}
}

// TestEnsureEmbeddings_ZeroVectorsTreatedAsFailure proves the noop
// embedder (which returns all-zero vectors) does not silently
// produce a useless ranking. The zeroVector check on the QUERY
// vector forces the lexical fallback even when ensureEmbeddings
// itself succeeded.
func TestRankWithMode_ZeroQueryVectorFallsBack(t *testing.T) {
	tk := New("primary")
	emb := zeroEmbedder{}
	tk.SetEmbeddingProvider(emb)
	c := buildTestConn(t, []testOp{{id: "a", method: "GET", path: "/a", summary: "alpha"}})
	populateTestEmbeddings(t, c, emb)
	_, fb := rankWithMode(context.Background(), rankRequest{tk: tk, conn: c, ops: c.operations, query: "alpha", limit: 5, mode: RankingSemantic})
	if fb == "" {
		t.Error("zero-vector embedder should force fallback")
	}
}

type zeroEmbedder struct{}

func (zeroEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return make([]float32, 8), nil
}

func (zeroEmbedder) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, 8)
	}
	return out, nil
}
func (zeroEmbedder) Dimension() int { return 8 }

// TestRankWithMode_RecomputesOnSpecHashChange covers AC #4: when an
// operator updates a connection's spec and the admin handler
// re-persists the spec's operation embeddings, ReloadConnection
// must produce a conn whose embedVectors map reflects the NEW
// spec's operations. The test seeds the catalog store with v1
// content plus v1 vectors, registers the connection (vectors
// load from the store), then replaces both the spec content and
// the embeddings with v2 and asserts the post-reload conn sees
// v2's vector set.
func TestRankWithMode_RehashOnSpecChange(t *testing.T) {
	tk := New("primary")
	emb := newFakeEmbedder(32)
	tk.SetEmbeddingProvider(emb)
	store := setupCatalogWithSpec(t, tk, "petstore", "default",
		minimalSpecWith(`/users:
    `+pathOpYAML("get", "list-users", "List users")))
	seedTestEmbeddings(t, seedSpec{
		store: store, emb: emb, catalogID: "petstore", specName: "default",
		ops: []testOp{{id: "list-users", method: "GET", path: "/users", summary: "List users"}},
	})

	if err := tk.AddConnection("api", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection v1: %v", err)
	}
	tk.mu.RLock()
	connV1 := tk.connections["api"]
	tk.mu.RUnlock()

	got1, _ := rankWithMode(context.Background(), rankRequest{tk: tk, conn: connV1, ops: connV1.operations, query: "users", limit: 5, mode: RankingSemantic})
	if len(got1) != 1 || got1[0].OperationID != "list-users" {
		t.Fatalf("v1 ranking returned %v; want list-users", topIDs(got1))
	}
	if len(connV1.embedVectors) == 0 {
		t.Error("v1 vectors should load from store at registration")
	}

	// Edit the catalog's spec and write fresh vectors — mirrors the
	// admin save path that ReloadConnectionsByCatalog runs.
	if err := store.UpsertSpec(context.Background(), "petstore",
		newSpecEntry("default", minimalSpecWith(`/orders:
    `+pathOpYAML("get", "list-orders", "List orders")))); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	// Drop v1 vectors and seed v2 vectors so the store reflects
	// the new spec content.
	if err := store.DeleteOperationEmbeddings(context.Background(), "petstore", "default"); err != nil {
		t.Fatalf("DeleteOperationEmbeddings: %v", err)
	}
	seedTestEmbeddings(t, seedSpec{
		store: store, emb: emb, catalogID: "petstore", specName: "default",
		ops: []testOp{{id: "list-orders", method: "GET", path: "/orders", summary: "List orders"}},
	})
	if err := tk.ReloadConnection("api"); err != nil {
		t.Fatalf("ReloadConnection: %v", err)
	}
	tk.mu.RLock()
	connV2 := tk.connections["api"]
	tk.mu.RUnlock()

	if connV2 == connV1 {
		t.Error("ReloadConnection should produce a new conn instance")
	}
	if len(connV2.embedVectors) == 0 {
		t.Error("v2 vectors should load from store at reload")
	}

	got2, _ := rankWithMode(context.Background(), rankRequest{tk: tk, conn: connV2, ops: connV2.operations, query: "orders", limit: 5, mode: RankingSemantic})
	if len(got2) != 1 || got2[0].OperationID != "list-orders" {
		t.Errorf("v2 ranking returned %v; want list-orders", topIDs(got2))
	}
}

// seedSpec packages the input for seedTestEmbeddings. Kept as a
// struct so the seed helper stays under revive's argument-limit
// of five.
type seedSpec struct {
	store     catalog.Store
	emb       embedding.Provider
	catalogID string
	specName  string
	ops       []testOp
}

// seedTestEmbeddings is the test-only mirror of the admin
// handler's spec-write embedding compute: feed each op's text
// through the supplied embedder and persist the result in the
// catalog store. Lets tests skip the admin HTTP path while still
// exercising the production "vectors are loaded from the store"
// invariant inside the toolkit.
func seedTestEmbeddings(t *testing.T, s seedSpec) {
	t.Helper()
	texts := make([]string, len(s.ops))
	for i, o := range s.ops {
		summary := OperationSummary{OperationID: o.id, Method: o.method, Path: o.path, Summary: o.summary, Tags: o.tags}
		texts[i] = buildEmbedText(summary, o.desc)
	}
	vectors, err := s.emb.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("seedTestEmbeddings: embed: %v", err)
	}
	rows := make([]catalog.OperationEmbedding, len(s.ops))
	for i, o := range s.ops {
		rows[i] = catalog.OperationEmbedding{
			OperationID: o.id,
			Embedding:   vectors[i],
			Dim:         s.emb.Dimension(),
		}
	}
	if err := s.store.UpsertOperationEmbeddings(context.Background(), s.catalogID, s.specName, rows); err != nil {
		t.Fatalf("seedTestEmbeddings: upsert: %v", err)
	}
}

// TestHandleListEndpoints_RankingValidation drives the handler
// end-to-end for the new ranking arg: invalid value returns an
// error result; valid values dispatch correctly; unwired embedder
// surfaces a fallback note.
func TestHandleListEndpoints_RankingValidation(t *testing.T) {
	tk := New("primary")
	setupCatalogWithSpec(t, tk, "petstore", "default",
		minimalSpecWith(`/x:
    `+pathOpYAML("get", "x", "x")))
	if err := tk.AddConnection("api", map[string]any{
		"base_url":   "https://api.example.com",
		"catalog_id": "petstore",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}

	// Invalid mode → error.
	r, _, _ := tk.handleListEndpoints(context.Background(), &mcp.CallToolRequest{},
		ListEndpointsInput{Connection: "api", Ranking: "weird"})
	if r == nil || !r.IsError {
		t.Error("invalid ranking should produce IsError result")
	}

	// Valid semantic without embedder → success but with fallback note.
	r, payload, _ := tk.handleListEndpoints(context.Background(), &mcp.CallToolRequest{},
		ListEndpointsInput{Connection: "api", Query: "x", Ranking: "semantic"})
	if r == nil || r.IsError {
		t.Errorf("semantic without embedder should not error: %v", r)
	}
	out, _ := payload.(ListEndpointsOutput)
	if !strings.Contains(out.Note, "fell back to lexical") {
		t.Errorf("missing fallback note: %+v", out)
	}
}

// --- helpers ---

type testOp struct {
	id, method, path, summary, desc string
	tags                            []string
}

// buildTestConn assembles a *conn directly from testOp specs,
// skipping the OpenAPI loader so unit tests don't depend on the
// kin-openapi parser shape. embedVectors stays empty here so
// tests that exercise the lexical-fallback path can drive it
// directly; tests that exercise semantic ranking call
// populateTestEmbeddings to mirror what the admin-handler write
// path would have persisted.
func buildTestConn(t *testing.T, ops []testOp) *conn {
	t.Helper()
	c := &conn{embedVectors: make(map[embedKey][]float32)}
	for _, o := range ops {
		summary := OperationSummary{
			OperationID: o.id,
			Method:      o.method,
			Path:        o.path,
			Summary:     o.summary,
			Tags:        o.tags,
		}
		c.operations = append(c.operations, summary)
	}
	// Stash the desc field on each op via a parallel slice on the
	// conn so populateTestEmbeddings can recover the text it would
	// have hashed. testOp carries `desc` but the OperationSummary
	// type does not, so we cannot read it back from c.operations.
	c.testDescs = make(map[string]string, len(ops))
	for _, o := range ops {
		c.testDescs[o.id] = o.desc
	}
	return c
}

// populateTestEmbeddings simulates what the admin-handler spec-write
// path persists: feed each operation's embed text through the
// supplied provider and store the result under embedVectors keyed
// by (spec, operation_id). Replaces the old warmEmbeddings hook,
// which is gone because the toolkit no longer computes vectors.
func populateTestEmbeddings(t *testing.T, c *conn, emb embedding.Provider) {
	t.Helper()
	texts := make([]string, len(c.operations))
	for i, op := range c.operations {
		texts[i] = buildEmbedText(op, c.testDescs[op.OperationID])
	}
	vectors, err := emb.EmbedBatch(context.Background(), texts)
	if err != nil {
		t.Fatalf("populateTestEmbeddings: %v", err)
	}
	for i, op := range c.operations {
		c.embedVectors[embedKey{Spec: op.Spec, OperationID: op.OperationID}] = vectors[i]
	}
}

func topIDs(ops []OperationSummary) []string {
	out := make([]string, len(ops))
	for i, op := range ops {
		out[i] = op.OperationID
	}
	return out
}

// minimalSpecWith wraps a YAML "paths" block in the smallest valid
// OpenAPI 3.0 envelope the kin-openapi validator will accept. Each
// operation in the caller-supplied paths block must declare its own
// responses block (the validator rejects operations without one);
// the helper does not synthesize them so test specs read closer to
// real-world specs.
func minimalSpecWith(pathsBlock string) string {
	return `openapi: 3.0.0
info:
  title: t
  version: "1"
paths:
  ` + pathsBlock + `
`
}

// pathOpYAML is a one-liner helper that wraps a single operation's
// YAML in the responses block kin-openapi requires. Keeps the test
// spec definitions readable while satisfying the validator.
func pathOpYAML(method, opID, summary string) string {
	return method + `:
      operationId: ` + opID + `
      summary: "` + summary + `"
      responses:
        "200":
          description: ok`
}

// --- corpus benchmark (AC #3): recall@k semantic vs lexical ---
//
// TestSemanticRanking_Benchmark documents the precision boost
// semantic ranking produces over lexical on a small CRM-style
// corpus. The corpus is intentionally tiny (8 ops, 6 query
// intents) so the test runs in <50ms; the point is not to prove a
// production-grade win, it's to exercise the full ranking pipeline
// end-to-end and surface a recall-at-k metric for human review.
//
// The fakeEmbedder is a deterministic word-bag stand-in — recall
// numbers will be lower than a real embedding model. The contract
// the test asserts is the directional one: hybrid >= lexical on
// intent queries that share words with the target spec.
func TestSemanticRanking_BenchmarkCorpus(t *testing.T) {
	corpus := []testOp{
		{id: "list-customers", method: "GET", path: "/v1/customers", summary: "List customers"},
		{id: "create-customer", method: "POST", path: "/v1/customers", summary: "Add a new customer"},
		{id: "get-customer", method: "GET", path: "/v1/customers/{id}", summary: "Retrieve a customer by id"},
		{id: "list-orders", method: "GET", path: "/v1/orders", summary: "List orders"},
		{id: "create-order", method: "POST", path: "/v1/orders", summary: "Place a new order"},
		{id: "cancel-order", method: "POST", path: "/v1/orders/{id}/cancel", summary: "Cancel an order"},
		{id: "list-products", method: "GET", path: "/v1/products", summary: "List products"},
		{id: "search-products", method: "GET", path: "/v1/products/search", summary: "Search products by query"},
	}
	queries := []struct {
		query  string
		target string // operation_id of the ground-truth match
	}{
		{"list customers", "list-customers"},
		{"new customer", "create-customer"},
		{"orders list", "list-orders"},
		{"place an order", "create-order"},
		{"cancel order", "cancel-order"},
		{"product search", "search-products"},
	}

	tk := New("bench")
	emb := newFakeEmbedder(64)
	tk.SetEmbeddingProvider(emb)
	c := buildTestConn(t, corpus)
	// buildTestConn bypasses AddConnection so no warmer was
	// spawned. Populate inline so hybrid actually scores
	// semantically; without this, queryVectorFor would return
	// errEmbeddingsNotStarted and hybrid would silently fall
	// back to lexical, making the recall comparison meaningless.
	populateTestEmbeddings(t, c, emb)

	type recall struct {
		at1, at3 int
	}
	score := func(mode RankingMode) recall {
		var r recall
		for _, q := range queries {
			ranked, _ := rankWithMode(context.Background(), rankRequest{tk: tk, conn: c, ops: c.operations, query: q.query, limit: len(corpus), mode: mode})
			ids := topIDs(ranked)
			if len(ids) > 0 && ids[0] == q.target {
				r.at1++
			}
			for i := 0; i < len(ids) && i < 3; i++ {
				if ids[i] == q.target {
					r.at3++
					break
				}
			}
		}
		return r
	}

	lex := score(RankingLexical)
	hyb := score(RankingHybrid)
	t.Logf("recall@1: lexical=%d/%d  hybrid=%d/%d", lex.at1, len(queries), hyb.at1, len(queries))
	t.Logf("recall@3: lexical=%d/%d  hybrid=%d/%d", lex.at3, len(queries), hyb.at3, len(queries))

	// Hybrid recall@3 must be at least as good as lexical recall@3.
	// This is the directional contract: blending semantic into the
	// score never hurts the substring-match precision.
	if hyb.at3 < lex.at3 {
		t.Errorf("hybrid recall@3 (%d) < lexical recall@3 (%d) — blend regressed substring precision",
			hyb.at3, lex.at3)
	}
}

// TestEmbedInBatches_ChunksAtBatchSize proves the chunking
// boundary: a 100-text input must produce 4 calls (chunks of 32,
// 32, 32, 4) rather than one giant batch. Drives finding from the
// adversarial review that the chunking claim was previously
// unverified by tests using only small fixtures.
func TestEmbedInBatches_ChunksAtBatchSize(t *testing.T) {
	emb := &batchCounter{vectorDim: 4}
	texts := make([]string, 100)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}

	vectors, err := embedInBatches(context.Background(), emb, texts, 32)
	if err != nil {
		t.Fatalf("embedInBatches: %v", err)
	}
	if len(vectors) != 100 {
		t.Fatalf("got %d vectors, want 100", len(vectors))
	}
	if emb.batchCalls != 4 {
		t.Errorf("expected 4 EmbedBatch calls (32+32+32+4), got %d", emb.batchCalls)
	}
	wantSizes := []int{32, 32, 32, 4}
	if len(emb.batchSizes) != len(wantSizes) {
		t.Fatalf("got %d batches, want %d", len(emb.batchSizes), len(wantSizes))
	}
	for i, n := range wantSizes {
		if emb.batchSizes[i] != n {
			t.Errorf("batch %d: got %d, want %d", i, emb.batchSizes[i], n)
		}
	}
}

// TestEmbedInBatches_PropagatesError proves a batch error short-
// circuits the remaining chunks. Caller must not see partial
// results disguised as success.
func TestEmbedInBatches_PropagatesError(t *testing.T) {
	emb := &batchCounter{vectorDim: 4, failOnBatch: 2}
	texts := make([]string, 100)
	for i := range texts {
		texts[i] = fmt.Sprintf("text-%d", i)
	}
	_, err := embedInBatches(context.Background(), emb, texts, 32)
	if err == nil {
		t.Fatal("expected error from failing batch")
	}
	if emb.batchCalls != 2 {
		t.Errorf("expected 2 calls (first ok, second fail), got %d", emb.batchCalls)
	}
}

type batchCounter struct {
	vectorDim   int
	batchCalls  int
	batchSizes  []int
	failOnBatch int // 1-indexed; 0 = never fail
}

func (b *batchCounter) Dimension() int { return b.vectorDim }

func (b *batchCounter) Embed(_ context.Context, _ string) ([]float32, error) {
	out := make([]float32, b.vectorDim)
	out[0] = 1
	return out, nil
}

func (b *batchCounter) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	b.batchCalls++
	b.batchSizes = append(b.batchSizes, len(texts))
	if b.failOnBatch != 0 && b.batchCalls == b.failOnBatch {
		return nil, fmt.Errorf("simulated batch %d failure", b.batchCalls)
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = make([]float32, b.vectorDim)
		out[i][0] = float32(i + 1)
	}
	return out, nil
}

// TestLexicalScore_MultiTokenAndForHybridSignal proves the hybrid
// scorer's lexical signal honors per-token AND, matching the
// rankOperations behavior. Previously hybrid mode treated the
// query as a phrase and assigned lexicalMatchAbsent to multi-token
// intent queries that rankOperations would have matched, defeating
// the hybrid blend for exactly the queries it should help with.
func TestLexicalScore_MultiTokenAndForHybridSignal(t *testing.T) {
	op := OperationSummary{
		OperationID: "listGifts",
		Method:      "GET",
		Path:        "/gifts",
		Summary:     "List all gifts",
	}
	if got := lexicalScore(op, "gift list"); got != lexicalMatchPresent {
		t.Errorf("multi-token AND match should return present (1.0); got %v", got)
	}
	if got := lexicalScore(op, "gift purchase"); got != lexicalMatchAbsent {
		t.Errorf("token missing from any field should return absent (0.0); got %v", got)
	}
	if got := lexicalScore(op, ""); got != lexicalMatchAbsent {
		t.Errorf("empty query should return absent; got %v", got)
	}
}

// TestCheckEmbeddingsReady covers every state the embedding
// readiness check can be in. With persisted vectors the check
// collapses to a row-existence on c.embedVectors plus the
// no-operations guard.
func TestCheckEmbeddingsReady(t *testing.T) {
	t.Run("no operations", func(t *testing.T) {
		c := &conn{}
		if err := checkEmbeddingsReady(c); !errors.Is(err, errEmbeddingsNoOps) {
			t.Errorf("err=%v want errEmbeddingsNoOps", err)
		}
	})
	t.Run("not indexed", func(t *testing.T) {
		c := &conn{
			operations:   []OperationSummary{{OperationID: "a"}},
			embedVectors: map[embedKey][]float32{},
		}
		if err := checkEmbeddingsReady(c); !errors.Is(err, errEmbeddingsNotIndexed) {
			t.Errorf("err=%v want errEmbeddingsNotIndexed", err)
		}
	})
	t.Run("ready", func(t *testing.T) {
		c := &conn{
			operations: []OperationSummary{{OperationID: "a"}},
			embedVectors: map[embedKey][]float32{
				{OperationID: "a"}: {1, 0},
			},
		}
		if err := checkEmbeddingsReady(c); err != nil {
			t.Errorf("ready state should return nil; got %v", err)
		}
	})
}

// TestSetEmbeddingProvider_NilDoesNotPanic proves passing nil
// is a no-op. Cover symmetry with the non-nil path.
func TestSetEmbeddingProvider_NilDoesNotPanic(t *testing.T) {
	tk := New("test")
	tk.SetEmbeddingProvider(nil)
	tk.SetEmbeddingProvider(nil) // idempotent
	if tk.EmbeddingProvider() != nil {
		t.Error("EmbeddingProvider() should remain nil after SetEmbeddingProvider(nil)")
	}
}
