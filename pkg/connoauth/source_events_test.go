package connoauth

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/authevents"
)

func TestClassifyRevokedReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		want string
	}{
		{errRefreshTokenRevoked, "invalid_grant"},
		{errNoRefreshToken, "no_refresh_token"},
		{errRefreshExpired, "refresh_expired"},
		{errors.New("other"), "revoked"},
	}
	for _, tc := range cases {
		got := classifyRevokedReason(tc.err)
		if got != tc.want {
			t.Errorf("classifyRevokedReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// TestHandleRevokedEmitsHonestLeadEvent — each revocation cause must
// produce a lead event whose type accurately describes how the
// verdict was reached. The IdP-rejected case stays on
// TypeRefreshFailedRevoked. The locally-decided cases (deadline
// reached, no refresh token) emit TypeRefreshSkippedExpired /
// TypeRefreshSkippedNoToken so the History panel does not falsely
// attribute the decision to the upstream IdP. All three cases share
// the trailing TypeTokenDeletedRevoked — the credential is gone
// regardless of how the verdict was reached.
func TestHandleRevokedEmitsHonestLeadEvent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		cause     error
		wantLead  authevents.Type
		wantTrail authevents.Type
	}{
		{
			name:      "invalid_grant from IdP",
			cause:     errRefreshTokenRevoked,
			wantLead:  authevents.TypeRefreshFailedRevoked,
			wantTrail: authevents.TypeTokenDeletedRevoked,
		},
		{
			name:      "local deadline reached",
			cause:     errRefreshExpired,
			wantLead:  authevents.TypeRefreshSkippedExpired,
			wantTrail: authevents.TypeTokenDeletedRevoked,
		},
		{
			name:      "no refresh token stored",
			cause:     errNoRefreshToken,
			wantLead:  authevents.TypeRefreshSkippedNoToken,
			wantTrail: authevents.TypeTokenDeletedRevoked,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tokenStore := NewMemoryStore()
			eventStore := authevents.NewMemoryStore()
			writer := authevents.NewWriter(eventStore, nil)
			key := Key{Kind: KindMCP, Name: "alpha"}
			persisted := PersistedToken{
				Key: key, AccessToken: "at", RefreshToken: "rt",
			}
			if err := tokenStore.Set(context.Background(), persisted); err != nil {
				t.Fatalf("Set: %v", err)
			}
			src := NewSource(tokenStore, key, Config{TokenURL: "https://idp/token"}).
				WithEvents(writer).
				WithActor(authevents.SystemBackgroundRefresh)
			src.handleRevoked(context.Background(), &persisted, tc.cause)

			if _, err := tokenStore.Get(context.Background(), key); !errors.Is(err, ErrTokenNotFound) {
				t.Errorf("token row should be gone, got %v", err)
			}
			got, _ := eventStore.List(context.Background(),
				authevents.Filter{Kind: key.Kind, Name: key.Name, Limit: 10})
			if len(got) != 2 {
				t.Fatalf("len(got) = %d, want 2: %+v", len(got), got)
			}
			// Newest first.
			if got[0].Type != tc.wantTrail {
				t.Errorf("got[0].Type = %v, want %v (trail)", got[0].Type, tc.wantTrail)
			}
			if got[1].Type != tc.wantLead {
				t.Errorf("got[1].Type = %v, want %v (lead)", got[1].Type, tc.wantLead)
			}
			if got[0].Actor != authevents.SystemBackgroundRefresh {
				t.Errorf("actor = %q, want %q", got[0].Actor, authevents.SystemBackgroundRefresh)
			}
		})
	}
}

// TestRefreshSkippedNoTokenSilent: when refresh() encounters an empty
// RefreshToken, it returns errNoRefreshToken WITHOUT emitting its own
// event — the caller's handleRevoked path emits the
// TypeRefreshSkippedNoToken lead and the TypeTokenDeletedRevoked
// trail. refresh() emitting on its own would produce a third row in
// the History panel for a single incident.
func TestRefreshSkippedNoTokenSilent(t *testing.T) {
	t.Parallel()
	tokenStore := NewMemoryStore()
	eventStore := authevents.NewMemoryStore()
	writer := authevents.NewWriter(eventStore, nil)
	key := Key{Kind: KindMCP, Name: "beta"}
	src := NewSource(tokenStore, key, Config{TokenURL: "https://idp/token"}).WithEvents(writer)
	if _, err := src.refresh(context.Background(), &PersistedToken{Key: key}); !errors.Is(err, errNoRefreshToken) {
		t.Fatalf("expected errNoRefreshToken, got %v", err)
	}
	got, _ := eventStore.List(context.Background(), authevents.Filter{
		Kind: key.Kind, Name: key.Name, Limit: 5,
	})
	if len(got) != 0 {
		t.Errorf("refresh() should not emit any event for empty RefreshToken; got %+v", got)
	}
}

func TestSourceWithActorAcceptsEmpty(t *testing.T) {
	t.Parallel()
	src := NewSource(NewMemoryStore(), Key{Kind: KindMCP, Name: "x"}, Config{})
	if src.actor != authevents.SystemToolCall {
		t.Fatalf("default actor = %q, want %q", src.actor, authevents.SystemToolCall)
	}
	src.WithActor("")
	if src.actor != authevents.SystemToolCall {
		t.Fatalf("WithActor(empty) should not clobber default; got %q", src.actor)
	}
	src.WithActor("custom")
	if src.actor != "custom" {
		t.Fatalf("WithActor(custom) failed; got %q", src.actor)
	}
}
