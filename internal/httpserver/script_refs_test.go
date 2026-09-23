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

// TestCitedScripts proves the adapter reports a found script's label and owner,
// and answers a missing script and a failed read alike as not found, so a read
// failure withholds the citation instead of failing the page.
func TestCitedScripts(t *testing.T) {
	found := citedScripts(func(context.Context, string) (*scriptstore.Citation, error) {
		return &scriptstore.Citation{Label: "Orders sync", Owner: "jane@example.com"}, nil
	})
	label, owner, ok := found(context.Background(), "id")
	assert.True(t, ok)
	assert.Equal(t, "Orders sync", label)
	assert.Equal(t, "jane@example.com", owner)

	missing := citedScripts(func(context.Context, string) (*scriptstore.Citation, error) {
		return nil, nil //nolint:nilnil // the lookup's not-found answer
	})
	_, _, ok = missing(context.Background(), "id")
	assert.False(t, ok)

	failing := citedScripts(func(context.Context, string) (*scriptstore.Citation, error) {
		return nil, errors.New("db down")
	})
	_, _, ok = failing(context.Background(), "id\nforged")
	assert.False(t, ok)
}
