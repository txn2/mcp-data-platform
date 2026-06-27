package knowledgepage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func oneRow() *sqlmock.Rows { return sqlmock.NewRows([]string{"?column?"}).AddRow(1) }

// TestStore_ValidateRefTargets covers the up-front existence check (#690): each
// FK-backed type queries its catalog table, while a DataHub URN is free text and
// is not queried at all.
func TestStore_ValidateRefTargets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT 1 FROM portal_assets WHERE id").WithArgs("a1").WillReturnRows(oneRow())
	mock.ExpectQuery("SELECT 1 FROM prompts WHERE id").WithArgs("p1").WillReturnRows(oneRow())
	mock.ExpectQuery("SELECT 1 FROM portal_collections WHERE id").WithArgs("c1").WillReturnRows(oneRow())
	mock.ExpectQuery("SELECT 1 FROM portal_knowledge_pages WHERE id").WithArgs("kp1").WillReturnRows(oneRow())
	mock.ExpectQuery("SELECT 1 FROM connection_instances WHERE kind").WithArgs("trino", "warehouse").WillReturnRows(oneRow())

	refs := []EntityRef{
		{TargetType: RefTargetAsset, AssetID: "a1"},
		{TargetType: RefTargetPrompt, PromptID: "p1"},
		{TargetType: RefTargetCollection, CollectionID: "c1"},
		{TargetType: RefTargetKnowledgePage, RefPageID: "kp1"},
		{TargetType: RefTargetConnection, ConnectionKind: "trino", ConnectionName: "warehouse"},
		{TargetType: RefTargetDataHub, EntityURN: "urn:li:dataset:x"}, // no catalog FK, not queried
	}
	require.NoError(t, store.ValidateRefTargets(context.Background(), refs))
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestStore_ValidateRefTargets_Missing maps a non-existent target to ErrRefTargetNotFound.
func TestStore_ValidateRefTargets_Missing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT 1 FROM portal_assets WHERE id").WithArgs("gone").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"})) // no rows

	err = store.ValidateRefTargets(context.Background(), []EntityRef{{TargetType: RefTargetAsset, AssetID: "gone"}})
	require.ErrorIs(t, err, ErrRefTargetNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestStore_FilterExistingRefTargets keeps the existing targets and drops the
// missing ones (#690), and always keeps a free-text DataHub URN.
func TestStore_FilterExistingRefTargets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT 1 FROM portal_assets WHERE id").WithArgs("a1").WillReturnRows(oneRow())
	mock.ExpectQuery("SELECT 1 FROM portal_assets WHERE id").WithArgs("gone").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"})) // no rows -> dropped

	kept, err := store.FilterExistingRefTargets(context.Background(), []EntityRef{
		{TargetType: RefTargetAsset, AssetID: "a1"},
		{TargetType: RefTargetAsset, AssetID: "gone"},
		{TargetType: RefTargetDataHub, EntityURN: "urn:li:dataset:x"},
	})
	require.NoError(t, err)
	require.Len(t, kept, 2)
	assert.Equal(t, "a1", kept[0].AssetID)
	assert.Equal(t, RefTargetDataHub, kept[1].TargetType)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestStore_FilterExistingRefTargets_Error propagates a query failure.
func TestStore_FilterExistingRefTargets_Error(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT 1 FROM portal_assets WHERE id").WithArgs("a1").WillReturnError(errors.New("db down"))
	_, err = store.FilterExistingRefTargets(context.Background(), []EntityRef{{TargetType: RefTargetAsset, AssetID: "a1"}})
	require.Error(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestStore_ValidateRefTargets_QueryError surfaces a non-ErrNoRows query failure as
// a wrapped error, not a false "not found".
func TestStore_ValidateRefTargets_QueryError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectQuery("SELECT 1 FROM prompts WHERE id").WithArgs("p1").WillReturnError(errors.New("db down"))

	err = store.ValidateRefTargets(context.Background(), []EntityRef{{TargetType: RefTargetPrompt, PromptID: "p1"}})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRefTargetNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func refRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "page_id", "target_type", "asset_id", "prompt_id", "collection_id", "ref_page_id",
		"connection_kind", "connection_name", "entity_urn", "source", "created_by", "created_at",
	})
}

func TestStore_ListEntityRefs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)
	now := time.Now()

	mock.ExpectQuery("FROM knowledge_page_entity_refs WHERE page_id").
		WithArgs("kp1").
		WillReturnRows(refRows().
			AddRow("r1", "kp1", "datahub", nil, nil, nil, nil, nil, nil, "urn:li:dataset:x", "promoted", "alice", now).
			AddRow("r2", "kp1", "connection", nil, nil, nil, nil, "trino", "warehouse", nil, "manual", "bob", now))

	refs, err := store.ListEntityRefs(context.Background(), "kp1")
	require.NoError(t, err)
	require.Len(t, refs, 2)
	assert.Equal(t, RefTargetDataHub, refs[0].TargetType)
	assert.Equal(t, "urn:li:dataset:x", refs[0].EntityURN)
	assert.Equal(t, RefTargetConnection, refs[1].TargetType)
	assert.Equal(t, "trino", refs[1].ConnectionKind)
	assert.Equal(t, "warehouse", refs[1].ConnectionName)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_AddEntityRefs_InsertsWithConflictTarget(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	// Each distinct batch ref is inserted with ON CONFLICT DO NOTHING (the union
	// is enforced at the DB by the per-type unique index, race-safe).
	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs.*ON CONFLICT .page_id, entity_urn. WHERE entity_urn IS NOT NULL DO NOTHING").
		WithArgs(sqlmock.AnyArg(), "kp1", "datahub", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "urnA", "promoted", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs.*ON CONFLICT").
		WithArgs(sqlmock.AnyArg(), "kp1", "datahub", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "urnB", "promoted", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	// urnA is repeated in the batch and must be collapsed to a single insert.
	err = store.AddEntityRefs(context.Background(), "kp1", []EntityRef{
		DataHubRef("urnA", RefSourcePromoted),
		DataHubRef("urnB", RefSourcePromoted),
		DataHubRef("urnA", RefSourcePromoted),
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_AddEntityRefs_InternalTargets(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	// An asset reference uses its own conflict target and FK column.
	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs.*ON CONFLICT .page_id, asset_id. WHERE asset_id IS NOT NULL DO NOTHING").
		WithArgs(sqlmock.AnyArg(), "kp1", "asset", "asset-001", sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "manual", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// A connection reference uses the composite conflict target.
	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs.*ON CONFLICT .page_id, connection_kind, connection_name. WHERE connection_kind IS NOT NULL DO NOTHING").
		WithArgs(sqlmock.AnyArg(), "kp1", "connection", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), "trino", "warehouse", sqlmock.AnyArg(), "manual", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = store.AddEntityRefs(context.Background(), "kp1", []EntityRef{
		{TargetType: RefTargetAsset, AssetID: "asset-001", Source: RefSourceManual},
		{TargetType: RefTargetConnection, ConnectionKind: "trino", ConnectionName: "warehouse", Source: RefSourceManual},
	})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_ReplaceEntityRefs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM knowledge_page_entity_refs WHERE page_id").
		WithArgs("kp1").
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs").
		WithArgs(sqlmock.AnyArg(), "kp1", "datahub", sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "urnA", "promoted", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.ReplaceEntityRefs(context.Background(), "kp1", []EntityRef{DataHubRef("urnA", RefSourcePromoted)})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestEntityRefIdentity covers the per-target de-dup key used by the union.
func TestEntityRefIdentity(t *testing.T) {
	assert.Equal(t, "datahub:u", EntityRef{TargetType: RefTargetDataHub, EntityURN: "u"}.identity())
	assert.Equal(t, "asset:a", EntityRef{TargetType: RefTargetAsset, AssetID: "a"}.identity())
	assert.Equal(t, "prompt:p", EntityRef{TargetType: RefTargetPrompt, PromptID: "p"}.identity())
	assert.Equal(t, "collection:c", EntityRef{TargetType: RefTargetCollection, CollectionID: "c"}.identity())
	assert.Equal(t, "knowledge_page:kp", EntityRef{TargetType: RefTargetKnowledgePage, RefPageID: "kp"}.identity())
	assert.Equal(t, "connection:trino/warehouse",
		EntityRef{TargetType: RefTargetConnection, ConnectionKind: "trino", ConnectionName: "warehouse"}.identity())
	assert.Equal(t, "bogus:", EntityRef{TargetType: "bogus"}.identity())
	assert.NotEqual(t,
		EntityRef{TargetType: RefTargetAsset, AssetID: "a"}.identity(),
		EntityRef{TargetType: RefTargetPrompt, PromptID: "a"}.identity())
}

func TestStore_ReplaceEntityRefsBySource(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	store := NewPostgresStore(db)

	mock.ExpectBegin()
	mock.ExpectExec("DELETE FROM knowledge_page_entity_refs WHERE page_id = .* AND source = ").
		WithArgs("kp1", "manual").
		WillReturnResult(sqlmock.NewResult(0, 1))
	// The ref is stamped with the given source on insert.
	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs").
		WithArgs(sqlmock.AnyArg(), "kp1", "asset", "asset-001", sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), "manual", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = store.ReplaceEntityRefsBySource(context.Background(), "kp1", RefSourceManual,
		[]EntityRef{{TargetType: RefTargetAsset, AssetID: "asset-001"}})
	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestStore_AddEntityRefs_ForeignKeyViolation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectExec("INSERT INTO knowledge_page_entity_refs").
		WillReturnError(&pq.Error{Code: "23503"})
	err = NewPostgresStore(db).AddEntityRefs(context.Background(), "kp1",
		[]EntityRef{{TargetType: RefTargetAsset, AssetID: "ghost"}})
	require.ErrorIs(t, err, ErrRefTargetNotFound)
}

func TestRefConflictTarget(t *testing.T) {
	cases := map[string]string{
		RefTargetAsset:         "(page_id, asset_id) WHERE asset_id IS NOT NULL",
		RefTargetPrompt:        "(page_id, prompt_id) WHERE prompt_id IS NOT NULL",
		RefTargetCollection:    "(page_id, collection_id) WHERE collection_id IS NOT NULL",
		RefTargetKnowledgePage: "(page_id, ref_page_id) WHERE ref_page_id IS NOT NULL",
		RefTargetConnection:    "(page_id, connection_kind, connection_name) WHERE connection_kind IS NOT NULL",
		RefTargetDataHub:       "(page_id, entity_urn) WHERE entity_urn IS NOT NULL",
		"bogus":                "",
	}
	for targetType, want := range cases {
		assert.Equal(t, want, refConflictTarget(targetType), targetType)
	}
}

func TestStore_EntityRefs_ErrorPaths(t *testing.T) {
	t.Run("list query error", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectQuery("FROM knowledge_page_entity_refs WHERE page_id").WithArgs("kp1").WillReturnError(errBoom)
		_, err := NewPostgresStore(db).ListEntityRefs(context.Background(), "kp1")
		require.Error(t, err)
	})

	t.Run("add insert error", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectExec("INSERT INTO knowledge_page_entity_refs").WillReturnError(errBoom)
		err := NewPostgresStore(db).AddEntityRefs(context.Background(), "kp1", []EntityRef{DataHubRef("u", RefSourcePromoted)})
		require.Error(t, err)
	})

	t.Run("replace delete error rolls back", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectBegin()
		mock.ExpectExec("DELETE FROM knowledge_page_entity_refs WHERE page_id").WithArgs("kp1").WillReturnError(errBoom)
		mock.ExpectRollback()
		err := NewPostgresStore(db).ReplaceEntityRefs(context.Background(), "kp1", []EntityRef{DataHubRef("u", RefSourcePromoted)})
		require.Error(t, err)
	})

	t.Run("replace begin error", func(t *testing.T) {
		db, mock, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		mock.ExpectBegin().WillReturnError(errBoom)
		err := NewPostgresStore(db).ReplaceEntityRefs(context.Background(), "kp1", []EntityRef{DataHubRef("u", RefSourcePromoted)})
		require.Error(t, err)
	})

	t.Run("add with no refs is a no-op", func(t *testing.T) {
		db, _, _ := sqlmock.New()
		defer db.Close() //nolint:errcheck // test cleanup
		require.NoError(t, NewPostgresStore(db).AddEntityRefs(context.Background(), "kp1", nil))
	})
}
