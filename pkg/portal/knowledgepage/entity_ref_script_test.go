package knowledgepage

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testScriptID is a script id in the form scripts.id takes (a UUID).
const testScriptID = "6f1c0a52-8d8e-4f7b-9a3e-2b8c1d0e4f55"

// A script citation is existence-checked against the scripts table like every
// other internal target (#1855): a script that exists passes, one that does not
// is ErrRefTargetNotFound, which is what refuses a nonexistent explicit
// reference on apply_knowledge.
func TestStore_ValidateRefTargets_Script(t *testing.T) {
	t.Run("an existing script passes", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT 1 FROM scripts WHERE id").WithArgs(testScriptID).WillReturnRows(oneRow())

		require.NoError(t, NewPostgresStore(db).ValidateRefTargets(context.Background(),
			[]EntityRef{{TargetType: RefTargetScript, ScriptID: testScriptID}}))
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("a missing script is refused", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("SELECT 1 FROM scripts WHERE id").WithArgs(testScriptID).
			WillReturnRows(sqlmock.NewRows([]string{"?column?"}))

		err = NewPostgresStore(db).ValidateRefTargets(context.Background(),
			[]EntityRef{{TargetType: RefTargetScript, ScriptID: testScriptID}})
		require.ErrorIs(t, err, ErrRefTargetNotFound)
		assert.Contains(t, err.Error(), "script:"+testScriptID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("an id that is not a uuid is missing without a query", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup

		err = NewPostgresStore(db).ValidateRefTargets(context.Background(),
			[]EntityRef{{TargetType: RefTargetScript, ScriptID: "script_01HK7"}})
		require.ErrorIs(t, err, ErrRefTargetNotFound)
		assert.NoError(t, mock.ExpectationsWereMet(), "no query is sent for an id the column cannot hold")
	})
}

// The tolerant filter (insight-carried and body references) drops a script
// citation whose script is gone and keeps one that exists.
func TestStore_FilterExistingRefTargets_Script(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	const gone = "0b7e2f0c-1111-4a4a-8b8b-000000000000"
	mock.ExpectQuery("SELECT 1 FROM scripts WHERE id").WithArgs(testScriptID).WillReturnRows(oneRow())
	mock.ExpectQuery("SELECT 1 FROM scripts WHERE id").WithArgs(gone).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}))

	kept, err := NewPostgresStore(db).FilterExistingRefTargets(context.Background(), []EntityRef{
		{TargetType: RefTargetScript, ScriptID: testScriptID},
		{TargetType: RefTargetScript, ScriptID: gone},
	})
	require.NoError(t, err)
	require.Len(t, kept, 1)
	assert.Equal(t, testScriptID, kept[0].ScriptID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A script citation is written into script_id, with every other target column
// NULL (the exactly-one CHECK), and de-duplicated against the per-page script
// index so a re-apply is a no-op.
func TestStore_AddEntityRefs_Script(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs.*script_id.*ON CONFLICT .page_id, script_id. WHERE script_id IS NOT NULL DO NOTHING").
		WithArgs(sqlmock.AnyArg(), "kp1", RefTargetScript, nil, nil, nil, nil, nil, nil, nil,
			testScriptID, RefSourcePromoted, "alice@example.com").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = NewPostgresStore(db).AddEntityRefs(context.Background(), "kp1", []EntityRef{
		{TargetType: RefTargetScript, ScriptID: testScriptID, CreatedBy: "alice@example.com"},
		{TargetType: RefTargetScript, ScriptID: testScriptID}, // same target in one batch: collapsed
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A stored script citation reads back with its script id and serializes to the
// same mcp:script:<id> form it was cited by.
func TestStore_ListEntityRefs_Script(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery("SELECT .*script_id.* FROM knowledge_page_entity_refs WHERE page_id").
		WithArgs("kp1").
		WillReturnRows(refRows().
			AddRow("r1", "kp1", RefTargetScript, nil, nil, nil, nil, nil, nil, nil, testScriptID, RefSourceManual, "bob", time.Now()))

	refs, err := NewPostgresStore(db).ListEntityRefs(context.Background(), "kp1")
	require.NoError(t, err)
	require.Len(t, refs, 1)
	assert.Equal(t, RefTargetScript, refs[0].TargetType)
	assert.Equal(t, testScriptID, refs[0].ScriptID)
	assert.Equal(t, "mcp:script:"+testScriptID, refs[0].URN())
	assert.NoError(t, mock.ExpectationsWereMet())
}

// The reverse lookup (the pages citing a script) reads the script column, and an
// id the column cannot hold is cited by no page without a query.
func TestStore_ListPagesReferencing_Script(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)
	mock.ExpectQuery("FROM knowledge_page_entity_refs r.*r.script_id = ").
		WithArgs(testScriptID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "title"}).AddRow("kp1", "orders-sync", "Orders sync"))

	pages, err := store.ListPagesReferencing(context.Background(),
		EntityRef{TargetType: RefTargetScript, ScriptID: testScriptID})
	require.NoError(t, err)
	require.Len(t, pages, 1)
	assert.Equal(t, "Orders sync", pages[0].Title)

	pages, err = store.ListPagesReferencing(context.Background(),
		EntityRef{TargetType: RefTargetScript, ScriptID: "not-a-uuid"})
	require.NoError(t, err)
	assert.Empty(t, pages)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// A script named in a page body is picked up like any other mcp: reference, and
// one whose id is not a UUID is skipped as unparseable prose.
func TestScanBodyRefs_Script(t *testing.T) {
	body := "Orders are kept in sync by [the sync script](mcp:script:" + testScriptID + ").\n" +
		"It runs daily; see mcp:script:" + testScriptID + ". Not a script: mcp:script:script_01HK7."
	refs := ScanBodyRefs(body)
	require.Len(t, refs, 1)
	assert.Equal(t, RefTargetScript, refs[0].TargetType)
	assert.Equal(t, testScriptID, refs[0].ScriptID)
	assert.Equal(t, RefSourceInline, refs[0].Source)
}
