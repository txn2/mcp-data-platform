// Package endpointapi is the admin REST surface that lists every endpoint
// binding on the deployment with its traffic (#2208): its script and owner,
// its address, access and limits, and over a day or a week the requests it
// answered by status class, their duration, its failures, its distinct
// callers and the requests it refused before its handler ran.
//
// An endpoint answers HTTP with platform data, and a public one answers
// anyone. The only views of a binding were one script's page and
// manage_script endpoint_list; an administrator could not see what the
// deployment exposes, under whose script, and who is calling it, without
// opening scripts one at a time.
package endpointapi

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eprefusal"
	"github.com/txn2/mcp-data-platform/internal/httpjson"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The routes.
const (
	listPath   = "/api/v1/admin/endpoints"
	detailPath = "/api/v1/admin/endpoints/{name}"
)

// The ranges a page reads, and the chart's bucket for each.
const (
	RangeDay  = "day"
	RangeWeek = "week"
)

// How much one detail read reports.
const (
	maxFailures = 50
	maxCallers  = 10
)

// Config is what the routes read.
type Config struct {
	DB       *sql.DB
	Bindings epbinding.Store
	// Script reads a binding's script by id.
	Script func(ctx context.Context, id string) (*script.Script, error)
	// BaseURL is the deployment's public address, which an endpoint's URL
	// is under.
	BaseURL string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Script is the script an endpoint runs, as the list names it.
type Script struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Owner       string `json:"owner"`
}

// Endpoint is one binding and its traffic over the range.
type Endpoint struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Methods   []string `json:"methods"`
	Path      string   `json:"path"`
	AuthMode  string   `json:"auth_mode"`
	OpenReads bool     `json:"open_reads"`
	Enabled   bool     `json:"enabled"`
	// Limits are the binding's per-request and per-minute limits.
	TimeoutSeconds  int       `json:"timeout_seconds"`
	MaxBodyBytes    int       `json:"max_body_bytes"`
	RateLimit       int       `json:"rate_limit"`
	CallerRateLimit int       `json:"caller_rate_limit"`
	UpdatedBy       string    `json:"updated_by"`
	UpdatedAt       time.Time `json:"updated_at"`
	Script          Script    `json:"script"`
	Traffic         Traffic   `json:"traffic"`
	// Refused is how many requests it refused before its handler ran, and
	// RefusedByReason the same by reason (bot_check, rate_limit,
	// caller_rate_limit, body_limit, credential, origin).
	Refused         int64            `json:"refused"`
	RefusedByReason map[string]int64 `json:"refused_by_reason"`
}

// List is the list route's answer.
type List struct {
	Range     string     `json:"range"`
	Since     time.Time  `json:"since"`
	Endpoints []Endpoint `json:"endpoints"`
}

// Detail is one endpoint's page: the binding and its totals, the request
// chart, its busiest callers and its recent failed requests.
type Detail struct {
	Range string    `json:"range"`
	Since time.Time `json:"since"`
	Endpoint
	// BucketSeconds is how long each chart bucket is.
	BucketSeconds int64               `json:"bucket_seconds"`
	Series        []Bucket            `json:"series"`
	TopCallers    []Caller            `json:"top_callers"`
	Failures      []epbinding.Failure `json:"failures"`
}

type handler struct {
	cfg      Config
	traffic  trafficReader
	refusals *eprefusal.Store
}

// Register mounts the routes, each behind wrap, the admin API's
// authentication.
func Register(mux *http.ServeMux, wrap func(http.Handler) http.Handler, cfg Config) {
	if cfg.DB == nil || cfg.Bindings == nil || cfg.Script == nil {
		return
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	h := &handler{cfg: cfg, traffic: trafficReader{db: cfg.DB}, refusals: eprefusal.NewStore(cfg.DB)}
	mux.Handle("GET "+listPath, wrap(http.HandlerFunc(h.list)))
	mux.Handle("GET "+detailPath, wrap(http.HandlerFunc(h.detail)))
}

// list handles GET /api/v1/admin/endpoints.
//
// @Summary      List every endpoint with its traffic
// @Description  Every endpoint binding on the deployment, ordered by name: its script (id, name, owner), address, methods, path, access (public or signed_in), open_reads, whether it is on, its limits, and who last changed it and when. traffic is what it answered over the range: requests by status class, failures, p50 and p95 duration, distinct signed-in callers and the last request's time, read from each request's audit row. refused counts the requests it turned away before its handler ran, by reason: the bot check, the endpoint's or a caller's rate limit, the body limit, a missing or refused credential, an origin the binding does not list. A deployment with no bindings answers an empty list.
// @Tags         Endpoints
// @Produce      json
// @Param        range  query  string  false  "day (the default) or week"  Enums(day, week)
// @Success      200  {object}  List
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/endpoints [get]
func (h *handler) list(w http.ResponseWriter, r *http.Request) {
	rng, since, ok := h.window(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	bindings, err := h.cfg.Bindings.List(ctx, epbinding.Filter{})
	if err != nil {
		fail(w, r, err)
		return
	}
	traffic, err := h.traffic.byEndpoint(ctx, since)
	if err != nil {
		fail(w, r, err)
		return
	}
	refused, err := h.refusals.Since(ctx, "", since)
	if err != nil {
		fail(w, r, err)
		return
	}
	byEndpoint := refusalsByEndpoint(refused)
	out := List{Range: rng, Since: since, Endpoints: make([]Endpoint, 0, len(bindings))}
	for i := range bindings {
		out.Endpoints = append(out.Endpoints, h.endpoint(ctx, &bindings[i], traffic[bindings[i].Name], byEndpoint[bindings[i].ID]))
	}
	httpjson.WriteJSON(w, http.StatusOK, out)
}

// detail handles GET /api/v1/admin/endpoints/{name}.
//
// @Summary      Get one endpoint with its traffic
// @Description  One endpoint binding as the list reports it, with its requests over the range in buckets (an hour for day, six hours for week) by status class beside the requests it refused, its ten busiest signed-in callers, and its most recent failed requests (kept 7 days).
// @Tags         Endpoints
// @Produce      json
// @Param        name   path   string  true   "Endpoint name"
// @Param        range  query  string  false  "day (the default) or week"  Enums(day, week)
// @Success      200  {object}  Detail
// @Failure      400  {object}  httpjson.ProblemDetail
// @Failure      404  {object}  httpjson.ProblemDetail
// @Failure      500  {object}  httpjson.ProblemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/endpoints/{name} [get]
func (h *handler) detail(w http.ResponseWriter, r *http.Request) {
	rng, since, ok := h.window(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	b, err := h.cfg.Bindings.GetByName(ctx, r.PathValue("name"))
	if errors.Is(err, epbinding.ErrNotFound) {
		httpjson.WriteError(w, http.StatusNotFound, "no endpoint has this name")
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	d, err := h.read(ctx, b, rng, since)
	if err != nil {
		fail(w, r, err)
		return
	}
	httpjson.WriteJSON(w, http.StatusOK, d)
}

// read assembles one endpoint's page.
func (h *handler) read(ctx context.Context, b *epbinding.Binding, rng string, since time.Time) (*Detail, error) {
	traffic, err := h.traffic.byEndpoint(ctx, since)
	if err != nil {
		return nil, err
	}
	refused, err := h.refusals.Since(ctx, b.ID, since)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	step := bucketOf(rng)
	classes, err := h.traffic.series(ctx, b.Name, since, step)
	if err != nil {
		return nil, err
	}
	callers, err := h.traffic.topCallers(ctx, b.Name, since, maxCallers)
	if err != nil {
		return nil, err
	}
	failures, err := h.cfg.Bindings.Failures(ctx, b.ID, maxFailures)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store names the read
	}
	if failures == nil {
		failures = []epbinding.Failure{}
	}
	return &Detail{
		Range: rng, Since: since,
		Endpoint:      h.endpoint(ctx, b, traffic[b.Name], refusalsByEndpoint(refused)[b.ID]),
		BucketSeconds: int64(step / time.Second),
		Series:        series(since, h.cfg.Now(), step, classes, refused),
		TopCallers:    callers,
		Failures:      failures,
	}, nil
}

// endpoint renders one binding with its traffic and refusals.
func (h *handler) endpoint(ctx context.Context, b *epbinding.Binding, traffic *Traffic, refused map[string]int64) Endpoint {
	e := Endpoint{
		ID: b.ID, Name: b.Name, URL: h.cfg.BaseURL + b.URLPath(), Methods: b.Methods, Path: b.Path,
		AuthMode: b.AuthMode, OpenReads: b.OpenReads, Enabled: b.Enabled,
		TimeoutSeconds: b.TimeoutSeconds, MaxBodyBytes: b.MaxBodyBytes, RateLimit: b.RateLimit,
		CallerRateLimit: b.CallerRateLimit, UpdatedBy: b.UpdatedBy, UpdatedAt: b.UpdatedAt,
		Script: Script{ID: b.ScriptID}, RefusedByReason: map[string]int64{},
	}
	if e.Methods == nil {
		e.Methods = []string{}
	}
	if traffic != nil {
		e.Traffic = *traffic
	}
	for reason, n := range refused {
		e.RefusedByReason[reason] = n
		e.Refused += n
	}
	if sc, err := h.cfg.Script(ctx, b.ScriptID); err == nil && sc != nil {
		e.Script = Script{ID: sc.ID, Name: sc.Name, DisplayName: sc.DisplayName, Owner: sc.OwnerEmail}
	}
	return e
}

// window reads the range a request asks for, answering 400 for one that is
// not day or week.
func (h *handler) window(w http.ResponseWriter, r *http.Request) (rng string, since time.Time, ok bool) {
	rng = r.URL.Query().Get("range")
	now := h.cfg.Now().UTC()
	switch rng {
	case "", RangeDay:
		return RangeDay, now.Add(-24 * time.Hour), true
	case RangeWeek:
		return RangeWeek, now.Add(-7 * 24 * time.Hour), true
	}
	httpjson.WriteError(w, http.StatusBadRequest, "range must be day or week")
	return "", time.Time{}, false
}

// The chart's bucket over a day and over a week.
const (
	dayBucket  = time.Hour
	weekBucket = 6 * time.Hour
)

// bucketOf is the chart bucket for a range.
func bucketOf(rng string) time.Duration {
	if rng == RangeWeek {
		return weekBucket
	}
	return dayBucket
}

// series lays the requests and refusals out in buckets of step from since to
// now, a bucket for every step whether or not anything arrived in it.
func series(since, now time.Time, step time.Duration, classes map[int64]*StatusClasses, refused []eprefusal.Count) []Bucket {
	secs := int64(step / time.Second)
	first, last := since.Unix()/secs, now.Unix()/secs
	out := make([]Bucket, 0, last-first+1)
	index := map[int64]int{}
	for b := first; b <= last; b++ {
		index[b] = len(out)
		bucket := Bucket{Start: time.Unix(b*secs, 0).UTC()}
		if c := classes[b]; c != nil {
			bucket.StatusClasses = *c
		}
		out = append(out, bucket)
	}
	for _, c := range refused {
		if i, ok := index[c.Hour.Unix()/secs]; ok {
			out[i].Refused += c.Count
		}
	}
	return out
}

// refusalsByEndpoint totals counts by endpoint and reason.
func refusalsByEndpoint(counts []eprefusal.Count) map[string]map[string]int64 {
	out := map[string]map[string]int64{}
	for _, c := range counts {
		if out[c.EndpointID] == nil {
			out[c.EndpointID] = map[string]int64{}
		}
		out[c.EndpointID][c.Reason] += c.Count
	}
	return out
}

// fail logs a read that failed and answers 500.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "admin endpoints: a read failed", "error", logsan.SanitizeForLog(err.Error()))
	httpjson.WriteError(w, http.StatusInternalServerError, "the endpoints could not be read")
}
