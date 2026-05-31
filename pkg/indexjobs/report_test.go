package indexjobs

import (
	"context"
	"errors"
	"testing"
	"time"
)

// recordStore embeds noopStore and records Enqueue calls, with
// configurable Counts / List results and injectable errors, so Reporter
// tests can assert what the Reporter asked the queue to do.
type recordStore struct {
	noopStore
	counts      *KindCounts
	countsErr   error
	list        []Job
	listErr     error
	enqErr      error
	enqueued    []Key
	enqTrigger  []Trigger
	failures    []FailedUnit
	failuresErr error
	resolved    int
	resolveErr  error
	resolveKey  Key
}

func (s *recordStore) Counts(context.Context, string) (*KindCounts, error) {
	return s.counts, s.countsErr
}

func (s *recordStore) List(context.Context, ListFilter) ([]Job, error) {
	return s.list, s.listErr
}

func (s *recordStore) Enqueue(_ context.Context, k Key, t Trigger) (bool, error) {
	if s.enqErr != nil {
		return false, s.enqErr
	}
	s.enqueued = append(s.enqueued, k)
	s.enqTrigger = append(s.enqTrigger, t)
	return true, nil
}

func (s *recordStore) ActiveFailures(context.Context, string, int) ([]FailedUnit, error) {
	return s.failures, s.failuresErr
}

func (s *recordStore) ResolveFailures(_ context.Context, k Key) (int, error) {
	s.resolveKey = k
	return s.resolved, s.resolveErr
}

// coverageSink is a stubSink that also reports coverage, for the
// CoverageReporter-present path.
type coverageSink struct {
	stubSink
	cov    Coverage
	covErr error
}

func (s *coverageSink) Coverage(context.Context) (Coverage, error) {
	return s.cov, s.covErr
}

func newTestRegistry(t *testing.T, sink Sink) *Registry {
	t.Helper()
	r := NewRegistry()
	if err := r.Register(&stubSource{kind: sink.Kind()}, sink); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

func TestReporter_KindsAndCounts(t *testing.T) {
	t.Parallel()
	store := &recordStore{counts: &KindCounts{SourceKind: "k1", Pending: 2, Failed: 1}}
	rep := NewReporter(store, newTestRegistry(t, &stubSink{kind: "k1"}))

	if got := rep.Kinds(); len(got) != 1 || got[0] != "k1" {
		t.Fatalf("Kinds = %v; want [k1]", got)
	}
	c, err := rep.Counts(context.Background(), "k1")
	if err != nil {
		t.Fatalf("Counts: %v", err)
	}
	if c.Pending != 2 || c.Failed != 1 {
		t.Errorf("Counts = %+v; want Pending 2 Failed 1", c)
	}
}

func TestReporter_CountsError(t *testing.T) {
	t.Parallel()
	rep := NewReporter(&recordStore{countsErr: errors.New("db down")}, newTestRegistry(t, &stubSink{kind: "k1"}))
	if _, err := rep.Counts(context.Background(), "k1"); err == nil {
		t.Fatal("expected counts error")
	}
}

func TestReporter_ListError(t *testing.T) {
	t.Parallel()
	rep := NewReporter(&recordStore{listErr: errors.New("db down")}, newTestRegistry(t, &stubSink{kind: "k1"}))
	if _, err := rep.List(context.Background(), ListFilter{}); err == nil {
		t.Fatal("expected list error")
	}
}

func TestReporter_Coverage(t *testing.T) {
	t.Parallel()
	t.Run("unknown kind", func(t *testing.T) {
		t.Parallel()
		rep := NewReporter(&recordStore{}, newTestRegistry(t, &stubSink{kind: "k1"}))
		if _, err := rep.Coverage(context.Background(), "ghost"); !errors.Is(err, ErrUnknownKind) {
			t.Fatalf("Coverage(ghost) err = %v; want ErrUnknownKind", err)
		}
	})
	t.Run("sink without coverage reports nil", func(t *testing.T) {
		t.Parallel()
		rep := NewReporter(&recordStore{}, newTestRegistry(t, &stubSink{kind: "k1"}))
		cov, err := rep.Coverage(context.Background(), "k1")
		if err != nil {
			t.Fatalf("Coverage: %v", err)
		}
		if cov != nil {
			t.Errorf("Coverage = %+v; want nil for non-reporter sink", cov)
		}
	})
	t.Run("sink with coverage", func(t *testing.T) {
		t.Parallel()
		sink := &coverageSink{stubSink: stubSink{kind: "k1"}, cov: Coverage{Indexed: 7, Expected: 10, ExpectedKnown: true}}
		rep := NewReporter(&recordStore{}, newTestRegistry(t, sink))
		cov, err := rep.Coverage(context.Background(), "k1")
		if err != nil {
			t.Fatalf("Coverage: %v", err)
		}
		if cov == nil || cov.Indexed != 7 || cov.Expected != 10 || !cov.ExpectedKnown {
			t.Errorf("Coverage = %+v; want {7 10 true}", cov)
		}
	})
	t.Run("coverage error wrapped", func(t *testing.T) {
		t.Parallel()
		sink := &coverageSink{stubSink: stubSink{kind: "k1"}, covErr: errors.New("boom")}
		rep := NewReporter(&recordStore{}, newTestRegistry(t, sink))
		if _, err := rep.Coverage(context.Background(), "k1"); err == nil {
			t.Fatal("expected error from coverage")
		}
	})
}

func TestReporter_List(t *testing.T) {
	t.Parallel()
	store := &recordStore{list: []Job{{ID: 1}, {ID: 2}}}
	rep := NewReporter(store, newTestRegistry(t, &stubSink{kind: "k1"}))
	jobs, err := rep.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != 2 {
		t.Errorf("List len = %d; want 2", len(jobs))
	}
}

func TestReporter_Reindex(t *testing.T) {
	t.Parallel()
	t.Run("unknown kind", func(t *testing.T) {
		t.Parallel()
		rep := NewReporter(&recordStore{}, newTestRegistry(t, &stubSink{kind: "k1"}))
		if _, err := rep.Reindex(context.Background(), "ghost", ""); !errors.Is(err, ErrUnknownKind) {
			t.Fatalf("Reindex(ghost) err = %v; want ErrUnknownKind", err)
		}
	})
	t.Run("explicit source id", func(t *testing.T) {
		t.Parallel()
		store := &recordStore{}
		rep := NewReporter(store, newTestRegistry(t, &stubSink{kind: "k1", gaps: []string{"a", "b"}}))
		ids, err := rep.Reindex(context.Background(), "k1", "unit-1")
		if err != nil {
			t.Fatalf("Reindex: %v", err)
		}
		if len(ids) != 1 || ids[0] != "unit-1" {
			t.Fatalf("enqueued ids = %v; want [unit-1]", ids)
		}
		if len(store.enqueued) != 1 || store.enqueued[0].SourceID != "unit-1" || store.enqueued[0].SourceKind != "k1" {
			t.Errorf("enqueued = %+v; want one k1/unit-1", store.enqueued)
		}
		if store.enqTrigger[0] != TriggerManualRetry {
			t.Errorf("trigger = %q; want manual_retry", store.enqTrigger[0])
		}
	})
	t.Run("kind-wide uses gaps", func(t *testing.T) {
		t.Parallel()
		store := &recordStore{}
		rep := NewReporter(store, newTestRegistry(t, &stubSink{kind: "k1", gaps: []string{"a", "b"}}))
		ids, err := rep.Reindex(context.Background(), "k1", "")
		if err != nil {
			t.Fatalf("Reindex: %v", err)
		}
		if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
			t.Fatalf("enqueued ids = %v; want [a b]", ids)
		}
		if len(store.enqueued) != 2 {
			t.Errorf("enqueued %d jobs; want 2", len(store.enqueued))
		}
	})
	t.Run("enqueue error returns partial", func(t *testing.T) {
		t.Parallel()
		store := &recordStore{enqErr: errors.New("db down")}
		rep := NewReporter(store, newTestRegistry(t, &stubSink{kind: "k1"}))
		if _, err := rep.Reindex(context.Background(), "k1", "unit-1"); err == nil {
			t.Fatal("expected enqueue error")
		}
	})
	t.Run("find gaps error", func(t *testing.T) {
		t.Parallel()
		rep := NewReporter(&recordStore{}, newTestRegistry(t, &gapErrSink{stubSink: stubSink{kind: "k1"}}))
		if _, err := rep.Reindex(context.Background(), "k1", ""); err == nil {
			t.Fatal("expected find-gaps error")
		}
	})
}

// gapErrSink is a stubSink whose FindGaps fails, for the Reindex
// kind-wide error path.
type gapErrSink struct {
	stubSink
}

func (*gapErrSink) FindGaps(context.Context) ([]string, error) {
	return nil, errors.New("gap query failed")
}

func TestDeriveVerdict(t *testing.T) {
	t.Parallel()
	activity := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		counts *KindCounts
		cov    *Coverage
		want   Verdict
	}{
		{
			name:   "running takes priority over failures",
			counts: &KindCounts{Running: 1, UnresolvedFailures: 3},
			cov:    &Coverage{Indexed: 1, Expected: 10, ExpectedKnown: true},
			want:   VerdictIndexing,
		},
		{
			name:   "pending is indexing",
			counts: &KindCounts{Pending: 2},
			want:   VerdictIndexing,
		},
		{
			name:   "unresolved failures degrade when idle",
			counts: &KindCounts{Succeeded: 5, UnresolvedFailures: 1, LastActivity: &activity},
			cov:    &Coverage{Indexed: 5, Expected: 5, ExpectedKnown: true},
			want:   VerdictDegraded,
		},
		{
			name:   "known coverage shortfall degrades",
			counts: &KindCounts{Succeeded: 3, LastActivity: &activity},
			cov:    &Coverage{Indexed: 3, Expected: 10, ExpectedKnown: true},
			want:   VerdictDegraded,
		},
		{
			name:   "complete with history is healthy",
			counts: &KindCounts{Succeeded: 10, LastActivity: &activity},
			cov:    &Coverage{Indexed: 10, Expected: 10, ExpectedKnown: true},
			want:   VerdictHealthy,
		},
		{
			// Complete with no job history (vectors seeded outside the
			// queue) is the same resting state as one with history: both
			// are healthy. Recency lives in last_activity, not the verdict.
			name:   "complete without history is healthy (the #509 seeded case)",
			counts: &KindCounts{},
			cov:    &Coverage{Indexed: 34, Expected: 34, ExpectedKnown: true},
			want:   VerdictHealthy,
		},
		{
			name:   "in-sync continuous kind with history is healthy",
			counts: &KindCounts{Succeeded: 1, LastActivity: &activity},
			cov:    &Coverage{Indexed: 50, ExpectedKnown: false},
			want:   VerdictHealthy,
		},
		{
			name:   "in-sync continuous kind without history is healthy",
			counts: &KindCounts{},
			cov:    &Coverage{Indexed: 50, ExpectedKnown: false},
			want:   VerdictHealthy,
		},
		{
			name:   "no coverage with history is healthy",
			counts: &KindCounts{Succeeded: 2, LastActivity: &activity},
			want:   VerdictHealthy,
		},
		{
			name:   "nil counts is healthy",
			counts: nil,
			cov:    &Coverage{Indexed: 1, Expected: 1, ExpectedKnown: true},
			want:   VerdictHealthy,
		},
		{
			name:   "empty kind is healthy",
			counts: &KindCounts{},
			want:   VerdictHealthy,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DeriveVerdict(tc.counts, tc.cov); got != tc.want {
				t.Errorf("DeriveVerdict() = %q; want %q", got, tc.want)
			}
		})
	}
}

func TestReporter_ActiveFailures(t *testing.T) {
	t.Parallel()
	t.Run("passes through units", func(t *testing.T) {
		t.Parallel()
		want := []FailedUnit{{SourceKind: "k1", SourceID: "u1", Occurrences: 2, LastError: "boom"}}
		rep := NewReporter(&recordStore{failures: want}, newTestRegistry(t, &stubSink{kind: "k1"}))
		got, err := rep.ActiveFailures(context.Background(), "k1", 50)
		if err != nil {
			t.Fatalf("ActiveFailures: %v", err)
		}
		if len(got) != 1 || got[0].SourceID != "u1" || got[0].Occurrences != 2 {
			t.Errorf("ActiveFailures = %+v", got)
		}
	})
	t.Run("wraps store error", func(t *testing.T) {
		t.Parallel()
		rep := NewReporter(&recordStore{failuresErr: errors.New("db down")}, newTestRegistry(t, &stubSink{kind: "k1"}))
		if _, err := rep.ActiveFailures(context.Background(), "", 0); err == nil {
			t.Fatal("expected active-failures error")
		}
	})
}

func TestReporter_Resolve(t *testing.T) {
	t.Parallel()
	t.Run("resolves for unregistered kind too", func(t *testing.T) {
		t.Parallel()
		// A leftover-kind tombstone (no consumer registered) must still
		// be dismissable; Resolve does not gate on registration.
		store := &recordStore{resolved: 3}
		rep := NewReporter(store, newTestRegistry(t, &stubSink{kind: "k1"}))
		n, err := rep.Resolve(context.Background(), "ghost_kind", "u9")
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if n != 3 {
			t.Errorf("resolved = %d; want 3", n)
		}
		if store.resolveKey != (Key{SourceKind: "ghost_kind", SourceID: "u9"}) {
			t.Errorf("resolveKey = %+v", store.resolveKey)
		}
	})
	t.Run("wraps store error", func(t *testing.T) {
		t.Parallel()
		rep := NewReporter(&recordStore{resolveErr: errors.New("db down")}, newTestRegistry(t, &stubSink{kind: "k1"}))
		if _, err := rep.Resolve(context.Background(), "k1", "u1"); err == nil {
			t.Fatal("expected resolve error")
		}
	})
}
