// Package scrape reads the platform's Prometheus /metrics endpoint and extracts
// the runtime and platform counters the load harness needs (goroutines, heap,
// resident memory, CPU seconds, audit drops, rate-limit rejections). Parsed
// snapshots are embedded in the JSON report so a run is self-contained.
//
// It parses the classic Prometheus text exposition format directly rather than
// depending on github.com/prometheus/common/expfmt: that package's v0.67 parser
// panics on its uninitialized global name-validation scheme, and the harness
// only needs a fixed set of scalar metrics selected by name.
package scrape

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MetricsOfInterest are the metric family names the harness records. Names are
// the Prometheus exposition names emitted by the platform's client_golang
// collectors (go_*, process_*) and the OTEL Prometheus exporter for the two
// platform counters (issue #921). A counter exported by OTEL carries the
// `_total` suffix.
var MetricsOfInterest = []string{
	"go_goroutines",
	"go_memstats_heap_inuse_bytes",
	"go_memstats_heap_alloc_bytes",
	"process_resident_memory_bytes",
	"process_cpu_seconds_total",
	"audit_events_dropped_total",
	"mcp_rate_limited_total",
}

// Snapshot is one scrape at a point in time. Values holds the selected metric
// families summed across all label sets (the counters and gauges of interest
// are process-global, so summing collapses any per-attribute series into one
// number). Missing metrics are simply absent from the map.
type Snapshot struct {
	At     time.Time          `json:"at"`
	Label  string             `json:"label"` // "before", "during", "after", ...
	Values map[string]float64 `json:"values"`
}

// Scraper fetches and parses a metrics endpoint.
type Scraper struct {
	URL    string
	Client *http.Client
}

// New returns a Scraper for the given /metrics URL. A nil client uses a
// short-timeout default.
func New(url string, client *http.Client) *Scraper {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Scraper{URL: url, Client: client}
}

// Snapshot fetches the endpoint once and returns the selected metrics labeled
// with the given phase label. A scrape failure is returned as an error; callers
// decide whether a missing scrape is fatal.
func (s *Scraper) Snapshot(ctx context.Context, label string) (Snapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, http.NoBody)
	if err != nil {
		return Snapshot{}, fmt.Errorf("building scrape request: %w", err)
	}
	resp, err := s.Client.Do(req)
	if err != nil {
		return Snapshot{}, fmt.Errorf("scraping %s: %w", s.URL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Snapshot{}, fmt.Errorf("scraping %s: status %d", s.URL, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Snapshot{}, fmt.Errorf("reading scrape body: %w", err)
	}
	values, err := SelectFromText(body, MetricsOfInterest)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{At: time.Now(), Label: label, Values: values}, nil
}

// SelectFromText parses a Prometheus text exposition payload and returns, for
// each requested name that is present, the sum of its sample values across all
// label sets. Exposed separately from the HTTP path so it is unit-testable
// without a server.
//
// It fails only on a line whose metric name is one the caller asked for: a
// malformed or exotic line for a metric the harness does not track is skipped,
// not fatal. This keeps one odd unrelated line (for example a label value that
// the exposition format allows but the harness does not model) from discarding
// the whole scrape — and with it the counter-delta and stability measurements
// the audit-burst and soak scenarios depend on.
func SelectFromText(body []byte, names []string) (map[string]float64, error) {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	out := make(map[string]float64)
	for i, raw := range strings.Split(string(body), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest := splitName(line)
		if !want[name] {
			continue // untracked (or unnamed) line: never fatal
		}
		value, err := parseValue(rest)
		if err != nil {
			return nil, fmt.Errorf("line %d, metric %q: %w", i+1, name, err)
		}
		out[name] += value
	}
	return out, nil
}

// parseValue extracts the sample value — the first whitespace-delimited field
// after the metric name and label set. A trailing timestamp or ` # {exemplar}`
// is ignored because only the first field is read.
func parseValue(rest string) (float64, error) {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0, errors.New("no value")
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("value %q: %w", fields[0], err)
	}
	return value, nil
}

// splitName returns the metric name and the remainder of the line after the
// name and any {label set}. The name is the run of characters before the first
// '{' or whitespace. A '}' inside a double-quoted label value does not terminate
// the label set.
func splitName(line string) (name, rest string) {
	brace := strings.IndexByte(line, '{')
	space := strings.IndexAny(line, " \t")
	// A label set immediately follows the name with no space.
	if brace >= 0 && (space < 0 || brace < space) {
		name = line[:brace]
		end := labelSetEnd(line, brace)
		if end < 0 {
			return name, "" // malformed: unterminated labels -> no value follows
		}
		return name, line[end+1:]
	}
	if space < 0 {
		return line, ""
	}
	return line[:space], line[space:]
}

// labelSetEnd returns the index of the '}' that closes the label set opened at
// openBrace, skipping any '}' that appears inside a double-quoted label value
// (label values are arbitrary UTF-8 and may legally contain braces). Returns -1
// if the label set is never closed.
func labelSetEnd(line string, openBrace int) int {
	inQuote := false
	escaped := false
	for i := openBrace + 1; i < len(line); i++ {
		switch c := line[i]; {
		case escaped:
			escaped = false
		case c == '\\':
			escaped = true
		case c == '"':
			inQuote = !inQuote
		case c == '}' && !inQuote:
			return i
		}
	}
	return -1
}
