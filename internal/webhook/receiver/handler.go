package receiver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpobs"
	"github.com/txn2/mcp-data-platform/internal/webhook/whauth"
	"github.com/txn2/mcp-data-platform/internal/webhook/whevent"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
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
	// OutcomeSenderGone is a delivery whose sender disconnected before its
	// segment was written. Unlike write_failed, its write goes on and may
	// well store the events; a log line says how it ended.
	OutcomeSenderGone = "sender_gone"
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
	d := delivery{source: src.Name, started: started}
	body, status := readBody(req, src.Config.MaxBodyBytes)
	if status != 0 {
		r.refuse(w, d, refusal{outcome: OutcomeTooLarge, reason: "the body is larger than max_body_bytes", status: status}, nil)
		return
	}
	if err := whauth.Verify(src.Source, whauth.Request{Header: req.Header, PathToken: token, Body: body}, r.now()); err != nil {
		r.refuse(w, d, refusal{outcome: OutcomeUnauthorized, reason: err.Error(), status: http.StatusUnauthorized}, nil)
		return
	}
	if allowed, wait := r.allow(src.Source); !allowed {
		httpobs.MarkRateLimited(req, httpobs.LimiterWebhook)
		r.refuse(w, d, refusal{outcome: OutcomeRateLimited, reason: "the source's rate limit was reached", status: http.StatusTooManyRequests, retryAfter: wait}, nil)
		return
	}
	events, err := r.build(req, src, body)
	if err != nil {
		r.refuse(w, d, refusal{outcome: OutcomeInvalidBody, reason: err.Error(), status: http.StatusBadRequest}, nil)
		return
	}
	d.identify(body, events, src.Config.Window())
	r.accept(req.Context(), w, src.Source, d)
}

// accept buffers the events and answers once their segment is written.
func (r *Receiver) accept(ctx context.Context, w http.ResponseWriter, src whsource.Source, d delivery) {
	if len(d.events) > src.Config.BufferLimit {
		// A batch the buffer could never hold would be answered 503 on every
		// retry. It is refused once, with what the sender has to change.
		r.refuse(w, d, refusal{
			outcome: OutcomeTooLarge,
			reason: fmt.Sprintf("the request holds %d events, more than the source's buffer_limit of %d",
				len(d.events), src.Config.BufferLimit),
			status: http.StatusRequestEntityTooLarge,
		}, nil)
		return
	}
	if r.cfg.Spool != nil {
		r.acceptSpooled(ctx, w, src, d)
		return
	}
	buf := r.bufferFor(src.Name)
	if buf == nil {
		r.refuse(w, d, refusal{outcome: OutcomeWriteFailed, reason: "the platform is shutting down", status: http.StatusServiceUnavailable, retryAfter: retryStopping}, nil)
		return
	}
	bt, err := buf.admit(d.events, src.Config.BufferLimit)
	if errors.Is(err, errClosed) {
		r.refuse(w, d, refusal{outcome: OutcomeWriteFailed, reason: "the platform is shutting down", status: http.StatusServiceUnavailable, retryAfter: retryStopping}, nil)
		return
	}
	if err != nil {
		r.refuse(w, d, refusal{outcome: OutcomeBufferFull, reason: "the source's buffer is full", status: http.StatusServiceUnavailable, retryAfter: retryBufferFull}, nil)
		return
	}
	var res written
	select {
	case res = <-bt.done:
	case <-ctx.Done():
		r.senderGone(d, bt)
		return
	}
	if res.err != nil {
		r.refuse(w, d, refusal{
			outcome: OutcomeWriteFailed, reason: "the events could not be written to object storage",
			status: http.StatusServiceUnavailable, retryAfter: retryWriteFailed, detail: res.err.Error(),
		}, &res.stages)
		return
	}
	r.observeAccepted(ctx, src.Name, d, res.stages)
	writeJSON(w, http.StatusAccepted, `{"accepted":`+strconv.Itoa(len(d.events))+`}`)
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
	// detail is the error behind the refusal, kept for the source's page and
	// the log and never sent to the sender.
	detail string
}

// refuse answers a request that stored nothing, keeps it for the source's
// page, and logs it with what is known of the delivery and, when its write
// ran, how long each stage took.
func (r *Receiver) refuse(w http.ResponseWriter, d delivery, why refusal, st *stages) {
	kept := why.reason
	if why.detail != "" {
		kept += ": " + why.detail
	}
	r.count(d.source, why.outcome)
	r.reject(d, why.outcome, kept)
	r.logRefused(d, why.outcome, kept, st)
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
	b, err := wirejson.Marshal(map[string]string{"error": msg})
	if err != nil {
		b = []byte(`{"error":"error"}`)
	}
	writeJSON(w, status, string(b))
}
