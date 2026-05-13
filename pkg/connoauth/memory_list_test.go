package connoauth

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStoreListReturnsRowsWithoutSecrets(t *testing.T) {
	t.Parallel()
	s := NewMemoryStore()
	now := time.Now().UTC()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	must(s.Set(context.Background(), PersistedToken{
		Key:          Key{Kind: KindMCP, Name: "alpha"},
		AccessToken:  "at",
		RefreshToken: "rt",
		ExpiresAt:    now.Add(time.Hour),
	}))
	must(s.Set(context.Background(), PersistedToken{
		Key:         Key{Kind: KindAPI, Name: "beta"},
		AccessToken: "at2",
	}))
	got, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	for _, row := range got {
		if row.AccessToken != "" {
			t.Errorf("List MUST NOT return access token plaintext: %+v", row)
		}
	}
	// First row had a refresh token → sentinel should be present.
	// Second row had no refresh token → empty.
	byName := map[string]PersistedToken{}
	for _, row := range got {
		byName[row.Key.Name] = row
	}
	if byName["alpha"].RefreshToken != refreshTokenSentinel {
		t.Errorf("alpha should have refresh sentinel; got %q", byName["alpha"].RefreshToken)
	}
	if byName["beta"].RefreshToken != "" {
		t.Errorf("beta has no refresh token; sentinel should be empty; got %q", byName["beta"].RefreshToken)
	}
}
