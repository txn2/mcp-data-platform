package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// awareSessionKey is the context key for the AwareHandler session ID.
type awareSessionKey struct{}

// AwareSessionID returns the session ID set by AwareHandler, or "".
func AwareSessionID(ctx context.Context) string {
	if id, ok := ctx.Value(awareSessionKey{}).(string); ok {
		return id
	}
	return ""
}

// WithAwareSessionID returns a context carrying the given session ID.
// This is used by AwareHandler internally and exposed for middleware that
// needs to read the session ID via AwareSessionID.
func WithAwareSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, awareSessionKey{}, sessionID)
}

const (
	// sessionIDHeader is the MCP session header name.
	sessionIDHeader = "Mcp-Session-Id"

	// sessionIDBytes is the number of random bytes for session ID generation.
	sessionIDBytes = 16

	// bearerPrefixLen is the length of the "Bearer " prefix in Authorization headers.
	bearerPrefixLen = 7

	// slogKeyError is the slog attribute key for error values.
	slogKeyError = "error"

	// httpErrInternal is the response body for HTTP 500 errors.
	httpErrInternal = "internal server error"

	// touchTimeout is the maximum time for async session touch operations.
	touchTimeout = 5 * time.Second

	// sseHeartbeatInterval is the cadence at which the SSE long-poll
	// stream emits a comment-frame keepalive. Most reverse proxies
	// (Cloudflare, nginx, AWS ALB) terminate idle SSE connections at
	// 30-60s; 25s gives a comfortable margin while keeping the
	// per-connection bandwidth negligible (about 3 bytes per heartbeat).
	sseHeartbeatInterval = 25 * time.Second

	// sseAcceptType is the MIME type that signals the client wants the
	// streamable HTTP server-push channel rather than the regular
	// request/response RPC path. Per MCP spec §2.2.3.
	sseAcceptType = "text/event-stream"

	// sessionIDKey is the structured-log attribute key for the session
	// ID. Centralized so every log site (touch failures, SSE write
	// failures, delete failures) uses the same key — operators who
	// grep `session_id=` find every event for the session.
	sessionIDKey = "session_id"
)

// HandlerConfig configures an AwareHandler.
type HandlerConfig struct {
	Store Store
	TTL   time.Duration
	// Broadcaster delivers server-pushed MCP notifications (e.g.
	// notifications/tools/list_changed) to the per-session SSE
	// long-poll stream. When nil, GET requests are forwarded to the
	// inner handler unchanged — useful for tests or for transport
	// modes that don't need server push.
	Broadcaster Broadcaster
}

// AwareHandler wraps an HTTP handler to manage MCP sessions against
// an external Store. It is used when the SDK runs in stateless mode to
// provide session persistence (e.g. for zero-downtime restarts).
type AwareHandler struct {
	inner       http.Handler
	store       Store
	ttl         time.Duration
	broadcaster Broadcaster
}

// NewAwareHandler creates a handler that manages sessions externally.
func NewAwareHandler(inner http.Handler, cfg HandlerConfig) *AwareHandler {
	return &AwareHandler{
		inner:       inner,
		store:       cfg.Store,
		ttl:         cfg.TTL,
		broadcaster: cfg.Broadcaster,
	}
}

// ServeHTTP dispatches the request based on session state.
func (h *AwareHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodDelete {
		h.handleDelete(w, r)
		return
	}

	sessionID := r.Header.Get(sessionIDHeader)
	if sessionID == "" {
		h.handleInitialize(w, r)
		return
	}

	if r.Method == http.MethodGet && acceptsSSE(r) && h.broadcaster != nil {
		h.handleSSE(w, r, sessionID)
		return
	}

	h.handleExisting(w, r, sessionID)
}

// acceptsSSE reports whether the request signals it wants the
// server-push event stream (MCP spec §2.2.3). Matching is a contains
// check against the lowercased Accept header value so clients that
// send a list (e.g. "application/json, text/event-stream") still
// trigger the SSE branch, and so a strict proxy that title-cases
// media types (`Text/Event-Stream`) does not silently bypass SSE
// delivery — RFC 7231 §3.1.1.1 specifies media types are
// case-insensitive.
func acceptsSSE(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept")), sseAcceptType)
}

// handleInitialize creates a new session for requests without a session ID.
func (h *AwareHandler) handleInitialize(w http.ResponseWriter, r *http.Request) {
	sessionID, err := generateSessionID()
	if err != nil {
		slog.Error("session: failed to generate ID", slogKeyError, err)
		http.Error(w, httpErrInternal, http.StatusInternalServerError)
		return
	}

	now := time.Now()
	sess := &Session{
		ID:           sessionID,
		UserID:       hashToken(extractToken(r)),
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(h.ttl),
		State:        make(map[string]any),
	}

	if err := h.store.Create(r.Context(), sess); err != nil {
		slog.Error("session: failed to create", slogKeyError, err)
		http.Error(w, httpErrInternal, http.StatusInternalServerError)
		return
	}

	slog.Debug("session: created", sessionIDKey, sessionID)

	sw := &sessionIDWriter{
		ResponseWriter: w,
		sessionID:      sessionID,
	}
	r.Header.Set(sessionIDHeader, sessionID)
	r = r.WithContext(WithAwareSessionID(r.Context(), sessionID))
	h.inner.ServeHTTP(sw, r)
}

// handleExisting validates and forwards requests with an existing session ID.
// If the session is expired or missing but the request carries auth credentials,
// a new session is created transparently so API-key clients recover without
// user intervention.
func (h *AwareHandler) handleExisting(w http.ResponseWriter, r *http.Request, sessionID string) {
	sess, err := h.store.Get(r.Context(), sessionID)
	if err != nil {
		slog.Error("session: store error", slogKeyError, err)
		http.Error(w, httpErrInternal, http.StatusInternalServerError)
		return
	}
	if sess == nil {
		// Session expired or was cleaned up. If the request carries auth
		// credentials, revive the session using the SAME ID. Clients like
		// Claude Desktop do not update their stored session ID from
		// response headers, so generating a new ID would cause every
		// subsequent request to trigger another revive — breaking
		// provenance tracking and enrichment dedup.
		if extractToken(r) != "" {
			slog.Info("session: reviving expired session",
				sessionIDKey, sanitizeLogValue(sessionID)) // #nosec G706 -- sessionID sanitized via sanitizeLogValue
			if err := h.reviveSession(r.Context(), sessionID, r); err != nil {
				slog.Error("session: failed to revive", slogKeyError, err)
				http.Error(w, httpErrInternal, http.StatusInternalServerError)
				return
			}
			r = r.WithContext(WithAwareSessionID(r.Context(), sessionID))
			h.inner.ServeHTTP(w, r)
			return
		}
		http.Error(w, "session not found or expired", http.StatusNotFound)
		return
	}

	if !validateOwnership(sess, r) {
		http.Error(w, "session ownership mismatch", http.StatusForbidden)
		return
	}

	// Touch with a detached context so the update is not canceled when
	// the HTTP response completes before the goroutine runs.
	go func() { // #nosec G118 -- detached context is required; touch must outlive the HTTP response
		ctx, cancel := context.WithTimeout(context.Background(), touchTimeout)
		defer cancel()
		if err := h.store.Touch(ctx, sessionID); err != nil {
			slog.Debug("session: touch failed", sessionIDKey, sanitizeLogValue(sessionID), slogKeyError, err) // #nosec G706 -- sessionID sanitized via sanitizeLogValue
		}
	}()

	r = r.WithContext(WithAwareSessionID(r.Context(), sessionID))
	h.inner.ServeHTTP(w, r)
}

// handleSSE serves the long-lived server-push stream for the named
// session. It bypasses the inner handler — when the SDK runs in
// stateless mode the inner handler would 405 the GET (per MCP spec
// "server MUST return 405 if it doesn't offer SSE stream"), so the
// broadcaster path is the platform's substitute for the SDK's native
// (stateful-only) push channel.
//
// Behavior:
//
//   - Validates the session through the same store.Get + ownership
//     path as POST so a forged Mcp-Session-Id can't open a stream
//     against another user's session.
//   - Subscribes to the broadcaster (sessionID is recorded for log
//     attribution; events fan out to all subscribers — see
//     pkg/session.Broadcaster doc).
//   - Streams every event as a JSON-RPC 2.0 notification framed for
//     SSE: `data: {"jsonrpc":"2.0","method":...,"params":...}\n\n`.
//   - Sends a comment-frame heartbeat every sseHeartbeatInterval so
//     intermediate proxies don't terminate the connection during
//     periods with no events.
//   - Returns when the client disconnects (r.Context().Done()) or
//     when the broadcaster closes the subscription.
//
// Note: an SSE stream that has already opened keeps running even if
// the underlying session is later DELETEd; the heartbeat Touch will
// fail silently and the stream continues until the client
// disconnects. This is acceptable because the events fan-out is
// server-wide (no per-session payload), so an orphaned stream cannot
// leak information that wasn't already broadcast — but operators
// noticing "ghost" SSE clients should look at session DELETE
// activity for context.
func (h *AwareHandler) handleSSE(w http.ResponseWriter, r *http.Request, sessionID string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		// Without Flusher, every write would buffer indefinitely —
		// bail loudly so misconfigured middleware (a buffering wrapper
		// in front of us) is found in dev rather than mystery-debugged
		// in prod.
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	if status := h.validateSSESession(r, sessionID); status != http.StatusOK {
		http.Error(w, sseStatusMessage(status), status)
		return
	}

	writeSSEHeaders(w, sessionID)
	flusher.Flush()

	ctx := r.Context()
	sub := h.broadcaster.Subscribe(ctx, sessionID)
	defer sub.Close()

	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	h.streamSSEEvents(ctx, w, flusher, sub, sessionID)
}

// writeSSEHeaders sets the response headers for an SSE stream and
// commits the 200 status. Extracted from handleSSE to keep the loop's
// cyclomatic complexity below the project ceiling.
func writeSSEHeaders(w http.ResponseWriter, sessionID string) {
	w.Header().Set("Content-Type", sseAcceptType)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// X-Accel-Buffering disables nginx's response buffering so SSE
	// frames reach the browser immediately. Other proxies (Cloudflare,
	// AWS ALB) ignore the header but it's harmless to send.
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set(sessionIDHeader, sessionID)
	w.WriteHeader(http.StatusOK)
}

// streamSSEEvents runs the heartbeat / event-forwarding loop until
// the client disconnects or the broadcaster closes the subscription.
// Returns when the connection should terminate. Each heartbeat tick
// also touches the session store so a long-poll client doesn't have
// its session expire mid-stream — without the touch, the next
// reconnect would 404 on a session that was actively in use.
//
// Method on AwareHandler so the store is reached via h.store rather
// than another parameter (revive's argument-limit is 5).
func (h *AwareHandler) streamSSEEvents(ctx context.Context, w http.ResponseWriter, flusher http.Flusher, sub Subscription, sessionID string) {
	heartbeat := time.NewTicker(sseHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
			touchCtx, cancel := context.WithTimeout(context.Background(), touchTimeout)
			if err := h.store.Touch(touchCtx, sessionID); err != nil {
				slog.Debug("session: SSE touch failed",
					sessionIDKey, sanitizeLogValue(sessionID),
					slogKeyError, err)
			}
			cancel()
		case ev, ok := <-sub.Events():
			if !ok {
				return
			}
			if err := writeSSEEvent(w, ev); err != nil {
				slog.Debug("session: SSE write failed",
					sessionIDKey, sanitizeLogValue(sessionID),
					slogKeyError, err)
				return
			}
			flusher.Flush()
		}
	}
}

// validateSSESession checks the session exists, is unexpired (or can
// be revived from credentials), and is owned by the caller. Returns
// http.StatusOK when the GET should be served, or the HTTP status code
// the caller should write back. Distinguishes infrastructure errors
// (500), missing/unauthenticated sessions (404), and ownership
// mismatches (403) — mirroring the POST path's status mapping so a
// caller observing GET vs POST cannot infer different facts about the
// session's existence.
func (h *AwareHandler) validateSSESession(r *http.Request, sessionID string) int {
	sess, err := h.store.Get(r.Context(), sessionID)
	if err != nil {
		slog.Error("session: SSE store error", slogKeyError, err)
		return http.StatusInternalServerError
	}
	if sess == nil {
		// Allow revival when credentials are present, mirroring the
		// POST path. Without revival, every reconnecting Claude
		// Desktop / Claude.ai would 404 here after its session row
		// expired even though it still holds a valid token.
		if extractToken(r) == "" {
			return http.StatusNotFound
		}
		// Mirror the POST handleExisting log so operators correlating
		// session revive events across POST and SSE see both paths
		// — without this, only failures in the SSE revive path were
		// visible.
		slog.Info("session: reviving expired session (SSE)",
			sessionIDKey, sanitizeLogValue(sessionID)) // #nosec G706 -- sessionID sanitized via sanitizeLogValue
		if err := h.reviveSession(r.Context(), sessionID, r); err != nil {
			slog.Error("session: SSE revive failed", slogKeyError, err)
			return http.StatusInternalServerError
		}
		return http.StatusOK
	}
	if !validateOwnership(sess, r) {
		return http.StatusForbidden
	}
	return http.StatusOK
}

// sseStatusMessage maps HTTP status codes returned by validateSSESession
// onto the body text written to the client. Kept narrowly scoped to
// the SSE branch so the wording matches the POST path exactly
// ("session ownership mismatch" → 403, "session not found or expired"
// → 404, plain "internal" → 500).
func sseStatusMessage(status int) string {
	switch status {
	case http.StatusForbidden:
		return "session ownership mismatch"
	case http.StatusInternalServerError:
		return httpErrInternal
	default:
		return "session not found or expired"
	}
}

// writeSSEEvent encodes ev as a JSON-RPC 2.0 notification and writes
// it as a single SSE "data:" frame followed by the required blank
// line (WHATWG HTML §server-sent events).
func writeSSEEvent(w http.ResponseWriter, ev Event) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  ev.Method,
	}
	if ev.Params != nil {
		payload["params"] = ev.Params
	} else {
		payload["params"] = map[string]any{}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal sse event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return fmt.Errorf("write sse event: %w", err)
	}
	return nil
}

// handleDelete removes the session and forwards the DELETE to the SDK.
func (h *AwareHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get(sessionIDHeader)
	if sessionID != "" {
		if err := h.store.Delete(r.Context(), sessionID); err != nil {
			slog.Debug("session: delete failed", sessionIDKey, sanitizeLogValue(sessionID), slogKeyError, err) // #nosec G706 -- sessionID sanitized via sanitizeLogValue
		}
	}
	h.inner.ServeHTTP(w, r)
}

// reviveSession recreates an expired or missing session using the same ID.
// This ensures session stability when clients don't update their Mcp-Session-Id
// from response headers (e.g. Claude Desktop). The expired row (if any) is
// deleted first to avoid unique constraint violations in the database store.
func (h *AwareHandler) reviveSession(ctx context.Context, sessionID string, r *http.Request) error {
	// Remove expired-but-not-yet-cleaned row (if any) to avoid INSERT conflict.
	_ = h.store.Delete(ctx, sessionID)

	now := time.Now()
	if err := h.store.Create(ctx, &Session{
		ID:           sessionID,
		UserID:       hashToken(extractToken(r)),
		CreatedAt:    now,
		LastActiveAt: now,
		ExpiresAt:    now.Add(h.ttl),
		State:        make(map[string]any),
	}); err != nil {
		return fmt.Errorf("reviving session: %w", err)
	}
	return nil
}

// validateOwnership checks that the request token matches the session owner.
// Anonymous sessions (empty UserID) skip this check.
func validateOwnership(sess *Session, r *http.Request) bool {
	if sess.UserID == "" {
		return true
	}
	currentHash := hashToken(extractToken(r))
	return currentHash == sess.UserID
}

// sessionIDWriter wraps http.ResponseWriter to inject the Mcp-Session-Id
// header before the first write.
type sessionIDWriter struct {
	http.ResponseWriter
	sessionID    string
	headerWriten bool
}

// WriteHeader injects the session ID header before delegating to the wrapped writer.
func (w *sessionIDWriter) WriteHeader(statusCode int) {
	if !w.headerWriten {
		w.ResponseWriter.Header().Set(sessionIDHeader, w.sessionID)
		w.headerWriten = true
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *sessionIDWriter) Write(b []byte) (int, error) {
	if !w.headerWriten {
		w.ResponseWriter.Header().Set(sessionIDHeader, w.sessionID)
		w.headerWriten = true
	}
	n, err := w.ResponseWriter.Write(b)
	if err != nil {
		return n, fmt.Errorf("writing response: %w", err)
	}
	return n, nil
}

// Flush implements http.Flusher for SSE streaming compatibility.
func (w *sessionIDWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// generateSessionID creates a cryptographically random session ID.
func generateSessionID() (string, error) {
	b := make([]byte, sessionIDBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// extractToken gets the bearer token from the Authorization header.
func extractToken(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return r.Header.Get("X-API-Key")
	}
	return auth[bearerPrefixLen:]
}

// sanitizeLogValue strips control characters (newlines, tabs, carriage returns)
// from a string before it is used as a structured log value, preventing log injection.
func sanitizeLogValue(s string) string {
	return strings.NewReplacer("\n", "", "\r", "", "\t", "").Replace(s)
}

// hashToken returns the SHA-256 hex digest of a token, or empty for empty tokens.
func hashToken(token string) string {
	if token == "" {
		return ""
	}
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
