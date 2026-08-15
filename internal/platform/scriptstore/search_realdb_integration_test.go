//go:build integration

package scriptstore

// The real-schema proof for #1302. The ranking query calls script_fts() with
// the exact argument list migration 000102 builds its GIN index on, and that
// function composes an IMMUTABLE expression over a JSONB column — none of which
// sqlmock can check. These tests run the search and contract paths against the
// migrated schema, so the index expression, the tsquery match, the visibility
// predicate, and the lifecycle filter all get a vote.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// seedScript writes one script with the given identity and searchable text.
func seedScript(t *testing.T, s *Store, name, scope, owner string, personas []string, params []script.Param) *script.Script {
	t.Helper()
	sc := &script.Script{
		Name: name, DisplayName: "Daily Sales Report",
		Description: "Summarize yesterday's revenue by region",
		Source:      "print(1)\n", Scope: scope, Personas: personas, OwnerEmail: owner,
		Params: params, Enabled: true, Status: script.StatusDraft,
		Tags: []string{"revenue"},
	}
	require.NoError(t, s.Create(context.Background(), sc, testAuthor))
	return sc
}

// TestRealDB_SearchRanksOnTheIndexedDocument proves the composed document is
// what a caller matches against: the title, the description, the tags, and the
// parameter contract. The parameter arm is the one sqlmock could never check —
// it reads a JSONB array inside an IMMUTABLE function the index is built on.
func TestRealDB_SearchRanksOnTheIndexedDocument(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	seedScript(t, s, "daily-sales", script.ScopeGlobal, "jane@example.com", nil,
		[]script.Param{{
			Name: "report_date", Type: script.ParamTypeDate, Required: true,
			Description: "The business date to report on",
		}})

	for _, intent := range []string{"revenue by region", "daily sales report", "report_date", "business date"} {
		got, err := s.Search(ctx, script.SearchQuery{QueryText: intent})
		require.NoError(t, err, intent)
		require.Len(t, got, 1, "intent %q should match the seeded script", intent)
		assert.Greater(t, got[0].Score, 0.0, "a match must carry a positive relevance score")
	}

	none, err := s.Search(ctx, script.SearchQuery{QueryText: "kubernetes ingress"})
	require.NoError(t, err)
	assert.Empty(t, none, "an unrelated intent must match nothing")
}

// TestRealDB_SearchAppliesVisibility proves the scope predicate runs in SQL:
// another owner's personal script is absent, a persona script appears only for
// a member, and global scripts reach everyone.
func TestRealDB_SearchAppliesVisibility(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	seedScript(t, s, "global-report", script.ScopeGlobal, "admin@example.com", nil, nil)
	seedScript(t, s, "analyst-report", script.ScopePersona, "admin@example.com", []string{"analyst"}, nil)
	seedScript(t, s, "janes-report", script.ScopePersonal, "jane@example.com", nil, nil)
	seedScript(t, s, "bobs-report", script.ScopePersonal, "bob@example.com", nil, nil)

	names := func(q script.SearchQuery) []string {
		got, err := s.Search(ctx, q)
		require.NoError(t, err)
		out := make([]string, 0, len(got))
		for _, sc := range got {
			out = append(out, sc.Script.Name)
		}
		return out
	}

	jane := names(script.SearchQuery{QueryText: "revenue by region", OwnerEmail: "jane@example.com", Personas: []string{"analyst"}})
	assert.ElementsMatch(t, []string{"global-report", "analyst-report", "janes-report"}, jane,
		"a caller sees global scripts, their persona's, and their own — never another owner's personal script")

	anon := names(script.SearchQuery{QueryText: "revenue by region"})
	assert.Equal(t, []string{"global-report"}, anon,
		"a caller with no identity and no persona membership sees only global scripts")

	nonMember := names(script.SearchQuery{QueryText: "revenue by region", OwnerEmail: "bob@example.com", Personas: []string{"engineer"}})
	assert.ElementsMatch(t, []string{"global-report", "bobs-report"}, nonMember)
}

// TestRealDB_SearchExcludesDeadEnds proves the lifecycle filter: a disabled
// script and a retired one are not ranked, while a draft is — an unapproved
// script is a solved process waiting for a reviewer, and the contract says
// plainly that nothing will execute it.
func TestRealDB_SearchExcludesDeadEnds(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	draft := seedScript(t, s, "draft-report", script.ScopeGlobal, "admin@example.com", nil, nil)
	disabled := seedScript(t, s, "off-report", script.ScopeGlobal, "admin@example.com", nil, nil)
	disabled.Enabled = false
	require.NoError(t, s.Update(ctx, disabled))
	retired := seedScript(t, s, "old-report", script.ScopeGlobal, "admin@example.com", nil, nil)
	retired.Status = script.StatusDeprecated
	require.NoError(t, s.Update(ctx, retired))

	got, err := s.Search(ctx, script.SearchQuery{QueryText: "revenue by region"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, draft.Name, got[0].Script.Name)

	// The retired script is still reachable by reference: discovery hides a dead
	// end, but a caller holding a reference to one gets the document that says
	// so, rather than a not-found reading as though it never existed.
	c, err := s.Contract(ctx, retired.ID)
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Contains(t, c.Approval.Refusal, "deprecated")
}

// TestRealDB_ContractComposesFromTheRealSchema proves the four reads behind one
// contract line up against real rows, including the approved version behind the
// execution gate.
func TestRealDB_ContractComposesFromTheRealSchema(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := seedScript(t, s, "daily-sales", script.ScopeGlobal, "jane@example.com", nil,
		[]script.Param{{Name: "report_date", Type: script.ParamTypeDate, Required: true}})

	before, err := s.Contract(ctx, sc.ID)
	require.NoError(t, err)
	require.NotNil(t, before)
	assert.False(t, before.Approval.Approved)
	assert.Contains(t, before.Approval.Refusal, "no approved version")
	assert.Nil(t, before.Schedule)
	assert.Nil(t, before.LastRun)

	_, err = s.ApproveVersion(ctx, sc.ID, 1, "admin@example.com",
		script.Grants{Connections: []string{"warehouse"}})
	require.NoError(t, err)

	after, err := s.Contract(ctx, sc.ID)
	require.NoError(t, err)
	require.NotNil(t, after)
	assert.True(t, after.Approval.Approved)
	assert.Equal(t, 1, after.Approval.Version)
	assert.Equal(t, "admin@example.com", after.Approval.ApprovedBy)
	require.Len(t, after.Params, 1)
	assert.Equal(t, "report_date", after.Params[0].Name)

	missing, err := s.Contract(ctx, "00000000-0000-0000-0000-000000000000")
	require.NoError(t, err)
	assert.Nil(t, missing, "a stale reference is a normal answer, not a failure")
}

// TestRealDB_SearchSurvivesAScriptWithNoParameters proves the IMMUTABLE
// parameter extraction is total: an empty JSONB array must index like any other
// row rather than making the row unwritable.
func TestRealDB_SearchSurvivesAScriptWithNoParameters(t *testing.T) {
	db := testdb.New(t)
	s := New(db)

	seedScript(t, s, "no-params", script.ScopeGlobal, "admin@example.com", nil, nil)

	got, err := s.Search(context.Background(), script.SearchQuery{QueryText: "revenue"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "no-params", got[0].Script.Name)
}
