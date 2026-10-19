package whalert

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

var now = time.Date(2026, 10, 17, 12, 0, 30, 0, time.UTC)

func ago(d time.Duration) *time.Time {
	t := now.Add(-d)
	return &t
}

// levels is each finding's level by key.
func levels(fs []levelalert.Finding) map[string]levelalert.Level {
	out := map[string]levelalert.Level{}
	for _, f := range fs {
		out[f.Key] = f.Level
	}
	return out
}

func spool(mut func(*whstore.SpoolState)) Readings {
	sp := whstore.SpoolState{
		Replica: "a", Dir: "/spool", MaxBytes: 1000, MaxAge: time.Hour, UpdatedAt: now.Add(-10 * time.Second),
	}
	mut(&sp)
	return Readings{Now: now, Spools: []whstore.SpoolState{sp}, BaseURL: "https://p"}
}

// TestSpoolConditions walks each per-replica row of #2149's table across its
// warning and critical defaults.
func TestSpoolConditions(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*whstore.SpoolState)
		key  string
		want levelalert.Level
	}{
		{"fill under", func(s *whstore.SpoolState) { s.Bytes = 499 }, "webhook:spool_fill:a", ""},
		{"fill warning", func(s *whstore.SpoolState) { s.Bytes = 500 }, "webhook:spool_fill:a", levelalert.Warning},
		{"fill critical", func(s *whstore.SpoolState) { s.Bytes = 800 }, "webhook:spool_fill:a", levelalert.Critical},
		{"age warning", func(s *whstore.SpoolState) { s.Events, s.Oldest = 1, ago(16*time.Minute) }, "webhook:spool_age:a", levelalert.Warning},
		{"age critical", func(s *whstore.SpoolState) { s.Events, s.Oldest = 1, ago(37*time.Minute) }, "webhook:spool_age:a", levelalert.Critical},
		{"drain 2 in a row", func(s *whstore.SpoolState) { s.ConsecutiveFailures, s.FailingSince = 2, ago(time.Minute) }, "webhook:drain_failing:a", ""},
		{"drain 3 in a row", func(s *whstore.SpoolState) { s.ConsecutiveFailures, s.FailingSince = 3, ago(time.Minute) }, "webhook:drain_failing:a", levelalert.Warning},
		{"drain 10 in a row", func(s *whstore.SpoolState) { s.ConsecutiveFailures, s.FailingSince = 10, ago(time.Minute) }, "webhook:drain_failing:a", levelalert.Critical},
		{"drain failing 5 min", func(s *whstore.SpoolState) { s.ConsecutiveFailures, s.FailingSince = 1, ago(5*time.Minute) }, "webhook:drain_failing:a", levelalert.Critical},
		{"volume 30% free", func(s *whstore.SpoolState) { s.FsTotal, s.FsFree = new(int64(100)), new(int64(30)) }, "webhook:spool_volume:a", ""},
		{"volume 25% free", func(s *whstore.SpoolState) { s.FsTotal, s.FsFree = new(int64(100)), new(int64(25)) }, "webhook:spool_volume:a", levelalert.Warning},
		{"volume 10% free", func(s *whstore.SpoolState) { s.FsTotal, s.FsFree = new(int64(100)), new(int64(10)) }, "webhook:spool_volume:a", levelalert.Critical},
		{"stopped with events", func(s *whstore.SpoolState) { s.Events, s.UpdatedAt = 4, now.Add(-2*time.Minute) }, "webhook:not_reporting:a", levelalert.Critical},
		{"stopped empty", func(s *whstore.SpoolState) { s.UpdatedAt = now.Add(-time.Hour) }, "webhook:not_reporting:a", ""},
		{"stopped empty, last volume low", func(s *whstore.SpoolState) {
			s.UpdatedAt, s.FsTotal, s.FsFree = now.Add(-time.Hour), new(int64(100)), new(int64(5))
		}, "webhook:spool_volume:a", ""},
		{"stopped mid failure streak", func(s *whstore.SpoolState) {
			s.UpdatedAt, s.ConsecutiveFailures, s.FailingSince = now.Add(-time.Hour), 2, ago(2*time.Hour)
		}, "webhook:drain_failing:a", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := levels(Evaluate(Defaults(), spool(tc.mut)))
			assert.Equal(t, tc.want, got[tc.key], "%v", got)
		})
	}
}

// TestStoppedReplicaIsJudgedByItsSilenceAlone: a replica that stopped
// reporting with events is critical, and what it last reported is not judged
// as current.
func TestStoppedReplicaIsJudgedByItsSilenceAlone(t *testing.T) {
	fs := Evaluate(Defaults(), spool(func(s *whstore.SpoolState) {
		s.Events, s.Bytes, s.UpdatedAt = 4, 900, now.Add(-5*time.Minute)
	}))
	require.Len(t, fs, 1)
	assert.Equal(t, "webhook:not_reporting:a", fs[0].Key)
	assert.Equal(t, "Replica a holds 4 webhook events and stopped reporting", fs[0].Title)
	assert.Equal(t, "https://p/portal/admin/webhooks", fs[0].Link)
}

func minuteCounts(counts map[int]map[string]int64) map[time.Time]map[string]int64 {
	out := map[time.Time]map[string]int64{}
	for minsAgo, c := range counts {
		out[now.Truncate(time.Minute).Add(-time.Duration(minsAgo)*time.Minute)] = c
	}
	return out
}

func source(rd *whstore.SourceReadings) Readings {
	return Readings{
		Now: now, Sources: map[string]bool{"esp": true, "off": false}, Grace: 2 * time.Minute,
		BaseURL: "https://p", PerSource: map[string]*whstore.SourceReadings{"esp": rd},
	}
}

// TestSourceConditions walks each per-source row of #2149's table.
func TestSourceConditions(t *testing.T) {
	everyMinute := map[int]map[string]int64{}
	for m := range 5 {
		everyMinute[m] = map[string]int64{"spool_full": 1, "accepted": 10}
	}
	cases := []struct {
		name string
		rd   whstore.SourceReadings
		key  string
		want levelalert.Level
	}{
		{"no refusals", whstore.SourceReadings{Outcomes: minuteCounts(map[int]map[string]int64{0: {"accepted": 9}})}, "webhook:refusals:esp", ""},
		{"one refusal", whstore.SourceReadings{Outcomes: minuteCounts(map[int]map[string]int64{3: {"write_failed": 1}})}, "webhook:refusals:esp", levelalert.Warning},
		{"refusal too old", whstore.SourceReadings{Outcomes: minuteCounts(map[int]map[string]int64{6: {"buffer_full": 9}})}, "webhook:refusals:esp", ""},
		{"refusals sustained", whstore.SourceReadings{Outcomes: minuteCounts(everyMinute)}, "webhook:refusals:esp", levelalert.Critical},
		{"unauthorized 10%", whstore.SourceReadings{Outcomes: minuteCounts(map[int]map[string]int64{
			1: {"accepted": 18, "unauthorized": 2}, 14: {"accepted": 0},
		})}, "webhook:unauthorized:esp", levelalert.Warning},
		{"unauthorized on little traffic", whstore.SourceReadings{Outcomes: minuteCounts(map[int]map[string]int64{
			1: {"accepted": 1, "unauthorized": 1},
		})}, "webhook:unauthorized:esp", ""},
		{"ack p99 fast", whstore.SourceReadings{Ack: whstore.AckLatency{Count: 9, P99MS: 250}}, "webhook:ack_p99:esp", ""},
		{"ack p99 1s", whstore.SourceReadings{Ack: whstore.AckLatency{Count: 9, P99MS: 1000}}, "webhook:ack_p99:esp", levelalert.Warning},
		{"ack p99 5s", whstore.SourceReadings{Ack: whstore.AckLatency{Count: 9, P99MS: 5000}}, "webhook:ack_p99:esp", levelalert.Critical},
		{"compaction within grace", whstore.SourceReadings{OldestOwedEnd: ago(90 * time.Second)}, "webhook:compaction:esp", ""},
		{"compaction overdue", whstore.SourceReadings{OldestOwedEnd: ago(5 * time.Minute)}, "webhook:compaction:esp", levelalert.Warning},
		{"compaction 30m overdue", whstore.SourceReadings{OldestOwedEnd: ago(33 * time.Minute)}, "webhook:compaction:esp", levelalert.Critical},
		{"compaction failing", whstore.SourceReadings{FailingWindows: 2, LastError: "boom"}, "webhook:compaction:esp", levelalert.Warning},
		{"silence not opted in", whstore.SourceReadings{LastSegmentAt: ago(48 * time.Hour)}, "webhook:silent:esp", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rd := tc.rd
			got := levels(Evaluate(Defaults(), source(&rd)))
			assert.Equal(t, tc.want, got[tc.key], "%v", got)
		})
	}
}

// TestPerSourceOverrides: a source's own rules replace the defaults for it
// alone, silence included, and a disabled source is not judged.
func TestPerSourceOverrides(t *testing.T) {
	settings := Defaults()
	silent, off := 60, 0
	settings.Sources = map[string]SourceRules{
		"esp": {SilentAfterMinutes: &silent, AckP99MS: &levelalert.Thresholds{Warning: 100}, RefusalMinutes: &off},
	}
	rd := &whstore.SourceReadings{
		LastSegmentAt: ago(2 * time.Hour), Ack: whstore.AckLatency{Count: 3, P99MS: 250},
		Outcomes: minuteCounts(map[int]map[string]int64{0: {"spool_full": 3}}),
	}
	fs := Evaluate(settings, source(rd))
	got := levels(fs)
	assert.Equal(t, levelalert.Warning, got["webhook:silent:esp"])
	assert.Equal(t, levelalert.Warning, got["webhook:ack_p99:esp"])
	assert.NotContains(t, got, "webhook:refusals:esp", "a source may turn a condition off")
	for _, f := range fs {
		assert.Equal(t, "https://p/portal/admin/webhooks/esp", f.Link)
		assert.NotContains(t, f.Body, "payload")
	}
	r := source(rd)
	r.Sources = map[string]bool{"esp": false}
	assert.Empty(t, Evaluate(settings, r), "a disabled source is not judged")

	r = source(nil)
	r.PerSource = nil
	assert.Empty(t, Evaluate(Defaults(), r), "a source with no readings yet finds nothing")
}

func TestTitlesNameTheValueAndWhere(t *testing.T) {
	fs := Evaluate(Defaults(), spool(func(s *whstore.SpoolState) {
		s.Bytes, s.Events, s.Oldest = 850, 3, ago(40*time.Minute)
		s.ConsecutiveFailures, s.FailingSince, s.LastError = 12, ago(7*time.Minute), "bucket gone"
		s.FsTotal, s.FsFree = new(int64(100)), new(int64(5))
	}))
	byKey := map[string]levelalert.Finding{}
	for _, f := range fs {
		byKey[f.Key] = f
	}
	assert.Equal(t, "Webhook spool on a is 85% full", byKey["webhook:spool_fill:a"].Title)
	assert.Equal(t, "Oldest webhook event on a has waited 40m0s", byKey["webhook:spool_age:a"].Title)
	assert.Contains(t, byKey["webhook:drain_failing:a"].Body, "bucket gone")
	assert.Equal(t, "Webhook spool volume on a has 5% free", byKey["webhook:spool_volume:a"].Title)
	assert.Equal(t, "", page("", "x"))
	assert.Equal(t, 3*time.Second, roundDur(3200*time.Millisecond))
}
