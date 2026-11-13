package scriptstore

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptshare"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestCodeSources is #2273's read: every active script's id, name, kind,
// version and source, which a save is compared with.
func TestCodeSources(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, name, library, version, source_code FROM scripts WHERE status = $1")).
		WithArgs(script.StatusActive).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "library", "version", "source_code"}).
			AddRow("l1", "date-windows", true, 3, "def a():\n    return 1\n").
			AddRow("s1", "weekly", false, 7, "def main():\n    return 1\n"))
	got, err := s.CodeSources(context.Background())
	require.NoError(t, err)
	assert.Equal(t, []scriptshare.Source{
		{ID: "l1", Name: "date-windows", Library: true, Version: 3, Code: "def a():\n    return 1\n"},
		{ID: "s1", Name: "weekly", Version: 7, Code: "def main():\n    return 1\n"},
	}, got)
	var _ scriptshare.Sources = s

	mock.ExpectQuery("SELECT id, name, library").WillReturnError(errors.New("boom"))
	_, err = s.CodeSources(context.Background())
	require.ErrorContains(t, err, "reading script sources")

	mock.ExpectQuery("SELECT id, name, library").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow("x", "y"))
	_, err = s.CodeSources(context.Background())
	require.ErrorContains(t, err, "reading script sources", "a row that does not scan")
}
