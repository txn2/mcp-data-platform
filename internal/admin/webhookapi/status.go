package webhookapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/webhook/whadmin"
)

// statusPath is the overview route.
const statusPath = "/api/v1/admin/webhooks/status"

// statusRange is a span the overview's series can cover and the bucket it is
// drawn in. Only 48 hours of per-minute counts are kept, so the spans stop at
// a day.
type statusRange struct {
	span, step time.Duration
}

// statusRanges are the spans the range parameter names. The hour is drawn a
// minute to a bar, the day fifteen minutes to a bar: 60 and 96 bars.
var statusRanges = map[string]statusRange{
	"hour": {span: time.Hour, step: time.Minute},
	"day":  {span: 24 * time.Hour, step: 15 * time.Minute},
}

// defaultStatusRange is the range read when none is named.
const defaultStatusRange = "hour"

// status handles GET /api/v1/admin/webhooks/status.
//
// @Summary      Status of every webhook source
// @Description  Returns every source's health, counts and acknowledgement latency in one response, the request volume by source and outcome over the range, and the newest 50 rejected requests of each outcome across all sources whose latest rejection falls within the range, each with its source (never a body). With `source`, every part of the response is that one source's. A rejected request whose events were read names them: `request_hash` is the SHA-256 of its body and `event_ids` the ids of its first 20 events. Latency is how long a delivery answered 202 waited for that answer, over the last hour and the last day; `p50_ms` and `p99_ms` are the upper bound of the bucket the percentile falls in (10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000 and 30000 ms), capped at `max_ms`, and `slowest` is the slowest delivery with how long it spent in the buffer, recording its window and writing its segment. Health is disabled for a source turned off, failing for one with a window whose last compaction failed, silent for an enabled source that received no event in the last 24 hours (including one that never received any), and receiving otherwise; when more than one applies the first in that order is reported. The volume series reads the per-minute request counts at or after `from`, the same bound as each source's `last_hour` (range=hour) or `last_day` (range=day), so the series sums to those counts. `spools` is every receiver's local spool, by replica, whatever `source` names: the events it acknowledged once they were synced to its disk and has not yet drained to object storage, the bounds past which it answers 503, and how its drain is going; a receiver that has not reported for two minutes is `reporting: false`, and what it holds waits for it to start again.
// @Tags         Webhooks
// @Produce      json
// @Param        range   query  string  false  "Span of the volume series and the rejections: hour (one-minute buckets) or day (fifteen-minute buckets)"  Enums(hour, day)  default(hour)
// @Param        source  query  string  false  "Name of one source to report on; every source when absent"
// @Success      200  {object}  StatusOverview
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/webhooks/status [get]
func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("range")
	if name == "" {
		name = defaultStatusRange
	}
	rng, ok := statusRanges[name]
	if !ok {
		httpjson.WriteError(w, http.StatusBadRequest, "range must be hour or day")
		return
	}
	ov, err := h.cfg.Service.Overview(r.Context(), r.URL.Query().Get("source"), rng.span, rng.step)
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
		GeneratedAt: ov.Now, Range: rangeName, From: ov.Since,
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
