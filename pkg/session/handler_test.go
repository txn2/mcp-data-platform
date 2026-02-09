package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	handlerTestTTL           = 30 * time.Minute
	handlerTestGoroutines    = 10
	handlerTestPath          = "/"
	handlerTestProtectedSess = "protected-sess"
	handlerTestAuthHeader    = "Authorization"
	handlerTestSessID        = "test-sess"
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

	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
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

	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
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

	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set("X-API-Key", "my-api-key")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	sessionID := w.Header().Get(sessionIDHeader)
	require.NotEmpty(t, sessionID)

	sess, err := store.Get(context.Background(), sessionID)
	require.NoError(t, err)
	require.NotNil(t, sess)
	assert.Equal(t, hashToken("my-api-key"), sess.UserID)
}

func TestHandler_ExistingSession_Valid(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	// Pre-create a session
	sess := newTestSession("existing-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "existing-sess")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "inner handler should be called for valid session")
}

func TestHandler_ExistingSession_NotFound(t *testing.T) {
	handler, _, inner := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "nonexistent-sess")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.False(t, inner.wasCalled(), "inner handler should NOT be called for missing session")
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandler_ExistingSession_Expired(t *testing.T) {
	handler, store, inner := newTestHandler()
	ctx := context.Background()

	// Create an already-expired session
	sess := newTestSession("expired-sess", -time.Second)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "expired-sess")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.False(t, inner.wasCalled(), "inner handler should NOT be called for expired session")
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
	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
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

	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
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

	req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
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

	req := httptest.NewRequest(http.MethodDelete, handlerTestPath, http.NoBody)
	req.Header.Set(sessionIDHeader, "delete-me")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "inner handler should be called for DELETE")

	got, err := store.Get(ctx, "delete-me")
	require.NoError(t, err)
	assert.Nil(t, got, "session should be deleted from store")
}

func TestHandler_Delete_NoSessionID(t *testing.T) {
	handler, _, inner := newTestHandler()

	req := httptest.NewRequest(http.MethodDelete, handlerTestPath, http.NoBody)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.True(t, inner.wasCalled(), "DELETE without session ID should still forward to inner")
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
			req := httptest.NewRequest(http.MethodGet, handlerTestPath, http.NoBody)
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

func TestHandler_ConcurrentAccess(t *testing.T) {
	handler, store, _ := newTestHandler()
	ctx := context.Background()

	// Pre-create a session
	sess := newTestSession("concurrent-sess", handlerTestTTL)
	sess.UserID = ""
	require.NoError(t, store.Create(ctx, sess))

	var wg sync.WaitGroup
	for range handlerTestGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				req := httptest.NewRequest(http.MethodPost, handlerTestPath, http.NoBody)
				req.Header.Set(sessionIDHeader, "concurrent-sess")
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, req)
			}
		}()
	}
	wg.Wait()
}
