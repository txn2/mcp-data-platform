package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/catalogfake"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/catalog"
)

// testCatalog returns a fake catalog holding the dataset testEntityURN names,
// with one column, amount.
func testCatalog() *catalogfake.Store {
	f := newFakeCatalog()
	f.AddDataset("acme", "test.table", "amount")
	return f
}

// testDatasetRef is the catalog reference testEntityURN resolves to.
const testDatasetRef = "mcp:dataset:(trino,acme,test.table)"

// appliedChangeset applies changes through the toolkit and returns the
// changeset it recorded.
func appliedChangeset(t *testing.T, tk *Toolkit, csStore *spyChangesetStore, changes ...ApplyChange) Changeset {
	t.Helper()
	res, _, err := tk.handleApplyKnowledge(ctxWithUser("u", "s", "admin"), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, Changes: changes, Confirm: true,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", res.Content)
	return csStore.Changesets[len(csStore.Changesets)-1]
}

func TestHandleRollback_MissingChangesetID(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil, applyKnowledgeInput{Action: "rollback"})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_ConfirmationRequired(t *testing.T) {
	tk, err := New(testName, &fullSpyStore{})
	require.NoError(t, err)
	tk.SetApplyConfig(ApplyConfig{Enabled: true, RequireConfirmation: true}, &spyChangesetStore{}, newFakeCatalog())

	result, _, callErr := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs1"})
	require.Nil(t, callErr)
	require.False(t, result.IsError)
	m := parseJSONResult(t, result)
	assert.Equal(t, true, m["confirmation_required"])
}

func TestHandleRollback_NotFound(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "missing"})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_EntityMismatch(t *testing.T) {
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, testCatalog())
	cs := appliedChangeset(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "pii"})
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: cs.ID, EntityURN: "mcp:dataset:(trino,acme,other)"})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_Success(t *testing.T) {
	store := &fullSpyStore{Insights: []Insight{{ID: "ins-1", Status: StatusPending}}}
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	tk := newApplyToolkit(t, store, csStore, cat)
	res, _, err := tk.handleApplyKnowledge(ctxWithUser("u", "s", "admin"), nil, applyKnowledgeInput{
		Action: "apply", EntityURN: testEntityURN, InsightIDs: []string{"ins-1"},
		Changes: []ApplyChange{{ChangeType: "add_tag", Detail: "pii"}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	cs := csStore.Changesets[0]

	result, _, err := tk.handleApplyKnowledge(ctxWithUser("admin-1", "s", "admin"), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: cs.ID, Confirm: true})
	require.Nil(t, err)
	require.False(t, result.IsError, "%v", result.Content)
	m := parseJSONResult(t, result)
	assert.Equal(t, cs.ID, m["changeset_id"])
	assert.Empty(t, cat.Aspects(catalog.DatasetRef("trino", "acme", "test", "table")).Tags)
	assert.True(t, csStore.Changesets[0].RolledBack)
	assert.Equal(t, StatusPending, store.Insights[0].Status)
	assert.Equal(t, []any{"ins-1"}, m["insights_returned_to_review"])
}

func TestHandleRollback_AlreadyRolledBack(t *testing.T) {
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, testCatalog())
	cs := appliedChangeset(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "pii"})
	csStore.Changesets[0].RolledBack = true
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: cs.ID, Confirm: true})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleRollback_Unrevertible(t *testing.T) {
	cs := Changeset{
		ID:        "cs1",
		TargetURN: testEntityURN,
		NewValue:  map[string]any{"change_0": changeEntry("raise_incident", "title", "desc")},
	}
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{Changesets: []Changeset{cs}}, testCatalog())
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: "cs1", Confirm: true})
	require.Nil(t, err)
	assert.True(t, result.IsError)
	assert.Contains(t, resultText(result), "applied to DataHub")
}

// A newer write of the same aspect blocks the rollback of the older change,
// names the newer change, and reverts nothing.
func TestHandleRollback_Conflict(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
	older := appliedChangeset(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "a"},
		ApplyChange{ChangeType: "update_description", Detail: "first"})
	appliedChangeset(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "b"})

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: older.ID, Confirm: true})
	require.Nil(t, err)
	require.True(t, result.IsError)
	assert.Contains(t, resultText(result), "rollback blocked")
	assert.Contains(t, resultText(result), "tags of "+testDatasetRef)
	a := cat.Aspects(catalog.DatasetRef("trino", "acme", "test", "table"))
	assert.Equal(t, []string{"a", "b"}, a.Tags)
	assert.Equal(t, "first", a.Description, "the description was not reverted either")
	assert.False(t, csStore.Changesets[0].RolledBack)
}

func TestHandleRollback_GenericError(t *testing.T) {
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, testCatalog())
	cs := appliedChangeset(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "pii"})
	csStore.RollbackErr = errStub

	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "rollback", ChangesetID: cs.ID, Confirm: true})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestRevertChangeset_StoreErrors(t *testing.T) {
	csStore := &spyChangesetStore{}
	cat := testCatalog()
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, cat)
	cs := appliedChangeset(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "pii"})
	cat.FailWrite = errStub
	_, err := RevertChangeset(context.Background(), RollbackDeps{Catalog: cat, Changesets: csStore, Insights: &fullSpyStore{}}, &cs, "admin")
	require.ErrorIs(t, err, errStub)
	assert.False(t, csStore.Changesets[0].RolledBack)

	_, err = RevertChangeset(context.Background(), RollbackDeps{Changesets: csStore}, &cs, "admin")
	require.Error(t, err, "no catalog to roll back in")
}

func TestHandleListChangesets_MissingEntityURN(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{}, newFakeCatalog())
	result, _, err := tk.handleApplyKnowledge(context.Background(), nil,
		applyKnowledgeInput{Action: "list_changesets"})
	require.Nil(t, err)
	assert.True(t, result.IsError)
}

func TestHandleListChangesets_Success(t *testing.T) {
	csStore := &spyChangesetStore{}
	tk := newApplyToolkit(t, &fullSpyStore{}, csStore, testCatalog())
	cs := appliedChangeset(t, tk, csStore, ApplyChange{ChangeType: "add_tag", Detail: "pii"})
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
	assert.Equal(t, cs.ID, entry["changeset_id"])
	assert.Equal(t, testDatasetRef, entry["target_urn"])
	assert.Equal(t, false, entry["rolled_back"])
	assert.Equal(t, testDatasetRef, csStore.LastFilter.EntityURN,
		"a DataHub dataset URN lists the changesets recorded under its catalog reference")
}

func TestHandleListChangesets_StoreError(t *testing.T) {
	tk := newApplyToolkit(t, &fullSpyStore{}, &spyChangesetStore{ListErr: errStub}, newFakeCatalog())
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
	writer := testCatalog()
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
			{"change_type": "add_glossary_term", "target": "", "detail": "Revenue"},
		},
	})
	changesetID, ok := applyResp["changeset_id"].(string)
	require.True(t, ok, "apply response must carry changeset_id")
	require.NotEmpty(t, changesetID)
	ds := catalog.DatasetRef("trino", "acme", "test", "table")
	require.Len(t, writer.Aspects(ds).GlossaryTerms, 1, "apply attached the term")

	// 2. list_changesets surfaces it, under the catalog reference
	listResp := callTool(ctx, t, clientSess, map[string]any{
		"action": "list_changesets", "entity_urn": testDatasetRef,
	})
	list, ok := listResp["changesets"].([]any)
	require.True(t, ok)
	require.Len(t, list, 1)
	first, ok := list[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, changesetID, first["changeset_id"])
	assert.Equal(t, true, first["revertible"])

	// 3. rollback reverts the add and records it
	rbResp := callTool(ctx, t, clientSess, map[string]any{
		"action": "rollback", "changeset_id": changesetID, "confirm": true,
	})
	assert.Equal(t, changesetID, rbResp["changeset_id"])
	assert.Empty(t, writer.Aspects(ds).GlossaryTerms, "rollback removed the term")
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
