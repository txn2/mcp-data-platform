package knowledge

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// errTextOf returns the text body of a tool result.
func errTextOf(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, result.Content)
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected *mcp.TextContent")
	return tc.Text
}

// A deployment with no DataHub connection resolves the noop writer, whose writes
// return nil having reached nothing. These tests pin the refusal that keeps such a
// deployment from reporting catalog writes it never performed.

// spyPromptCreator records prompts created through the add_prompt change type, the
// one apply change that does not write to DataHub.
type spyPromptCreator struct {
	created []*prompt.Prompt
}

func (s *spyPromptCreator) Create(_ context.Context, p *prompt.Prompt) error {
	s.created = append(s.created, p)
	return nil
}

func (*spyPromptCreator) RegisterRuntimePrompt(*prompt.Prompt) {}

func TestDataHubWritable(t *testing.T) {
	assert.False(t, datahubWritable(nil), "nil writer reaches no DataHub")
	assert.False(t, datahubWritable(&NoopDataHubWriter{}), "noop writer reaches no DataHub")
	assert.True(t, datahubWritable(&spyWriter{}), "a real writer reaches DataHub")
}

func TestHandleApply_NoDataHubConnection_Refuses(t *testing.T) {
	store := &fullSpyStore{}
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, store, csStore, &NoopDataHubWriter{})

	result, _, callErr := tk.handleApplyKnowledge(
		ctxWithUser("admin-1", "sess-1", "admin"), nil,
		applyKnowledgeInput{
			Action:    actionApply,
			EntityURN: testEntityURN,
			Changes: []ApplyChange{
				{ChangeType: "update_description", Detail: "New description"},
				{ChangeType: "add_tag", Detail: "important"},
			},
			Confirm: true,
		})
	require.Nil(t, callErr)
	require.True(t, result.IsError, "apply must refuse when no DataHub connection is configured")

	msg := errTextOf(t, result)
	assert.Contains(t, msg, "update_description", "names the blocked change types")
	assert.Contains(t, msg, "add_tag")
	assert.Contains(t, msg, "knowledge.apply.datahub_connection", "names the way to enable catalog writes")
	assert.Contains(t, msg, "knowledge_page", "names the sink that works without a catalog")

	assert.Empty(t, csStore.Changesets, "a refused apply records no changeset")
	assert.Empty(t, store.MarkAppliedCalls, "a refused apply marks no insight applied")
}

func TestHandleApply_NoDataHubConnection_AllowsAddPromptOnly(t *testing.T) {
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, &NoopDataHubWriter{})
	creator := &spyPromptCreator{}
	tk.SetPromptCreator(creator)

	result, _, callErr := tk.handleApplyKnowledge(
		ctxWithUser("admin-1", "sess-1", "admin"), nil,
		applyKnowledgeInput{
			Action:    actionApply,
			EntityURN: testEntityURN,
			Changes: []ApplyChange{
				{ChangeType: string(actionAddPrompt), Target: "weekly-revenue-check", Detail: "Check weekly revenue."},
			},
			Confirm: true,
		})
	require.Nil(t, callErr)
	require.False(t, result.IsError, "add_prompt creates a platform prompt, not a DataHub write: %v", result.Content)
	require.Len(t, creator.created, 1)
	assert.Equal(t, "weekly-revenue-check", creator.created[0].Name)
}

func TestRevertChangeset_NoDataHubConnection_Refuses(t *testing.T) {
	cs := baseChangeset("cs1",
		map[string]any{"change_0": changeEntry("add_tag", "", "urn:li:tag:new")},
		map[string]any{"tags": []any{}},
	)
	cs.SourceInsightIDs = []string{"ins-1"}
	store := seededStore(cs)
	insights := &fullSpyStore{Insights: []Insight{{ID: "ins-1", Status: StatusApplied}}}

	res, err := RevertChangeset(context.Background(),
		RollbackDeps{Writer: &NoopDataHubWriter{}, Changesets: store, Insights: insights}, cs, "admin")
	require.ErrorIs(t, err, ErrDataHubUnavailable)
	assert.Nil(t, res)
	assert.False(t, store.Changesets[0].RolledBack, "a refused rollback must not mark the changeset rolled back")
	assert.Equal(t, StatusApplied, insights.Insights[0].Status, "the source insight keeps its applied status")
}

func TestRevertPageChangeset_NoDataHubConnection_Succeeds(t *testing.T) {
	cs := pageChangeset("seasons", changeCreatePage, 1, map[string]any{})
	pw := newFakePageWriter()
	pw.pages["seasons"] = &knowledgepage.Page{ID: "kp1", Slug: "seasons", CurrentVersion: 1}
	csStore := &spyChangesetStore{Changesets: []Changeset{cs}}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, &NoopDataHubWriter{})
	tk.SetPageWriter(pw)

	res, _, err := tk.handleApplyKnowledge(pageCtx(), &mcp.CallToolRequest{},
		applyKnowledgeInput{Action: actionRollback, ChangesetID: "cs1", Confirm: true})
	require.NoError(t, err)
	require.False(t, res.IsError, "knowledge-page rollback needs no DataHub: %v", res.Content)
	assert.Contains(t, pw.deleted, "kp1")
	assert.True(t, csStore.Changesets[0].RolledBack)
}
