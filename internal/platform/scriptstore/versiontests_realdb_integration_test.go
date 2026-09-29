//go:build integration

package scriptstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/internal/testreport"
)

// TestRealDB_AVersionKeepsItsTestReport stores the report a save produced on
// the version it saved and reads it back (#1972). A version written without
// running the tests, over the same source, keeps the report of the one before;
// a version with a different source and no report has none.
func TestRealDB_AVersionKeepsItsTestReport(t *testing.T) {
	db := testdb.New(t)
	s := New(db)
	ctx := context.Background()

	report := &testreport.Report{
		Tests:    []testreport.Outcome{{Name: "test_empty", Passed: true, Line: 12}},
		Passed:   1,
		Coverage: testreport.Coverage{Statements: 10, Covered: 9, Percent: 90, MissedLines: []int{7}},
	}
	sc := newScript("tested", "jane@example.com")
	sc.Tests = report
	require.NoError(t, s.Create(ctx, sc, testAuthor))
	first, err := s.GetVersion(ctx, sc.ID, sc.Version)
	require.NoError(t, err)
	require.NotNil(t, first)
	assert.Equal(t, report, first.Tests)

	live, err := s.GetByID(ctx, sc.ID)
	require.NoError(t, err)
	live.Description = "Only the description changed."
	require.NoError(t, s.UpdateWithVersion(ctx, live, testAuthor))
	second, err := s.GetVersion(ctx, sc.ID, live.Version)
	require.NoError(t, err)
	require.Greater(t, second.Version, first.Version)
	assert.Equal(t, report, second.Tests, "the same source keeps the report it was saved with")

	live.Source = "def main():\n    return 2\n"
	require.NoError(t, s.UpdateWithVersion(ctx, live, testAuthor))
	third, err := s.GetVersion(ctx, sc.ID, live.Version)
	require.NoError(t, err)
	assert.Nil(t, third.Tests, "new source with no report has none")

	versions, err := s.ListVersions(ctx, sc.ID)
	require.NoError(t, err)
	require.Len(t, versions, 3)
}
