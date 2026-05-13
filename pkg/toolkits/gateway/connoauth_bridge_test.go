package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/connoauth"
)

// fakeConnOAuthStore is a tiny in-memory connoauth.Store that lets the
// bridge tests assert exactly which Key the bridge uses on each call.
// The real connoauth.MemoryStore would work too, but capturing the
// last-used key lets us prove the bridge always passes
// (Kind: "mcp", Name: connection) rather than something else.
type fakeConnOAuthStore struct {
	getKey, setKey, delKey connoauth.Key
	getErr                 error
	setErr                 error
	delErr                 error
	row                    *connoauth.PersistedToken
	setRow                 connoauth.PersistedToken
}

func (f *fakeConnOAuthStore) Get(_ context.Context, key connoauth.Key) (*connoauth.PersistedToken, error) {
	f.getKey = key
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.row, nil
}

func (f *fakeConnOAuthStore) Set(_ context.Context, t connoauth.PersistedToken) error {
	f.setKey = t.Key
	f.setRow = t
	return f.setErr
}

func (f *fakeConnOAuthStore) Delete(_ context.Context, key connoauth.Key) error {
	f.delKey = key
	return f.delErr
}

func (*fakeConnOAuthStore) List(_ context.Context) ([]connoauth.PersistedToken, error) {
	return nil, nil
}

func TestConnOAuthBridge_Get_MapsKindAndFields(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC().Truncate(time.Second)
	store := &fakeConnOAuthStore{
		row: &connoauth.PersistedToken{
			Key:              connoauth.Key{Kind: connoauth.KindMCP, Name: "vendor"},
			AccessToken:      "at",
			RefreshToken:     "rt",
			ExpiresAt:        now.Add(time.Hour),
			RefreshExpiresAt: now.Add(24 * time.Hour),
			Scope:            "openid offline_access",
			AuthenticatedBy:  "user@example.com",
			AuthenticatedAt:  now,
			UpdatedAt:        now,
		},
	}
	bridge := NewConnOAuthTokenStore(store)
	got, err := bridge.Get(context.Background(), "vendor")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if store.getKey.Kind != connoauth.KindMCP || store.getKey.Name != "vendor" {
		t.Fatalf("bridge must call Get with Kind=mcp Name=vendor, got %+v", store.getKey)
	}
	if got.AccessToken != "at" || got.RefreshToken != "rt" {
		t.Fatalf("token fields not mapped: %+v", got)
	}
	if got.ConnectionName != "vendor" {
		t.Fatalf("ConnectionName not mapped: %q", got.ConnectionName)
	}
	if !got.ExpiresAt.Equal(now.Add(time.Hour)) || !got.RefreshExpiresAt.Equal(now.Add(24*time.Hour)) {
		t.Fatalf("expiry fields not mapped: %+v", got)
	}
	if got.AuthenticatedBy != "user@example.com" {
		t.Fatalf("authenticated_by not mapped: %q", got.AuthenticatedBy)
	}
}

func TestConnOAuthBridge_Get_NotFoundMapsToToolkitSentinel(t *testing.T) {
	t.Parallel()
	store := &fakeConnOAuthStore{getErr: connoauth.ErrTokenNotFound}
	bridge := NewConnOAuthTokenStore(store)
	_, err := bridge.Get(context.Background(), "missing")
	if !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("expected ErrTokenNotFound (toolkit sentinel), got %v", err)
	}
}

func TestConnOAuthBridge_Get_OtherErrorWraps(t *testing.T) {
	t.Parallel()
	store := &fakeConnOAuthStore{getErr: errors.New("db down")}
	bridge := NewConnOAuthTokenStore(store)
	_, err := bridge.Get(context.Background(), "x")
	if err == nil || errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("non-NotFound errors must surface as wrapped, got %v", err)
	}
}

func TestConnOAuthBridge_Set_RoutesToMCPKind(t *testing.T) {
	t.Parallel()
	store := &fakeConnOAuthStore{}
	bridge := NewConnOAuthTokenStore(store)
	now := time.Now().UTC()
	err := bridge.Set(context.Background(), PersistedToken{
		ConnectionName:   "vendor",
		AccessToken:      "at",
		RefreshToken:     "rt",
		ExpiresAt:        now.Add(time.Hour),
		RefreshExpiresAt: now.Add(24 * time.Hour),
		Scope:            "openid",
		AuthenticatedBy:  "u@example.com",
		AuthenticatedAt:  now,
	})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if store.setKey.Kind != connoauth.KindMCP || store.setKey.Name != "vendor" {
		t.Fatalf("Set must route to kind=mcp, got %+v", store.setKey)
	}
	if store.setRow.AccessToken != "at" || store.setRow.RefreshToken != "rt" {
		t.Fatalf("token fields not propagated: %+v", store.setRow)
	}
	if !store.setRow.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("ExpiresAt not propagated")
	}
}

func TestConnOAuthBridge_Set_ErrorWraps(t *testing.T) {
	t.Parallel()
	store := &fakeConnOAuthStore{setErr: errors.New("encrypt failed")}
	bridge := NewConnOAuthTokenStore(store)
	err := bridge.Set(context.Background(), PersistedToken{ConnectionName: "x", AccessToken: "y"})
	if err == nil {
		t.Fatal("expected error to surface")
	}
}

func TestConnOAuthBridge_Delete_RoutesToMCPKind(t *testing.T) {
	t.Parallel()
	store := &fakeConnOAuthStore{}
	bridge := NewConnOAuthTokenStore(store)
	if err := bridge.Delete(context.Background(), "vendor"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if store.delKey.Kind != connoauth.KindMCP || store.delKey.Name != "vendor" {
		t.Fatalf("Delete must route to kind=mcp, got %+v", store.delKey)
	}
}

func TestConnOAuthBridge_Delete_ErrorWraps(t *testing.T) {
	t.Parallel()
	store := &fakeConnOAuthStore{delErr: errors.New("db gone")}
	bridge := NewConnOAuthTokenStore(store)
	if err := bridge.Delete(context.Background(), "x"); err == nil {
		t.Fatal("expected error to surface")
	}
}
