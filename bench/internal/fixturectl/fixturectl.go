// Package fixturectl is the harness-side client for the fixture service's
// /_bench/ control plane (#1027): reset between attempts, state dumps for
// mutation grading, and the access log for the failure taxonomy. It also
// implements the state grader: task.StateCheck assertions evaluated
// against a dump.
package fixturectl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// maxBodyBytes caps a control-plane response body.
const maxBodyBytes = 32 << 20

// Client talks to one fixture service.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New builds a client. apiKey may be empty when the service runs with
// auth disabled.
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// BaseURL returns the fixture service base URL (the b2 workspace context
// needs it in prose).
func (c *Client) BaseURL() string { return c.baseURL }

// APIKey returns the fixture credential (b2 workspace context).
func (c *Client) APIKey() string { return c.apiKey }

// Reset restores seed state and clears the access log. Called before
// every attempt so mutations never leak across attempts.
func (c *Client) Reset(ctx context.Context) error {
	return c.reset(ctx, nil)
}

// ResetWorld restores seed state and clears the access log, resetting into
// the named world profile (#1054): one call sets an attempt's starting
// world. The profile must be in the committed registry; an unknown name is
// an error rather than a silent default.
func (c *Client) ResetWorld(ctx context.Context, profile string) error {
	return c.reset(ctx, map[string]string{"profile": profile})
}

// reset issues the reset request with an optional body.
func (c *Client) reset(ctx context.Context, body any) error {
	var out struct {
		Reset bool `json:"reset"`
	}
	if err := c.doBody(ctx, http.MethodPost, "/_bench/reset", body, &out); err != nil {
		return err
	}
	if !out.Reset {
		return errors.New("fixturectl: reset not acknowledged")
	}
	return nil
}

// World reads the fixture's current world state.
func (c *Client) World(ctx context.Context) (apigen.World, error) {
	var out apigen.World
	if err := c.do(ctx, http.MethodGet, "/_bench/world", &out); err != nil {
		return apigen.World{}, err
	}
	return out, nil
}

// SetWorld changes the world without resetting anything else (#1054).
// This is the between-sessions world change that makes a stored belief
// stale: the access log spans the change, so a recheck after it is
// detectable as verification.
func (c *Client) SetWorld(ctx context.Context, profile string) (apigen.World, error) {
	var out apigen.World
	if err := c.doBody(ctx, http.MethodPost, "/_bench/world", map[string]string{"profile": profile}, &out); err != nil {
		return apigen.World{}, err
	}
	return out, nil
}

// SetPhase labels subsequent access-log entries with a session phase, so a
// capture session's calls and a query session's calls are separable in one
// unreset log.
func (c *Client) SetPhase(ctx context.Context, phase string) error {
	var out struct {
		Phase string `json:"phase"`
	}
	if err := c.doBody(ctx, http.MethodPost, "/_bench/phase", map[string]string{"phase": phase}, &out); err != nil {
		return err
	}
	if out.Phase != phase {
		return fmt.Errorf("fixturectl: phase set to %q, want %q", out.Phase, phase)
	}
	return nil
}

// StateDump returns one collection's rows ("customers", "orders", or a
// distractor resource key).
func (c *Client) StateDump(ctx context.Context, resource string) ([]map[string]any, error) {
	var out struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := c.do(ctx, http.MethodGet, "/_bench/state/"+resource, &out); err != nil {
		return nil, err
	}
	return out.Rows, nil
}

// RequestLogEntry mirrors the service's access-log record.
type RequestLogEntry struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Status      int    `json:"status"`
	OperationID string `json:"operation_id,omitempty"`
	// Phase is the session label in force when the request arrived, empty
	// when the harness declared none.
	Phase string `json:"phase,omitempty"`
}

// Requests returns the access log accumulated since the last reset.
func (c *Client) Requests(ctx context.Context) ([]RequestLogEntry, error) {
	var out struct {
		Requests []RequestLogEntry `json:"requests"`
	}
	if err := c.do(ctx, http.MethodGet, "/_bench/requests", &out); err != nil {
		return nil, err
	}
	return out.Requests, nil
}

// do issues one bodyless control-plane request and decodes the JSON
// response.
func (c *Client) do(ctx context.Context, method, path string, out any) error {
	return c.doBody(ctx, method, path, nil, out)
}

// doBody issues one control-plane request with an optional JSON body and
// decodes the JSON response.
func (c *Client) doBody(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("fixturectl: marshal %s body: %w", path, err)
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("fixturectl: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("X-API-Key", c.apiKey)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("fixturectl: %s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	if err != nil {
		return fmt.Errorf("fixturectl: read %s: %w", path, err)
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("fixturectl: %s %s: HTTP %d: %.200s", method, path, res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("fixturectl: decode %s: %w", path, err)
	}
	return nil
}

// GradeState evaluates a state-graded task's checks against post-run
// state. It returns (correct, detail): detail names the first failed
// check for the results file.
func (c *Client) GradeState(ctx context.Context, checks []task.StateCheck) (bool, string, error) {
	for i, chk := range checks {
		rows, err := c.StateDump(ctx, chk.Resource)
		if err != nil {
			return false, "", err
		}
		ok, detail := evalCheck(chk, rows)
		if !ok {
			return false, fmt.Sprintf("check %d (%s): %s", i, chk.Resource, detail), nil
		}
	}
	return true, "", nil
}

// evalCheck evaluates one assertion against a dump.
func evalCheck(chk task.StateCheck, rows []map[string]any) (bool, string) {
	if chk.ID != 0 {
		return evalRowCheck(chk, rows)
	}
	// Existence mode: some row must match every field.
	for _, row := range rows {
		if fieldsMatch(row, chk.Fields) {
			return true, ""
		}
	}
	return false, "no row matches the creation check"
}

// evalRowCheck asserts the fields on the row with the check's id.
func evalRowCheck(chk task.StateCheck, rows []map[string]any) (bool, string) {
	for _, row := range rows {
		if !numEqual(row["id"], chk.ID) {
			continue
		}
		for name, want := range chk.Fields {
			if !valueEqual(row[name], want) {
				return false, fmt.Sprintf("row %d field %s = %v, want %v", chk.ID, name, row[name], want)
			}
		}
		return true, ""
	}
	return false, fmt.Sprintf("no row with id %d", chk.ID)
}

// fieldsMatch reports whether a row satisfies every expected field.
func fieldsMatch(row map[string]any, fields map[string]any) bool {
	for name, want := range fields {
		if !valueEqual(row[name], want) {
			return false
		}
	}
	return true
}

// valueEqual compares a dumped JSON value against an expected check
// value, tolerating the numeric-type spread of JSON decoding (float64)
// vs YAML task loading (int).
func valueEqual(got, want any) bool {
	if gn, ok := toFloat(got); ok {
		if wn, wok := toFloat(want); wok {
			return gn == wn
		}
		return false
	}
	return fmt.Sprint(got) == fmt.Sprint(want)
}

// numEqual compares a dumped id against an int64.
func numEqual(got any, want int64) bool {
	gn, ok := toFloat(got)
	return ok && gn == float64(want)
}

// toFloat normalizes the numeric types JSON and YAML decoding produce.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
