package knowledgepage

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
