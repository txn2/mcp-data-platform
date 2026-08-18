package scriptlayer

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptindex"
)

// TestIndexProducer covers the write-path index-job seam the queue binds
// (#1370): the Postgres store this layer builds carries a producer for the
// scripts kind, and there is nothing to bind where the layer built no store of
// its own.
func TestIndexProducer(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{DB: db, AdminPersona: "admin"})
	require.NotNil(t, h.IndexProducer())
	assert.Equal(t, scriptindex.SourceKind, h.IndexProducer().Kind())

	assert.Nil(t, New(Config{AdminPersona: "admin"}).IndexProducer(),
		"no database means no store to notify for")
	assert.Nil(t, New(Config{Store: newMemStore(), AdminPersona: "admin"}).IndexProducer(),
		"an injected store owns its own indexing arrangements")

	var nilHandle *Handle
	assert.Nil(t, nilHandle.IndexProducer())
}
