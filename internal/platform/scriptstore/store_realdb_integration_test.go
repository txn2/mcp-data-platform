//go:build integration

package scriptstore

// The real-schema proof for #1283. sqlmock rubber-stamps SQL that Postgres
// rejects — a nil slice bound through pq.Array into a NOT NULL column is the
// canonical example, and that class of defect has shipped from this repo
// before. These tests run the write paths against the actual migrated schema,
// so the CHECK constraints, the partial unique indexes on name, the
// scripts/script_versions foreign keys, and the NOT NULL columns all get a vote.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// newScript returns a valid personal script with NIL slice fields, which is the
// shape a caller that never touched tags or personas actually produces. The
// status is deliberately left empty: Create defaults it to active.
func newScript(name, owner string) *script.Script {
	return &script.Script{
		Name: name, DisplayName: "Daily", Description: "A daily report",
		Source: "print(1)\n", Scope: script.ScopePersonal, OwnerEmail: owner,
		Enabled: true,
	}
}

func TestRealDB_CreateNormalizesNilSlices(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("daily", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor), "nil params/personas/tags must not reach a NOT NULL column")
	require.NotEmpty(t, sc.ID)

	got, err := s.GetPersonal(ctx, "jane@example.com", "daily")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, []script.Param{}, got.Params)
	assert.Equal(t, []string{}, got.Personas)
	assert.Equal(t, []string{}, got.Tags)
	assert.Equal(t, 1, got.Version)
	assert.Equal(t, script.StatusActive, got.Status, "a saved script runs: creation defaults straight to active")

	versions, err := s.ListVersions(ctx, sc.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1, "a script never exists without the snapshot that explains it")
	assert.Equal(t, script.VersionStatusApplied, versions[0].Status)
	assert.Equal(t, "print(1)\n", versions[0].Source)
}

// TestRealDB_PersonalNamesAreUniquePerOwner exercises the two partial unique
// indexes: two analysts may each keep their own "daily", while a shared name is
// unique platform-wide.
func TestRealDB_PersonalNamesAreUniquePerOwner(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.Create(ctx, newScript("daily", "jane@example.com"), testAuthor))
	require.NoError(t, s.Create(ctx, newScript("daily", "bob@example.com"), testAuthor))
	assert.Error(t, s.Create(ctx, newScript("daily", "jane@example.com"), testAuthor),
		"the same owner may not hold two scripts of the same name")

	shared := newScript("rollup", "admin@example.com")
	shared.Scope = script.ScopeGlobal
	require.NoError(t, s.Create(ctx, shared, testAuthor), testAuthor)

	second := newScript("rollup", "other@example.com")
	second.Scope = script.ScopeGlobal
	assert.Error(t, s.Create(ctx, second, testAuthor), "a shared name is unique platform-wide")
}

// TestRealDB_EditFunnelAlwaysAppliesToTheLiveRow drives the domain funnel
// against the real store: every edit lands on the live row with a new applied
// version, so the saved script and the script a run executes are the same code.
func TestRealDB_EditFunnelAlwaysAppliesToTheLiveRow(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("daily", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor))

	before := *sc
	after := *sc
	after.Source = "print(2)\n"
	require.NoError(t, script.ApplyEdit(ctx, s, script.Edit{Before: &before, After: &after, Author: testAuthor}))
	assert.Equal(t, 2, after.Version)

	live, err := s.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, "print(2)\n", live.Source)

	// A second edit lands the same way: there is no gate that could park it.
	secondBefore := *live
	secondAfter := *live
	secondAfter.Source = "print(3)\n"
	require.NoError(t, script.ApplyEdit(ctx, s, script.Edit{Before: &secondBefore, After: &secondAfter, Author: testAuthor}))

	live, err = s.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	assert.Equal(t, "print(3)\n", live.Source, "the live row is what the next run executes")
	assert.Equal(t, 3, live.Version)

	versions, err := s.ListVersions(ctx, sc.ID)
	require.NoError(t, err)
	require.Len(t, versions, 3)
	assert.Equal(t, script.VersionStatusApplied, versions[0].Status,
		"every save is an applied version, not a proposal")
}

// TestRealDB_DeleteCascadesVersions covers the other direction: removing a
// script takes its history with it.
func TestRealDB_DeleteCascadesVersions(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("daily", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor), testAuthor)
	require.NoError(t, s.Delete(ctx, sc.ID))

	versions, err := s.ListVersions(ctx, sc.ID)
	require.NoError(t, err)
	assert.Empty(t, versions)

	got, err := s.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestRealDB_ListFilters(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	require.NoError(t, s.Create(ctx, newScript("daily-sales", "jane@example.com"), testAuthor))
	require.NoError(t, s.Create(ctx, newScript("weekly-rollup", "bob@example.com"), testAuthor))

	all, err := s.List(ctx, script.ListFilter{})
	require.NoError(t, err)
	assert.Len(t, all, 2)

	mine, err := s.List(ctx, script.ListFilter{OwnerEmail: "jane@example.com"})
	require.NoError(t, err)
	require.Len(t, mine, 1)
	assert.Equal(t, "daily-sales", mine[0].Name)

	// The three-column search clause is the one whose placeholder index is
	// repeated, so it is worth running against a real planner.
	found, err := s.List(ctx, script.ListFilter{Search: "rollup"})
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, "weekly-rollup", found[0].Name)

	enabled := false
	none, err := s.List(ctx, script.ListFilter{Enabled: &enabled})
	require.NoError(t, err)
	assert.Empty(t, none)
}

// TestRealDB_StatusCheckConstraint proves the lifecycle values the domain
// declares are the ones the schema accepts.
func TestRealDB_StatusCheckConstraint(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	sc := newScript("daily", "jane@example.com")
	require.NoError(t, s.Create(ctx, sc, testAuthor), testAuthor)

	for _, status := range []string{
		script.StatusActive, script.StatusDeprecated, script.StatusSuperseded,
	} {
		sc.Status = status
		assert.NoError(t, s.Update(ctx, sc), "schema must accept the domain status %q", status)
	}

	sc.Status = "retired"
	assert.Error(t, s.Update(ctx, sc), "a status the domain does not declare must be refused by the schema")

	// 'draft' left the lifecycle with the review gate: migration 000118 narrowed
	// the CHECK, so the retired state cannot be written back.
	sc.Status = "draft"
	assert.Error(t, s.Update(ctx, sc), "the retired draft status must be refused by the schema")
}
