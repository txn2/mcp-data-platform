package platform

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// A prompt shared with the caller is visible as shared-<name> and renders for
// prompts/get, making it a real runnable prompt for the recipient.
func TestSharedPrompt_VisibleAndRunnable(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	// Sarah owns a personal prompt; it is shared with bob.
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopePersonal,
		OwnerEmail: "sarah@example.com", Content: "shared report {x}", Enabled: true,
	}
	p.portalShareStore = &stubShareStore{promptRefs: []portal.SharedPromptRef{
		{PromptID: "p1", ShareID: "s1", SharedBy: "sarah@example.com", Permission: portal.PermissionViewer},
	}}

	ctx := context.Background()
	// Bob (the recipient) sees it as shared-report.
	out := p.listVisiblePrompts(ctx, "bob@example.com", nil)
	names := map[string]bool{}
	for _, pr := range out {
		names[pr.Name] = true
	}
	assert.True(t, names["shared-report"], "shared prompt visible to recipient as shared-<name>")

	// And it resolves for prompts/get.
	res, ok := p.getDynamicPrompt(ctx, "bob@example.com", nil, "shared-report", map[string]string{"x": "Y"})
	require.True(t, ok, "shared prompt resolves for the recipient")
	require.NotNil(t, res)

	// An anonymous caller cannot fetch it.
	_, ok = p.getDynamicPrompt(ctx, "", nil, "shared-report", nil)
	assert.False(t, ok, "anonymous caller cannot resolve a shared prompt")
}

// A prompt that was shared while personal but later promoted to a shared scope
// is no longer served via the shared- alias (it is served under global-/persona-),
// avoiding a duplicate.
func TestSharedPrompt_PromotedNotDoubleServed(t *testing.T) {
	p, store := newTestPlatformWithPromptStore()
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, // promoted away from personal
		OwnerEmail: "sarah@example.com", Content: "x", Enabled: true,
	}
	p.portalShareStore = &stubShareStore{promptRefs: []portal.SharedPromptRef{
		{PromptID: "p1", ShareID: "s1", SharedBy: "sarah@example.com", Permission: portal.PermissionViewer},
	}}

	ctx := context.Background()
	out := p.listVisiblePrompts(ctx, "bob@example.com", nil)
	for _, pr := range out {
		assert.NotEqual(t, "shared-report", pr.Name, "promoted prompt must not be served via shared- alias")
	}
	_, ok := p.getSharedPrompt(ctx, "bob@example.com", "report", nil)
	assert.False(t, ok, "promoted prompt not resolvable via shared- prefix")
}
