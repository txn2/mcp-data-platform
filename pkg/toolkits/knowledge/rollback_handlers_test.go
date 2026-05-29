package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func termApplyChangeset(id, termURN string) Changeset {
	return Changeset{
		ID:            id,
		TargetURN:     testEntityURN,
		ChangeType:    "add_glossary_term",
		CreatedAt:     time.Date(2026, 5, 10, 0, 0, 0, 0, time.UTC),
		PreviousValue: map[string]any{"glossary_terms": []any{}},
		NewValue:      map[string]any{"change_0": changeEntry("add_glossary_term", "", termURN)},
	}
}

func TestHandleRollback_MissingChangesetID(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "rollback"})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_ConfirmationRequired(t *testing.T) {
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, &spyWriter{})

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs1"})
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	m := parseJSONResult(t, result)
	assert.Equal(t, true, m["confirmation_required"])
}

func TestHandleRollback_NotFound(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "missing"})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_EntityMismatch(t *testing.T) {
	cs := termApplyChangeset("cs1", "urn:li:glossaryTerm:x")
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}}, &spyWriter{})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs1", EntityURN: "urn:li:dataset:other"})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_Success(t *testing.T) {
	cs := termApplyChangeset("cs1", "urn:li:glossaryTerm:x")
	cs.SourceInsightIDs = []string{"ins-1"}
	store := &fullSpyStore{Insights: []Insight{{ID: "ins-1", Status: StatusApplied}}}
	csStore := &spyChangesetStore{Changesets: []Changeset{cs}}
	writer := &spyWriter{}
	tk := newApplyToolkit(t, store, csStore, writer)

	result, _, err := tk.handleApplyKnowledge(ctxWithUser("admin-1", "s", "admin"), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs1", Confirm: true})
	require.Nil(t, err)
	require.False(t, result.IsError)
	m := parseJSONResult(t, result)
	assert.Equal(t, "cs1", m["changeset_id"])
	require.Len(t, writer.WriteCalls, 1)
	assert.Equal(t, "RemoveGlossaryTerm", writer.WriteCalls[0].Method)
	assert.True(t, csStore.Changesets[0].RolledBack)
	assert.Equal(t, StatusRolledBack, store.Insights[0].Status)
}

func TestHandleRollback_AlreadyRolledBack(t *testing.T) {
	cs := termApplyChangeset("cs1", "urn:li:glossaryTerm:x")
	cs.RolledBack = true
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}}, &spyWriter{})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs1", Confirm: true})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_Unrevertible(t *testing.T) {
	cs := Changeset{
		ID:        "cs1",
		TargetURN: testEntityURN,
		NewValue:  map[string]any{"change_0": changeEntry("raise_incident", "title", "desc")},
	}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}}, &spyWriter{})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs1", Confirm: true})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_Conflict(t *testing.T) {
	cs := termApplyChangeset("cs-old", "urn:li:glossaryTerm:a")
	newer := termApplyChangeset("cs-new", "urn:li:glossaryTerm:b")
	newer.CreatedAt = cs.CreatedAt.Add(time.Hour)
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs, newer}}, &spyWriter{})

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs-old", Confirm: true})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_GenericError(t *testing.T) {
	// Reverting succeeds but recording the rollback fails -> generic error branch.
	cs := termApplyChangeset("cs1", "urn:li:glossaryTerm:x")
	tk := newApplyToolkit(t, &fullSpyStore{},
		&spyChangesetStore{Changesets: []Changeset{cs}, RollbackErr: errStub}, &spyWriter{})

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs1", Confirm: true})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestRevertChangeset_WriterErrors(t *testing.T) {
	cases := []struct {
		name   string
		change map[string]any
		prev   map[string]any
	}{
		{"description", changeEntry("update_description", "", "new"), map[string]any{"description": "old"}},
		{"tag", changeEntry("add_tag", "", "pii"), map[string]any{"tags": []any{}}},
		{"documentation", changeEntry("add_documentation", "https://x", "d"), map[string]any{}},
		{"restore_removed_tag", changeEntry("remove_tag", "", "pii"), map[string]any{"tags": []any{normalizeTagURN("pii")}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := baseChangeset("cs1", map[string]any{"change_0": tc.change}, tc.prev)
			_, err := RevertChangeset(context.Background(), RollbackDeps{Writer: &spyWriter{FailAtCall: 1}, Changesets: seededStore(cs), Insights: &fullSpyStore{}}, cs, "admin")
			require.Error(t, err)
		})
	}
}

func TestHandleListChangesets_MissingEntityURN(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, &spyWriter{})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "list_changesets"})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleListChangesets_Success(t *testing.T) {
	cs := termApplyChangeset("cs1", "urn:li:glossaryTerm:x")
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}}, &spyWriter{})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "list_changesets", EntityURN: testEntityURN})
	require.Nil(t, err)
	require.False(t, result.IsError)
	m := parseJSONResult(t, result)
	assert.EqualValues(t, 1, m["total"])
	list, ok := m["changesets"].([]any)
	require.True(t, ok)
	require.Len(t, list, 1)
	entry, ok := list[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "cs1", entry["changeset_id"])
	assert.Equal(t, false, entry["rolled_back"])
}

func TestHandleListChangesets_StoreError(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{ListErr: errStub}, &spyWriter{})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "list_changesets", EntityURN: testEntityURN})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestValidateAction_RollbackAndList(t *testing.T) {
	assert.NoError(t, ValidateAction("rollback"))
	assert.NoError(t, ValidateAction("list_changesets"))
}

var errStub = errors.New("stub store failure")

// TestApplyRollbackEndToEnd exercises apply -> list_changesets -> rollback through
// a real mcp.Server and in-memory client session, proving the new actions are
// registered and that an apply's changeset is discoverable and revertible end to
// end (not just that the handler functions work in isolation).
func TestApplyRollbackEndToEnd(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "ins-1", Status: StatusPending, EntityURNs: []string{testEntityURN}}}}
	csStore := &spyChangesetStore{}
	writer := &spyWriter{Metadata: &EntityMetadata{GlossaryTerms: []string{}, Tags: []string{}, Owners: []string{}}}
	tk := newApplyToolkit(t, store, csStore, writer)

	s := mcp.NewServer(&mcp.Implementation{Name: testName, Version: testVersion}, nil)
	tk.RegisterTools(s)

	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	serverSess, err := s.Connect(ctx, t1, nil)
	require.NoError(t, err)
	defer func() { _ = serverSess.Close() }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	clientSess, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer func() { _ = clientSess.Close() }()

	// 1. apply add_glossary_term
	applyResp := callTool(ctx, t, clientSess, map[string]any{
		"action":     "apply",
		"entity_urn": testEntityURN,
		"changes": []map[string]any{
			{"change_type": "add_glossary_term", "target": "", "detail": "urn:li:glossaryTerm:e2e"},
		},
	})
	changesetID, ok := applyResp["changeset_id"].(string)
	require.True(t, ok, "apply response must carry changeset_id")
	require.NotEmpty(t, changesetID)

	// 2. list_changesets surfaces it
	listResp := callTool(ctx, t, clientSess, map[string]any{
		"action": "list_changesets", "entity_urn": testEntityURN,
	})
	list, ok := listResp["changesets"].([]any)
	require.True(t, ok)
	require.Len(t, list, 1)
	first, ok := list[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, changesetID, first["changeset_id"])

	// 3. rollback reverts the add and records it
	rbResp := callTool(ctx, t, clientSess, map[string]any{
		"action": "rollback", "changeset_id": changesetID, "confirm": true,
	})
	assert.Equal(t, changesetID, rbResp["changeset_id"])

	require.Len(t, writer.WriteCalls, 2, "one AddGlossaryTerm on apply, one RemoveGlossaryTerm on rollback")
	assert.Equal(t, "AddGlossaryTerm", writer.WriteCalls[0].Method)
	assert.Equal(t, "RemoveGlossaryTerm", writer.WriteCalls[1].Method)
	require.Len(t, csStore.Changesets, 1)
	assert.True(t, csStore.Changesets[0].RolledBack)
}

// callTool invokes apply_knowledge through the client session and returns the
// decoded JSON result, failing the test on a tool error.
func callTool(ctx context.Context, t *testing.T, sess *mcp.ClientSession, args map[string]any) map[string]any {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: applyToolName, Arguments: args})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool returned error: %v", res.Content)
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var m map[string]any
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &m))
	return m
}
