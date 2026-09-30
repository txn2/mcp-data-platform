package whadmin

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

func TestHealthOf(t *testing.T) {
	recent := now.Add(-time.Hour)
	stale := now.Add(-SilentAfter - time.Second)
	edge := now.Add(-SilentAfter)
	on := whsource.Source{Enabled: true}
	cases := []struct {
		name string
		src  whsource.Source
		sum  whstore.Summary
		want Health
	}{
		{"disabled outranks failing and silence", whsource.Source{}, whstore.Summary{Failing: 2}, HealthDisabled},
		{"failing outranks silence", on, whstore.Summary{Failing: 1}, HealthFailing},
		{"failing while receiving", on, whstore.Summary{Failing: 1, LastSegmentAt: &recent}, HealthFailing},
		{"never received an event", on, whstore.Summary{}, HealthSilent},
		{"no event in the last day", on, whstore.Summary{LastSegmentAt: &stale}, HealthSilent},
		{"an event exactly a day ago still counts", on, whstore.Summary{LastSegmentAt: &edge}, HealthReceiving},
		{"receiving", on, whstore.Summary{LastSegmentAt: &recent, Pending: 3}, HealthReceiving},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, HealthOf(c.src, c.sum, now))
		})
	}
}

func TestOverview(t *testing.T) {
	r := newRig()
	recent := now.Add(-time.Minute)
	r.sources.rows["zeta"] = whsource.Source{Name: "zeta", Enabled: true}
	r.sources.rows["alpha"] = whsource.Source{Name: "alpha", Enabled: true}
	r.sources.rows["off"] = whsource.Source{Name: "off"}
	r.windows.overview = whstore.Overview{
		Since: now.Add(-time.Hour),
		Summaries: map[string]whstore.Summary{
			"alpha": {LastHour: map[string]int64{"accepted": 4}, LastDay: map[string]int64{"accepted": 9}, LastSegmentAt: &recent},
			"zeta":  {LastHour: map[string]int64{}, LastDay: map[string]int64{}, Failing: 1, LastError: "segment x: bad gzip"},
		},
		Volume:     []whstore.VolumePoint{{Source: "alpha", At: now.Add(-time.Minute), Outcome: "accepted", Count: 4}},
		Rejections: []whstore.Rejection{{Source: "zeta", At: now, Outcome: "unauthorized", Reason: "the signature does not match"}},
	}

	ov, err := r.svc.Overview(ctx, time.Hour, time.Minute)
	require.NoError(t, err)
	assert.Equal(t, time.Hour, r.windows.span)
	assert.Equal(t, time.Minute, r.windows.step)
	assert.Equal(t, now, ov.Now)
	assert.Equal(t, now.Add(-time.Hour), ov.Since)
	assert.Equal(t, time.Minute, ov.Step)
	require.Len(t, ov.Sources, 3)
	assert.Equal(t, []string{"alpha", "off", "zeta"}, []string{ov.Sources[0].Source.Name, ov.Sources[1].Source.Name, ov.Sources[2].Source.Name})
	assert.Equal(t, HealthReceiving, ov.Sources[0].Health)
	assert.Equal(t, HealthDisabled, ov.Sources[1].Health)
	assert.Equal(t, HealthFailing, ov.Sources[2].Health)
	assert.Equal(t, "segment x: bad gzip", ov.Sources[2].Summary.LastError)
	assert.NotNil(t, ov.Sources[1].Summary.LastHour, "a source with no summary gets empty counts, not nil")
	assert.NotNil(t, ov.Sources[1].Summary.LastDay)
	assert.Len(t, ov.Volume, 1)
	assert.Len(t, ov.Rejections, 1)
}

func TestOverviewEmpty(t *testing.T) {
	r := newRig()
	ov, err := r.svc.Overview(ctx, time.Hour, time.Minute)
	require.NoError(t, err)
	assert.NotNil(t, ov.Sources)
	assert.Empty(t, ov.Sources)
	assert.NotNil(t, ov.Volume, "no volume is an empty list, never nil")
	assert.Empty(t, ov.Volume)
	assert.NotNil(t, ov.Rejections)
	assert.Empty(t, ov.Rejections)
}

func TestOverviewFailures(t *testing.T) {
	r := newRig()
	r.sources.err["list"] = errBoom
	_, err := r.svc.Overview(ctx, time.Hour, time.Minute)
	require.ErrorIs(t, err, errBoom)

	r = newRig()
	r.windows.err["overview"] = errBoom
	_, err = r.svc.Overview(ctx, time.Hour, time.Minute)
	require.ErrorIs(t, err, errBoom)
}
