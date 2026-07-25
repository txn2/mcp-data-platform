// Package apisetup registers the API-connection study's fixtures with a
// running platform through the admin REST API (#1027): the b1 arms get a
// catalog + tier spec + an `api` connection to the fixture service, the
// b0 arm gets an `mcp` connection to the per-endpoint server. Runtime
// registration through the admin surface is the platform's shipped
// connection path; the arm YAML profiles carry no connection state.
package apisetup

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
)

// ConnectionName is the connection every arm registers. The b0 gateway
// prefixes proxied tool names with it ("acme__<operationId>"); the b1
// arms address it via api_* tool calls.
const ConnectionName = "acme"

// CatalogID is the b1 catalog id (also its spec name).
const CatalogID = "bench-acme"

// Client drives the platform admin API.
type Client struct {
	baseURL  string
	adminKey string
	http     *http.Client
}

// New builds a client against the platform base URL using the admin API
// key (Bearer).
func New(baseURL, adminKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		adminKey: adminKey,
		http:     &http.Client{Timeout: timeout},
	}
}

// RegisterB1 sets up the search-then-invoke arm: catalog, inline tier
// spec, and the api connection pointing at the fixture service.
func (c *Client) RegisterB1(ctx context.Context, specContent, fixtureURL, fixtureKey string) error {
	if err := c.do(ctx, http.MethodPost, "/api/v1/admin/api-catalogs", map[string]any{
		"id":           CatalogID,
		"name":         CatalogID,
		"display_name": "Bench fixture catalog",
	}, nil); err != nil && !isConflict(err) {
		return fmt.Errorf("create catalog: %w", err)
	}
	if err := c.do(ctx, http.MethodPut, "/api/v1/admin/api-catalogs/"+CatalogID+"/specs/"+CatalogID, map[string]any{
		"source_kind": "inline",
		"content":     specContent,
	}, nil); err != nil {
		return fmt.Errorf("upsert spec: %w", err)
	}
	if err := c.do(ctx, http.MethodPut, "/api/v1/admin/connection-instances/api/"+ConnectionName, map[string]any{
		"config": map[string]any{
			"base_url":   fixtureURL,
			"auth_mode":  "api_key",
			"credential": fixtureKey,
			"catalog_id": CatalogID,
		},
		"description": "Bench fixture service",
	}, nil); err != nil {
		return fmt.Errorf("register api connection: %w", err)
	}
	return nil
}

// RegisterB0 sets up the per-endpoint arm: an mcp connection to the
// generated epmcp server.
func (c *Client) RegisterB0(ctx context.Context, epmcpURL string) error {
	if err := c.do(ctx, http.MethodPut, "/api/v1/admin/connection-instances/mcp/"+ConnectionName, map[string]any{
		"config":      map[string]any{"endpoint": epmcpURL},
		"description": "Bench per-endpoint MCP server (#1027)",
	}, nil); err != nil {
		return fmt.Errorf("register mcp connection: %w", err)
	}
	return nil
}

// WaitEmbedDrain polls the catalog's embedding status until every
// operation has a persisted vector (the b1-hyb readiness gate) or the
// context expires.
func (c *Client) WaitEmbedDrain(ctx context.Context, wantOps int, poll time.Duration) error {
	for {
		done, status, err := c.embedStatus(ctx, wantOps)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("embed drain: %w (last status: %s)", ctx.Err(), status)
		case <-time.After(poll):
		}
	}
}

// embedStatus reads one embedding-status snapshot.
func (c *Client) embedStatus(ctx context.Context, wantOps int) (bool, string, error) {
	var out struct {
		Specs []struct {
			SpecName       string `json:"spec_name"`
			OperationCount int    `json:"operation_count"`
			EmbeddingCount int    `json:"embedding_count"`
			JobStatus      string `json:"job_status"`
		} `json:"specs"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v1/admin/api-catalogs/"+CatalogID+"/embedding-status", nil, &out); err != nil {
		return false, "", err
	}
	for _, s := range out.Specs {
		if s.SpecName != CatalogID {
			continue
		}
		status := fmt.Sprintf("%s: %d/%d embedded (job %s)", s.SpecName, s.EmbeddingCount, s.OperationCount, s.JobStatus)
		if s.JobStatus == "failed" {
			return false, "", fmt.Errorf("embed drain: job failed (%s)", status)
		}
		return s.EmbeddingCount >= wantOps && s.OperationCount >= wantOps, status, nil
	}
	return false, "spec not yet visible", nil
}

// conflictError marks an HTTP 409 (already exists).
type conflictError struct{ msg string }

func (e conflictError) Error() string { return e.msg }

// isConflict reports whether the error is an already-exists conflict,
// which registration treats as idempotent success.
func isConflict(err error) bool {
	var ce conflictError
	return errors.As(err, &ce)
}

// do issues one admin request with optional JSON body and decode target.
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	req, err := c.newRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if res.StatusCode == http.StatusConflict {
		return conflictError{fmt.Sprintf("%s %s: HTTP 409: %.200s", method, path, raw)}
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return fmt.Errorf("%s %s: HTTP %d: %.300s", method, path, res.StatusCode, raw)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return nil
}

// newRequest assembles one authenticated admin request.
func (c *Client) newRequest(ctx context.Context, method, path string, body any) (*http.Request, error) {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		payload = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.adminKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}
