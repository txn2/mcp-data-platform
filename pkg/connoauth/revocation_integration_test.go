package connoauth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

// capturedSink records what the platform was told about a revocation.
type capturedSink struct {
	mu   sync.Mutex
	seen []authevents.Revocation
}

func (s *capturedSink) Revoked(_ context.Context, rev authevents.Revocation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, rev)
}

func (s *capturedSink) all() []authevents.Revocation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]authevents.Revocation(nil), s.seen...)
}

// TestSource_RevocationReachesTheSink_Integration is the cross-component
// criterion for #1694: a real refresh rejected by a real HTTP token endpoint,
// through the assembled Source and its auth-event writer, arrives at the sink
// carrying the identity that authorized the connection.
//
// A unit test on the sink proves only that the sink works. What this proves is
// that the value it needs actually reaches it: AuthenticatedBy is read off a
// row the same call then deletes, so a mistake in that ordering would leave
// every alert addressed to nobody while every other gate stayed green.
func TestSource_RevocationReachesTheSink_Integration(t *testing.T) {
	t.Parallel()
	idp := newFakeIDP(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Refresh token revoked"}`))
	})
	store := NewMemoryStore()
	key := Key{Kind: KindAPI, Name: "billing"}
	if err := store.Set(context.Background(), PersistedToken{
		Key:             key,
		AccessToken:     "at-old",
		RefreshToken:    "rt-dead",
		ExpiresAt:       time.Now().Add(-time.Minute),
		AuthenticatedBy: "ops@example.com",
		AuthenticatedAt: time.Now().Add(-24 * time.Hour),
	}); err != nil {
		t.Fatalf("seeding the persisted token: %v", err)
	}

	sink := &capturedSink{}
	src := NewSource(store, key, Config{
		TokenURL: idp.tokenURL(), ClientID: "c", ClientSecret: "s",
	}).WithEvents(authevents.NewWriter(authevents.NewMemoryStore(), nil).WithRevocations(sink))

	if _, err := src.Token(context.Background()); !errors.Is(err, ErrNeedsReauth) {
		t.Fatalf("expected ErrNeedsReauth, got %v", err)
	}

	announced := sink.all()
	if len(announced) != 1 {
		t.Fatalf("expected exactly one announcement, got %d", len(announced))
	}
	got := announced[0]
	if got.AuthorizedBy != "ops@example.com" {
		t.Errorf("AuthorizedBy = %q; the alert has nobody to address without it", got.AuthorizedBy)
	}
	if got.Kind != KindAPI || got.Name != "billing" {
		t.Errorf("announced %s/%s; want api/billing", got.Kind, got.Name)
	}
	if got.Reason != "invalid_grant" {
		t.Errorf("Reason = %q; want the RFC 6749 code the upstream returned", got.Reason)
	}
	if got.IDPHost == "" {
		t.Error("the announcement names no upstream")
	}
}

// TestSource_LocalVerdictsAlsoAnnounce covers the two causes that never call
// the upstream. Both end with the credential discarded and the connection
// needing reauthorization, which is the thing nobody was being told.
func TestSource_LocalVerdictsAlsoAnnounce(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		token      PersistedToken
		wantReason string
	}{
		{
			name: "nothing to refresh with",
			token: PersistedToken{
				AccessToken: "at-old", ExpiresAt: time.Now().Add(-time.Minute),
				AuthenticatedBy: "ops@example.com",
			},
			wantReason: "no_refresh_token",
		},
		{
			name: "the refresh deadline passed",
			token: PersistedToken{
				AccessToken: "at-old", RefreshToken: "rt",
				ExpiresAt:        time.Now().Add(-time.Minute),
				RefreshExpiresAt: time.Now().Add(-time.Hour),
				AuthenticatedBy:  "ops@example.com",
			},
			wantReason: "refresh_expired",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			store := NewMemoryStore()
			key := Key{Kind: KindMCP, Name: "vendor"}
			tt.token.Key = key
			if err := store.Set(context.Background(), tt.token); err != nil {
				t.Fatalf("seeding the persisted token: %v", err)
			}
			sink := &capturedSink{}
			src := NewSource(store, key, Config{
				TokenURL: "https://idp.example.com/token", ClientID: "c", ClientSecret: "s",
			}).WithEvents(authevents.NewWriter(authevents.NewMemoryStore(), nil).WithRevocations(sink))

			if _, err := src.Token(context.Background()); !errors.Is(err, ErrNeedsReauth) {
				t.Fatalf("expected ErrNeedsReauth, got %v", err)
			}
			announced := sink.all()
			if len(announced) != 1 {
				t.Fatalf("expected one announcement, got %d", len(announced))
			}
			if announced[0].Reason != tt.wantReason {
				t.Errorf("Reason = %q; want %q", announced[0].Reason, tt.wantReason)
			}
			if announced[0].AuthorizedBy != "ops@example.com" {
				t.Errorf("AuthorizedBy = %q", announced[0].AuthorizedBy)
			}
		})
	}
}
