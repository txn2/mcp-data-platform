package indexjobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parkStore serves a fixed set of park candidates to the reconciler's
// scan while recording what the sweep went on to enqueue.
type parkStore struct {
	sweepStore
	candidates []ParkCandidate
	failErr    error
	gotMin     int
	gotLimit   int
}

func (s *parkStore) ParkCandidates(_ context.Context, minOccurrences, limit int) ([]ParkCandidate, error) {
	s.gotMin, s.gotLimit = minOccurrences, limit
	return s.candidates, s.failErr
}

func TestFailedUnit_ParkedUntil(t *testing.T) {
	t.Parallel()

	last := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		occurrences int
		lastFailed  time.Time
		wantParked  bool
		wantDelay   time.Duration
	}{
		{"first failure is never parked", 1, last, false, 0},
		{"one below the threshold", ParkThreshold - 1, last, false, 0},
		{"at the threshold parks at the base delay", ParkThreshold, last, true, ParkBaseDelay},
		{"each further failure doubles", ParkThreshold + 1, last, true, 2 * ParkBaseDelay},
		{"and again", ParkThreshold + 2, last, true, 4 * ParkBaseDelay},
		{"capped at the maximum", ParkThreshold + 5, last, true, ParkMaxDelay},
		// The unit that motivated this had 835 open failures. The exponent
		// cap keeps that from shifting the duration past an int64.
		{"a pathological count stays at the cap", 835, last, true, ParkMaxDelay},
		{"no last-failure timestamp cannot be parked", ParkThreshold, time.Time{}, false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := ParkCandidate{Occurrences: tt.occurrences, LastFailedAt: tt.lastFailed}
			until, parked := c.ParkedUntil()
			require.Equal(t, tt.wantParked, parked)
			if !tt.wantParked {
				assert.True(t, until.IsZero(), "an unparked unit reports the zero time")
				return
			}
			assert.Equal(t, tt.lastFailed.Add(tt.wantDelay), until)
			assert.LessOrEqual(t, until.Sub(tt.lastFailed), ParkMaxDelay,
				"no delay may exceed the cap")

			// The triage panel reads the same policy through FailedUnit, so
			// the two must never report different deadlines.
			u := FailedUnit{Occurrences: tt.occurrences, LastFailedAt: tt.lastFailed}
			gotUntil, gotParked := u.ParkedUntil()
			assert.Equal(t, parked, gotParked)
			assert.Equal(t, until, gotUntil)
		})
	}
}

// TestReconciler_DefersAParkedUnit is the second half of #1350: a unit
// that keeps failing must stop being re-queued on every five-minute
// sweep. Before this, 835 identical failures accumulated over three days.
func TestReconciler_DefersAParkedUnit(t *testing.T) {
	t.Parallel()

	store := &parkStore{candidates: []ParkCandidate{{
		Key:          Key{SourceKind: "k", SourceID: "stuck"},
		Occurrences:  ParkThreshold,
		LastFailedAt: time.Now(),
	}}}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"stuck"}})

	NewReconciler(store, reg, time.Second).reconcileOnce()

	assert.Empty(t, store.enqueuedKeys(), "a parked unit must not be re-queued by the sweep")
	assert.Equal(t, ParkThreshold, store.gotMin, "the scan must ask for the park population")
	assert.Equal(t, parkScanLimit, store.gotLimit)
}

// TestReconciler_EnqueuesOnceTheParkWindowElapses proves the deferral is
// a delay and not a permanent block. Nothing but time is required to
// resume, which is what keeps a provider outage from freezing the index.
func TestReconciler_EnqueuesOnceTheParkWindowElapses(t *testing.T) {
	t.Parallel()

	store := &parkStore{candidates: []ParkCandidate{{
		Key:          Key{SourceKind: "k", SourceID: "stuck"},
		Occurrences:  ParkThreshold,
		LastFailedAt: time.Now().Add(-2 * ParkMaxDelay),
	}}}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"stuck"}})

	NewReconciler(store, reg, time.Second).reconcileOnce()

	assert.Equal(t, []Key{{SourceKind: "k", SourceID: "stuck"}}, store.enqueuedKeys(),
		"a unit past its park window must be re-queued")
}

// TestReconciler_ParksOnlyTheFailingUnit proves the deferral is per unit.
// A healthy gap in the same kind and the same sweep must still be closed.
func TestReconciler_ParksOnlyTheFailingUnit(t *testing.T) {
	t.Parallel()

	store := &parkStore{candidates: []ParkCandidate{{
		Key:          Key{SourceKind: "k", SourceID: "stuck"},
		Occurrences:  ParkThreshold,
		LastFailedAt: time.Now(),
	}}}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"stuck", "fine"}})

	NewReconciler(store, reg, time.Second).reconcileOnce()

	assert.Equal(t, []Key{{SourceKind: "k", SourceID: "fine"}}, store.enqueuedKeys())
}

// TestReconciler_ParkIsScopedToTheFailingKind proves the park key carries
// the source kind. Two kinds may use the same source id, and parking one
// must not silence the other.
func TestReconciler_ParkIsScopedToTheFailingKind(t *testing.T) {
	t.Parallel()

	store := &parkStore{candidates: []ParkCandidate{{
		Key:          Key{SourceKind: "other", SourceID: "shared"},
		Occurrences:  ParkThreshold,
		LastFailedAt: time.Now(),
	}}}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"shared"}})

	NewReconciler(store, reg, time.Second).reconcileOnce()

	assert.Equal(t, []Key{{SourceKind: "k", SourceID: "shared"}}, store.enqueuedKeys(),
		"a failure under another kind must not park this one")
}

// TestReconciler_ParkScanFailureStillClosesGaps proves the degradation
// direction. If the park scan cannot be read, the sweep must fall back to
// re-queueing everything: closing gaps is its job, and losing one tick of
// deferral is a far smaller fault than skipping reconciliation.
func TestReconciler_ParkScanFailureStillClosesGaps(t *testing.T) {
	t.Parallel()

	store := &parkStore{failErr: errors.New("db down")}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"u1"}})

	NewReconciler(store, reg, time.Second).reconcileOnce()

	assert.Equal(t, []Key{{SourceKind: "k", SourceID: "u1"}}, store.enqueuedKeys())
}

// TestReconciler_ParkDoesNotApplyBelowTheThreshold proves a unit that has
// merely failed once is re-queued on the very next sweep, unchanged from
// before. A transient failure must still self-heal within one interval.
func TestReconciler_ParkDoesNotApplyBelowTheThreshold(t *testing.T) {
	t.Parallel()

	store := &parkStore{candidates: []ParkCandidate{{
		Key:          Key{SourceKind: "k", SourceID: "blip"},
		Occurrences:  ParkThreshold - 1,
		LastFailedAt: time.Now(),
	}}}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"blip"}})

	NewReconciler(store, reg, time.Second).reconcileOnce()

	assert.Equal(t, []Key{{SourceKind: "k", SourceID: "blip"}}, store.enqueuedKeys())
}

// TestStore_ParkCandidatesQuery pins the two properties the deferral
// depends on: the query selects by open-failure count, and it orders on
// the unit key. A recency ordering would sort a deferred unit (whose last
// failure is frozen, because it stopped being re-queued) out of a bounded
// window and quietly undo the deferral.
func TestStore_ParkCandidatesQuery(t *testing.T) {
	t.Parallel()
	store, mock, closeDB := newMockStore(t)
	defer closeDB()

	last := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)
	mock.ExpectQuery(`HAVING COUNT\(\*\) >= \$1`).
		WithArgs(ParkThreshold, parkScanLimit).
		WillReturnRows(sqlmock.NewRows([]string{"source_kind", "source_id", "occ", "last_failed"}).
			AddRow("resources", "r1", 4, last).
			AddRow("resources", "r2", 3, nil))

	got, err := store.ParkCandidates(context.Background(), ParkThreshold, parkScanLimit)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, Key{SourceKind: "resources", SourceID: "r1"}, got[0].Key)
	assert.Equal(t, 4, got[0].Occurrences)
	assert.Equal(t, last, got[0].LastFailedAt)
	// A NULL last-failure cannot be parked: there is nothing to date the
	// deferral from, so the unit must keep being re-queued.
	assert.True(t, got[1].LastFailedAt.IsZero())
	_, parked := got[1].ParkedUntil()
	assert.False(t, parked)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestStore_ParkCandidatesOrdersOnTheKey guards the ordering itself, which
// is the whole point of not reusing ActiveFailures here.
func TestStore_ParkCandidatesOrdersOnTheKey(t *testing.T) {
	t.Parallel()
	store, mock, closeDB := newMockStore(t)
	defer closeDB()

	mock.ExpectQuery(`ORDER BY source_kind, source_id`).
		WithArgs(ParkThreshold, parkScanLimit).
		WillReturnRows(sqlmock.NewRows([]string{"source_kind", "source_id", "occ", "last_failed"}))

	_, err := store.ParkCandidates(context.Background(), ParkThreshold, parkScanLimit)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestStore_ParkCandidatesErrors covers the read's failure paths. They
// matter more than most: the reconciler treats a scan error as "defer
// nothing", so an error that were swallowed here would silently restore
// the every-five-minutes re-queue the deferral exists to stop.
func TestStore_ParkCandidatesErrors(t *testing.T) {
	t.Parallel()

	t.Run("query error", func(t *testing.T) {
		t.Parallel()
		store, mock, closeDB := newMockStore(t)
		defer closeDB()
		mock.ExpectQuery("HAVING COUNT").WillReturnError(errors.New("db down"))

		_, err := store.ParkCandidates(context.Background(), ParkThreshold, parkScanLimit)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "park candidates")
	})

	t.Run("scan error", func(t *testing.T) {
		t.Parallel()
		store, mock, closeDB := newMockStore(t)
		defer closeDB()
		mock.ExpectQuery("HAVING COUNT").WillReturnRows(
			sqlmock.NewRows([]string{"source_kind", "source_id", "occ", "last_failed"}).
				AddRow("resources", "r1", "not-an-int", nil))

		_, err := store.ParkCandidates(context.Background(), ParkThreshold, parkScanLimit)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scan park candidate")
	})

	t.Run("iteration error", func(t *testing.T) {
		t.Parallel()
		store, mock, closeDB := newMockStore(t)
		defer closeDB()
		mock.ExpectQuery("HAVING COUNT").WillReturnRows(
			sqlmock.NewRows([]string{"source_kind", "source_id", "occ", "last_failed"}).
				AddRow("resources", "r1", 3, nil).
				RowError(0, errors.New("connection lost")))

		_, err := store.ParkCandidates(context.Background(), ParkThreshold, parkScanLimit)
		require.Error(t, err)
	})
}

// TestReconciler_ParkScanErrorIsNotSilent pairs with the above: a scan
// that errors must fall back to re-queueing, which is the safe direction.
func TestReconciler_ParkScanErrorFallsBackToEnqueue(t *testing.T) {
	t.Parallel()

	store := &parkStore{failErr: errors.New("db down"), candidates: []ParkCandidate{{
		Key:          Key{SourceKind: "k", SourceID: "u1"},
		Occurrences:  ParkThreshold + 10,
		LastFailedAt: time.Now(),
	}}}
	reg := registryWith(&stubSource{kind: "k"}, &stubSink{kind: "k", gaps: []string{"u1"}})

	NewReconciler(store, reg, time.Second).reconcileOnce()

	assert.Equal(t, []Key{{SourceKind: "k", SourceID: "u1"}}, store.enqueuedKeys(),
		"an unreadable scan must not be read as 'everything is parked'")
}
