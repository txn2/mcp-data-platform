package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/pkg/audit"
)

const (
	// auditToolName is the action recorded in the audit log for every
	// proxied query. The observability:read capability that gates the
	// proxy is enforced by the platform-supplied Authorizer (see
	// platform.NewObservabilityAuthorizer), not in this package.
	auditToolName    = "observability.query"
	auditSource      = "observability"
	maxAuditQueryLen = 1024

	pathQuery      = "/api/v1/query"
	pathQueryRange = "/api/v1/query_range"
)

// Decision is the result of authorizing an inbound proxy request. The
// platform supplies an Authorizer that authenticates the request
// token, checks the observability:read capability, and resolves the
// persona; the proxy package stays free of auth/persona imports.
type Decision struct {
	Authenticated bool // a valid token was presented
	Allowed       bool // the caller's persona grants observability:read
	UserID        string
	Email         string
	Persona       string
}

// Authorizer authorizes a proxy request from its context (the request
// token is on the context, placed there by the auth middleware).
type Authorizer interface {
	Authorize(ctx context.Context) Decision
}

// Handler serves the authenticated PromQL proxy endpoints.
type Handler struct {
	base    *url.URL // nil when unconfigured -> 503
	client  *http.Client
	user    string
	pass    string
	authz   Authorizer
	limiter *rateLimiter
	auditor audit.Logger // may be nil (audit disabled)
}

// New builds a proxy handler. A bad Prometheus URL is a fatal config
// error; an empty URL is valid and puts the handler in 503 mode.
func New(cfg Config, authz Authorizer, auditor audit.Logger) (*Handler, error) {
	var base *url.URL
	if cfg.URL != "" {
		parsed, err := cfg.parseBase()
		if err != nil {
			return nil, err
		}
		base = parsed
	}
	return &Handler{
		base: base,
		client: &http.Client{
			Timeout: cfg.timeout(),
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		user:    cfg.BasicAuthUser,
		pass:    cfg.BasicAuthPass,
		authz:   authz,
		limiter: newRateLimiter(cfg.ratePerSecond()),
		auditor: auditor,
	}, nil
}

// Register mounts the proxy endpoints on mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/observability/query", h.serveQuery)
	mux.HandleFunc("GET /api/v1/observability/query_range", h.serveQueryRange)
}

func (h *Handler) serveQuery(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, pathQuery, []string{"query", "time", "timeout"})
}

func (h *Handler) serveQueryRange(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, pathQueryRange, []string{"query", "start", "end", "step", "timeout"})
}

// serve runs the authz -> rate-limit -> forward -> audit pipeline for
// one read-only Prometheus endpoint. passParams are the inbound query
// parameters copied (encoded) to the upstream request; nothing else
// from the inbound request reaches the upstream.
func (h *Handler) serve(w http.ResponseWriter, r *http.Request, upstreamPath string, passParams []string) {
	dec := h.authz.Authorize(r.Context())
	if !dec.Authenticated {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !dec.Allowed {
		writeError(w, http.StatusForbidden, "forbidden: observability:read permission required")
		return
	}

	key := dec.Persona
	if key == "" {
		key = dec.UserID
	}
	if !h.limiter.allow(key) {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	if h.base == nil {
		writeError(w, http.StatusServiceUnavailable, "observability backend not configured")
		return
	}

	start := time.Now()
	status, err := h.forward(w, r, upstreamPath, passParams)
	h.audit(r.Context(), auditRecord{
		dec:          dec,
		upstreamPath: upstreamPath,
		query:        r.URL.Query().Get("query"),
		status:       status,
		gwErr:        err,
		dur:          time.Since(start),
	})
}

// forward issues the upstream request and copies its status and body
// through unchanged. Returns the upstream status code (0 on a gateway
// error) and any gateway-level error. On a gateway error it writes a
// 502/504 itself so the caller only needs to audit.
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, upstreamPath string, passParams []string) (int, error) {
	vals := url.Values{}
	for _, p := range passParams {
		if v := r.URL.Query().Get(p); v != "" {
			vals.Set(p, v)
		}
	}

	// Build the upstream URL from the pre-parsed, validated base. The
	// host, scheme, and path are fixed operator config; only the
	// encoded query-string values are request-controlled.
	reqURL := *h.base
	reqURL.Path = strings.TrimRight(h.base.Path, "/") + upstreamPath
	reqURL.RawQuery = vals.Encode()

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, reqURL.String(), http.NoBody)
	if err != nil {
		writeError(w, http.StatusBadGateway, "failed to build upstream request")
		return 0, fmt.Errorf("building upstream request: %w", err)
	}
	if h.user != "" || h.pass != "" {
		req.SetBasicAuth(h.user, h.pass)
	}

	resp, err := h.client.Do(req) // #nosec G107 -- URL host/scheme/path are fixed from validated operator config; only encoded query-string values are request-controlled
	if err != nil {
		if isTimeout(err) {
			writeError(w, http.StatusGatewayTimeout, "prometheus query timed out")
		} else {
			writeError(w, http.StatusBadGateway, "prometheus unreachable")
		}
		return 0, fmt.Errorf("prometheus request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Deliberately forward only Content-Type. Prometheus JSON responses
	// are uncompressed (the proxy sends no Accept-Encoding), so no
	// Content-Encoding or other hop-by-hop headers need passing through.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
	return resp.StatusCode, nil
}

// auditRecord bundles the per-request inputs to audit so the method
// stays within the argument-count limit.
type auditRecord struct {
	dec          Decision
	upstreamPath string
	query        string
	status       int
	gwErr        error
	dur          time.Duration
}

// audit records one observability.query event. Best-effort and
// detached from the request context so a slow audit store does not
// block the response or get canceled when the request ends. No-op when
// no audit store is configured.
func (h *Handler) audit(ctx context.Context, p auditRecord) {
	if h.auditor == nil {
		return
	}

	endpoint := "query"
	if p.upstreamPath == pathQueryRange {
		endpoint = "query_range"
	}
	success := p.gwErr == nil && p.status > 0 && p.status < http.StatusBadRequest
	errMsg := ""
	switch {
	case p.gwErr != nil:
		errMsg = p.gwErr.Error()
	case p.status >= http.StatusBadRequest:
		errMsg = http.StatusText(p.status)
	}

	ev := audit.NewEvent(auditToolName).
		WithUser(p.dec.UserID, p.dec.Email).
		WithPersona(p.dec.Persona).
		WithToolkit(auditSource, "").
		WithParameters(map[string]any{"endpoint": endpoint, "query": truncate(p.query, maxAuditQueryLen)}).
		WithResult(success, errMsg, p.dur.Milliseconds()).
		WithTransport("http", auditSource)
	event := *ev

	detached := context.WithoutCancel(ctx)
	go func() { _ = h.auditor.Log(detached, event) }()
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}

// truncate caps s at n bytes, backing off to the previous UTF-8 rune
// boundary so the audited query never ends in a partial rune.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.ValidString(s[:n]) {
		n--
	}
	return s[:n]
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]string{"status": "error", "error": msg})
	_, _ = w.Write(body)
}
