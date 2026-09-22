package observability

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestIndexJobInstruments_Exposed records one of every index-job observation
// and reads the scrape a Prometheus would, so the exposed names and labels the
// Indexing dashboard queries (ui/src/pages/indexing/metrics.ts) are the ones
// the exporter writes (#1837).
func TestIndexJobInstruments_Exposed(t *testing.T) {
	m := newEnabledMetrics(t)
	ctx := context.Background()

	m.IndexJobEnqueued(ctx, "calls", "write", true)
	m.IndexJobEnqueued(ctx, "calls", "reconciler", false)
	m.IndexJobStarted(ctx, "calls")
	m.IndexJobStarted(ctx, "resources")
	m.IndexJobFinished(ctx, "calls", "write", "succeeded", 4*time.Minute)
	m.IndexJobItems(ctx, "calls", 30, 2)
	m.IndexEmbedCall(ctx, "calls", 32, "timeout", 90*time.Second)
	m.IndexLeasesReleased(ctx, 2)
	m.IndexLeasesReleased(ctx, 0)
	m.IndexUnitsDeferred(ctx, "calls", 3)
	m.IndexUnitsDeferred(ctx, "calls", 0)

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`indexjob_enqueued_total{kind="calls",result="created",trigger="write"} 1`,
		`indexjob_enqueued_total{kind="calls",result="folded",trigger="reconciler"} 1`,
		`indexjob_jobs_total{kind="calls",outcome="succeeded",trigger="write"} 1`,
		// A finished job leaves the running gauge; one still running stays.
		`indexjob_running{kind="calls"} 0`,
		`indexjob_running{kind="resources"} 1`,
		`indexjob_items_total{kind="calls",result="embedded"} 30`,
		`indexjob_items_total{kind="calls",result="reused"} 2`,
		`indexjob_embed_calls_total{kind="calls",status="timeout"} 1`,
		`indexjob_embed_texts_total{kind="calls"} 32`,
		`indexjob_leases_released_total 2`,
		`indexjob_units_deferred_total{kind="calls"} 3`,
		// The long buckets: a four-minute pass lands under 300s, not +Inf.
		`indexjob_duration_seconds_bucket{kind="calls",outcome="succeeded",le="300"} 1`,
		`indexjob_duration_seconds_bucket{kind="calls",outcome="succeeded",le="120"} 0`,
		`indexjob_embed_call_duration_seconds_bucket{kind="calls",le="120"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
		}
	}
	// Every other histogram keeps the default buckets, which stop at 60.
	m.RecordTrinoQuery(ctx, StatusOK, "select", time.Second)
	body = scrapeMetrics(t, m.Handler())
	if strings.Contains(body, `trino_query_duration_seconds_bucket{query_kind="select",le="300"}`) {
		t.Error("the index-job buckets leaked onto another histogram")
	}
	if !strings.Contains(body, `trino_query_duration_seconds_bucket{query_kind="select",le="60"} 1`) {
		t.Errorf("trino histogram lost its default buckets\n%s", body)
	}
}

// TestIndexQueueGauges_FromSampler: the database gauges are what the sampler
// returned, per kind; a kind without a known denominator reports indexed
// alone, and one without coverage reports neither.
func TestIndexQueueGauges_FromSampler(t *testing.T) {
	m := newEnabledMetrics(t)
	m.RegisterIndexQueue(func(context.Context) ([]IndexQueueSample, error) {
		return []IndexQueueSample{
			{
				Kind: "calls", Pending: 2558, Running: 2, Retrying: 7, FailedUnits: 1,
				OldestRunnableWait: 90 * time.Second,
				CoverageKnown:      true, Indexed: 833090, Expected: 835775, ExpectedKnown: true,
			},
			{Kind: "tools", CoverageKnown: true, Indexed: 140},
			{Kind: "scripts", Pending: 1},
		}, nil
	})

	body := scrapeMetrics(t, m.Handler())
	for _, want := range []string{
		`indexjob_queue_jobs{kind="calls",state="pending"} 2558`,
		`indexjob_queue_jobs{kind="calls",state="running"} 2`,
		`indexjob_queue_jobs{kind="calls",state="retrying"} 7`,
		`indexjob_failed_units{kind="calls"} 1`,
		`indexjob_oldest_runnable_wait_seconds{kind="calls"} 90`,
		`indexjob_vectors_indexed{kind="calls"} 833090`,
		`indexjob_vectors_expected{kind="calls"} 835775`,
		`indexjob_vectors_indexed{kind="tools"} 140`,
		`indexjob_queue_jobs{kind="scripts",state="pending"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q\n--- body ---\n%s", want, body)
		}
	}
	for _, absent := range []string{
		`indexjob_vectors_expected{kind="tools"}`,
		`indexjob_vectors_indexed{kind="scripts"}`,
	} {
		if strings.Contains(body, absent) {
			t.Errorf("scrape carries %q, which the sample did not know", absent)
		}
	}
}

// TestIndexQueueGauges_SamplerErrorKeepsTheScrape: a failed database read
// drops the index gauges from that scrape and nothing else.
func TestIndexQueueGauges_SamplerErrorKeepsTheScrape(t *testing.T) {
	m := newEnabledMetrics(t)
	m.RegisterIndexQueue(func(context.Context) ([]IndexQueueSample, error) {
		return nil, errors.New("db down")
	})
	m.IndexJobStarted(context.Background(), "calls")

	body := scrapeMetrics(t, m.Handler())
	if strings.Contains(body, "indexjob_queue_jobs{") {
		t.Errorf("a failed sample reported queue gauges\n%s", body)
	}
	if !strings.Contains(body, `indexjob_running{kind="calls"} 1`) {
		t.Errorf("a failed sample took the rest of the scrape with it\n%s", body)
	}
}

// TestIndexJobRecorders_NilSafe: with metrics disabled every recorder is a
// no-op on the nil receiver, which is what lets the queue record
// unconditionally.
func TestIndexJobRecorders_NilSafe(_ *testing.T) {
	var m *Metrics
	ctx := context.Background()
	m.IndexJobEnqueued(ctx, "k", "write", true)
	m.IndexJobStarted(ctx, "k")
	m.IndexJobFinished(ctx, "k", "write", "succeeded", time.Second)
	m.IndexJobItems(ctx, "k", 1, 1)
	m.IndexEmbedCall(ctx, "k", 1, "ok", time.Second)
	m.IndexLeasesReleased(ctx, 1)
	m.IndexUnitsDeferred(ctx, "k", 1)
	m.RegisterIndexQueue(nil)
}
