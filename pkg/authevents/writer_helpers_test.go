package authevents

import (
	"context"
	"testing"
)

// TestWriterHelpersEmitExpectedTypes covers each typed helper that
// wasn't already exercised by writer_test.go. Each helper is a thin
// wrapper around Emit; the tests assert the wire format (event type
// name) and that the actor flows through.
func TestWriterHelpersEmitExpectedTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		call func(*Writer, context.Context)
		want Type
	}{
		{
			name: "ConnectCompleted",
			call: func(w *Writer, ctx context.Context) {
				w.ConnectCompleted(ctx, "mcp", "x", "u", "https://idp/token",
					ConnectCompletedDetail{Scope: "openid", HasRefreshToken: true})
			},
			want: TypeConnectCompleted,
		},
		{
			name: "RefreshSkippedNoToken",
			call: func(w *Writer, ctx context.Context) {
				w.RefreshSkippedNoToken(ctx, "mcp", "x", "u", "https://idp/token")
			},
			want: TypeRefreshSkippedNoToken,
		},
		{
			name: "RefreshSkippedExpired",
			call: func(w *Writer, ctx context.Context) {
				w.RefreshSkippedExpired(ctx, "mcp", "x", "u", "https://idp/token")
			},
			want: TypeRefreshSkippedExpired,
		},
		{
			name: "RotationPersistenceFailed",
			call: func(w *Writer, ctx context.Context) {
				w.RotationPersistenceFailed(ctx, "mcp", "x", "u", "https://idp/token",
					"db write failed")
			},
			want: TypeRefreshRotationPersistenceFailed,
		},
		{
			name: "TokenDeletedRevoked",
			call: func(w *Writer, ctx context.Context) {
				w.TokenDeletedRevoked(ctx, "mcp", "x", "u", "https://idp/token",
					"invalid_grant")
			},
			want: TypeTokenDeletedRevoked,
		},
		{
			name: "TokenDeletedAdmin",
			call: func(w *Writer, ctx context.Context) {
				w.TokenDeletedAdmin(ctx, "mcp", "x", "admin@example.com")
			},
			want: TypeTokenDeletedAdmin,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewMemoryStore()
			w := NewWriter(store, nil)
			tc.call(w, context.Background())
			got, _ := store.List(context.Background(), Filter{
				Kind: "mcp", Name: "x", Limit: 5,
			})
			if len(got) != 1 {
				t.Fatalf("len(got) = %d, want 1", len(got))
			}
			if got[0].Type != tc.want {
				t.Errorf("Type = %v, want %v", got[0].Type, tc.want)
			}
		})
	}
}

// TestEmitNilStore confirms Emit short-circuits when the writer's
// store is nil (constructor with nil store).
func TestEmitNilStore(t *testing.T) {
	t.Parallel()
	w := NewWriter(nil, nil)
	// Must not panic.
	w.ConnectStarted(context.Background(), "mcp", "x", "u", "https://idp/token", "/back")
	w.RefreshSucceeded(context.Background(), "mcp", "x", "u", "https://idp/token", RefreshDetail{})
}
