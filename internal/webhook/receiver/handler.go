package receiver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/webhook/whauth"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// PathPrefix is where the receiver is mounted.
const PathPrefix = "/hooks/"

// Outcomes a request is counted under.
const (
	OutcomeAccepted      = "accepted"
	OutcomeUnauthorized  = "unauthorized"
	OutcomeTooLarge      = "too_large"
	OutcomeRateLimited   = "rate_limited"
	OutcomeBufferFull    = "buffer_full"
	OutcomeWriteFailed   = "write_failed"
	OutcomeUnknownSource = "unknown_source"
	OutcomeInvalidBody   = "invalid_body"
)

// Retry-After values, in seconds, for the answers a sender should retry.
const (
	retryBufferFull  = 1
	retryWriteFailed = 5
	retryStopping    = 5
)

// CloudEvents abuse-protection handshake headers.
const (
	headerRequestOrigin = "WebHook-Request-Origin"
	headerAllowedOrigin = "WebHook-Allowed-Origin"
	headerAllowedRate   = "WebHook-Allowed-Rate"
)

// notFoundBody is the one answer to an unknown and to a disabled source, so a
// caller cannot tell which names exist.
const notFoundBody = `{"error":"not found"}`

// ServeHTTP handles /hooks/{source} and /hooks/{source}/{token}.
func (r *Receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	started := r.now()
	name, token, ok := splitPath(req.URL.Path)
	src, served := r.lookupFresh(req.Context(), name)
	if !ok || !served || (token != "" && src.Auth.Mode != whsource.AuthPathToken) {
		r.count(name, OutcomeUnknownSource)
		writeJSON(w, http.StatusNotFound, notFoundBody)
		return
	}
	switch req.Method {
	case http.MethodOptions:
		handshake(w, req, src.Source)
	case http.MethodPost:
		r.post(w, req, src, token, started)
	default:
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "only POST delivers events")
	}
}

// post checks a delivery in the order the source's contract gives -- size,
// authentication, rate, body -- and accepts it once all four pass.
func (r *Receiver) post(w http.ResponseWriter, req *http.Request, src served, token string, started time.Time) {
	body, status := readBody(req, src.Config.MaxBodyBytes)
	if status != 0 {
		r.refuse(w, src.Name, refusal{OutcomeTooLarge, "the body is larger than max_body_bytes", status, 0})
		return
	}
	if err := whauth.Verify(src.Source, whauth.Request{Header: req.Header, PathToken: token, Body: body}, r.now()); err != nil {
		r.cfg.Logger.Info("webhooks: request refused", "source", logsan.SanitizeForLog(src.Name),
			"reason", logsan.SanitizeForLog(err.Error()))
		r.refuse(w, src.Name, refusal{OutcomeUnauthorized, err.Error(), http.StatusUnauthorized, 0})
		return
	}
	if allowed, wait := r.allow(src.Source); !allowed {
		r.refuse(w, src.Name, refusal{OutcomeRateLimited, "the source's rate limit was reached", http.StatusTooManyRequests, wait})
		return
	}
	events, err := r.build(req, src, body)
	if err != nil {
		r.refuse(w, src.Name, refusal{OutcomeInvalidBody, err.Error(), http.StatusBadRequest, 0})
		return
	}
	r.accept(req.Context(), w, src.Source, events, started)
}

// accept buffers the events and answers once their segment is written.
func (r *Receiver) accept(ctx context.Context, w http.ResponseWriter, src whsource.Source, events []whevent.Event, started time.Time) {
	if len(events) > src.Config.BufferLimit {
		// A batch the buffer could never hold would be answered 503 on every
		// retry. It is refused once, with what the sender has to change.
		r.refuse(w, src.Name, refusal{
			OutcomeTooLarge,
			fmt.Sprintf("the request holds %d events, more than the source's buffer_limit of %d", len(events), src.Config.BufferLimit),
			http.StatusRequestEntityTooLarge, 0,
		})
		return
	}
	buf := r.bufferFor(src.Name)
	if buf == nil {
		r.refuse(w, src.Name, refusal{OutcomeWriteFailed, "the platform is shutting down", http.StatusServiceUnavailable, retryStopping})
		return
	}
	bt, err := buf.admit(events, src.Config.BufferLimit)
	if errors.Is(err, errClosed) {
		r.refuse(w, src.Name, refusal{OutcomeWriteFailed, "the platform is shutting down", http.StatusServiceUnavailable, retryStopping})
		return
	}
	if err != nil {
		r.refuse(w, src.Name, refusal{OutcomeBufferFull, "the source's buffer is full", http.StatusServiceUnavailable, retryBufferFull})
		return
	}
	select {
	case err = <-bt.done:
	case <-ctx.Done():
		// The sender went away before its segment was written. It saw no
		// answer, so it retries; the write goes on, and compaction removes
		// the second copy.
		r.count(src.Name, OutcomeWriteFailed)
		return
	}
	if err != nil {
		r.cfg.Logger.Warn("webhooks: segment write failed", "source", logsan.SanitizeForLog(src.Name),
			logKeyError, logsan.SanitizeForLog(err.Error()))
		r.refuse(w, src.Name, refusal{OutcomeWriteFailed, "the events could not be written to object storage", http.StatusServiceUnavailable, retryWriteFailed})
		return
	}
	r.count(src.Name, OutcomeAccepted)
	if r.cfg.Metrics != nil {
		r.cfg.Metrics.WebhookEvents(ctx, src.Name, len(events))
		r.cfg.Metrics.WebhookAck(ctx, src.Name, r.now().Sub(started))
	}
	writeJSON(w, http.StatusAccepted, `{"accepted":`+strconv.Itoa(len(events))+`}`)
}

// build turns the body into events. A form-encoded body carries its JSON in
// the payload field.
func (r *Receiver) build(req *http.Request, src served, body []byte) ([]whevent.Event, error) {
	doc, err := whevent.JSONBody(req.Header.Get("Content-Type"), body)
	if err != nil {
		return nil, err //nolint:wrapcheck // whevent's refusal is the sentence the sender is answered with
	}
	return whevent.Build(doc, src.paths, r.now(), r.cfg.Replica) //nolint:wrapcheck // as above
}

// handshake answers the CloudEvents abuse-protection OPTIONS request on a
// source that declares it. Any other source has nothing at OPTIONS.
func handshake(w http.ResponseWriter, req *http.Request, src whsource.Source) {
	origin := strings.TrimSpace(req.Header.Get(headerRequestOrigin))
	if src.Config.Handshake != whsource.HandshakeCloudEvents || origin == "" {
		w.Header().Set("Allow", "POST")
		writeError(w, http.StatusMethodNotAllowed, "this source does not answer a webhook handshake")
		return
	}
	w.Header().Set(headerAllowedOrigin, origin)
	if rpm := src.Config.RateLimitPerMinute; rpm > 0 {
		w.Header().Set(headerAllowedRate, strconv.Itoa(rpm))
	}
	w.Header().Set("Allow", "POST, OPTIONS")
	w.WriteHeader(http.StatusOK)
}

// refusal is how a request that stored nothing is answered: the outcome it is
// counted under, the reason the sender and the source's page are given, the
// status, and how many seconds to wait before retrying, zero when a retry
// would be refused again.
type refusal struct {
	outcome    string
	reason     string
	status     int
	retryAfter int
}

// refuse answers a request that stored nothing, and keeps it for the source's
// page.
func (r *Receiver) refuse(w http.ResponseWriter, source string, why refusal) {
	r.count(source, why.outcome)
	r.reject(source, why.outcome, why.reason)
	if why.retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(why.retryAfter))
	}
	writeError(w, why.status, why.reason)
}

// splitPath reads /hooks/{source} and /hooks/{source}/{token}.
func splitPath(path string) (name, token string, ok bool) {
	rest, found := strings.CutPrefix(path, PathPrefix)
	if !found || rest == "" {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	switch {
	case len(parts) == 1:
		return parts[0], "", true
	case len(parts) == 2 && parts[1] != "":
		return parts[0], parts[1], true
	}
	return parts[0], "", false
}

// readBody reads at most limit bytes, and answers 413 for a body larger than
// that whether or not it declared its length.
func readBody(req *http.Request, limit int64) (body []byte, status int) {
	if req.ContentLength > limit {
		return nil, http.StatusRequestEntityTooLarge
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, limit+1))
	if err != nil || int64(len(body)) > limit {
		return nil, http.StatusRequestEntityTooLarge
	}
	return body, 0
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	b, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		b = []byte(`{"error":"error"}`)
	}
	writeJSON(w, status, string(b))
}
