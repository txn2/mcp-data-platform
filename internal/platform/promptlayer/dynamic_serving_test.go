package promptlayer

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// promptText extracts the single text message of a rendered prompt result.
func promptText(t *testing.T, res *mcp.GetPromptResult) string {
	t.Helper()
	require.NotNil(t, res)
	require.Len(t, res.Messages, 1)
	tc, ok := res.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok)
	return tc.Text
}

func TestListVisiblePrompts_BareNamesAndScoping(t *testing.T) {
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

	// Sarah (an analyst) sees globals, her persona's prompts, and her own
	// personal, every one under its bare stored name (no scope prefixes).
	assert.True(t, names["g1"], "global prompt visible under its bare name")
	assert.True(t, names["pa"], "her persona's prompt visible under its bare name")
	assert.True(t, names["mine"], "her personal prompt visible under its bare name")
	assert.Len(t, out, 3, "no prefixed duplicates are served")
	// Not another persona's prompt, nor another user's personal prompt.
	assert.False(t, names["pe"], "a non-member persona prompt must not be visible")
	assert.False(t, names["bob"], "another user's personal prompt must not be visible")
}

// A personal prompt shadowing a same-named global: the personal wins the bare
// name for its owner, and the global stays distinctly visible under its legacy
// qualified name with an annotated description, resolvable on prompts/get.
func TestListVisiblePrompts_ShadowedGlobalStaysVisibleQualified(t *testing.T) {
	h, store := newTestHandle()
	// The mock store's Get is keyed by map key, so the globally-unique shared
	// namespace entry uses the bare name; the personal row (found by iteration
	// in GetPersonal/List) uses a distinct key.
	store.prompts["report"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopeGlobal, Content: "global body",
		Description: "the global one", Enabled: true,
	}
	store.prompts["report:sarah"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "personal body", DisplayName: "My Report", Enabled: true,
		Arguments: []prompt.Argument{{Name: "topic", Description: "what", Required: true}},
	}

	out := h.ListVisible(context.Background(), "sarah@example.com", nil)
	byName := map[string]*mcp.Prompt{}
	for _, pr := range out {
		byName[pr.Name] = pr
	}
	require.Len(t, out, 2, "both prompts are listed, never silently hidden")
	require.NotNil(t, byName["report"], "personal prompt wins the bare name")
	assert.Equal(t, "My Report", byName["report"].Title, "title carries the display name")
	require.NotNil(t, byName["global-report"], "shadowed global is listed under its qualified name")
	assert.Contains(t, byName["global-report"].Description, "shadowed",
		"shadowed entry is annotated, not silently renamed")

	// prompts/get agrees with the list: bare resolves the personal, the
	// qualified name resolves the shadowed global.
	res, ok := h.GetByName(context.Background(), "sarah@example.com", nil, "report", nil)
	require.True(t, ok)
	assert.Equal(t, "personal body", promptText(t, res))
	res, ok = h.GetByName(context.Background(), "sarah@example.com", nil, "global-report", nil)
	require.True(t, ok)
	assert.Equal(t, "global body", promptText(t, res))

	// A viewer without the personal prompt still gets the global under its
	// bare name: promotion/shadowing is per-viewer, not global renaming.
	out = h.ListVisible(context.Background(), "bob@example.com", nil)
	require.Len(t, out, 1)
	assert.Equal(t, "report", out[0].Name, "unshadowed viewer sees the global bare")
	res, ok = h.GetByName(context.Background(), "bob@example.com", nil, "report", nil)
	require.True(t, ok)
	assert.Equal(t, "global body", promptText(t, res))
}

// A bare name owned by the static registry is never claimed by a database
// prompt: the entry is served qualified and bare prompts/get declines so the
// built-in keeps serving it.
func TestListVisiblePrompts_StaticNameKeepsBareAuthority(t *testing.T) {
	h, store := newTestHandle()
	h.promptInfos = []registry.PromptInfo{{Name: "explore-available-data", Content: "builtin"}}
	h.snapshotStaticNames()
	store.prompts["p1"] = &prompt.Prompt{
		Name: "explore-available-data", Scope: prompt.ScopePersonal,
		OwnerEmail: "sarah@example.com", Content: "impostor", Enabled: true,
	}

	out := h.ListVisible(context.Background(), "sarah@example.com", nil)
	require.Len(t, out, 1)
	assert.Equal(t, "personal-explore-available-data", out[0].Name,
		"personal prompt colliding with a built-in is served qualified")

	_, ok := h.GetByName(context.Background(), "sarah@example.com", nil, "explore-available-data", nil)
	assert.False(t, ok, "bare built-in name is declined so the static registry serves it")
	res, ok := h.GetByName(context.Background(), "sarah@example.com", nil, "personal-explore-available-data", nil)
	require.True(t, ok, "the qualified name still resolves the personal prompt")
	assert.Equal(t, "impostor", promptText(t, res))
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
	assert.True(t, names["g1"], "operator global prompt is served under its bare name")
	assert.False(t, names["sys"], "system row must not be served as a duplicate")
	assert.Len(t, out, 1)
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
	assert.True(t, names["g"], "anonymous sees globals under bare names")
	assert.False(t, names["pa"], "anonymous must not see persona prompts")
	assert.False(t, names["mine"], "anonymous must not see personal prompts")
	assert.Len(t, out, 1, "anonymous list contains only the global prompt")

	_, ok := h.GetByName(context.Background(), "", nil, "mine", nil)
	assert.False(t, ok, "anonymous cannot fetch a personal prompt by bare name")
	_, ok = h.GetByName(context.Background(), "", nil, "pa", nil)
	assert.False(t, ok, "anonymous cannot fetch a persona prompt by bare name")
	_, ok = h.GetByName(context.Background(), "", nil, "g", nil)
	assert.True(t, ok, "anonymous can fetch a global prompt by bare name")
	_, ok = h.GetByName(context.Background(), "", nil, "global-g", nil)
	assert.True(t, ok, "the legacy global- prefixed form still resolves")
}

// Clients that learned the pre-bare-name prefixed forms (global-, personal-,
// <persona>-) keep resolving them through the deprecation window.
func TestGetDynamicPrompt_LegacyPrefixCompat(t *testing.T) {
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

// A stored name that literally equals another prompt's qualified fallback name
// ("global-x" while a global "x" is shadowed): the qualified entry is omitted
// because bare resolution wins on prompts/get, so listing it would duplicate a
// name that resolves to a different prompt.
func TestListVisiblePrompts_QualifiedNameClaimedBareIsNotDuplicated(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["x"] = &prompt.Prompt{
		Name: "x", Scope: prompt.ScopeGlobal, Content: "global body", Enabled: true,
	}
	store.prompts["x:sarah"] = &prompt.Prompt{
		Name: "x", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "personal x", Enabled: true,
	}
	store.prompts["global-x:sarah"] = &prompt.Prompt{
		Name: "global-x", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "personal global-x", Enabled: true,
	}

	out := h.ListVisible(context.Background(), "sarah@example.com", nil)
	counts := map[string]int{}
	for _, pr := range out {
		counts[pr.Name]++
	}
	assert.Equal(t, 1, counts["x"], "personal x wins the bare name once")
	assert.Equal(t, 1, counts["global-x"], "no duplicate global-x entries")
	require.Len(t, out, 2)

	// prompts/get agrees with the list on every listed name.
	res, ok := h.GetByName(context.Background(), "sarah@example.com", nil, "x", nil)
	require.True(t, ok)
	assert.Equal(t, "personal x", promptText(t, res))
	res, ok = h.GetByName(context.Background(), "sarah@example.com", nil, "global-x", nil)
	require.True(t, ok)
	assert.Equal(t, "personal global-x", promptText(t, res),
		"the stored bare name outranks the legacy-prefix reading")
}

// Two shadowed prompts can collide on one qualified name when a persona is
// literally named after a reserved prefix token: a prompt of persona "shared"
// named "report" qualifies as "shared-report", the same fallback name as a
// prompt named "report" shared user-to-user with the caller. (A global and a
// persona prompt can never collide this way: they live in one jointly-unique
// namespace.) The entry kept is the one the legacy prefix resolution serves,
// so the list and prompts/get agree.
func TestListVisiblePrompts_QualifiedCollisionFollowsLegacyResolution(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report:persona"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopePersona, Personas: []string{"shared"},
		Content: "persona body", Enabled: true,
	}
	store.prompts["report:eve"] = &prompt.Prompt{
		ID: "e1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "eve@example.com",
		Content: "eve body", Enabled: true,
	}
	store.prompts["report:sarah"] = &prompt.Prompt{
		Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "personal body", Enabled: true,
	}
	h.shareStore = &stubShareLister{promptRefs: []portal.SharedPromptRef{
		{PromptID: "e1", ShareID: "s1", SharedBy: "eve@example.com", Permission: portal.PermissionViewer},
	}}

	out := h.ListVisible(context.Background(), "sarah@example.com", []string{"shared"})
	counts := map[string]int{}
	for _, pr := range out {
		counts[pr.Name]++
	}
	assert.Equal(t, 1, counts["report"], "personal wins the bare name once")
	assert.Equal(t, 1, counts["shared-report"], "one qualified entry survives the collision")
	require.Len(t, out, 2)

	// prompts/get on the surviving qualified name serves what legacy prefix
	// resolution serves: the user-to-user share (the shared- cut runs before
	// the persona fall-through).
	res, ok := h.GetByName(context.Background(), "sarah@example.com", []string{"shared"}, "shared-report", nil)
	require.True(t, ok)
	assert.Equal(t, "eve body", promptText(t, res))
}

// qualifiedPreference pins the legacy-resolution ordering contract: on a
// qualified-name collision the kept entry must match getLegacyPrefixed's cut
// order (personal-, then global-, then shared-, then the persona fall-through).
func TestQualifiedPreference_MatchesLegacyCutOrder(t *testing.T) {
	assert.Greater(t, qualifiedPreference(rankPersonal), qualifiedPreference(rankGlobal))
	assert.Greater(t, qualifiedPreference(rankGlobal), qualifiedPreference(rankShared))
	assert.Greater(t, qualifiedPreference(rankShared), qualifiedPreference(rankPersona))
}
