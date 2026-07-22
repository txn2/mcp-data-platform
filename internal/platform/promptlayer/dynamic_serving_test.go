package promptlayer

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

func TestListVisiblePrompts_ScopePrefixesAndScoping(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["g1"] = &prompt.Prompt{Name: "g1", Scope: prompt.ScopeGlobal, Enabled: true}
	store.prompts["pa"] = &prompt.Prompt{Name: "pa", Scope: prompt.ScopePersona, Personas: []string{"analyst"}, Enabled: true}
	store.prompts["pe"] = &prompt.Prompt{Name: "pe", Scope: prompt.ScopePersona, Personas: []string{"engineer"}, Enabled: true}
	store.prompts["mine"] = &prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com", Enabled: true}
	store.prompts["bob"] = &prompt.Prompt{Name: "bob", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com", Enabled: true}

	out := h.ListVisible(context.Background(), "sarah@example.com", []string{"analyst"})
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

func TestListVisiblePrompts_ExcludesSystemRows(t *testing.T) {
	// Ingested static prompts (source=system) are served under their bare name
	// via AddPrompt; they must not also appear as a global- prefixed entry.
	h, store := newTestHandle()
	store.prompts["g1"] = &prompt.Prompt{Name: "g1", Scope: prompt.ScopeGlobal, Source: prompt.SourceOperator, Enabled: true}
	store.prompts["sys"] = &prompt.Prompt{Name: "sys", Scope: prompt.ScopeGlobal, Source: prompt.SourceSystem, Enabled: true}

	out := h.ListVisible(context.Background(), "", nil)
	names := map[string]bool{}
	for _, pr := range out {
		names[pr.Name] = true
	}
	assert.True(t, names["global-g1"], "operator global prompt is served")
	assert.False(t, names["global-sys"], "system row must not be served as a global- duplicate")
}

func TestRegisterDatabasePrompts_SkipsSystemRows(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["op"] = &prompt.Prompt{Name: "op", Scope: prompt.ScopeGlobal, Source: prompt.SourceOperator, Enabled: true}
	store.prompts["sys"] = &prompt.Prompt{Name: "sys", Scope: prompt.ScopeGlobal, Source: prompt.SourceSystem, Enabled: true}

	h.registerDatabasePrompts()

	infos := map[string]bool{}
	h.promptInfosMu.RLock()
	for _, i := range h.promptInfos {
		infos[i.Name] = true
	}
	h.promptInfosMu.RUnlock()
	assert.True(t, infos["op"], "operator database prompt registered for admin listing")
	assert.False(t, infos["sys"], "system row must be skipped (already served via AddPrompt)")
}

func TestPromptServing_AnonymousIsFailClosed(t *testing.T) {
	// An anonymous caller (empty email, no personas) sees only globals and can
	// fetch only globals, never personal or persona prompts.
	h, store := newTestHandle()
	store.prompts["g"] = &prompt.Prompt{Name: "g", Scope: prompt.ScopeGlobal, Enabled: true}
	store.prompts["pa"] = &prompt.Prompt{Name: "pa", Scope: prompt.ScopePersona, Personas: []string{"analyst"}, Enabled: true}
	store.prompts["mine"] = &prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com", Enabled: true}

	out := h.ListVisible(context.Background(), "", nil)
	names := map[string]bool{}
	for _, pr := range out {
		names[pr.Name] = true
	}
	assert.True(t, names["global-g"], "anonymous sees globals")
	assert.False(t, names["analyst-pa"], "anonymous must not see persona prompts")
	assert.False(t, names["personal-mine"], "anonymous must not see personal prompts")
	assert.Len(t, out, 1, "anonymous list contains only the global prompt")

	_, ok := h.GetByName(context.Background(), "", nil, "personal-mine", nil)
	assert.False(t, ok, "anonymous cannot fetch a personal prompt")
	_, ok = h.GetByName(context.Background(), "", nil, "analyst-pa", nil)
	assert.False(t, ok, "anonymous cannot fetch a persona prompt")
	_, ok = h.GetByName(context.Background(), "", nil, "global-g", nil)
	assert.True(t, ok, "anonymous can fetch a global prompt")
}

func TestGetDynamicPrompt_ResolvesByPrefix(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["g1"] = &prompt.Prompt{Name: "g1", Scope: prompt.ScopeGlobal, Content: "global {x}", Enabled: true}
	store.prompts["pa"] = &prompt.Prompt{Name: "pa", Scope: prompt.ScopePersona, Personas: []string{"analyst"}, Content: "persona", Enabled: true}
	store.prompts["mine"] = &prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com", Content: "personal", Enabled: true}

	ctx := context.Background()
	analyst := []string{"analyst"}

	_, ok := h.GetByName(ctx, "sarah@example.com", analyst, "personal-mine", nil)
	assert.True(t, ok, "own personal prompt resolves via personal- prefix")

	res, ok := h.GetByName(ctx, "sarah@example.com", analyst, "global-g1", map[string]string{"x": "Y"})
	require.True(t, ok, "global prompt resolves via global- prefix")
	require.NotNil(t, res)

	_, ok = h.GetByName(ctx, "sarah@example.com", analyst, "analyst-pa", nil)
	assert.True(t, ok, "persona prompt resolves for a member via <persona>- prefix")

	_, ok = h.GetByName(ctx, "sarah@example.com", []string{"engineer"}, "analyst-pa", nil)
	assert.False(t, ok, "a non-member cannot resolve a persona prompt by its prefix")

	_, ok = h.GetByName(ctx, "bob@example.com", nil, "personal-mine", nil)
	assert.False(t, ok, "another user cannot resolve someone else's personal prompt")

	_, ok = h.GetByName(ctx, "sarah@example.com", analyst, "personal-g1", nil)
	assert.False(t, ok, "a global prompt is not reachable under the personal- prefix")

	_, ok = h.GetByName(ctx, "sarah@example.com", analyst, "global-nope", nil)
	assert.False(t, ok, "an unknown name resolves to nothing")
}

// A persona may literally be named "global" or "personal". The reserved-prefix
// branches of GetByName must fall through to persona resolution so such a
// persona's prompts remain fetchable.
func TestGetDynamicPrompt_ReservedPrefixPersonaName(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopePersona, Personas: []string{"global"},
		Content: "persona-global report", Enabled: true,
	}
	store.prompts["runbook"] = &prompt.Prompt{
		Name: "runbook", Scope: prompt.ScopePersona, Personas: []string{"personal"},
		Content: "persona-personal runbook", Enabled: true,
	}

	ctx := context.Background()
	_, ok := h.GetByName(ctx, "u@example.com", []string{"global"}, "global-report", nil)
	assert.True(t, ok, "a persona named 'global' resolves its prompt via fall-through")
	_, ok = h.GetByName(ctx, "u@example.com", []string{"personal"}, "personal-runbook", nil)
	assert.True(t, ok, "a persona named 'personal' resolves its prompt via fall-through")
}

// Personal prompts are excluded from the name-keyed runtime metadata (their
// names collide across owners), and unregistering by name must not drop an
// unrelated shared entry of the same name.
func TestRuntimePromptMetadata_ExcludesPersonal(t *testing.T) {
	h, _ := newTestHandle()
	h.RegisterRuntimePrompt(&prompt.Prompt{Name: "g", Scope: prompt.ScopeGlobal})
	h.RegisterRuntimePrompt(&prompt.Prompt{Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "a@x"})

	tracked := func() map[string]bool {
		names := map[string]bool{}
		h.promptInfosMu.RLock()
		defer h.promptInfosMu.RUnlock()
		for _, i := range h.promptInfos {
			names[i.Name] = true
		}
		return names
	}

	names := tracked()
	assert.True(t, names["g"], "global prompt is tracked")
	assert.False(t, names["mine"], "personal prompt is not tracked")

	h.RegisterRuntimePrompt(&prompt.Prompt{Name: "shared", Scope: prompt.ScopeGlobal})
	h.UnregisterRuntimePrompt("g")
	after := tracked()
	assert.False(t, after["g"], "unregister drops the named global entry")
	assert.True(t, after["shared"], "unrelated shared entries are retained")
}

// When persona and prompt names both contain hyphens a presented name can split
// more than one way; the most specific (longest) persona prefix must win
// deterministically.
func TestGetPersonaPrompt_LongestPrefixWins(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["engineer-report"] = &prompt.Prompt{
		Name: "engineer-report", Scope: prompt.ScopePersona, Personas: []string{"data"},
		Content: "data persona", Enabled: true,
	}
	store.prompts["report"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopePersona, Personas: []string{"data-engineer"},
		Content: "data-engineer persona", Enabled: true,
	}

	res, ok := h.GetByName(context.Background(), "u@example.com",
		[]string{"data", "data-engineer"}, "data-engineer-report", nil)
	require.True(t, ok)
	require.Len(t, res.Messages, 1)
	tc, isText := res.Messages[0].Content.(*mcp.TextContent)
	require.True(t, isText)
	assert.Equal(t, "data-engineer persona", tc.Text,
		"longest persona prefix (data-engineer) wins over data")
}
