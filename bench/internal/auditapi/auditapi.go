// Package auditapi reads efficiency metrics back from the platform's admin
// audit API. The benchmark does not instrument its own timing: the audit log
// is the measurement instrument, and a run fails loudly when audit rows are
// missing for one of its sessions (that missing data is itself a platform
// defect the benchmark must surface, not paper over).
package auditapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// pageSize is the per_page used when fetching events.
const pageSize = 200

// pollInterval is the delay between polls while waiting for rows to land.
// Arm profiles run audit with delivery: sync, so convergence is immediate in
// practice; the poll covers request scheduling slack only.
const pollInterval = 250 * time.Millisecond

// Event is the subset of the platform audit event the benchmark scores.
// Field names mirror pkg/audit.Event's JSON encoding.
type Event struct {
	Timestamp             time.Time `json:"timestamp"`
	DurationMS            int64     `json:"duration_ms"`
	SessionID             string    `json:"session_id"`
	ToolName              string    `json:"tool_name"`
	Success               bool      `json:"success"`
	ErrorMessage          string    `json:"error_message,omitempty"`
	EnrichmentApplied     bool      `json:"enrichment_applied"`
	EnrichmentTokensFull  int       `json:"enrichment_tokens_full"`
	EnrichmentTokensDedup int       `json:"enrichment_tokens_dedup"`
	EnrichmentMode        string    `json:"enrichment_mode,omitempty"`
	EventKind             string    `json:"event_kind,omitempty"`
}

// envelope is the admin list response.
type envelope struct {
	Data    []Event `json:"data"`
	Total   int     `json:"total"`
	Page    int     `json:"page"`
	PerPage int     `json:"per_page"`
}

// Client queries the admin audit API with the harness's admin credential.
type Client struct {
	base string
	http *http.Client
}

// New returns a Client for the platform base URL using the supplied
// authenticated HTTP client.
func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// EventsForSession fetches every audit event recorded for a session handle.
func (c *Client) EventsForSession(ctx context.Context, sessionID string) ([]Event, error) {
	var all []Event
	for page := 1; ; page++ {
		env, err := c.fetchPage(ctx, sessionID, page)
		if err != nil {
			return nil, err
		}
		all = append(all, env.Data...)
		if len(all) >= env.Total || len(env.Data) == 0 {
			return all, nil
		}
	}
}

// WaitForSession polls until the session's audit rows land within
// [minCount, maxCount] and returns them. The bounds come from the harness's
// client-side accounting: minCount is the calls it CONFIRMED reached the
// handler chain (each must have a row — fewer means audit lost data, which
// fails the run loudly), while maxCount adds the indeterminate calls
// (transport-level errors such as a protocol error or client timeout, where
// the platform may or may not have audited the call before the failure
// surfaced). More rows than maxCount means the accounting itself is wrong and
// the result is not publishable.
func (c *Client) WaitForSession(ctx context.Context, sessionID string, minCount, maxCount int, timeout time.Duration) ([]Event, error) {
	deadline := time.Now().Add(timeout)
	got := -1
	for {
		events, err := c.EventsForSession(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		done, err := settled(sessionID, len(events), got, minCount, maxCount)
		if done || err != nil {
			return events, err
		}
		got = len(events)
		if time.Now().After(deadline) {
			if got >= minCount {
				return events, nil
			}
			return nil, fmt.Errorf("audit rows for session %s: got %d of at least %d within %s (missing rows fail the run)",
				sessionID, got, minCount, timeout)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// settled decides whether a poll's row count terminates the wait: an
// overcount is a hard error, hitting maxCount is complete, and an in-bounds
// count stable across one poll interval means the indeterminate calls that
// could still add rows did not.
func settled(sessionID string, count, previous, minCount, maxCount int) (bool, error) {
	switch {
	case count > maxCount:
		return false, fmt.Errorf("audit rows for session %s: got %d, expected at most %d (overcount)",
			sessionID, count, maxCount)
	case count == maxCount:
		return true, nil
	case count >= minCount && count == previous:
		return true, nil
	}
	return false, nil
}

// fetchPage fetches one page of events for a session.
func (c *Client) fetchPage(ctx context.Context, sessionID string, page int) (*envelope, error) {
	q := url.Values{}
	q.Set("session_id", sessionID)
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(pageSize))
	q.Set("sort_by", "timestamp")
	q.Set("sort_order", "asc")
	endpoint := c.base + "/api/v1/admin/audit/events?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build audit request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("audit request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("read audit response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("audit API status %d: %.300s", resp.StatusCode, string(body))
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parse audit response: %w", err)
	}
	return &env, nil
}

// Metrics summarizes one session's audit trail for the report.
type Metrics struct {
	// AuditedCalls is the number of audited tool calls under the session
	// handle. platform_info is not among them: its own audit row carries the
	// transport session id because the handle is minted inside its handler.
	AuditedCalls int `json:"audited_calls"`
	// Errors counts audited calls with success=false.
	Errors int `json:"errors"`
	// TotalDurationMS sums server-side handler time across the session.
	TotalDurationMS int64 `json:"total_duration_ms"`
	// EnrichedCalls counts calls where cross-enrichment was applied.
	EnrichedCalls int `json:"enriched_calls"`
	// EnrichmentTokensDedup sums the deduplicated enrichment token volume.
	EnrichmentTokensDedup int `json:"enrichment_tokens_dedup"`
}

// Summarize folds a session's events into Metrics.
func Summarize(events []Event) Metrics {
	var m Metrics
	for _, e := range events {
		m.AuditedCalls++
		if !e.Success {
			m.Errors++
		}
		m.TotalDurationMS += e.DurationMS
		if e.EnrichmentApplied {
			m.EnrichedCalls++
			m.EnrichmentTokensDedup += e.EnrichmentTokensDedup
		}
	}
	return m
}
