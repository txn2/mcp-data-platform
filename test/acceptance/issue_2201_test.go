//go:build integration

package acceptance

import (
	"fmt"
	"strconv"
	"testing"
	"time"
)

// Acceptance for #2201: the Storage page on a fresh install projected a
// first-boot burst as daily growth, repeated it under the 30-day heading, and
// read a store that never changed size as shrinking. Through the admin
// storage route: an hour and a half of history gives no rate and says how much
// each rate needs, three days give the weekly rate and not the monthly one,
// and a store holding one size grows at exactly zero.
//
// Wire forms: the storage report is a GET with no body; the settings write
// takes `budgets` as an object of bucket to a JSON number, the one form its
// schema admits.

// issue2201Seed writes a synthetic bucket store's samples, at interval from
// now-span to now, used as a function of the time since the first, and a
// budget so the store has a capacity. It returns the store's key.
func issue2201Seed(t *testing.T, span, interval time.Duration, used func(time.Duration) int64) string {
	t.Helper()
	c := connect(t)
	db := devDB(t)
	bucket := "acc2201-" + strconv.FormatInt(time.Now().UnixNano()%1e9, 36)
	store := "bucket:" + bucket
	now := time.Now().UTC().Truncate(time.Second)
	first := now.Add(-span)
	var last int64
	for at := first; !at.After(now); at = at.Add(interval) {
		last = used(at.Sub(first))
		if _, err := db.Exec(`INSERT INTO storage_samples (store, sampled_at, used_bytes, total_bytes) VALUES ($1, $2, $3, $4)`,
			store, at, last, int64(1)<<40); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM storage_samples WHERE store = $1`, store)
		_, _ = db.Exec(`DELETE FROM storage_usage WHERE bucket = $1`, bucket)
	})
	putSettings(t, c, issue2148Path, map[string]any{"budgets": map[string]any{bucket: int64(1) << 40}})
	waitFor(t, "the synthetic store in the report", func() (bool, string) {
		if _, err := db.Exec(`INSERT INTO storage_usage (bucket, purpose, bytes, objects, updated_at) VALUES ($1, 'resources', $2, 1, NOW())
			ON CONFLICT (bucket, purpose) DO UPDATE SET bytes = EXCLUDED.bytes`, bucket, last); err != nil {
			t.Fatal(err)
		}
		st := issue2148Store(t, c, store)
		return st != nil, fmt.Sprintf("%v", st)
	})
	return store
}

// TestIssue2201_AFirstBootBurstIsNoDailyRate: 16 MB to 28 MB over 1h42m, the
// samples #2201 was reported from, is no rate, and the report says how much
// history the store has and each rate needs.
func TestIssue2201_AFirstBootBurstIsNoDailyRate(t *testing.T) {
	store := issue2201Seed(t, 102*time.Minute, 6*time.Minute, func(d time.Duration) int64 {
		return 16_075_923 + int64(float64(28_011_667-16_075_923)*d.Minutes()/102)
	})
	st := issue2148Store(t, connect(t), store)
	if st["rate_7d_bytes_per_day"] != nil || st["rate_30d_bytes_per_day"] != nil {
		t.Fatalf("an hour and a half of history gave a rate: %v", st)
	}
	if st["exhaustion_at"] != nil {
		t.Fatalf("a store with no rate has a fill date: %v", st)
	}
	if h, _ := st["history_hours"].(float64); h < 1.6 || h > 1.8 {
		t.Fatalf("history_hours = %v; the samples span 1.7 h", st["history_hours"])
	}
	if st["rate_7d_min_history_hours"] != 24.0 || st["rate_30d_min_history_hours"] != 168.0 {
		t.Fatalf("the report does not say what each rate needs: %v", st)
	}
}

// TestIssue2201_TheMonthlyRateWaitsForAWeek: three days of history give the
// weekly rate and no monthly one, so the two columns are never one
// measurement shown twice.
func TestIssue2201_TheMonthlyRateWaitsForAWeek(t *testing.T) {
	const perDay = int64(1) << 30
	store := issue2201Seed(t, 72*time.Hour, time.Hour, func(d time.Duration) int64 {
		return 10*perDay + int64(float64(perDay)*d.Hours()/24)
	})
	st := issue2148Store(t, connect(t), store)
	r7, ok := st["rate_7d_bytes_per_day"].(float64)
	if !ok || r7 < float64(perDay)*0.9 || r7 > float64(perDay)*1.1 {
		t.Fatalf("three days growing 1 GiB a day: rate_7d = %v", st["rate_7d_bytes_per_day"])
	}
	if st["rate_30d_bytes_per_day"] != nil {
		t.Fatalf("three days of history gave a 30-day rate: %v", st)
	}
	if st["rate_used"] != "7d" {
		t.Fatalf("the projection should use the 7-day rate: %v", st)
	}
}

// TestIssue2201_AFlatStoreGrowsAtZero: a store at 16,801,792 bytes at every
// sample for ten days grows at exactly zero, not -0.15 B/day.
func TestIssue2201_AFlatStoreGrowsAtZero(t *testing.T) {
	store := issue2201Seed(t, 10*24*time.Hour, 15*time.Minute, func(time.Duration) int64 { return 16_801_792 })
	st := issue2148Store(t, connect(t), store)
	if st["rate_7d_bytes_per_day"] != 0.0 || st["rate_30d_bytes_per_day"] != 0.0 {
		t.Fatalf("a store holding one size: rate_7d = %v, rate_30d = %v; want exactly 0",
			st["rate_7d_bytes_per_day"], st["rate_30d_bytes_per_day"])
	}
	if st["exhaustion_at"] != nil {
		t.Fatalf("a flat store has a fill date: %v", st)
	}
}
