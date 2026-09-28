package legacyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

type lister struct {
	scripts  []script.Script
	filter   script.ListFilter
	listErr  error
	countErr error
}

func (l *lister) List(_ context.Context, f script.ListFilter) ([]script.Script, error) {
	l.filter = f
	return l.scripts, l.listErr
}

func (l *lister) Count(context.Context, script.ListFilter) (int, error) {
	return len(l.scripts) + 3, l.countErr
}

// untidy carries lint findings: a function with no docstring and a variable
// it never uses.
const untidy = `def main():
    unused = 1
    print("hi")
`

const clean = `def main():
    """Prints hi."""
    print("hi")
`

const testMain = `
def test_main():
    """Runs main."""
    main()
    assert.eq(testing.outputs().log, "hi\n")
`

const tested = clean + testMain

func serve(t *testing.T, l *lister) (*httptest.ResponseRecorder, Listing) {
	t.Helper()
	mux := http.NewServeMux()
	New(l).RegisterAdmin(mux, "/api/v1/admin", func(h http.Handler) http.Handler { return h })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/admin/scripts/legacy", http.NoBody))
	var out Listing
	if rec.Code == http.StatusOK {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	}
	return rec, out
}

func TestTheViewListsWhatTheHarnessHasNotCaughtUpWith(t *testing.T) {
	l := &lister{scripts: []script.Script{
		{ID: "a", Name: "findings-and-tests", Source: untidy + testMain, Legacy: true},
		{ID: "b", Name: "clean-no-tests", Source: clean, TestsOptional: true},
		{ID: "c", Name: "caught-up", Source: tested, Legacy: true, TestsOptional: true},
		{ID: "d", Name: "does-not-parse", Source: "def (", TestsOptional: true},
	}}
	rec, out := serve(t, l)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.True(t, l.filter.PreHarness)
	assert.Equal(t, script.SortName, l.filter.Sort)

	byName := map[string]Script{}
	for _, s := range out.Data {
		byName[s.Name] = s
	}
	require.Len(t, out.Data, 3, "the caught-up script is not listed")
	assert.NotContains(t, byName, "caught-up")
	assert.Positive(t, byName["findings-and-tests"].LintFindings)
	assert.Equal(t, 1, byName["findings-and-tests"].Tests)
	assert.Equal(t, 0, byName["clean-no-tests"].LintFindings)
	assert.Equal(t, 0, byName["clean-no-tests"].Tests)
	assert.Equal(t, 0, byName["does-not-parse"].Tests)
	assert.Equal(t, 3, out.Total)
	assert.Equal(t, 4, out.Examined)
	assert.Equal(t, 7, out.PreHarness, "the count says how many exist past the page")
}

func TestAnEmptyViewIsAnEmptyList(t *testing.T) {
	rec, _ := serve(t, &lister{})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"data": [], "total": 0, "examined": 0, "pre_harness": 3}`, rec.Body.String())
}

func TestAFailedListIsAServerError(t *testing.T) {
	rec, _ := serve(t, &lister{listErr: errors.New("down")})
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestAFailedCountFallsBackToThePage(t *testing.T) {
	rec, out := serve(t, &lister{scripts: []script.Script{{Name: "x", Source: untidy, Legacy: true}}, countErr: errors.New("down")})
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 1, out.PreHarness)
}
