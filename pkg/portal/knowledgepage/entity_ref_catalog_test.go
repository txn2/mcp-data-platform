package knowledgepage

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testDatasetRef = "mcp:dataset:(trino,acme,hive.sales.orders)"

// A platform catalog reference parses as a catalog citation in its canonical
// form, round-trips through URN, and is citable on a page (#1923).
func TestParseEntityRef_Catalog(t *testing.T) {
	for _, s := range []string{
		testDatasetRef,
		"mcp:column:(trino,acme,hive.sales.orders,amount)",
		"mcp:glossary_term:net-revenue",
		"mcp:glossary_node:finance",
		"mcp:tag:pii",
		"mcp:domain:sales",
		"mcp:data_product:revenue",
		`mcp:schema_document:(api,billing,"openapi.json")`,
	} {
		ref, err := ParseEntityRef(s)
		require.NoError(t, err, s)
		assert.Equal(t, RefTargetCatalog, ref.TargetType, s)
		assert.Equal(t, s, ref.URN(), s)
		assert.Equal(t, RefTargetCatalog+":"+s, ref.identity())
		_, err = ParseCitableRef(s)
		require.NoError(t, err, s)
	}
	_, err := ParseEntityRef("mcp:dataset:(trino)")
	assert.ErrorContains(t, err, "invalid catalog reference")
	assert.Equal(t, "(page_id, entity_urn) WHERE entity_urn IS NOT NULL", refConflictTarget(RefTargetCatalog))
}

// A catalog citation is checked against the catalog: by its canonical
// reference, or a definition by the slug its reference used.
func TestStore_ValidateRefTargets_Catalog(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	mock.ExpectQuery("SELECT 1 FROM catalog_entities").
		WithArgs(testDatasetRef, "dataset", "", "trino", "acme").WillReturnRows(oneRow())
	mock.ExpectQuery("SELECT 1 FROM catalog_entities").
		WithArgs("mcp:glossary_term:net-revenue", "glossary_term", "net-revenue", "", "").
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}))

	store := NewPostgresStore(db)
	require.NoError(t, store.ValidateRefTargets(context.Background(),
		[]EntityRef{{TargetType: RefTargetCatalog, EntityURN: testDatasetRef}}))
	err = store.ValidateRefTargets(context.Background(),
		[]EntityRef{{TargetType: RefTargetCatalog, EntityURN: "mcp:glossary_term:net-revenue"}})
	require.ErrorIs(t, err, ErrRefTargetNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())

	assert.Equal(t, []any{"not a reference", "", "", "", ""}, catalogEntityArgs("not a reference"))
	assert.Equal(t, []any{`mcp:schema_document:(api,billing,"openapi.json")`, "schema_document", "openapi.json", "api", "billing"},
		catalogEntityArgs(`mcp:schema_document:(api,billing,"openapi.json")`))
}

// A catalog citation is found by the page that cites it.
func TestReverseLookup_Catalog(t *testing.T) {
	clause, args := reverseLookupFilter(EntityRef{TargetType: RefTargetCatalog, EntityURN: testDatasetRef})
	assert.Equal(t, "r.entity_urn = $1", clause)
	assert.Equal(t, []any{testDatasetRef}, args)
}
