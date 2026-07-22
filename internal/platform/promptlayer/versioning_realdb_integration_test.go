//go:build integration

package promptlayer

// The assembled-system proof for the #1009 acceptance criterion: editing an
// approved global prompt's content through the real manage_prompt tool does
// NOT change what other users are served over the real prompts/get path — the
// served content is the approved snapshot, not the draft — until the draft
// version is approved, after which the new content serves with its version
// and approval provenance in _meta. The stack is real end to end: a pgvector
// Postgres store behind the notifying wrapper, an mcp.Server with the
// prompt-visibility middleware, and in-memory client sessions.

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	pgstore "github.com/txn2/mcp-data-platform/pkg/prompt/postgres"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

func TestVersioning_RealDB_DraftNotServedUntilApproved(t *testing.T) {
	db := testdb.New(t)
	h := New(Config{Store: pgstore.New(db), AdminPersona: "admin", Registry: registry.NewRegistry()})
	ctx := context.Background()

	// Seed an approved global prompt through the real store.
	seed := &prompt.Prompt{
		Name: "daily-sales-report", DisplayName: "Daily Sales Report",
		Content: "approved body", Scope: prompt.ScopeGlobal,
		Source: prompt.SourceOperator, Enabled: true, OwnerEmail: "jane@example.com",
	}
	require.NoError(t, h.Store().Create(ctx, seed))
	loaded, err := h.Store().Get(ctx, "daily-sales-report")
	require.NoError(t, err)
	require.NoError(t, loaded.ApplyStatusTransition(prompt.StatusApproved, "", "jane@example.com", true, time.Now().UTC()))
	require.NoError(t, h.Store().Update(ctx, loaded))

	getServed := func() (*mcp.GetPromptResult, string) {
		session, cleanup := connectServingClient(t, h, "bob@example.com")
		defer cleanup()
		res, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: "global-daily-sales-report"})
		require.NoError(t, err)
		tc, ok := res.Messages[0].Content.(*mcp.TextContent)
		require.True(t, ok)
		return res, tc.Text
	}

	res, text := getServed()
	assert.Equal(t, "approved body", text)
	assert.EqualValues(t, 1, res.Meta["prompt_version"])
	assert.Equal(t, "jane@example.com", res.Meta["prompt_approved_by"])

	// An admin edits the content through the real manage_prompt handler: the
	// edit is deferred as a pending draft version.
	toolRes, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{
		Command: "update", Name: "daily-sales-report", Content: "draft body",
	})
	require.NoError(t, err)
	require.False(t, toolRes.IsError, resultText(toolRes))
	assert.Contains(t, resultText(toolRes), "pending_approval")

	// THE acceptance criterion: another user is still served the approved
	// snapshot, not the draft.
	res, text = getServed()
	assert.Equal(t, "approved body", text, "the draft must not be served before approval")
	assert.EqualValues(t, 1, res.Meta["prompt_version"])

	// Approve the draft through the store's versioning capability (the same
	// call the admin REST endpoint makes).
	vs, ok := h.Store().(prompt.VersionStore)
	require.True(t, ok, "the wrapped store preserves the versioning capability")
	updated, err := vs.ApproveVersion(ctx, loaded.ID, 2, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, "draft body", updated.Content)

	// The approved snapshot now serves, with its provenance updated.
	res, text = getServed()
	assert.Equal(t, "draft body", text, "the approved version is served after approval")
	assert.EqualValues(t, 2, res.Meta["prompt_version"])
	assert.Equal(t, "admin@example.com", res.Meta["prompt_approved_by"])
	assert.NotEmpty(t, res.Meta["prompt_approved_at"])
}
