package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// errStore wraps MemoryStore to inject errors for specific operations.
type errStore struct {
	*MemoryStore
	getErr    error
	createErr error
	deleteErr error
	touchErr  error
}

func (s *errStore) Touch(ctx context.Context, id string) error {
	if s.touchErr != nil {
		return s.touchErr
	}
	return s.MemoryStore.Touch(ctx, id)
}

func (s *errStore) Delete(ctx context.Context, id string) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	return s.MemoryStore.Delete(ctx, id)
}

func (s *errStore) Get(ctx context.Context, id string) (*Session, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.MemoryStore.Get(ctx, id)
}

func (s *errStore) Create(ctx context.Context, sess *Session) error {
	if s.createErr != nil {
		return s.createErr
	}
	return s.MemoryStore.Create(ctx, sess)
}

const (
	handlerTestTTL           = 30 * time.Minute
	handlerTestGoroutines    = 10
	handlerTestPath          = "/"
	handlerTestProtectedSess = "protected-sess"
	handlerTestAuthHeader    = "Authorization"
	handlerTestSessID        = "test-sess"
	handlerTestAPIKey        = "my-api-key"
	sha256HexLen             = 64
)

// testInnerHandler records whether it was called and the session ID.
type testInnerHandler struct {
	mu        sync.Mutex
	called    bool
	sessionID string
}

func (h *testInnerHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.called = true
	h.sessionID = r.Header.Get(sessionIDHeader)
	h.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (h *testInnerHandler) wasCalled() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.called
}

func (h *testInnerHandler) getSessionID() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.sessionID
}

func newTestHandler() (*AwareHandler, *MemoryStore, *testInnerHandler) {
	store := NewMemoryStore(handlerTestTTL)
	inner := &testInnerHandler{}
	handler := NewAwareHandler(inner, HandlerConfig{
		Store: store,
		TTL:   handlerTestTTL,
	})
	return handler, store, inner
}

func TestHandler_Initialize_CreatesSession(t *testing.T) {
	handler, store, inner := newTestHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "inner handler should be called")
	assert.NotEmpty(t, inner.getSessionID(), "session ID should be set on request")

	sessionID := w.Header().Get(sessionIDHeader)
	assert.NotEmpty(t, sessionID, "Mcp-Session-Id header should be in response")

	sess, err := store.Get(context.Background(), sessionID)
	require.NoError(t, err)
	assert.NotNil(t, sess, "session should exist in store")
}

func TestHandler_Initialize_WithBearerToken(t *testing.T) {
	handler, store, _ := newTestHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(handlerTestAuthHeader, "Bearer my-test-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	sessionID := w.Header().Get(sessionIDHeader)
	require.NotEmpty(t, sessionID)

	sess, err := store.Get(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.NotEmpty(t, sess.UserID, "UserID should be token hash")
	assert.Equal(t, hashToken("my-test-token"), sess.UserID)
}

func TestHandler_Initialize_WithAPIKey(t *testing.T) {
	handler, store, _ := newTestHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set("X-API-Key", handlerTestAPIKey)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	sessionID := w.Header().Get(sessionIDHeader)
	require.NotEmpty(t, sessionID)

	sess, err := store.Get(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, hashToken(handlerTestAPIKey), sess.UserID)
}

func TestHandler_ExistingSession_Valid(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	// Pre-create a session
	sess := newTestSession("existing-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "existing-sess")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "inner handler should be called for valid session")
}

func TestHandler_ExistingSession_NotFound(t *testing.T) {
	handler, _, inner := newTestHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "nonexistent-sess")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.False(t, inner.wasCalled(), "inner handler should NOT be called for missing session")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_ExistingSession_Expired(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	// Create an already-expired session with no auth credentials
	sess := newTestSession("expired-sess", -time.Second)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "expired-sess")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.False(t, inner.wasCalled(), "inner handler should NOT be called for expired session without credentials")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_ExpiredSession_RecoveryWithBearer(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	// Create an already-expired session
	sess := newTestSession("expired-bearer", -time.Second)
	sess.UserID = hashToken("my-bearer-token")
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "expired-bearer")
	req.Header.Set(handlerTestAuthHeader, "Bearer my-bearer-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "inner handler should be called after session recovery")
	assert.Equal(t, http.StatusOK, w.Code)

	// Session should be revived with the SAME ID (not a new random one)
	assert.Equal(t, "expired-bearer", inner.getSessionID(),
		"revived session should keep the original ID")

	// Revived session should exist in store with correct owner
	revived, err := store.Get(ctx, "expired-bearer")
	require.NoError(t, err)
	assert.NotNil(t, revived, "revived session should exist in store")
	assert.Equal(t, hashToken("my-bearer-token"), revived.UserID)
}

func TestHandler_MissingSession_RecoveryWithAPIKey(t *testing.T) {
	handler, store, inner := newTestHandler()

	// No session created at all — completely missing
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "does-not-exist")
	req.Header.Set("X-API-Key", handlerTestAPIKey)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "inner handler should be called after session recovery")
	assert.Equal(t, http.StatusOK, w.Code)

	// Session should be revived with the SAME ID
	assert.Equal(t, "does-not-exist", inner.getSessionID(),
		"revived session should keep the original ID")

	revived, err := store.Get(context.Background(), "does-not-exist")
	require.NoError(t, err)
	assert.NotNil(t, revived, "revived session should exist in store")
	assert.Equal(t, hashToken(handlerTestAPIKey), revived.UserID)
}

func TestHandler_ExpiredSession_NoCredentials_Returns404(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	// Expired session with no credentials on the request
	sess := newTestSession("expired-anon", -time.Second)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "expired-anon")
	// No Authorization or X-API-Key headers
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.False(t, inner.wasCalled(), "inner handler should NOT be called without credentials")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_HijackPrevention_DifferentToken(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	// Create session owned by a specific token
	sess := newTestSession(handlerTestProtectedSess, handlerTestTTL)
	sess.UserID = hashToken("original-token")
	require.NoError(t, store.Create(ctx, sess))

	// Attempt to use the session with a different token
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, handlerTestProtectedSess)
	req.Header.Set(handlerTestAuthHeader, "Bearer different-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.False(t, inner.wasCalled(), "inner handler should NOT be called for token mismatch")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandler_HijackPrevention_SameToken(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	sess := newTestSession(handlerTestProtectedSess, handlerTestTTL)
	sess.UserID = hashToken("valid-token")
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, handlerTestProtectedSess)
	req.Header.Set(handlerTestAuthHeader, "Bearer valid-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "inner handler should be called for matching token")
}

func TestHandler_HijackPrevention_AnonymousSkipped(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	sess := newTestSession("anon-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "anon-sess")
	req.Header.Set(handlerTestAuthHeader, "Bearer any-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "anonymous sessions should skip ownership check")
}

func TestHandler_Delete(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	sess := newTestSession("delete-me", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "delete-me")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code, "successful termination reports 204")
	assert.False(t, inner.wasCalled(),
		"DELETE is answered by AwareHandler; the stateless inner handler rejects non-POST")

	got, err := store.Get(ctx, "delete-me")
	require.NoError(t, err)
	assert.Nil(t, got, "session should be deleted from store")
}

func TestHandler_Delete_NoSessionID(t *testing.T) {
	handler, _, inner := newTestHandler()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, handlerTestPath, http.NoBody)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "DELETE without a session ID is a bad request")
	assert.False(t, inner.wasCalled(), "DELETE without session ID must not reach inner")
}

// TestHandler_Delete_RealStatelessSDK pins the DELETE contract against the
// actual SDK handler AwareHandler is mounted over in database-session mode,
// not a stub. go-sdk v1.7.0 made stateless servers POST-only (SEP-2575), so a
// forwarded DELETE returns 405 with Allow: POST — this asserts the client
// still sees 204 for a termination that did in fact remove the stored session.
func TestHandler_Delete_RealStatelessSDK(t *testing.T) {
	ctx := context.Background()
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "session-test", Version: "0.0.1"}, nil)
	stateless := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)

	store := NewMemoryStore(handlerTestTTL)
	handler := NewAwareHandler(stateless, HandlerConfig{Store: store, TTL: handlerTestTTL})

	sess := newTestSession("real-sdk-delete", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequestWithContext(ctx, http.MethodDelete, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "real-sdk-delete")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Empty(t, w.Header().Get("Allow"), "no 405 Allow header should leak through")

	got, err := store.Get(ctx, "real-sdk-delete")
	require.NoError(t, err)
	assert.Nil(t, got, "session row is gone")
}

func TestSessionIDWriter_Flush(_ *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sessionIDWriter{
		ResponseWriter: rec,
		sessionID:      handlerTestSessID,
	}
	// Flush should not panic even if underlying writer supports it
	sw.Flush()
}

func TestSessionIDWriter_WriteSetsHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sessionIDWriter{
		ResponseWriter: rec,
		sessionID:      handlerTestSessID,
	}

	_, err := sw.Write([]byte("hello"))
	require.NoError(t, err)

	assert.Equal(t, handlerTestSessID, rec.Header().Get(sessionIDHeader))
}

func TestSessionIDWriter_WriteHeaderSetsHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &sessionIDWriter{
		ResponseWriter: rec,
		sessionID:      handlerTestSessID,
	}

	sw.WriteHeader(http.StatusOK)
	assert.Equal(t, handlerTestSessID, rec.Header().Get(sessionIDHeader))
}

func TestGenerateSessionID(t *testing.T) {
	id1, err := generateSessionID()
	require.NoError(t, err)
	assert.Len(t, id1, sessionIDBytes*2, "hex-encoded 16 bytes = 32 chars")

	id2, err := generateSessionID()
	require.NoError(t, err)
	assert.NotEqual(t, id1, id2, "IDs should be unique")
}

func TestExtractToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		apiKey string
		want   string
	}{
		{"bearer token", "Bearer my-token", "", "my-token"},
		{"api key", "", "api-key-123", "api-key-123"},
		{"bearer preferred over api key", "Bearer tok", "key", "tok"},
		{"no auth", "", "", ""},
		{"non-bearer auth", "Basic dXNlcjpwYXNz", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, handlerTestPath, http.NoBody)
			if tt.header != "" {
				req.Header.Set(handlerTestAuthHeader, tt.header)
			}
			if tt.apiKey != "" {
				req.Header.Set("X-API-Key", tt.apiKey)
			}
			got := extractToken(req)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHashToken(t *testing.T) {
	assert.Empty(t, hashToken(""), "empty token should return empty hash")
	h := hashToken("test")
	assert.Len(t, h, sha256HexLen, "SHA-256 hex should be 64 chars")
	assert.Equal(t, h, hashToken("test"), "same input should produce same hash")
	assert.NotEqual(t, h, hashToken("other"), "different input should produce different hash")
}

func TestAwareSessionID_EmptyContext(t *testing.T) {
	got := AwareSessionID(context.Background())
	assert.Empty(t, got, "plain context should return empty string")
}

func TestAwareSessionID_Roundtrip(t *testing.T) {
	ctx := WithAwareSessionID(context.Background(), "test-session-123")
	got := AwareSessionID(ctx)
	assert.Equal(t, "test-session-123", got)
}

// contextCapturingHandler captures the AwareSessionID from the request context.
type contextCapturingHandler struct {
	mu             sync.Mutex
	awareSessionID string
	capturedCalled bool
}

func (h *contextCapturingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.capturedCalled = true
	h.awareSessionID = AwareSessionID(r.Context())
	h.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func TestHandler_Initialize_SetsContextSessionID(t *testing.T) {
	store := NewMemoryStore(handlerTestTTL)
	capture := &contextCapturingHandler{}
	handler := NewAwareHandler(capture, HandlerConfig{
		Store: store,
		TTL:   handlerTestTTL,
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	assert.True(t, capture.capturedCalled, "inner handler should be called")
	assert.NotEmpty(t, capture.awareSessionID, "AwareSessionID should be set in context")

	// The context session ID should match the response header session ID
	responseSessionID := w.Header().Get(sessionIDHeader)
	assert.Equal(t, responseSessionID, capture.awareSessionID,
		"context session ID should match response header session ID")
}

func TestHandler_ExistingSession_SetsContextSessionID(t *testing.T) {
	store := NewMemoryStore(handlerTestTTL)
	capture := &contextCapturingHandler{}
	handler := NewAwareHandler(capture, HandlerConfig{
		Store: store,
		TTL:   handlerTestTTL,
	})

	// Pre-create a session
	sess := newTestSession("ctx-existing-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(context.Background(), sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "ctx-existing-sess")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	assert.True(t, capture.capturedCalled, "inner handler should be called")
	assert.Equal(t, "ctx-existing-sess", capture.awareSessionID,
		"AwareSessionID should be the existing session ID")
}

func TestHandler_ConcurrentAccess(t *testing.T) {
	handler, store, _ := newTestHandler()
	ctx := context.Background()

	// Pre-create a session
	sess := newTestSession("concurrent-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	var wg sync.WaitGroup
	for range handlerTestGoroutines {
		wg.Go(func() {
			for range 50 {
				req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
				req.Header.Set(sessionIDHeader, "concurrent-sess")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
			}
		})
	}
	wg.Wait()
}

func TestHandler_SessionRevive_KeepsSameID(t *testing.T) {
	store := NewMemoryStore(handlerTestTTL)
	capture := &contextCapturingHandler{}
	handler := NewAwareHandler(capture, HandlerConfig{
		Store: store,
		TTL:   handlerTestTTL,
	})

	// Create an expired session
	sess := newTestSession("old-session-for-revive", -time.Second)
	sess.UserID = hashToken("revive-token")
	require.NoError(t, store.Create(context.Background(), sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "old-session-for-revive")
	req.Header.Set(handlerTestAuthHeader, "Bearer revive-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	assert.True(t, capture.capturedCalled, "inner handler should be called")
	assert.Equal(t, "old-session-for-revive", capture.awareSessionID,
		"revived session should keep the original ID")
}

func TestHandler_SessionStability_AcrossMultipleRequests(t *testing.T) {
	store := NewMemoryStore(handlerTestTTL)
	capture := &contextCapturingHandler{}
	handler := NewAwareHandler(capture, HandlerConfig{
		Store: store,
		TTL:   handlerTestTTL,
	})

	const sessionID = "stable-session-id"
	const token = "stable-token"

	// Create an expired session
	sess := newTestSession(sessionID, -time.Second)
	sess.UserID = hashToken(token)
	require.NoError(t, store.Create(context.Background(), sess))

	// First request: triggers revive
	req1 := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req1.Header.Set(sessionIDHeader, sessionID)
	req1.Header.Set(handlerTestAuthHeader, "Bearer "+token)
	w1 := httptest.NewRecorder()

	handler.ServeHTTP(w1, req1)

	capture.mu.Lock()
	assert.Equal(t, sessionID, capture.awareSessionID,
		"first request should revive with same ID")
	capture.capturedCalled = false
	capture.mu.Unlock()

	// Second request: session now exists, should be handled normally (no revive)
	req2 := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req2.Header.Set(sessionIDHeader, sessionID)
	req2.Header.Set(handlerTestAuthHeader, "Bearer "+token)
	w2 := httptest.NewRecorder()

	handler.ServeHTTP(w2, req2)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	assert.True(t, capture.capturedCalled, "second request should be handled")
	assert.Equal(t, sessionID, capture.awareSessionID,
		"second request should use the same session ID")
}

func TestHandler_ReviveSession_UpdatesTimestamps(t *testing.T) {
	handler, store, _ := newTestHandler()
	ctx := context.Background()

	const sessionID = "revive-timestamps"
	const token = "ts-token"

	// Create an expired session
	oldSess := newTestSession(sessionID, -time.Second)
	oldSess.UserID = hashToken(token)
	require.NoError(t, store.Create(ctx, oldSess))

	before := time.Now()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, sessionID)
	req.Header.Set(handlerTestAuthHeader, "Bearer "+token)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	after := time.Now()

	// Verify the revived session has correct fields
	revived, err := store.Get(ctx, sessionID)
	require.NoError(t, err)
	require.NotNil(t, revived, "revived session should exist")

	assert.Equal(t, sessionID, revived.ID, "ID should be preserved")
	assert.Equal(t, hashToken(token), revived.UserID, "UserID should match token")
	assert.True(t, !revived.CreatedAt.Before(before) && !revived.CreatedAt.After(after),
		"CreatedAt should be recent")
	assert.True(t, !revived.ExpiresAt.Before(before.Add(handlerTestTTL)),
		"ExpiresAt should be TTL from now")
}

func TestHandler_HandleExisting_StoreGetError(t *testing.T) {
	es := &errStore{
		MemoryStore: NewMemoryStore(handlerTestTTL),
		getErr:      errors.New("db connection lost"),
	}
	inner := &testInnerHandler{}
	handler := NewAwareHandler(inner, HandlerConfig{Store: es, TTL: handlerTestTTL})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "any-session")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.False(t, inner.wasCalled(), "inner handler should not be called on store error")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandler_ReviveSession_CreateError(t *testing.T) {
	es := &errStore{
		MemoryStore: NewMemoryStore(handlerTestTTL),
		createErr:   errors.New("insert failed"),
	}
	inner := &testInnerHandler{}
	handler := NewAwareHandler(inner, HandlerConfig{Store: es, TTL: handlerTestTTL})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "revive-fail")
	req.Header.Set(handlerTestAuthHeader, "Bearer some-token")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.False(t, inner.wasCalled(), "inner handler should not be called when revive fails")
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// TestHandler_Delete_StoreError exercises the delete-failure branch: a
// store Delete error is logged (session ID sanitized via logsan) but is
// non-fatal, so the client still sees 204. Termination is best effort — the
// row expires at TTL regardless — and this is the status the request received
// before the handler stopped forwarding to the SDK.
func TestHandler_Delete_StoreError(t *testing.T) {
	es := &errStore{
		MemoryStore: NewMemoryStore(handlerTestTTL),
		deleteErr:   errors.New("delete failed"),
	}
	inner := &testInnerHandler{}
	handler := NewAwareHandler(inner, HandlerConfig{Store: es, TTL: handlerTestTTL})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "delete-fail")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code, "store failure is non-fatal for termination")
	assert.False(t, inner.wasCalled(), "DELETE is never forwarded to the stateless inner handler")
}

// flushRecorder is httptest.ResponseRecorder + http.Flusher so the SSE
// branch's flusher type assertion succeeds in tests.
type flushRecorder struct{ *httptest.ResponseRecorder }

func (flushRecorder) Flush() {}

func newSSEHandler(t *testing.T) (*AwareHandler, *MemoryStore, *MemoryBroadcaster) {
	t.Helper()
	store := NewMemoryStore(handlerTestTTL)
	b := NewMemoryBroadcaster(nil)
	t.Cleanup(func() { _ = b.Close() })
	t.Cleanup(func() { _ = store.Close() })
	h := NewAwareHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Inner handler must NOT be called for valid SSE GET.
		w.WriteHeader(http.StatusTeapot)
	}), HandlerConfig{Store: store, TTL: handlerTestTTL, Broadcaster: b})
	return h, store, b
}

func TestHandler_SSE_DeliversBroadcastEvent(t *testing.T) {
	handler, store, broker := newSSEHandler(t)
	ctx := context.Background()

	sess := newTestSession("sse-sess", handlerTestTTL)
	sess.UserID = "" // anonymous session — no ownership check
	require.NoError(t, store.Create(ctx, sess))

	srv := httptest.NewServer(handler)
	defer srv.Close()

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, srv.URL+"/", http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(sessionIDHeader, "sse-sess")

	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
	require.Equal(t, "sse-sess", resp.Header.Get(sessionIDHeader))

	// Subscriptions register inside handleSSE before the for-loop;
	// poll until the broadcaster sees us.
	deadline := time.After(time.Second)
	for broker.SubscriberCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("subscription did not register")
		case <-time.After(5 * time.Millisecond):
		}
	}

	require.NoError(t, broker.Publish(ctx, Event{Method: "notifications/tools/list_changed"}))

	got := readSSEUntil(t, resp.Body, `"method":"notifications/tools/list_changed"`, time.Second)
	assert.Contains(t, got, ": connected", "expected initial heartbeat")
	assert.Contains(t, got, `"jsonrpc":"2.0"`)
}

// readSSEUntil reads from r until the buffered output contains the
// needle or the deadline is reached. Returns whatever was buffered.
//
// Reads run in a goroutine so the timeout actually fires even when
// the underlying io.Reader is blocked waiting for the next byte —
// without the goroutine, a missed publish could leave the test
// hanging until the package-level test timeout (default 10 m). The
// outer goroutine (this function) owns `got` exclusively; the inner
// goroutine only writes to `ch`.
func readSSEUntil(t *testing.T, r interface{ Read([]byte) (int, error) }, needle string, timeout time.Duration) string {
	t.Helper()
	type readResult struct {
		n   int
		err error
	}
	var got strings.Builder
	deadline := time.After(timeout)
	for {
		buf := make([]byte, 256)
		ch := make(chan readResult, 1)
		go func() { n, err := r.Read(buf); ch <- readResult{n, err} }()
		select {
		case <-deadline:
			t.Fatalf("did not see %q in SSE stream within %v; got=%q", needle, timeout, got.String())
			return got.String()
		case res := <-ch:
			if res.n > 0 {
				got.Write(buf[:res.n])
				if strings.Contains(got.String(), needle) {
					return got.String()
				}
			}
			if res.err != nil {
				t.Fatalf("read error before seeing %q in SSE stream: %v; got=%q", needle, res.err, got.String())
				return got.String()
			}
		}
	}
}

func TestHandler_SSE_Returns404ForUnknownSession(t *testing.T) {
	handler, _, _ := newSSEHandler(t)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, handlerTestPath, http.NoBody)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(sessionIDHeader, "ghost-session")
	rec := httptest.NewRecorder()
	w := flushRecorder{rec}

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestHandler_SSE_RevivesExpiredSessionWithToken(t *testing.T) {
	handler, store, broker := newSSEHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, handlerTestPath, http.NoBody)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(sessionIDHeader, "revived-session")
	req.Header.Set(handlerTestAuthHeader, "Bearer the-token")
	rec := httptest.NewRecorder()
	w := flushRecorder{rec}

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	// Subscription only registers after a successful revive.
	deadline := time.After(time.Second)
	for broker.SubscriberCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("subscription not registered after revive")
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Verify the session was actually persisted with the token-derived owner.
	sess, err := store.Get(context.Background(), "revived-session")
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, hashToken("the-token"), sess.UserID)

	cancel()
	<-done
}

func TestHandler_SSE_RejectsForeignSession(t *testing.T) {
	handler, store, _ := newSSEHandler(t)
	ctx := context.Background()

	owned := newTestSession("owned", handlerTestTTL)
	owned.UserID = hashToken("owner-token")
	require.NoError(t, store.Create(ctx, owned))

	req := httptest.NewRequestWithContext(ctx, http.MethodGet, handlerTestPath, http.NoBody)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(sessionIDHeader, "owned")
	req.Header.Set(handlerTestAuthHeader, "Bearer attacker-token")
	rec := httptest.NewRecorder()
	w := flushRecorder{rec}

	handler.ServeHTTP(w, req)

	// validateOwnership returns false → handler emits 403, matching the
	// POST path's StatusForbidden in handleExisting. Both surfaces
	// must agree on the same store invariant: divergent status codes
	// would let a probing caller infer different facts about the
	// session via GET vs POST.
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestHandler_SSE_FallsThroughWhenNoBroadcaster(t *testing.T) {
	store := NewMemoryStore(handlerTestTTL)
	t.Cleanup(func() { _ = store.Close() })

	innerCalled := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		innerCalled = true
		w.WriteHeader(http.StatusOK)
	})
	handler := NewAwareHandler(inner, HandlerConfig{Store: store, TTL: handlerTestTTL})

	sess := newTestSession("plain-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(context.Background(), sess))

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, handlerTestPath, http.NoBody)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(sessionIDHeader, "plain-sess")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.True(t, innerCalled, "without broadcaster, GET must fall through to inner")
}

func TestWriteSSEEvent_FormatIsJSONRPC2(t *testing.T) {
	rec := httptest.NewRecorder()
	err := writeSSEEvent(rec, Event{Method: "notifications/tools/list_changed", Params: map[string]any{"k": "v"}})
	require.NoError(t, err)

	body := rec.Body.String()
	assert.Contains(t, body, `"jsonrpc":"2.0"`)
	assert.Contains(t, body, `"method":"notifications/tools/list_changed"`)
	assert.Contains(t, body, `"params":{"k":"v"}`)
	// SSE frame must end with double newline per WHATWG HTML §server-sent events.
	assert.True(t, strings.HasSuffix(body, "\n\n"), "expected SSE double-newline terminator, got %q", body)
}

// signalLogHandler is a slog.Handler that closes a channel the first
// time a log record whose message contains a needle is emitted. It lets
// a test wait deterministically for a specific (possibly asynchronous)
// log line to execute rather than sleeping. Records are discarded after
// inspection; Enabled always returns true so debug-level sites fire.
type signalLogHandler struct {
	needle string
	fire   func()
}

func (*signalLogHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *signalLogHandler) Handle(_ context.Context, r slog.Record) error {
	if strings.Contains(r.Message, h.needle) {
		h.fire()
	}
	return nil
}

func (h *signalLogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *signalLogHandler) WithGroup(string) slog.Handler { return h }

// newLogSignal installs a default slog logger that closes the returned
// channel the first time a record's message contains needle. The
// previous default logger is restored via t.Cleanup. This gives tests a
// happens-after signal tied to the exact log site under test, so
// coverage of that line is deterministic rather than racy.
func newLogSignal(t *testing.T, needle string) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{})
	var once sync.Once
	prev := slog.Default()
	slog.SetDefault(slog.New(&signalLogHandler{
		needle: needle,
		fire:   func() { once.Do(func() { close(ch) }) },
	}))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return ch
}

// failingSSEWriter is an http.Flusher ResponseWriter whose Write fails
// for SSE data frames (payloads containing "data:") while letting the
// comment-frame writes (": connected", ": keepalive") succeed. This
// drives the SSE write-failure branch without disturbing stream setup.
type failingSSEWriter struct {
	*httptest.ResponseRecorder
}

func (w *failingSSEWriter) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "data:") {
		return 0, errors.New("connection reset by peer")
	}
	n, err := w.ResponseRecorder.Write(b)
	if err != nil {
		return n, fmt.Errorf("recorder write: %w", err)
	}
	return n, nil
}

func (failingSSEWriter) Flush() {}

// TestHandler_HandleExisting_AsyncTouchError covers handler.go:220 — the
// detached touch goroutine logs "session: touch failed" when the store's
// Touch returns an error. The request still succeeds (the touch is
// best-effort), so the assertion waits on the log signal to prove the
// async branch executed before the test returns.
func TestHandler_HandleExisting_AsyncTouchError(t *testing.T) {
	touchLogged := newLogSignal(t, "session: touch failed")

	es := &errStore{
		MemoryStore: NewMemoryStore(handlerTestTTL),
		touchErr:    errors.New("touch failed"),
	}
	ctx := context.Background()
	sess := newTestSession("touch-sess", handlerTestTTL)
	sess.UserID = "" // anonymous — ownership check passes, reaches touch goroutine
	require.NoError(t, es.Create(ctx, sess))

	inner := &testInnerHandler{}
	handler := NewAwareHandler(inner, HandlerConfig{Store: es, TTL: handlerTestTTL})

	req := httptest.NewRequestWithContext(ctx, http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "touch-sess")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "request should still be forwarded despite touch failure")

	select {
	case <-touchLogged:
	case <-time.After(2 * time.Second):
		t.Fatal("async touch goroutine did not log the touch failure")
	}
}

// TestHandler_SSE_HeartbeatTouchError covers handler.go:329 — on the
// heartbeat tick the SSE loop touches the store; a Touch error logs
// "session: SSE touch failed". The package heartbeat interval is
// shortened so the tick fires deterministically within the test.
func TestHandler_SSE_HeartbeatTouchError(t *testing.T) {
	orig := sseHeartbeatInterval
	sseHeartbeatInterval = 5 * time.Millisecond
	defer func() { sseHeartbeatInterval = orig }()

	touchLogged := newLogSignal(t, "session: SSE touch failed")

	es := &errStore{
		MemoryStore: NewMemoryStore(handlerTestTTL),
		touchErr:    errors.New("touch failed"),
	}
	b := NewMemoryBroadcaster(nil)
	t.Cleanup(func() { _ = b.Close() })
	handler := NewAwareHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot) // inner must not be reached for a valid SSE GET
	}), HandlerConfig{Store: es, TTL: handlerTestTTL, Broadcaster: b})

	sess := newTestSession("hb-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, es.Create(context.Background(), sess))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, handlerTestPath, http.NoBody)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(sessionIDHeader, "hb-sess")
	w := &failingSSEWriter{ResponseRecorder: httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	select {
	case <-touchLogged:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE heartbeat touch-failure log not observed")
	}

	cancel()
	<-done
}

// TestHandler_SSE_WriteError covers handler.go:339 — when writing a
// broadcast event to the client fails, the SSE loop logs "session: SSE
// write failed" and returns. A failing ResponseWriter forces the write
// error while a published event drives the event branch.
func TestHandler_SSE_WriteError(t *testing.T) {
	writeLogged := newLogSignal(t, "session: SSE write failed")

	store := NewMemoryStore(handlerTestTTL)
	t.Cleanup(func() { _ = store.Close() })
	b := NewMemoryBroadcaster(nil)
	t.Cleanup(func() { _ = b.Close() })
	handler := NewAwareHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}), HandlerConfig{Store: store, TTL: handlerTestTTL, Broadcaster: b})

	sess := newTestSession("write-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(context.Background(), sess))

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, handlerTestPath, http.NoBody)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(sessionIDHeader, "write-sess")
	w := &failingSSEWriter{ResponseRecorder: httptest.NewRecorder()}

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(w, req)
		close(done)
	}()

	// Wait for the SSE handler to register its subscription before publishing.
	deadline := time.After(time.Second)
	for b.SubscriberCount() == 0 {
		select {
		case <-deadline:
			t.Fatal("SSE subscription did not register")
		case <-time.After(5 * time.Millisecond):
		}
	}

	require.NoError(t, b.Publish(context.Background(), Event{Method: "notifications/tools/list_changed"}))

	select {
	case <-writeLogged:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE write-failure log not observed")
	}

	// The loop returns immediately after the write failure (handler.go:341).
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SSE handler did not return after write failure")
	}
}

func TestAcceptsSSE(t *testing.T) {
	cases := []struct {
		header string
		want   bool
	}{
		{"", false},
		{"application/json", false},
		{"text/event-stream", true},
		{"application/json, text/event-stream", true},
		{"text/event-stream; charset=utf-8", true},
		// RFC 7231 §3.1.1.1: media types are case-insensitive. Strict
		// proxies that title-case media types must not silently bypass
		// the SSE branch.
		{"Text/Event-Stream", true},
		{"TEXT/EVENT-STREAM", true},
	}
	for _, tc := range cases {
		t.Run(tc.header, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			if tc.header != "" {
				r.Header.Set("Accept", tc.header)
			}
			assert.Equal(t, tc.want, acceptsSSE(r))
		})
	}
}
