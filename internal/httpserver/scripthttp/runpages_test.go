package scripthttp

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// countingRuns is a run history that also counts, as the Postgres store does.
type countingRuns struct {
	*stubRuns
	total    int
	countErr error
	counted  script.RunFilter
}

func (c *countingRuns) CountRuns(_ context.Context, f script.RunFilter) (int, error) {
	c.counted = f
	return c.total, c.countErr
}

// A history longer than a page reports every run it holds, and a page further
// back reads past the newer ones (#1972).
func TestPortalListRuns_PagesAndReportsTheTrueTotal(t *testing.T) {
	runs := &countingRuns{stubRuns: &stubRuns{runs: []script.Run{{ID: "run_26", ScriptID: "script_1"}}}, total: 312}
	deps := portalDeps(portalStore(), runs.stubRuns, nil, owner)
	deps.Runs = runs
	rec := servePortal(t, deps, "/api/v1/portal/scripts/script_1/runs?per_page=25&page=2&status=failed")
	require.Equal(t, http.StatusOK, rec.Code)

	assert.Equal(t, 25, runs.lastFilter.Offset)
	assert.Equal(t, script.RunStatusFailed, runs.counted.Status, "the count is of the same filter")
	var body portalRunListResponse
	decodeInto(t, rec, &body)
	assert.Equal(t, 312, body.Total)
	assert.Len(t, body.Data, 1)
}

// Without a count, the total is what is known: the page and the runs before it.
func TestPortalListRuns_TotalWithoutACounter(t *testing.T) {
	for name, deps := range map[string]func(*countingRuns) Deps{
		"no counter": func(c *countingRuns) Deps { return portalDeps(portalStore(), c.stubRuns, nil, owner) },
		"count fails": func(c *countingRuns) Deps {
			d := portalDeps(portalStore(), c.stubRuns, nil, owner)
			c.countErr = errors.New("boom")
			d.Runs = c
			return d
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := &countingRuns{stubRuns: &stubRuns{runs: []script.Run{{ID: "a"}, {ID: "b"}}}}
			rec := servePortal(t, deps(c), "/api/v1/portal/scripts/script_1/runs?per_page=10&page=3")
			require.Equal(t, http.StatusOK, rec.Code)
			var body portalRunListResponse
			decodeInto(t, rec, &body)
			assert.Equal(t, 22, body.Total)
		})
	}
}
