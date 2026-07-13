package scrape

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const sampleExposition = `# HELP go_goroutines Number of goroutines that currently exist.
# TYPE go_goroutines gauge
go_goroutines 42
# HELP process_resident_memory_bytes Resident memory size in bytes.
# TYPE process_resident_memory_bytes gauge
process_resident_memory_bytes 7.1e+07
# HELP process_cpu_seconds_total Total user and system CPU time spent in seconds.
# TYPE process_cpu_seconds_total counter
process_cpu_seconds_total 12.5
# HELP audit_events_dropped_total Audit events dropped by the bounded async writer.
# TYPE audit_events_dropped_total counter
audit_events_dropped_total 8
# HELP mcp_rate_limited_total Tool calls refused by the per-user rate limiter.
# TYPE mcp_rate_limited_total counter
mcp_rate_limited_total 3
# HELP unrelated_metric Something we do not track.
# TYPE unrelated_metric gauge
unrelated_metric 999
`

func TestSelectFromText(t *testing.T) {
	got, err := SelectFromText([]byte(sampleExposition), MetricsOfInterest)
	if err != nil {
		t.Fatalf("SelectFromText: %v", err)
	}
	want := map[string]float64{
		"go_goroutines":                 42,
		"process_resident_memory_bytes": 7.1e7,
		"process_cpu_seconds_total":     12.5,
		"audit_events_dropped_total":    8,
		"mcp_rate_limited_total":        3,
	}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("%s = %v, want %v", name, got[name], v)
		}
	}
	if _, ok := got["unrelated_metric"]; ok {
		t.Error("unrelated_metric should not be selected")
	}
	// heap metrics are absent from the sample and should simply be missing.
	if _, ok := got["go_memstats_heap_inuse_bytes"]; ok {
		t.Error("absent metric should not appear in the selection")
	}
}

func TestSelectFromTextSumsAcrossLabels(t *testing.T) {
	// A counter with two label sets should collapse to the sum.
	body := `# TYPE mcp_rate_limited_total counter
mcp_rate_limited_total{user="a"} 2
mcp_rate_limited_total{user="b"} 5
`
	got, err := SelectFromText([]byte(body), []string{"mcp_rate_limited_total"})
	if err != nil {
		t.Fatalf("SelectFromText: %v", err)
	}
	if got["mcp_rate_limited_total"] != 7 {
		t.Errorf("summed counter = %v, want 7", got["mcp_rate_limited_total"])
	}
}

func TestSelectFromTextMalformedTrackedMetricErrors(t *testing.T) {
	// A tracked metric with an unparseable value is a hard error.
	body := "go_goroutines not_a_number\n"
	if _, err := SelectFromText([]byte(body), MetricsOfInterest); err == nil {
		t.Error("expected an error when a tracked metric has a bad value")
	}
}

func TestSelectFromTextUntrackedMalformedIsSkipped(t *testing.T) {
	// A malformed line for an UNTRACKED metric must not poison the scrape: the
	// tracked metric on the following line must still be selected.
	body := "this is not prometheus\nweird_untracked{q=\"a}b\"} bogus\ngo_goroutines 40\n"
	got, err := SelectFromText([]byte(body), MetricsOfInterest)
	if err != nil {
		t.Fatalf("untracked malformed lines must not error: %v", err)
	}
	if got["go_goroutines"] != 40 {
		t.Errorf("tracked metric after malformed untracked lines = %v, want 40", got["go_goroutines"])
	}
}

func TestSelectFromTextLabelValueWithBrace(t *testing.T) {
	// A '}' inside a quoted label value must not terminate the label set early.
	body := `go_goroutines{path="/a}b",q="x"} 55` + "\n"
	got, err := SelectFromText([]byte(body), []string{"go_goroutines"})
	if err != nil {
		t.Fatalf("SelectFromText: %v", err)
	}
	if got["go_goroutines"] != 55 {
		t.Errorf("value with brace-containing label = %v, want 55", got["go_goroutines"])
	}
}

func TestScraperSnapshot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleExposition))
	}))
	defer srv.Close()

	sc := New(srv.URL, srv.Client())
	snap, err := sc.Snapshot(context.Background(), "before")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snap.Label != "before" {
		t.Errorf("label = %q, want before", snap.Label)
	}
	if snap.Values["go_goroutines"] != 42 {
		t.Errorf("go_goroutines = %v, want 42", snap.Values["go_goroutines"])
	}
	if snap.At.IsZero() {
		t.Error("snapshot timestamp should be set")
	}
}

func TestScraperSnapshotNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	sc := New(srv.URL, srv.Client())
	if _, err := sc.Snapshot(context.Background(), "x"); err == nil {
		t.Error("expected an error on non-200 scrape")
	}
}
