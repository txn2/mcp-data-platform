//go:build integration

package knowledgepage

// Real-Postgres test for a knowledge page citing a managed script (#1855): the
// script_id column, its foreign key, the exactly-one CHECK and the per-page
// unique index added by migration 000154, which sqlmock cannot exercise.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func TestScriptRef_RealDB_CiteReadCascade(t *testing.T) {
	db := testdb.New(t)
	store := &postgresStore{db: db}
	ctx := context.Background()

	var scriptID string
	require.NoError(t, db.QueryRowContext(ctx,
		`INSERT INTO scripts (name, owner_email) VALUES ('orders-sync', 'owner@example.com') RETURNING id`).
		Scan(&scriptID))

	page := Page{ID: NewID(), Slug: "orders-sync", Title: "Orders sync", Body: "b", CreatedBy: "a@example.com"}
	require.NoError(t, store.Insert(ctx, page))

	ref, err := ParseCitableRef("mcp:script:" + scriptID)
	require.NoError(t, err)
	require.NoError(t, store.ValidateRefTargets(ctx, []EntityRef{ref}))

	// Cited twice: the per-page unique index makes the second a no-op.
	require.NoError(t, store.AddEntityRefs(ctx, page.ID, []EntityRef{ref}))
	require.NoError(t, store.AddEntityRefs(ctx, page.ID, []EntityRef{ref}))

	refs, err := store.ListEntityRefs(ctx, page.ID)
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, RefTargetScript, refs[0].TargetType)
	assert.Equal(t, scriptID, refs[0].ScriptID)
	assert.Equal(t, "mcp:script:"+scriptID, refs[0].URN())

	graph, err := store.ListEntityRefsForPages(ctx, []string{page.ID})
	require.NoError(t, err)
	require.Len(t, graph, 1)
	assert.Equal(t, scriptID, graph[0].ScriptID)

	citing, err := store.ListPagesReferencing(ctx, ref)
	require.NoError(t, err)
	require.Len(t, citing, 1)
	assert.Equal(t, page.ID, citing[0].ID)

	// A script that does not exist is refused by the existence check and by the
	// foreign key alike.
	ghost := EntityRef{TargetType: RefTargetScript, ScriptID: "0b7e2f0c-1111-4a4a-8b8b-000000000000"}
	require.ErrorIs(t, store.ValidateRefTargets(ctx, []EntityRef{ghost}), ErrRefTargetNotFound)
	require.ErrorIs(t, store.AddEntityRefs(ctx, page.ID, []EntityRef{ghost}), ErrRefTargetNotFound)

	// The CHECK refuses a script row that names a second target.
	_, err = db.ExecContext(ctx, `INSERT INTO knowledge_page_entity_refs
		(id, page_id, target_type, script_id, entity_urn) VALUES ($1, $2, 'script', $3, 'urn:li:tag:x')`,
		NewRefID(), page.ID, scriptID)
	require.Error(t, err, "a script reference carrying a second target violates chk_kp_entity_ref_target")

	// Deleting the script takes its citation with it.
	_, err = db.ExecContext(ctx, `DELETE FROM scripts WHERE id = $1`, scriptID)
	require.NoError(t, err)
	refs, err = store.ListEntityRefs(ctx, page.ID)
	require.NoError(t, err)
	assert.Empty(t, refs)
}
