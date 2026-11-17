//go:build integration

package sitetile_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitestore"
	"github.com/txn2/mcp-data-platform/internal/site/sitetile"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

// TestRealDB_AFailureUnderAnOlderRendererIsDrawnAgain pins #2681: an
// environment that spent its attempts under one renderer generation is not
// claimed again under that generation, is claimed again with its attempts
// started over under a newer one, and spends them again before it is left.
func TestRealDB_AFailureUnderAnOlderRendererIsDrawnAgain(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := sitestore.New(db)
	st := &site.Site{
		ID: sitestore.NewSiteID(), Name: "tiles", Description: "d", OwnerEmail: "jane@example.com", CreatedBy: "jane@example.com",
		MainEnvironment: site.EnvDev, Environments: []site.Environment{{Name: site.EnvDev}},
	}
	require.NoError(t, s.Create(ctx, st))
	rel := &site.Release{ID: sitestore.NewReleaseID(), SiteID: st.ID, ManifestSHA256: "x", Redirects: []site.Redirect{}, CreatedBy: "jane@example.com"}
	require.NoError(t, s.RecordRelease(ctx, rel, nil))
	_, err := db.ExecContext(ctx, `UPDATE site_environments SET release_number = $2 WHERE site_id = $1 AND name = 'dev'`, st.ID, rel.Number)
	require.NoError(t, err)

	tiles := sitetile.NewStore(db)
	const maxAttempts = 2
	// claim takes the environment if it is due, and leaves its lease
	// expired, as a render that never finished does.
	claim := func(renderer int) (sitetile.Work, bool) {
		t.Helper()
		work, err := tiles.Claim(ctx, renderer, time.Minute, 10, maxAttempts)
		require.NoError(t, err)
		for _, w := range work {
			if w.SiteID == st.ID {
				_, err := db.ExecContext(ctx, `UPDATE site_tiles SET lease_until = now() - interval '1 second' WHERE site_id = $1`, st.ID)
				require.NoError(t, err)
				return w, true
			}
		}
		return sitetile.Work{}, false
	}
	// spend uses up a generation's attempts on renders that never finish,
	// then records the failure the worker records at the last one.
	spend := func(renderer int) {
		t.Helper()
		var last sitetile.Work
		for i := 1; i <= maxAttempts; i++ {
			w, ok := claim(renderer)
			require.True(t, ok, "attempt %d under renderer %d", i, renderer)
			assert.Equal(t, i, w.Attempts, "its attempts under renderer %d", renderer)
			last = w
		}
		_, ok := claim(renderer)
		assert.False(t, ok, "spent under renderer %d, the release is left", renderer)
		require.NoError(t, tiles.RecordFailure(ctx, last, "the document did not finish drawing before the deadline", renderer))
		_, ok = claim(renderer)
		assert.False(t, ok, "a recorded failure stands under renderer %d", renderer)
	}
	spend(1)
	spend(2) // a newer renderer draws it again, its attempts started over
}
