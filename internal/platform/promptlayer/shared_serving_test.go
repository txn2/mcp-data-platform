package promptlayer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// A prompt shared with the caller is visible under its bare name and renders
// for prompts/get, making it a real runnable prompt for the recipient. The
// legacy shared-<name> form keeps resolving through the deprecation window.
func TestSharedPrompt_VisibleAndRunnable(t *testing.T) {
	h, store := newTestHandle()
	// Sarah owns a personal prompt; it is shared with bob.
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopePersonal,
		OwnerEmail: "sarah@example.com", Content: "shared report {x}", Enabled: true,
	}
	h.shareStore = &stubShareLister{promptRefs: []portal.SharedPromptRef{
		{PromptID: "p1", ShareID: "s1", SharedBy: "sarah@example.com", Permission: portal.PermissionViewer},
	}}

	ctx := context.Background()
	// Bob (the recipient) sees it under its bare name.
	out := h.ListVisible(ctx, "bob@example.com", nil)
	names := map[string]bool{}
	for _, pr := range out {
		names[pr.Name] = true
	}
	assert.True(t, names["report"], "shared prompt visible to recipient under its bare name")
	assert.False(t, names["shared-report"], "no shared- prefixed duplicate is listed")

	// And it resolves for prompts/get, bare and via the legacy prefix.
	res, ok := h.GetByName(ctx, "bob@example.com", nil, "report", map[string]string{"x": "Y"})
	require.True(t, ok, "shared prompt resolves for the recipient by bare name")
	require.NotNil(t, res)
	res, ok = h.GetByName(ctx, "bob@example.com", nil, "shared-report", map[string]string{"x": "Y"})
	require.True(t, ok, "legacy shared- prefixed form still resolves")
	require.NotNil(t, res)

	// An anonymous caller cannot fetch it either way.
	_, ok = h.GetByName(ctx, "", nil, "report", nil)
	assert.False(t, ok, "anonymous caller cannot resolve a shared prompt")
	_, ok = h.GetByName(ctx, "", nil, "shared-report", nil)
	assert.False(t, ok, "anonymous caller cannot resolve the legacy shared- form")
}

// A prompt that was shared while personal but later promoted to a shared scope
// is no longer served via the shared- alias (it is served under global-/persona-),
// avoiding a duplicate.
func TestSharedPrompt_PromotedNotDoubleServed(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, // promoted away from personal
		OwnerEmail: "sarah@example.com", Content: "x", Enabled: true,
	}
	h.shareStore = &stubShareLister{promptRefs: []portal.SharedPromptRef{
		{PromptID: "p1", ShareID: "s1", SharedBy: "sarah@example.com", Permission: portal.PermissionViewer},
	}}

	ctx := context.Background()
	out := h.ListVisible(ctx, "bob@example.com", nil)
	for _, pr := range out {
		assert.NotEqual(t, "shared-report", pr.Name, "promoted prompt must not be served via shared- alias")
	}
	_, ok := h.getSharedPrompt(ctx, "bob@example.com", "report", nil)
	assert.False(t, ok, "promoted prompt not resolvable via shared- prefix")
}
