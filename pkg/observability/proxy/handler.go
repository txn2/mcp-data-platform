package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
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
}

// New builds a proxy handler. A bad Prometheus URL is a fatal config
// error; an empty URL is valid and puts the handler in 503 mode.
func New(cfg Config, authz Authorizer) (*Handler, error) {
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

// serve runs the authz -> rate-limit -> forward pipeline for one
// read-only Prometheus endpoint. passParams are the inbound query
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

	h.forward(w, r, upstreamPath, passParams)
}

// forward issues the upstream request and copies its status and body
// through unchanged. On a gateway error it writes a 502/504 itself, so
// callers do not inspect a return value.
func (h *Handler) forward(w http.ResponseWriter, r *http.Request, upstreamPath string, passParams []string) {
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
		return
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
		return
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
}

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	body, _ := json.Marshal(map[string]string{"status": "error", "error": msg})
	_, _ = w.Write(body)
}
