package platform

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSearchgateStoreKind locks the branch that decides whether the search-first
// gate's discovery signal is replica-shared: with a database it must be the
// Postgres store, without one the in-memory store (#789).
func TestSearchgateStoreKind(t *testing.T) {
	assert.Equal(t, "memory", searchgateStoreKind(nil))

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	assert.Equal(t, "postgres", searchgateStoreKind(db))
}
