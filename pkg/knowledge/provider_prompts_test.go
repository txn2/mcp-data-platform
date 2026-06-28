package knowledge

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

type fakePromptSearcher struct {
	scored   []prompt.ScoredPrompt
	err      error
	got      prompt.SearchQuery
	called   bool
	prompt   *prompt.Prompt // GetByID result
	getErr   error
	gotGetID string
}

func (f *fakePromptSearcher) Search(_ context.Context, q prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	f.called = true
	f.got = q
	return f.scored, f.err
}

func (f *fakePromptSearcher) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	f.gotGetID = id
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.prompt, nil
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

func TestPromptsProvider_Fetch(t *testing.T) {
	// Prompt references carry the prompt's UUID id (validated on parse), so the
	// reference is built from a real UUID rather than a short label.
	const promptID = "550e8400-e29b-41d4-a716-446655440000"
	ref := knowledgepage.PromptRef(promptID)
	// live marks a prompt fixture approved and enabled, the lifecycle state the
	// search path requires before scope is even considered; tests that probe scope
	// start from a live prompt so scope is the only variable.
	live := func(p *prompt.Prompt) *prompt.Prompt {
		p.Status, p.Enabled = prompt.StatusApproved, true
		return p
	}

	t.Run("returns a global prompt to anyone", func(t *testing.T) {
		s := &fakePromptSearcher{prompt: live(&prompt.Prompt{ID: promptID, Name: "daily", DisplayName: "Daily Report", Content: "Summarize {date}.", Scope: prompt.ScopeGlobal})}
		doc, owned, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{})
		if !owned || err != nil {
			t.Fatalf("owned=%v err=%v", owned, err)
		}
		if s.gotGetID != promptID {
			t.Errorf("GetByID id = %q", s.gotGetID)
		}
		if doc.Source != SourcePrompts || doc.Title != "Daily Report" || doc.Body != "Summarize {date}." {
			t.Errorf("doc = %+v", doc)
		}
	})

	t.Run("declines a non-prompt reference", func(t *testing.T) {
		s := &fakePromptSearcher{}
		_, owned, err := NewPromptsProvider(s).Fetch(context.Background(), "mcp:asset:a1", Caller{})
		if owned || err != nil {
			t.Errorf("owned=%v err=%v, want declined", owned, err)
		}
		if s.gotGetID != "" {
			t.Errorf("GetByID must not be called for a non-prompt reference")
		}
	})

	t.Run("persona prompt visible only to the matching persona", func(t *testing.T) {
		s := &fakePromptSearcher{prompt: live(&prompt.Prompt{ID: "p1", Name: "a", Scope: prompt.ScopePersona, Personas: []string{"analyst"}})}
		if _, _, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{Persona: "analyst"}); err != nil {
			t.Errorf("matching persona should see the prompt: %v", err)
		}
		_, owned, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{Persona: "admin"})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound for a non-matching persona", owned, err)
		}
	})

	t.Run("personal prompt visible only to its owner", func(t *testing.T) {
		s := &fakePromptSearcher{prompt: live(&prompt.Prompt{ID: "p1", Name: "a", Scope: prompt.ScopePersonal, OwnerEmail: "alice@example.com"})}
		if _, _, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{Email: "alice@example.com"}); err != nil {
			t.Errorf("owner should see their personal prompt: %v", err)
		}
		_, owned, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{Email: "bob@example.com"})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound for a non-owner", owned, err)
		}
	})

	t.Run("missing prompt is not-found", func(t *testing.T) {
		s := &fakePromptSearcher{prompt: nil}
		_, owned, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + ErrNotFound", owned, err)
		}
	})

	t.Run("persona prompt is hidden from a caller carrying no persona", func(t *testing.T) {
		s := &fakePromptSearcher{prompt: live(&prompt.Prompt{ID: promptID, Scope: prompt.ScopePersona, Personas: []string{"analyst"}})}
		_, owned, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound for an empty persona", owned, err)
		}
	})

	t.Run("a disabled or unapproved prompt is hidden even from a permitted caller", func(t *testing.T) {
		// search filters status='approved' AND enabled=true before scope, so an admin
		// retiring a global prompt removes it from fetch too, even for a caller the
		// scope would otherwise admit.
		disabled := &prompt.Prompt{ID: promptID, Scope: prompt.ScopeGlobal, Status: prompt.StatusApproved, Enabled: false}
		if _, owned, err := NewPromptsProvider(&fakePromptSearcher{prompt: disabled}).Fetch(context.Background(), ref, Caller{}); !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound for a disabled prompt", owned, err)
		}
		unapproved := &prompt.Prompt{ID: promptID, Scope: prompt.ScopeGlobal, Status: prompt.StatusDraft, Enabled: true}
		if _, owned, err := NewPromptsProvider(&fakePromptSearcher{prompt: unapproved}).Fetch(context.Background(), ref, Caller{}); !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound for an unapproved prompt", owned, err)
		}
	})

	t.Run("an unknown scope fails closed", func(t *testing.T) {
		s := &fakePromptSearcher{prompt: live(&prompt.Prompt{ID: promptID, Scope: "future-scope"})}
		_, owned, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{Email: "a@b.c", Persona: "analyst"})
		if !owned || !errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want ErrNotFound for an unknown scope", owned, err)
		}
	})

	t.Run("prompt with no display name uses its unique name as title", func(t *testing.T) {
		s := &fakePromptSearcher{prompt: live(&prompt.Prompt{ID: promptID, Name: "daily-report", Scope: prompt.ScopeGlobal})}
		doc, _, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{})
		if err != nil || doc.Title != "daily-report" {
			t.Errorf("doc=%+v err=%v, want title from Name", doc, err)
		}
	})

	t.Run("store error surfaces as a real error", func(t *testing.T) {
		s := &fakePromptSearcher{getErr: errors.New("boom")}
		_, owned, err := NewPromptsProvider(s).Fetch(context.Background(), ref, Caller{})
		if !owned || err == nil || errors.Is(err, ErrNotFound) {
			t.Errorf("owned=%v err=%v, want owned + a non-not-found error", owned, err)
		}
	})
}
