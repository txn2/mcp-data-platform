package main

import (
	"context"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/knowledge"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// stubProvider is a knowledge.Provider that returns canned hits, so the adapter
// can be exercised against a real *knowledge.Router without external stores.
type stubProvider struct {
	name string
	hits []knowledge.Hit
}

func (s stubProvider) Name() string         { return s.name }
func (stubProvider) Scope() knowledge.Scope { return knowledge.ScopeShared }
func (s stubProvider) Search(context.Context, knowledge.Query) ([]knowledge.Hit, error) {
	return s.hits, nil
}

func TestPortalSearchAdapter_MapsRequestAndResponse(t *testing.T) {
	router := knowledge.NewRouter(nil, nil,
		stubProvider{name: "datahub", hits: []knowledge.Hit{
			{Text: "daily_sales", Source: "datahub", Ref: "urn:1", Score: 0.9, EntityURNs: []string{"urn:1"}, Dimension: "knowledge"},
		}},
		stubProvider{name: "memory", hits: []knowledge.Hit{
			{Text: "prefers UTC", Source: "memory", Ref: "mem1", Score: 0.5, Status: "active"},
		}},
	)
	adapter := portalSearchAdapter{router: router}

	res, err := adapter.Search(context.Background(), portal.SearchQuery{
		Intent: "sales",
		Caller: portal.SearchCaller{Email: "a@example.com", Persona: "analyst"},
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if res.Ranking != "lexical" {
		t.Errorf("ranking = %q, want lexical (nil embedder)", res.Ranking)
	}
	if len(res.Groups) != 2 {
		t.Fatalf("groups = %d, want 2", len(res.Groups))
	}
	// Find the datahub group and verify the hit fields survive the mapping.
	var found bool
	for _, g := range res.Groups {
		if g.Source != "datahub" {
			continue
		}
		found = true
		if len(g.Hits) != 1 {
			t.Fatalf("datahub hits = %d, want 1", len(g.Hits))
		}
		// Score is normalized/fused by the router, so assert it carried a value
		// rather than the raw provider score.
		h := g.Hits[0]
		if h.Text != "daily_sales" || h.Ref != "urn:1" || h.Score <= 0 ||
			h.Dimension != "knowledge" || len(h.EntityURNs) != 1 {
			t.Errorf("hit not mapped faithfully: %+v", h)
		}
	}
	if !found {
		t.Error("datahub group missing")
	}
	if len(res.Coverage) == 0 {
		t.Error("coverage should be populated")
	}
}

func TestPortalSearchAdapter_PropagatesError(t *testing.T) {
	// A router with a single failing provider returns an error (all providers
	// failed), which the adapter surfaces unchanged.
	router := knowledge.NewRouter(nil, nil, errProvider{})
	adapter := portalSearchAdapter{router: router}
	if _, err := adapter.Search(context.Background(), portal.SearchQuery{Intent: "x"}); err == nil {
		t.Fatal("expected error when all providers fail")
	}
}

type errProvider struct{}

func (errProvider) Name() string           { return "boom" }
func (errProvider) Scope() knowledge.Scope { return knowledge.ScopeShared }
func (errProvider) Search(context.Context, knowledge.Query) ([]knowledge.Hit, error) {
	return nil, context.DeadlineExceeded
}
