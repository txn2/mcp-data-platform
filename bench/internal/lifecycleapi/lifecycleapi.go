// Package lifecycleapi reads and drives the memory-insight-knowledge lifecycle
// state through the platform's admin knowledge API. The S5 protocols verify
// every lifecycle transition (capture, promotion, supersede) against this API,
// never inferring it from a transcript (issue #944): the audit log is the
// measurement instrument for efficiency, and the knowledge API is the
// measurement instrument for lifecycle state.
//
// It authenticates with the harness's admin credential (the same Bearer the
// audit read-back uses), so it reads insights across identities (each protocol
// episode captures under its own pool identity) and drives the reviewer-side
// approve + apply that the promote stage scripts.
package lifecycleapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// pageSize is the per_page used when listing insights or changesets.
const pageSize = 100

// Insight is the subset of the platform insight record the protocols verify.
// Field names mirror pkg/toolkits/knowledge.Insight's JSON encoding.
type Insight struct {
	ID           string     `json:"id"`
	CreatedAt    time.Time  `json:"created_at"`
	CapturedBy   string     `json:"captured_by"`
	Category     string     `json:"category"`
	InsightText  string     `json:"insight_text"`
	Status       string     `json:"status"`
	EntityURNs   []string   `json:"entity_urns"`
	SinkClass    string     `json:"sink_class,omitempty"`
	AppliedAt    *time.Time `json:"applied_at,omitempty"`
	ChangesetRef string     `json:"changeset_ref,omitempty"`
}

// LinksEntity reports whether the insight is anchored to urn.
func (in Insight) LinksEntity(urn string) bool {
	return slices.Contains(in.EntityURNs, urn)
}

// Changeset is the subset of the platform changeset record the protocols verify.
type Changeset struct {
	ID               string   `json:"id"`
	TargetURN        string   `json:"target_urn"`
	ChangeType       string   `json:"change_type"`
	SourceInsightIDs []string `json:"source_insight_ids"`
	AppliedBy        string   `json:"applied_by"`
	RolledBack       bool     `json:"rolled_back"`
}

// Sourced reports whether the changeset lists insightID among its sources.
func (c Changeset) Sourced(insightID string) bool {
	return slices.Contains(c.SourceInsightIDs, insightID)
}

// InsightFilter selects insights to list. Zero-valued fields are omitted.
type InsightFilter struct {
	CapturedBy string
	EntityURN  string
	Status     string
	// Since bounds the listing to insights created at or after this time
	// (the admin API's `since` param, RFC 3339). Capture verification passes the
	// episode's start time so a pending insight left behind by an interrupted
	// earlier run — same deterministic teacher identity, same curriculum URN —
	// can never fake this episode's capture.
	Since time.Time
}

// ChangesetFilter selects changesets to list. Zero-valued fields are omitted.
type ChangesetFilter struct {
	EntityURN string
	AppliedBy string
}

// insightEnvelope is the admin list response for insights.
type insightEnvelope struct {
	Data  []Insight `json:"data"`
	Total int       `json:"total"`
}

// changesetEnvelope is the admin list response for changesets.
type changesetEnvelope struct {
	Data  []Changeset `json:"data"`
	Total int         `json:"total"`
}

// statusUpdate is the body of PUT /insights/{id}/status.
type statusUpdate struct {
	Status      string `json:"status"`
	ReviewNotes string `json:"review_notes,omitempty"`
}

// Client queries the admin knowledge API with the harness's admin credential.
type Client struct {
	base string
	http *http.Client
}

// New returns a Client for the platform base URL using the supplied
// authenticated HTTP client.
func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{base: strings.TrimRight(baseURL, "/"), http: httpClient}
}

// ListInsights returns every insight matching the filter, following pagination.
func (c *Client) ListInsights(ctx context.Context, f InsightFilter) ([]Insight, error) {
	var all []Insight
	for page := 1; ; page++ {
		q := url.Values{}
		setNonEmpty(q, "captured_by", f.CapturedBy)
		setNonEmpty(q, "entity_urn", f.EntityURN)
		setNonEmpty(q, "status", f.Status)
		if !f.Since.IsZero() {
			q.Set("since", f.Since.UTC().Format(time.RFC3339))
		}
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(pageSize))
		var env insightEnvelope
		if err := c.getJSON(ctx, "/api/v1/admin/knowledge/insights", q, &env); err != nil {
			return nil, err
		}
		all = append(all, env.Data...)
		if len(all) >= env.Total || len(env.Data) == 0 {
			return all, nil
		}
	}
}

// GetInsight returns one insight by ID.
func (c *Client) GetInsight(ctx context.Context, id string) (*Insight, error) {
	var in Insight
	if err := c.getJSON(ctx, "/api/v1/admin/knowledge/insights/"+url.PathEscape(id), nil, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// ListChangesets returns every changeset matching the filter, following pagination.
func (c *Client) ListChangesets(ctx context.Context, f ChangesetFilter) ([]Changeset, error) {
	var all []Changeset
	for page := 1; ; page++ {
		q := url.Values{}
		setNonEmpty(q, "entity_urn", f.EntityURN)
		setNonEmpty(q, "applied_by", f.AppliedBy)
		q.Set("page", strconv.Itoa(page))
		q.Set("per_page", strconv.Itoa(pageSize))
		var env changesetEnvelope
		if err := c.getJSON(ctx, "/api/v1/admin/knowledge/changesets", q, &env); err != nil {
			return nil, err
		}
		all = append(all, env.Data...)
		if len(all) >= env.Total || len(env.Data) == 0 {
			return all, nil
		}
	}
}

// KnowledgePage is the subset of the portal knowledge-page record the harness
// reads (the cold-start preflight checks no curriculum slug already exists).
type KnowledgePage struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
}

// knowledgePageEnvelope is the portal list response for knowledge pages.
type knowledgePageEnvelope struct {
	Pages []KnowledgePage `json:"pages"`
	Total int             `json:"total"`
}

// ListKnowledgePages returns every live knowledge page, following pagination.
// It reads the portal REST list (the same authenticated client the admin
// knowledge reads use); the cold-start preflight scans it for curriculum slugs
// left by a prior run.
func (c *Client) ListKnowledgePages(ctx context.Context) ([]KnowledgePage, error) {
	var all []KnowledgePage
	for offset := 0; ; offset += pageSize {
		q := url.Values{}
		q.Set("limit", strconv.Itoa(pageSize))
		q.Set("offset", strconv.Itoa(offset))
		var env knowledgePageEnvelope
		if err := c.getJSON(ctx, "/api/v1/portal/knowledge-pages", q, &env); err != nil {
			return nil, err
		}
		all = append(all, env.Pages...)
		if len(all) >= env.Total || len(env.Pages) == 0 {
			return all, nil
		}
	}
}

// GetChangeset returns one changeset by ID.
func (c *Client) GetChangeset(ctx context.Context, id string) (*Changeset, error) {
	var cs Changeset
	if err := c.getJSON(ctx, "/api/v1/admin/knowledge/changesets/"+url.PathEscape(id), nil, &cs); err != nil {
		return nil, err
	}
	return &cs, nil
}

// Approve transitions an insight to approved so the promote stage can apply it
// (the status machine requires pending -> approved -> applied).
func (c *Client) Approve(ctx context.Context, id, notes string) error {
	body, err := json.Marshal(statusUpdate{Status: "approved", ReviewNotes: notes})
	if err != nil {
		return fmt.Errorf("marshal approve body: %w", err)
	}
	endpoint := c.base + "/api/v1/admin/knowledge/insights/" + url.PathEscape(id) + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build approve request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("approve request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("approve insight %s: status %d: %.300s", id, resp.StatusCode, string(raw))
	}
	return nil
}

// getJSON performs a GET and decodes the JSON body into out.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	endpoint := c.base + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("build request %s: %w", path, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read %s response: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: status %d: %.300s", path, resp.StatusCode, string(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parse %s response: %w", path, err)
	}
	return nil
}

// setNonEmpty sets a query parameter only when the value is non-empty.
func setNonEmpty(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}
