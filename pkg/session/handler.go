package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

const (
	// sessionIDHeader is the MCP session header name.
	sessionIDHeader = "Mcp-Session-Id"

	// sessionIDBytes is the number of random bytes for session ID generation.
	sessionIDBytes = 16

	// bearerPrefixLen is the length of the "Bearer " prefix in Authorization headers.
	bearerPrefixLen = 7

	// slogKeyError is the slog attribute key for error values.
	slogKeyError = "error"
)

// HandlerConfig configures an AwareHandler.
type HandlerConfig struct {
	Store Store
	TTL   time.Duration
}

// AwareHandler wraps an HTTP handler to manage MCP sessions against
// an external Store. It is used when the SDK runs in stateless mode to
// provide session persistence (e.g. for zero-downtime restarts).
type AwareHandler struct {
	inner http.Handler
	store Store
	ttl   time.Duration
}

// NewAwareHandler creates a handler that manages sessions externally.
func NewAwareHandler(inner http.Handler, cfg HandlerConfig) *AwareHandler {
	return &AwareHandler{
		inner: inner,
		store: cfg.Store,
		ttl:   cfg.TTL,
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

	h.handleExisting(w, r, sessionID)
}

// handleInitialize creates a new session for requests without a session ID.
func (h *AwareHandler) handleInitialize(w http.ResponseWriter, r *http.Request) {
	sessionID, err := generateSessionID()
	if err != nil {
		slog.Error("session: failed to generate ID", slogKeyError, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
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
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	slog.Debug("session: created", "session_id", sessionID)

	sw := &sessionIDWriter{
		ResponseWriter: w,
		sessionID:      sessionID,
	}
	r.Header.Set(sessionIDHeader, sessionID)
	h.inner.ServeHTTP(sw, r)
}

// handleExisting validates and forwards requests with an existing session ID.
func (h *AwareHandler) handleExisting(w http.ResponseWriter, r *http.Request, sessionID string) {
	sess, err := h.store.Get(r.Context(), sessionID)
	if err != nil {
		slog.Error("session: store error", slogKeyError, err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	if sess == nil {
		http.Error(w, "session not found or expired", http.StatusNotFound)
		return
	}

	if !validateOwnership(sess, r) {
		http.Error(w, "session ownership mismatch", http.StatusForbidden)
		return
	}

	// Touch asynchronously to avoid blocking the request.
	go func() {
		if err := h.store.Touch(r.Context(), sessionID); err != nil {
			slog.Debug("session: touch failed", "session_id", sessionID, slogKeyError, err)
		}
	}()

	h.inner.ServeHTTP(w, r)
}

// handleDelete removes the session and forwards the DELETE to the SDK.
func (h *AwareHandler) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get(sessionIDHeader)
	if sessionID != "" {
		if err := h.store.Delete(r.Context(), sessionID); err != nil {
			slog.Debug("session: delete failed", "session_id", sessionID, slogKeyError, err)
		}
	}
	h.inner.ServeHTTP(w, r)
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

// hashToken returns the SHA-256 hex digest of a token, or empty for empty tokens.
func hashToken(token string) string {
	if token == "" {
		return ""
	}
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
