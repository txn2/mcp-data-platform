package audit

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestZeroFill(t *testing.T) {
	base := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		buckets    []TimeseriesBucket
		start      time.Time
		end        time.Time
		resolution Resolution
		wantLen    int
		wantNonZ   int // expected number of non-zero Count buckets
	}{
		{
			name: "fills gaps in sparse data",
			buckets: []TimeseriesBucket{
				{Bucket: base, Count: 5, SuccessCount: 4, ErrorCount: 1},
				{Bucket: base.Add(2 * time.Hour), Count: 3, SuccessCount: 3},
				{Bucket: base.Add(4 * time.Hour), Count: 1, SuccessCount: 1},
			},
			start:      base,
			end:        base.Add(4 * time.Hour),
			resolution: ResolutionHour,
			wantLen:    5, // hours 0,1,2,3,4
			wantNonZ:   3,
		},
		{
			name:       "empty input returns full zero series",
			buckets:    nil,
			start:      base,
			end:        base.Add(3 * time.Hour),
			resolution: ResolutionHour,
			wantLen:    4, // hours 0,1,2,3
			wantNonZ:   0,
		},
		{
			name: "all populated returns unchanged length",
			buckets: []TimeseriesBucket{
				{Bucket: base, Count: 1},
				{Bucket: base.Add(time.Hour), Count: 2},
				{Bucket: base.Add(2 * time.Hour), Count: 3},
			},
			start:      base,
			end:        base.Add(2 * time.Hour),
			resolution: ResolutionHour,
			wantLen:    3,
			wantNonZ:   3,
		},
		{
			name: "single data point in 24h range",
			buckets: []TimeseriesBucket{
				{Bucket: base.Add(12 * time.Hour), Count: 7, SuccessCount: 7},
			},
			start:      base,
			end:        base.Add(23 * time.Hour),
			resolution: ResolutionHour,
			wantLen:    24, // hours 0..23
			wantNonZ:   1,
		},
		{
			name: "minute resolution",
			buckets: []TimeseriesBucket{
				{Bucket: base, Count: 2},
				{Bucket: base.Add(4 * time.Minute), Count: 1},
			},
			start:      base,
			end:        base.Add(4 * time.Minute),
			resolution: ResolutionMinute,
			wantLen:    5, // minutes 0,1,2,3,4
			wantNonZ:   2,
		},
		{
			name: "day resolution",
			buckets: []TimeseriesBucket{
				{Bucket: time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC), Count: 10},
				{Bucket: time.Date(2025, 6, 17, 0, 0, 0, 0, time.UTC), Count: 5},
			},
			start:      time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
			end:        time.Date(2025, 6, 18, 0, 0, 0, 0, time.UTC),
			resolution: ResolutionDay,
			wantLen:    4, // days 15,16,17,18
			wantNonZ:   2,
		},
		{
			name:       "start equals end returns single bucket",
			buckets:    nil,
			start:      base,
			end:        base,
			resolution: ResolutionHour,
			wantLen:    1,
			wantNonZ:   0,
		},
		{
			name:       "start within bucket truncates to boundary",
			buckets:    nil,
			start:      base.Add(30 * time.Minute),             // 10:30
			end:        base.Add(2*time.Hour + 30*time.Minute), // 12:30
			resolution: ResolutionHour,
			wantLen:    3, // 10:00, 11:00, 12:00
			wantNonZ:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ZeroFill(tt.buckets, tt.start, tt.end, tt.resolution)
			require.Len(t, result, tt.wantLen)

			nonZero := 0
			for _, b := range result {
				if b.Count > 0 {
					nonZero++
				}
			}
			assert.Equal(t, tt.wantNonZ, nonZero)

			// Verify ordering is ascending.
			for i := 1; i < len(result); i++ {
				assert.True(t, result[i].Bucket.After(result[i-1].Bucket),
					"bucket %d (%v) should be after bucket %d (%v)",
					i, result[i].Bucket, i-1, result[i-1].Bucket)
			}
		})
	}
}

func TestZeroFill_PreservesOriginalData(t *testing.T) {
	base := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	buckets := []TimeseriesBucket{
		{Bucket: base, Count: 5, SuccessCount: 4, ErrorCount: 1, AvgDurationMS: 42.5},
	}

	result := ZeroFill(buckets, base, base.Add(2*time.Hour), ResolutionHour)
	require.Len(t, result, 3)

	// Original data preserved.
	assert.Equal(t, 5, result[0].Count)
	assert.Equal(t, 4, result[0].SuccessCount)
	assert.Equal(t, 1, result[0].ErrorCount)
	assert.InDelta(t, 42.5, result[0].AvgDurationMS, 0.01)

	// Zero-filled buckets.
	assert.Equal(t, 0, result[1].Count)
	assert.Equal(t, 0, result[2].Count)
}

func TestZeroFill_InvalidResolution(t *testing.T) {
	base := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	buckets := []TimeseriesBucket{{Bucket: base, Count: 1}}

	result := ZeroFill(buckets, base, base.Add(time.Hour), Resolution("invalid"))
	assert.Equal(t, buckets, result)
}

func TestResolutionInterval(t *testing.T) {
	assert.Equal(t, time.Minute, resolutionInterval(ResolutionMinute))
	assert.Equal(t, time.Hour, resolutionInterval(ResolutionHour))
	assert.Equal(t, 24*time.Hour, resolutionInterval(ResolutionDay))
	assert.Equal(t, time.Duration(0), resolutionInterval(Resolution("invalid")))
}

func TestTruncateTime(t *testing.T) {
	ts := time.Date(2025, 6, 15, 14, 35, 42, 123456789, time.UTC)

	assert.Equal(t,
		time.Date(2025, 6, 15, 14, 35, 0, 0, time.UTC),
		truncateTime(ts, ResolutionMinute))

	assert.Equal(t,
		time.Date(2025, 6, 15, 14, 0, 0, 0, time.UTC),
		truncateTime(ts, ResolutionHour))

	assert.Equal(t,
		time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC),
		truncateTime(ts, ResolutionDay))

	// Invalid resolution returns time unchanged.
	assert.Equal(t, ts, truncateTime(ts, Resolution("invalid")))
}

func TestZeroFill_EmptySliceNotNil(t *testing.T) {
	base := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	// End before start after truncation → could produce empty result.
	result := ZeroFill(nil, base, base, ResolutionHour)
	assert.NotNil(t, result)
}

func TestCheckTimeseriesRange(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	ok := []struct {
		start, end time.Time
		r          Resolution
	}{
		{now.Add(-24 * time.Hour), now, ResolutionMinute},
		{now.Add(-6 * 24 * time.Hour), now, ResolutionMinute},
		{now.AddDate(-1, 0, 0), now, ResolutionHour},
		{now.AddDate(-20, 0, 0), now, ResolutionDay},
		{now, now, ResolutionMinute},
	}
	for _, c := range ok {
		if err := CheckTimeseriesRange(c.start, c.end, c.r); err != nil {
			t.Errorf("%s..%s %s refused: %v", c.start, c.end, c.r, err)
		}
	}
	refused := []struct {
		start, end time.Time
		r          Resolution
	}{
		{now.Add(-7 * 24 * time.Hour), now, ResolutionMinute},
		{time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), now, ResolutionMinute},
		{now, time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), ResolutionHour},
		{time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC), ResolutionDay},
		{now, now.Add(-time.Minute), ResolutionHour},
	}
	for _, c := range refused {
		if err := CheckTimeseriesRange(c.start, c.end, c.r); !errors.Is(err, ErrTimeseriesRange) {
			t.Errorf("%s..%s %s = %v, want ErrTimeseriesRange", c.start, c.end, c.r, err)
		}
	}
	if err := CheckTimeseriesRange(now, now.Add(1000*time.Hour), Resolution("week")); err != nil {
		t.Errorf("an unknown resolution has no bucket bound: %v", err)
	}
}

// TestZeroFill_NeverExpandsPastTheBound: a range the check refuses is answered
// with the sparse buckets, never a materialized series.
func TestZeroFill_NeverExpandsPastTheBound(t *testing.T) {
	start := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sparse := []TimeseriesBucket{{Bucket: end, Count: 3}}
	got := ZeroFill(sparse, start, end, ResolutionMinute)
	if len(got) != 1 {
		t.Fatalf("ZeroFill expanded a refused range to %d buckets", len(got))
	}
}
