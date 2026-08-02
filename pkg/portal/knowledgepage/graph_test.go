package knowledgepage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestQualifyColumns covers the alias helper the joined graph read uses to derive
// its projection from entityRefColumns: every column is aliased, in order, so
// scanEntityRef reads the same columns on both the per-page and corpus reads.
func TestQualifyColumns(t *testing.T) {
	assert.Equal(t, "r.id, r.page_id, r.target_type", qualifyColumns("id, page_id, target_type", "r"))
	assert.Equal(t, "p.id", qualifyColumns("id", "p"))

	base := strings.Split(entityRefColumns, ", ")
	prefixed := strings.Split(prefixedEntityRefColumns, ", ")
	require.Len(t, prefixed, len(base))
	for i := range base {
		assert.Equal(t, "r."+base[i], prefixed[i])
	}
}

func TestStore_ListEntityRefsForPages(t *testing.T) {
	created := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)

	t.Run("returns the references of every requested page", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()                                    //nolint:errcheck // test cleanup
		store := NewPostgresStoreSearcher(db).(GraphReader) //nolint:errcheck,forcetypeassert // concrete store implements GraphReader

		rows := sqlmock.NewRows([]string{
			"id", "page_id", "target_type", "asset_id", "prompt_id", "collection_id", "ref_page_id",
			"connection_kind", "connection_name", "entity_urn", "source", "created_by", "created_at",
		}).
			AddRow("kpr1", "kp1", RefTargetDataHub, nil, nil, nil, nil, nil, nil, "urn:li:dataset:x", RefSourcePromoted, "a@example.com", created).
			AddRow("kpr2", "kp2", RefTargetKnowledgePage, nil, nil, nil, "kp1", nil, nil, nil, RefSourceInline, "b@example.com", created)
		mock.ExpectQuery("FROM knowledge_page_entity_refs r").WillReturnRows(rows)

		refs, err := store.ListEntityRefsForPages(context.Background(), []string{"kp1", "kp2"})
		require.NoError(t, err)
		require.Len(t, refs, 2)
		assert.Equal(t, "kp1", refs[0].PageID)
		assert.Equal(t, "urn:li:dataset:x", refs[0].EntityURN)
		assert.Equal(t, "kp2", refs[1].PageID)
		assert.Equal(t, "kp1", refs[1].RefPageID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no pages does not query", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		store := &postgresStore{db: db}

		refs, err := store.ListEntityRefsForPages(context.Background(), nil)
		require.NoError(t, err)
		assert.Empty(t, refs)
		assert.NoError(t, mock.ExpectationsWereMet(), "an empty page set must not hit the database")
	})

	t.Run("query error is returned", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		store := &postgresStore{db: db}

		mock.ExpectQuery("FROM knowledge_page_entity_refs r").WillReturnError(errors.New("boom"))
		_, err = store.ListEntityRefsForPages(context.Background(), []string{"kp1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "querying entity refs for pages")
	})

	t.Run("scan error is returned", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close() //nolint:errcheck // test cleanup
		store := &postgresStore{db: db}

		// A short row makes the scan fail, proving the error is surfaced rather
		// than yielding a partial reference set.
		mock.ExpectQuery("FROM knowledge_page_entity_refs r").
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow("kpr1"))
		_, err = store.ListEntityRefsForPages(context.Background(), []string{"kp1"})
		require.Error(t, err)
	})
}

// TestMaxHonoredLimitIsTheStoresRealCeiling pins the contract callers bound their
// own page windows by: a limit at MaxHonoredLimit is honored, and one above it
// does NOT clamp down — it collapses to the small default. A caller that offers
// its own limit parameter and exceeds this value returns fewer rows the more it
// asks for, so this must fail loudly if the store's clamp ever changes shape.
func TestMaxHonoredLimitIsTheStoresRealCeiling(t *testing.T) {
	assert.Equal(t, MaxHonoredLimit, clampSearchLimit(MaxHonoredLimit))
	assert.Equal(t, DefaultSearchLimit, clampSearchLimit(MaxHonoredLimit+1))
	assert.Equal(t, DefaultSearchLimit, clampSearchLimit(0))
	assert.Equal(t, 25, clampSearchLimit(25))
}
