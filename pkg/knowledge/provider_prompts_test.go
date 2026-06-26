package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

type fakePromptSearcher struct {
	scored []prompt.ScoredPrompt
	err    error
	got    prompt.SearchQuery
	called bool
}

func (f *fakePromptSearcher) Search(_ context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	f.called = true
	f.got = q
	return f.scored, f.err
}

func TestPromptsProvider_Metadata(t *testing.T) {
	p := NewPromptsProvider(&fakePromptSearcher{})
	if p.Name() != SourcePrompts {
		t.Errorf("Name = %q", p.Name())
	}
	// Shared: global prompts are visible to everyone; the searcher self-filters
	// persona/personal prompts to the caller.
	if p.Scope() != ScopeShared {
		t.Errorf("Scope = %v, want shared", p.Scope())
	}
}

func TestPromptsProvider_NoIntentSkips(t *testing.T) {
	s := &fakePromptSearcher{}
	p := NewPromptsProvider(s)
	hits, err := p.Search(context.Background(), Query{EntityURNs: []string{"urn:x"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hits != nil || s.called {
		t.Error("prompts provider should not run without an intent")
	}
}

func TestPromptsProvider_ForwardsVisibilityAndMaps(t *testing.T) {
	s := &fakePromptSearcher{
		scored: []prompt.ScoredPrompt{
			{Prompt: prompt.Prompt{ID: "11111111-1111-1111-1111-111111111111", Name: "churn-calc", DisplayName: "Churn Calculation", Description: "how we compute churn"}, Score: 0.8},
			{Prompt: prompt.Prompt{Name: "bare"}, Score: 0.5},
		},
	}
	p := NewPromptsProvider(s)
	hits, err := p.Search(context.Background(), Query{
		Intent:    "churn",
		Embedding: []float32{0.1},
		Caller:    Caller{Email: "a@example.com", Persona: "analyst"},
		Limit:     6,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.got.OwnerEmail != "a@example.com" || s.got.Persona != "analyst" {
		t.Errorf("visibility identity not forwarded: %+v", s.got)
	}
	if s.got.Limit != 6 || len(s.got.Embedding) == 0 {
		t.Errorf("query not forwarded: %+v", s.got)
	}
	if len(hits) != 2 {
		t.Fatalf("len = %d, want 2", len(hits))
	}
	if hits[0].Source != SourcePrompts || hits[0].Ref != "churn-calc" || hits[0].Text != "Churn Calculation\nhow we compute churn" {
		t.Errorf("unexpected hit[0]: %+v", hits[0])
	}
	// The canonical reference uses the prompt UUID, not its name. The second
	// prompt has no UUID id, so it gets no reference (omitted).
	if hits[0].Reference != "mcp:prompt:11111111-1111-1111-1111-111111111111" {
		t.Errorf("canonical reference = %q, want mcp:prompt:<uuid>", hits[0].Reference)
	}
	if hits[1].Reference != "" {
		t.Errorf("prompt without a uuid id must have no reference, got %q", hits[1].Reference)
	}
	// No display name and no description falls back to the prompt name.
	if hits[1].Text != "bare" {
		t.Errorf("hit[1] text = %q, want %q", hits[1].Text, "bare")
	}
}

func TestPromptsProvider_SearchError(t *testing.T) {
	p := NewPromptsProvider(&fakePromptSearcher{err: errors.New("boom")})
	_, err := p.Search(context.Background(), Query{Intent: "q"})
	if err == nil {
		t.Fatal("expected error to propagate")
	}
}
