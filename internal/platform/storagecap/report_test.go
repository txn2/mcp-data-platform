package storagecap

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
)

func TestFindings(t *testing.T) {
	exhaustion := now.Add(5 * day)
	r := Report{Stores: []StoreView{
		{Store: "database", Name: "Database", UsedBytes: 95, TotalBytes: new(int64(100)), FreeBytes: new(int64(5)), PercentUsed: new(float64(95))},
		{
			Store: "bucket:portal", Name: "Bucket portal", UsedBytes: 80, TotalBytes: new(int64(100)), FreeBytes: new(int64(20)), PercentUsed: new(float64(80)),
			Forecast: Forecast{Rate7d: new(float64(4 << 30)), RateUsed: "7d", DaysLeft: new(float64(5)), ExhaustionAt: &exhaustion},
		},
		{
			Store: "spool:a", Name: "Webhook spool on a", UsedBytes: 10, TotalBytes: new(int64(100)), FreeBytes: new(int64(90)), PercentUsed: new(float64(10)),
			Forecast: Forecast{Rate30d: new(float64(1)), RateUsed: "30d", DaysLeft: new(float64(20)), ExhaustionAt: &exhaustion},
		},
		{Store: "bucket:other", Name: "Bucket other", UsedBytes: 10},
	}}
	got := Findings(r, Defaults(), "https://p/portal/admin/storage")
	byKey := map[string]levelalert.Finding{}
	for _, f := range got {
		byKey[f.Key] = f
	}
	require.Len(t, got, 4)
	assert.Equal(t, levelalert.Critical, byKey["storage:database:used"].Level)
	assert.Equal(t, "Database is 95% full", byKey["storage:database:used"].Title)
	assert.Contains(t, byKey["storage:database:used"].Body, "The critical threshold is 90%")
	assert.Equal(t, "https://p/portal/admin/storage", byKey["storage:database:used"].Link)
	assert.Equal(t, levelalert.Warning, byKey["storage:bucket:portal:used"].Level)
	assert.Equal(t, levelalert.Critical, byKey["storage:bucket:portal:exhaustion"].Level)
	assert.Equal(t, "Bucket portal fills in 5 days", byKey["storage:bucket:portal:exhaustion"].Title)
	assert.Contains(t, byKey["storage:bucket:portal:exhaustion"].Body, "4.0 GiB a day")
	assert.Equal(t, levelalert.Warning, byKey["storage:spool:a:exhaustion"].Level)
	assert.Equal(t, "critical", r.Stores[0].Level)
	assert.Equal(t, "critical", r.Stores[1].Level)
	assert.Equal(t, "warning", r.Stores[2].Level)
	assert.Empty(t, r.Stores[3].Level)

	off := Defaults()
	off.UsedPercent, off.DaysLeft = levelalert.Thresholds{}, levelalert.Thresholds{}
	assert.Empty(t, Findings(r, off, ""), "thresholds turned off find nothing")
}

func TestTextHelpers(t *testing.T) {
	assert.Equal(t, "512 B", bytesText(512))
	assert.Equal(t, "1.5 KiB", bytesText(1536))
	assert.Equal(t, "2.0 GiB", bytesText(2<<30))
	assert.Equal(t, "less than a day", daysText(0.4))
	assert.Equal(t, "1 day", daysText(1.2))
	assert.Equal(t, "12 days", daysText(11.6))
	assert.Equal(t, 0.0, rateOf(Forecast{}))
	assert.Equal(t, 3.0, rateOf(Forecast{Rate30d: new(float64(3)), RateUsed: "30d"}))
}

// TestUnjudged names the stores a report could not read: the cluster and its
// unbudgeted buckets when the cluster read failed, and a spool that missed
// its reports but is not long gone.
func TestUnjudged(t *testing.T) {
	r := Report{
		GeneratedAt: now,
		SeaweedFS:   &ClusterView{Error: "no SeaweedFS volume server answered"},
		Stores: []StoreView{
			{Store: "bucket:portal", Kind: KindBucket},
			{Store: "bucket:cloud", Kind: KindBucket, CapacityFrom: "budget"},
			{Store: "database", Kind: KindDatabase},
		},
		Spools: []SpoolView{
			{Replica: "late", UpdatedAt: now.Add(-5 * time.Minute)},
			{Replica: "gone", UpdatedAt: now.Add(-time.Hour)},
			{Replica: "fine", UpdatedAt: now, Reporting: true},
		},
	}
	assert.Equal(t, []string{"storage:seaweedfs:", "storage:bucket:portal:", "storage:spool:late:"}, Unjudged(r))
	assert.Empty(t, Unjudged(Report{GeneratedAt: now}))
}
