package webhookapi

import (
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
)

// statusPath is the overview route.
const statusPath = "/api/v1/admin/webhooks/status"

// statusRange is a span the overview's series can cover and the bucket it is
// drawn in.
type statusRange struct {
	span, step time.Duration
}

// statusRanges are the spans the range parameter names. The hour is drawn a
// minute to a bar, the day fifteen minutes to a bar and the week an hour to
// a bar: 60, 96 and 168 bars. The history is kept per minute for 48 hours and
// per hour after (#2150), so the week is read in hours.
var statusRanges = map[string]statusRange{
	"hour": {span: time.Hour, step: time.Minute},
	"day":  {span: 24 * time.Hour, step: 15 * time.Minute},
	"week": {span: 7 * 24 * time.Hour, step: time.Hour},
}

// The custom range: from and to, at most maxCustomSpan apart.
const (
	rangeCustom   = "custom"
	maxCustomSpan = 31 * 24 * time.Hour
	// perMinuteKept is how far back the history is per minute; a bucket
	// shorter than an hour before it would split an hourly row.
	perMinuteKept = 48 * time.Hour
)

// defaultStatusRange is the range read when none is named.
const defaultStatusRange = "hour"

// errRange is the refusal of a range the overview cannot read.
const errRange = "range must be hour, day, week or custom; custom takes from and to (RFC 3339), from before to, at most 31 days apart"

// spanOf reads the range parameters into the span the overview covers.
func spanOf(q url.Values, now time.Time) (whadmin.Span, string, bool) {
	name := q.Get("range")
	if name == "" {
		name = defaultStatusRange
	}
	if rng, ok := statusRanges[name]; ok {
		return whadmin.Span{Back: rng.span, Step: rng.step}, name, true
	}
	if name != rangeCustom {
		return whadmin.Span{}, name, false
	}
	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		return whadmin.Span{}, name, false
	}
	until := now
	if raw := q.Get("to"); raw != "" {
		if until, err = time.Parse(time.RFC3339, raw); err != nil {
			return whadmin.Span{}, name, false
		}
	}
	span := until.Sub(from)
	if span <= 0 || span > maxCustomSpan {
		return whadmin.Span{}, name, false
	}
	return whadmin.Span{From: from.UTC(), Until: until.UTC(), Step: customStep(from, span, now)}, name, true
}

// The longest custom span each bucket is drawn for, and the buckets past
// them.
const (
	// minuteBucketsUpTo is the longest span drawn in minutes.
	minuteBucketsUpTo = 2 * time.Hour
	// quarterHourBucketsUpTo is the longest span drawn in quarter hours.
	quarterHourBucketsUpTo = 36 * time.Hour
	// hourBucketsUpTo is the longest span drawn in hours.
	hourBucketsUpTo = 8 * 24 * time.Hour
	// quarterHourBucket is the bucket of a span up to quarterHourBucketsUpTo.
	quarterHourBucket = 15 * time.Minute
	// longBucket is the bucket of a span past hourBucketsUpTo.
	longBucket = 6 * time.Hour
)

// customStep is the bucket a custom span is drawn in: about a hundred bars,
// and never shorter than an hour where the history is kept by the hour.
func customStep(from time.Time, span time.Duration, now time.Time) time.Duration {
	var step time.Duration
	switch {
	case span <= minuteBucketsUpTo:
		step = time.Minute
	case span <= quarterHourBucketsUpTo:
		step = quarterHourBucket
	case span <= hourBucketsUpTo:
		step = time.Hour
	default:
		step = longBucket
	}
	if step < time.Hour && from.Before(now.Add(-perMinuteKept)) {
		step = time.Hour
	}
	return step
}

// status handles GET /api/v1/admin/webhooks/status.
//
// @Summary      Status of every webhook source
// @Description  Returns every source's health, counts and acknowledgement latency in one response, the request volume by source and outcome over the range, and the newest 50 rejected requests of each outcome across all sources whose latest rejection falls within the range, each with its source (never a body). With `source`, every part of the response is that one source's. A rejected request whose events were read names them: `request_hash` is the SHA-256 of its body and `event_ids` the ids of its first 20 events. Latency is how long a delivery answered 202 waited for that answer, over the last hour and the last day; `p50_ms` and `p99_ms` are the upper bound of the bucket the percentile falls in (10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000 and 30000 ms), capped at `max_ms`, and `slowest` is the slowest delivery with how long it spent in the buffer, recording its window and writing its segment. Health is disabled for a source turned off, failing for one with a window whose last compaction failed, silent for an enabled source that received no event in the last 24 hours (including one that never received any), and receiving otherwise; when more than one applies the first in that order is reported. The volume series reads the request counts at or after `from` and before `until`. `spools` is every receiver's local spool, by replica, whatever `source` names: the events it acknowledged once they were synced to its disk and has not yet drained to object storage, the bounds past which it answers 503, and how its drain is going; a receiver that has not reported for two minutes is `reporting: false`, and what it holds waits for it to start again. The history over the range (#2150): `statuses` counts requests by the HTTP status the sender was answered with (0 is a sender that went away before its answer); `replicas` is each receiver's events received and written to object storage and its spool's depth and oldest age; `ack` is the acknowledgement p50/p99 and each stage's average (buffer, mark, put; with a spool, put is the sync); `compactions` is each window's compaction with its duration, lag behind the window's end and sizes; `storage` is each source's raw and compacted bytes now; `sizing` sets the largest window the compactor held in memory against this replica's memory limit and warns at half of it. The history is kept per minute for 48 hours and per hour after, for webhooks.compactor.telemetry_retention_days (14). An answer the sender received from something in front of the platform (a 502 or 504 from an ingress while a replica restarts) never reached the platform and is in none of these counts.
// @Tags         Webhooks
// @Produce      json
// @Param        range   query  string  false  "Span of the series and the rejections: hour (one-minute buckets), day (fifteen-minute), week (hourly) or custom (from and to)"  Enums(hour, day, week, custom)  default(hour)
// @Param        from    query  string  false  "Start of a custom range, RFC 3339"
// @Param        to      query  string  false  "End of a custom range, RFC 3339; now when absent"
// @Param        source  query  string  false  "Name of one source to report on; every source when absent"
// @Success      200  {object}  StatusOverview
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/webhooks/status [get]
func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	span, name, ok := spanOf(r.URL.Query(), time.Now())
	if !ok {
		httpjson.WriteError(w, http.StatusBadRequest, errRange)
		return
	}
	ov, err := h.cfg.Service.Overview(r.Context(), r.URL.Query().Get("source"), span)
	if errors.Is(err, whadmin.ErrNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, "no webhook source has that name")
		return
	}
	if err != nil {
		httpjson.WriteError(w, http.StatusInternalServerError, "reading webhook status failed")
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, overviewOf(name, ov))
}

// overviewOf renders the overview. Every list and map is empty rather than
// null when it holds nothing.
func overviewOf(rangeName string, ov whadmin.Overview) StatusOverview {
	out := StatusOverview{
		GeneratedAt: ov.Now, Range: rangeName, From: ov.Since, Until: ov.Until,
		BucketSeconds:      int(ov.Step.Seconds()),
		SilentAfterSeconds: int(whadmin.SilentAfter.Seconds()),
		Sources:            make([]SourceStatus, 0, len(ov.Sources)),
		Volume:             make([]VolumePoint, 0, len(ov.Volume)),
		Rejections:         make([]SourceRejection, 0, len(ov.Rejections)),
		Spools:             make([]ReplicaSpool, 0, len(ov.Spools)),
	}
	for _, sp := range ov.Spools {
		st := sp.State
		out.Spools = append(out.Spools, ReplicaSpool{
			Replica: st.Replica, Dir: st.Dir, Bytes: st.Bytes, Events: st.Events, Files: st.Files,
			OldestAt: st.Oldest, MaxBytes: st.MaxBytes, MaxAgeSeconds: int64(st.MaxAge / time.Second),
			DrainedEvents: st.Drained, DrainFailures: st.DrainFailures, LastDrainAt: st.LastDrainAt,
			LastError: st.LastError, StartedAt: st.StartedAt, UpdatedAt: st.UpdatedAt, Reporting: sp.Reporting,
		})
	}
	for _, s := range ov.Sources {
		out.Sources = append(out.Sources, SourceStatus{
			Name: s.Source.Name, Enabled: s.Source.Enabled, Health: string(s.Health),
			AuthMode: s.Source.Auth.Mode, Connection: s.Source.Connection, Table: s.Source.TableName(),
			LastEventAt: s.Summary.LastSegmentAt,
			LastHour:    counts(s.Summary.LastHour), LastDay: counts(s.Summary.LastDay),
			Pending: s.Summary.Pending, Failing: s.Summary.Failing, LastError: s.Summary.LastError,
			AckLastHour: s.Summary.AckLastHour, AckLastDay: s.Summary.AckLastDay,
			SegmentsOpenWindow: s.Summary.SegmentsOpenWindow, SegmentsPerWindowP95: s.Summary.SegmentsPerWindowP95,
		})
	}
	for _, p := range ov.Volume {
		out.Volume = append(out.Volume, VolumePoint{At: p.At, Source: p.Source, Outcome: p.Outcome, Count: p.Count})
	}
	for _, r := range ov.Rejections {
		out.Rejections = append(out.Rejections, SourceRejection{
			Source: r.Source, At: r.At, FirstAt: r.FirstAt, Count: r.Count, Outcome: r.Outcome, Reason: r.Reason,
			RequestHash: r.RequestHash, EventIDs: nonNilIDs(r.EventIDs),
		})
	}
	out.History = historyOf(ov)
	return out
}

// counts returns m, or an empty map in place of nil.
func counts(m map[string]int64) map[string]int64 {
	if m == nil {
		return map[string]int64{}
	}
	return m
}

// nonNilIDs returns ids, or an empty list in place of nil.
func nonNilIDs(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}
