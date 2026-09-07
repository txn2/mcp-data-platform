package graphql

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/internal/opranking"
)

// indexed wires a toolkit's ranking dependencies: an embedder and the
// vectors an index pass would have written, computed with the same
// deterministic embedder so the two agree.
func indexed(t *testing.T, tk *Toolkit) {
	t.Helper()
	embedder := wordEmbedder{dim: 32}
	_, items, ok := tk.IndexItems("gql")
	if !ok {
		t.Fatal("no index items")
	}
	vectors := map[string][]float32{}
	for id, text := range items {
		vec, err := embedder.Embed(context.Background(), text)
		if err != nil {
			t.Fatalf("embed: %v", err)
		}
		vectors[id] = vec
	}
	tk.SetEmbeddingProvider(embedder)
	tk.SetVectorReader(memoryVectors{vectors: vectors})
	tk.ReloadVectors(context.Background(), "gql")
}

func TestParseRankingMode(t *testing.T) {
	for _, c := range []struct {
		in   string
		want RankingMode
		bad  bool
	}{
		{"", "", false},
		{"lexical", RankingLexical, false},
		{"SEMANTIC", RankingSemantic, false},
		{" hybrid ", RankingHybrid, false},
		{"vibes", "", true},
	} {
		got, err := ParseRankingMode(c.in)
		if c.bad {
			if err == nil {
				t.Errorf("%q was accepted", c.in)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%q gave (%q, %v)", c.in, got, err)
		}
	}
}

func TestAnOmittedRankingBecomesHybridOnceTheSchemaIsIndexed(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)

	// Before the index has run, a query that shares no vocabulary with
	// the schema matches nothing and the answer says so.
	before := callDiscover(t, tk, DiscoverInput{Connection: "gql", Query: "purchase requisition"})
	if len(before.Operations) != 0 {
		t.Errorf("operations = %+v; lexical is an AND filter", before.Operations)
	}

	indexed(t, tk)

	after := callDiscover(t, tk, DiscoverInput{Connection: "gql", Query: "purchase requisition"})
	if after.ShownSemantic == nil || *after.ShownSemantic == 0 {
		t.Errorf("out = %+v; an indexed connection ranks by intent", after)
	}
	if !strings.Contains(after.Note, "closest by intent") {
		t.Errorf("note = %q", after.Note)
	}
}

func TestAnExplicitLexicalRankingIsNotUpgraded(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	indexed(t, tk)
	out := callDiscover(t, tk, DiscoverInput{
		Connection: "gql", Query: "purchase requisition", Ranking: "lexical",
	})
	if len(out.Operations) != 0 {
		t.Errorf("operations = %+v; an explicit lexical ranking opts out of intent", out.Operations)
	}
}

func TestSemanticRankingFallsBackAndSaysWhy(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, tk *Toolkit)
		want  string
	}{
		{
			name:  "no embedding provider",
			setup: func(t *testing.T, _ *Toolkit) { t.Helper() },
			want:  "embedding provider not configured",
		},
		{
			name: "the schema has not been indexed",
			setup: func(t *testing.T, tk *Toolkit) {
				t.Helper()
				tk.SetEmbeddingProvider(wordEmbedder{dim: 8})
			},
			want: "has not been indexed",
		},
		{
			name: "the provider failed",
			setup: func(t *testing.T, tk *Toolkit) {
				t.Helper()
				indexed(t, tk)
				tk.SetEmbeddingProvider(wordEmbedder{dim: 8, err: errors.New("model is down")})
			},
			want: "model is down",
		},
		{
			name: "the provider returned a zero vector",
			setup: func(t *testing.T, tk *Toolkit) {
				t.Helper()
				indexed(t, tk)
				tk.SetEmbeddingProvider(wordEmbedder{dim: 32, zero: true})
			},
			want: "zero vector",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			u := newUpstream(t)
			tk := newToolkit(t, u, "namespaced", nil)
			c.setup(t, tk)
			out := callDiscover(t, tk, DiscoverInput{
				Connection: "gql", Query: "product query", Ranking: "semantic",
			})
			if !strings.Contains(out.Note, c.want) {
				t.Errorf("note = %q; want it to say %q", out.Note, c.want)
			}
			// The fallback still answers: a lexical result is a working
			// answer, not a failure.
			if len(out.Operations) == 0 {
				t.Error("the fallback returned nothing")
			}
		})
	}
}

func TestARankedRowCarriesItsScoreAndMatchFlag(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "namespaced", nil)
	indexed(t, tk)
	out := callDiscover(t, tk, DiscoverInput{Connection: "gql", Query: "product", Ranking: "hybrid"})
	if len(out.Operations) == 0 {
		t.Fatal("no operations")
	}
	for _, op := range out.Operations {
		if op.Score == nil || op.LexicalMatch == nil {
			t.Fatalf("%s carries no relevance: %+v", op.OperationID, op)
		}
	}
	if out.MatchedLexical == nil || *out.MatchedLexical == 0 {
		t.Errorf("matched = %v; the query's own word is in these operations", out.MatchedLexical)
	}
}

func TestEmbeddingsReadyDistinguishesItsThreeStates(t *testing.T) {
	u := newUpstream(t)
	bare := newToolkit(t, u, "", nil)
	c, _, _ := bare.lookup("gql")
	if err := embeddingsReady(c); !errors.Is(err, errEmbeddingsNoOps) {
		t.Errorf("a connection with no schema gave %v", err)
	}
	tk := newToolkit(t, u, "flat", nil)
	c2, _, _ := tk.lookup("gql")
	if err := embeddingsReady(c2); !errors.Is(err, errEmbeddingsNotIndexed) {
		t.Errorf("an unindexed connection gave %v", err)
	}
	indexed(t, tk)
	if err := embeddingsReady(c2); err != nil {
		t.Errorf("an indexed connection gave %v", err)
	}
}

func TestRankOnAnEmptyQueryIsNotRanked(t *testing.T) {
	u := newUpstream(t)
	tk := newToolkit(t, u, "flat", nil)
	c, _, _ := tk.lookup("gql")
	ops, _, _ := tk.Operations("gql")
	got := tk.rank(context.Background(), rankRequest{conn: c, ops: ops, query: "   ", limit: 2, mode: RankingHybrid})
	if len(got.operations) != 2 {
		t.Fatalf("operations = %d; want the limit applied", len(got.operations))
	}
	for _, op := range got.operations {
		if op.Score != nil {
			t.Errorf("%s carries a score; nothing was matched against", op.OperationID)
		}
	}
}

// TestResolveDropsAnIDTheIndexNoLongerHolds is the state a rank pass
// reaches when the schema was replaced between scoring and rendering: a
// verdict for an operation that is gone is dropped rather than rendered
// as an empty row.
func TestResolveDropsAnIDTheIndexNoLongerHolds(t *testing.T) {
	byID := map[string]gqlschema.Operation{"query:kept": {ID: "query:kept"}}
	got := resolve(byID, []opranking.Scored{{ID: "query:kept"}, {ID: "query:gone"}})
	if len(got) != 1 || got[0].op.ID != "query:kept" {
		t.Errorf("resolved = %+v", got)
	}
}
