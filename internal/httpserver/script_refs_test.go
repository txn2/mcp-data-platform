package httpserver

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
)

// TestPortalScriptRefs covers the lookup knowledge-page script citations are
// resolved through (#1855): none without a database, which the portal reads as
// every script citation unavailable.
func TestPortalScriptRefs(t *testing.T) {
	assert.Nil(t, portalScriptRefs(nil))

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup
	assert.NotNil(t, portalScriptRefs(db))
}

// TestCitedScripts proves the adapter reports a found script's label, and
// answers a missing script as not found and a failed read as an error, so
// the portal can withhold the citation instead of showing it as deleted.
func TestCitedScripts(t *testing.T) {
	found := citedScripts(func(context.Context, string) (*scriptstore.Citation, error) {
		return &scriptstore.Citation{Label: "Orders sync"}, nil
	})
	label, ok, err := found(context.Background(), "id")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Orders sync", label)

	missing := citedScripts(func(context.Context, string) (*scriptstore.Citation, error) {
		return nil, nil //nolint:nilnil // the lookup's not-found answer
	})
	_, ok, err = missing(context.Background(), "id")
	require.NoError(t, err)
	assert.False(t, ok)

	failing := citedScripts(func(context.Context, string) (*scriptstore.Citation, error) {
		return nil, errors.New("db down")
	})
	_, ok, err = failing(context.Background(), "id\nforged")
	require.Error(t, err, "a failed read is passed on, not answered as a missing script")
	assert.False(t, ok)
}
