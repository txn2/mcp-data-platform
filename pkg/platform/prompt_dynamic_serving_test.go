package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

func TestListVisiblePrompts_ScopePrefixesAndScoping(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["g1"] = &prompt.Prompt{Name: "g1", Scope: prompt.ScopeGlobal, Enabled: true}
	store.prompts["pa"] = &prompt.Prompt{Name: "pa", Scope: prompt.ScopePersona, Personas: []string{"analyst"}, Enabled: true}
	store.prompts["pe"] = &prompt.Prompt{Name: "pe", Scope: prompt.ScopePersona, Personas: []string{"engineer"}, Enabled: true}
	store.prompts["mine"] = &prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com", Enabled: true}
	store.prompts["bob"] = &prompt.Prompt{Name: "bob", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com", Enabled: true}

	out := p.listVisiblePrompts(context.Background(), "sarah@example.com", []string{"analyst"})
	names := map[string]bool{}
	for _, pr := range out {
		names[pr.Name] = true
	}

	// Sarah (an analyst) sees globals, her persona's prompts, and her own personal.
	assert.True(t, names["global-g1"], "global prompt visible with global- prefix")
	assert.True(t, names["analyst-pa"], "her persona's prompt visible with <persona>- prefix")
	assert.True(t, names["personal-mine"], "her personal prompt visible with personal- prefix")
	// Not another persona's prompt, nor another user's personal prompt.
	assert.False(t, names["engineer-pe"], "a non-member persona prompt must not be visible")
	assert.False(t, names["personal-bob"], "another user's personal prompt must not be visible")
}

func TestPromptServing_AnonymousIsFailClosed(t *testing.T) {
	// An anonymous caller (empty email, no personas) sees only globals and can
	// fetch only globals, never personal or persona prompts.
	p, store := newTestPlatformWithPromptStore()
	store.prompts["g"] = &prompt.Prompt{Name: "g", Scope: prompt.ScopeGlobal, Enabled: true}
	store.prompts["pa"] = &prompt.Prompt{Name: "pa", Scope: prompt.ScopePersona, Personas: []string{"analyst"}, Enabled: true}
	store.prompts["mine"] = &prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com", Enabled: true}

	out := p.listVisiblePrompts(context.Background(), "", nil)
	names := map[string]bool{}
	for _, pr := range out {
		names[pr.Name] = true
	}
	assert.True(t, names["global-g"], "anonymous sees globals")
	assert.False(t, names["analyst-pa"], "anonymous must not see persona prompts")
	assert.False(t, names["personal-mine"], "anonymous must not see personal prompts")
	assert.Len(t, out, 1, "anonymous list contains only the global prompt")

	_, ok := p.getDynamicPrompt(context.Background(), "", nil, "personal-mine", nil)
	assert.False(t, ok, "anonymous cannot fetch a personal prompt")
	_, ok = p.getDynamicPrompt(context.Background(), "", nil, "analyst-pa", nil)
	assert.False(t, ok, "anonymous cannot fetch a persona prompt")
	_, ok = p.getDynamicPrompt(context.Background(), "", nil, "global-g", nil)
	assert.True(t, ok, "anonymous can fetch a global prompt")
}

func TestGetDynamicPrompt_ResolvesByPrefix(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["g1"] = &prompt.Prompt{Name: "g1", Scope: prompt.ScopeGlobal, Content: "global {x}", Enabled: true}
	store.prompts["pa"] = &prompt.Prompt{Name: "pa", Scope: prompt.ScopePersona, Personas: []string{"analyst"}, Content: "persona", Enabled: true}
	store.prompts["mine"] = &prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com", Content: "personal", Enabled: true}

	ctx := context.Background()
	analyst := []string{"analyst"}

	_, ok := p.getDynamicPrompt(ctx, "sarah@example.com", analyst, "personal-mine", nil)
	assert.True(t, ok, "own personal prompt resolves via personal- prefix")

	res, ok := p.getDynamicPrompt(ctx, "sarah@example.com", analyst, "global-g1", map[string]string{"x": "Y"})
	require.True(t, ok, "global prompt resolves via global- prefix")
	require.NotNil(t, res)

	_, ok = p.getDynamicPrompt(ctx, "sarah@example.com", analyst, "analyst-pa", nil)
	assert.True(t, ok, "persona prompt resolves for a member via <persona>- prefix")

	_, ok = p.getDynamicPrompt(ctx, "sarah@example.com", []string{"engineer"}, "analyst-pa", nil)
	assert.False(t, ok, "a non-member cannot resolve a persona prompt by its prefix")

	_, ok = p.getDynamicPrompt(ctx, "bob@example.com", nil, "personal-mine", nil)
	assert.False(t, ok, "another user cannot resolve someone else's personal prompt")

	_, ok = p.getDynamicPrompt(ctx, "sarah@example.com", analyst, "personal-g1", nil)
	assert.False(t, ok, "a global prompt is not reachable under the personal- prefix")

	_, ok = p.getDynamicPrompt(ctx, "sarah@example.com", analyst, "global-nope", nil)
	assert.False(t, ok, "an unknown name resolves to nothing")
}
