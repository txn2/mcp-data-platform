package middleware

import (
	"testing"
	"time"
)

func newFailure(sql, connection string) FailedQuery {
	return FailedQuery{
		NormalizedSQL: normalizeSQLText(sql),
		RawSQL:        sql,
		Idents:        meaningfulIdentifiers(sql),
		Connection:    connection,
		ErrorMessage:  "Column 'x' cannot be resolved",
		FailedAt:      time.Now(),
	}
}

func TestSessionErrorTracker_RecordAndResolve(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()

	tr.RecordFailure("s1", newFailure("SELECT custmer_id FROM sales.orders", "primary"))

	// A related success on the same connection with different SQL resolves it.
	resolved := tr.TakeResolved("s1", "primary",
		meaningfulIdentifiers("SELECT customer_id FROM sales.orders"),
		normalizeSQLText("SELECT customer_id FROM sales.orders"))
	if resolved == nil {
		t.Fatal("expected the failure to resolve")
	}
	if resolved.RawSQL != "SELECT custmer_id FROM sales.orders" {
		t.Errorf("unexpected resolved failure: %+v", resolved)
	}
	// Consumed: a second success finds nothing.
	if again := tr.TakeResolved("s1", "primary",
		meaningfulIdentifiers("SELECT customer_id FROM sales.orders"), "select 1"); again != nil {
		t.Errorf("failure should have been consumed, got %+v", again)
	}
}

func TestSessionErrorTracker_DifferentConnectionNotPaired(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()

	tr.RecordFailure("s1", newFailure("SELECT custmer_id FROM hive.sales.orders", "clusterA"))
	// Same SQL shape but a DIFFERENT connection: physically different dataset.
	if resolved := tr.TakeResolved("s1", "clusterB",
		meaningfulIdentifiers("SELECT customer_id FROM hive.sales.orders"),
		normalizeSQLText("SELECT customer_id FROM hive.sales.orders")); resolved != nil {
		t.Errorf("cross-connection success must not pair, got %+v", resolved)
	}
}

func TestSessionErrorTracker_UnrelatedSuccessNotPaired(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()

	tr.RecordFailure("s1", newFailure("SELECT bad_col FROM sales.orders", "primary"))
	// A wholly unrelated success over the same table: low identifier overlap.
	if resolved := tr.TakeResolved("s1", "primary",
		meaningfulIdentifiers("SELECT count(*) FROM sales.orders"),
		normalizeSQLText("SELECT count(*) FROM sales.orders")); resolved != nil {
		t.Errorf("unrelated success must not pair, got %+v", resolved)
	}
}

func TestSessionErrorTracker_IdenticalRetryNotPaired(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()

	sql := "SELECT a, b FROM sales.orders WHERE id > 1"
	tr.RecordFailure("s1", newFailure(sql, "primary"))

	// Same normalized SQL succeeding on retry is a transient recovery, not a fix.
	if resolved := tr.TakeResolved("s1", "primary", meaningfulIdentifiers(sql), normalizeSQLText(sql)); resolved != nil {
		t.Errorf("identical retry must not pair, got %+v", resolved)
	}
	// A genuine edit of the same query still pairs.
	fix := "SELECT a, c FROM sales.orders WHERE id > 1"
	if resolved := tr.TakeResolved("s1", "primary", meaningfulIdentifiers(fix), normalizeSQLText(fix)); resolved == nil {
		t.Error("a genuine edit should pair")
	}
}

func TestSessionErrorTracker_OnlyBestMatchConsumed(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()

	// Two distinct misconceptions in one session over the same table.
	tr.RecordFailure("s1", newFailure("SELECT custmer_id FROM sales.orders", "primary"))
	tr.RecordFailure("s1", newFailure("SELECT amont FROM sales.orders WHERE region = 'x'", "primary"))

	// A fix for the second one resolves only it; the first remains for its own fix.
	fix2 := "SELECT amount FROM sales.orders WHERE region = 'x'"
	r := tr.TakeResolved("s1", "primary", meaningfulIdentifiers(fix2), normalizeSQLText(fix2))
	if r == nil || r.RawSQL != "SELECT amont FROM sales.orders WHERE region = 'x'" {
		t.Fatalf("expected the amount misconception to resolve, got %+v", r)
	}
	fix1 := "SELECT customer_id FROM sales.orders"
	r = tr.TakeResolved("s1", "primary", meaningfulIdentifiers(fix1), normalizeSQLText(fix1))
	if r == nil || r.RawSQL != "SELECT custmer_id FROM sales.orders" {
		t.Errorf("earlier misconception must not have been discarded, got %+v", r)
	}
}

func TestSessionErrorTracker_ExpiredNotPaired(t *testing.T) {
	tr := NewSessionErrorTracker(10*time.Millisecond, time.Minute)
	defer tr.Stop()

	f := newFailure("SELECT custmer_id FROM sales.orders", "primary")
	f.FailedAt = time.Now().Add(-time.Second) // already expired
	tr.RecordFailure("s1", f)

	fix := "SELECT customer_id FROM sales.orders"
	if resolved := tr.TakeResolved("s1", "primary", meaningfulIdentifiers(fix), normalizeSQLText(fix)); resolved != nil {
		t.Errorf("expired failure must not pair, got %+v", resolved)
	}
}

func TestSessionErrorTracker_BlankSessionIgnored(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()

	tr.RecordFailure("", newFailure("SELECT 1 FROM sales.orders", "primary"))
	if tr.SessionCount() != 0 {
		t.Errorf("blank session should not be tracked, count=%d", tr.SessionCount())
	}
	if resolved := tr.TakeResolved("", "primary", meaningfulIdentifiers("SELECT 1"), "select 1"); resolved != nil {
		t.Errorf("blank session resolve should be nil, got %+v", resolved)
	}
	if resolved := tr.TakeResolved("s1", "primary", nil, "select 1"); resolved != nil {
		t.Errorf("empty identifier set resolve should be nil, got %+v", resolved)
	}
}

func TestSessionErrorTracker_CapAndCleanup(t *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	defer tr.Stop()

	for range defaultMaxFailuresPerSession + 10 {
		tr.RecordFailure("s1", newFailure("SELECT bad FROM sales.orders", "primary"))
	}
	tr.mu.Lock()
	got := len(tr.sessions["s1"].failures)
	tr.mu.Unlock()
	if got != defaultMaxFailuresPerSession {
		t.Errorf("expected cap at %d, got %d", defaultMaxFailuresPerSession, got)
	}

	// cleanup evicts idle sessions past sessionTimeout.
	tr2 := NewSessionErrorTracker(time.Minute, 10*time.Millisecond)
	defer tr2.Stop()
	tr2.RecordFailure("old", newFailure("SELECT 1 FROM sales.orders", "primary"))
	tr2.mu.Lock()
	tr2.sessions["old"].lastAccess = time.Now().Add(-time.Second)
	tr2.mu.Unlock()
	tr2.cleanup()
	if tr2.SessionCount() != 0 {
		t.Errorf("idle session should be evicted, count=%d", tr2.SessionCount())
	}
}

func TestSessionErrorTracker_CleanupPrunesExpiredFailures(t *testing.T) {
	tr := NewSessionErrorTracker(10*time.Millisecond, time.Minute)
	defer tr.Stop()

	// Fresh session (not idle) but with an already-expired failure.
	f := newFailure("SELECT bad FROM sales.orders", "primary")
	f.FailedAt = time.Now().Add(-time.Second)
	tr.RecordFailure("s1", f)

	tr.cleanup()

	// The session survives (recently accessed) but the expired failure is pruned.
	if tr.SessionCount() != 1 {
		t.Fatalf("fresh session should survive cleanup, count=%d", tr.SessionCount())
	}
	tr.mu.Lock()
	remaining := len(tr.sessions["s1"].failures)
	tr.mu.Unlock()
	if remaining != 0 {
		t.Errorf("expired failure should be pruned, remaining=%d", remaining)
	}
}

func TestSessionErrorTracker_StopIdempotent(_ *testing.T) {
	tr := NewSessionErrorTracker(time.Minute, time.Minute)
	tr.StartCleanup(time.Millisecond)
	tr.Stop()
	tr.Stop() // must not panic
}

func TestJaccardSimilarity(t *testing.T) {
	a := meaningfulIdentifiers("SELECT custmer_id FROM sales.orders")
	b := meaningfulIdentifiers("SELECT customer_id FROM sales.orders")
	if sim := jaccardSimilarity(a, b); sim < minPairingSimilarity {
		t.Errorf("near-identical queries should clear the threshold, got %.2f", sim)
	}
	if jaccardSimilarity(nil, b) != 0 || jaccardSimilarity(a, nil) != 0 {
		t.Error("empty set similarity should be 0")
	}
}

func TestHasNovelIdent(t *testing.T) {
	failed := meaningfulIdentifiers("SELECT bad_col FROM sales.orders")
	// A column-rename fix introduces the corrected identifier.
	if !hasNovelIdent(meaningfulIdentifiers("SELECT good_col FROM sales.orders"), failed) {
		t.Error("a fix that renames the column should be novel")
	}
	// A bare aggregate contributes no new identifier (its idents ⊆ the failure).
	if hasNovelIdent(meaningfulIdentifiers("SELECT count(*) FROM sales.orders"), failed) {
		t.Error("a bare sub-query over the same table should not count as a fix")
	}
}
