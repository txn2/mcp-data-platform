package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
	"github.com/txn2/mcp-data-platform/pkg/connoauth"
	"github.com/txn2/mcp-data-platform/pkg/platform"
)

// TestDeleteConnectionInstanceWipesTokenAndEmitsAdminEvent exercises
// the delete path with an existing token row: the row must be wiped
// AND a token_deleted_admin event must be emitted. This is the case
// the original silent-delete bug would have broken.
func TestDeleteConnectionInstanceWipesTokenAndEmitsAdminEvent(t *testing.T) {
	t.Parallel()
	connStore := &mockConnectionStore{}
	tokenStore := connoauth.NewMemoryStore()
	eventStore := authevents.NewMemoryStore()
	writer := authevents.NewWriter(eventStore, nil)
	// Pre-seed a token row so the handler's hadToken branch fires.
	key := connoauth.Key{Kind: "mcp", Name: "alpha"}
	_ = tokenStore.Set(context.Background(), connoauth.PersistedToken{
		Key: key, AccessToken: "at", RefreshToken: "rt",
	})
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: connStore,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ConnOAuthStore:  tokenStore,
		AuthEvents:      writer,
		AuthEventStore:  eventStore,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/admin/connection-instances/mcp/alpha", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	// Token row gone.
	if _, err := tokenStore.Get(context.Background(), key); err == nil {
		t.Error("token row should have been deleted")
	}
	// token_deleted_admin event present.
	events, _ := eventStore.List(context.Background(), authevents.Filter{
		Kind: "mcp", Name: "alpha", Limit: 10,
	})
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != authevents.TypeTokenDeletedAdmin {
		t.Errorf("event type = %q, want token_deleted_admin", events[0].Type)
	}
}

// TestDeleteConnectionInstanceSkipsTokenWipeWhenNoRow exercises the
// inverse branch: a connection with no token row should NOT emit a
// token_deleted_admin event (no token existed to be deleted).
func TestDeleteConnectionInstanceSkipsTokenWipeWhenNoRow(t *testing.T) {
	t.Parallel()
	connStore := &mockConnectionStore{}
	tokenStore := connoauth.NewMemoryStore()
	eventStore := authevents.NewMemoryStore()
	writer := authevents.NewWriter(eventStore, nil)
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: connStore,
		ConfigStore:     &mockConfigStore{mode: "database"},
		ConnOAuthStore:  tokenStore,
		AuthEvents:      writer,
		AuthEventStore:  eventStore,
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/admin/connection-instances/mcp/empty", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	events, _ := eventStore.List(context.Background(), authevents.Filter{
		Kind: "mcp", Name: "empty", Limit: 10,
	})
	if len(events) != 0 {
		t.Errorf("no events should fire when no token row existed; got %d", len(events))
	}
}

// TestDeleteConnectionInstanceNoConnOAuthStore exercises the
// hadToken=false path when no token store is configured at all (the
// dev-without-DB shape). Coverage line.
func TestDeleteConnectionInstanceNoConnOAuthStore(t *testing.T) {
	t.Parallel()
	connStore := &mockConnectionStore{}
	h := NewHandler(Deps{
		Config:          testConfig(),
		ConnectionStore: connStore,
		ConfigStore:     &mockConfigStore{mode: "database"},
	}, nil)
	// Defensive: ensure no nil-pointer panic if the path runs without
	// a ConnOAuthStore wired. ConnectionInstance lookups go through
	// ConnectionStore which is the only required dep.
	_ = platform.ConnectionInstance{Kind: "mcp", Name: "x"}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodDelete,
		"/api/v1/admin/connection-instances/mcp/x", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}
