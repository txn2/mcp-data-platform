package feedbackapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/threads"
)

func TestIntParam(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		fallback int
		want     int
	}{
		{"present", "?limit=10", 50, 10},
		{"missing", "", 50, 50},
		{"unparseable", "?limit=abc", 50, 50},
		{"negative", "?limit=-1", 50, 50},
		{"zero is a real value, not a fallback", "?limit=0", 50, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x"+tt.query, http.NoBody)
			assert.Equal(t, tt.want, intParam(r, paramLimit, tt.fallback))
		})
	}
}

// TestAssetTargetLookupFailureIs500 pins the distinction the view check exists
// to preserve: a share lookup that fails is not a denial. Answering 403 would
// tell the caller they lack access when the truth is that access could not be
// determined.
func TestAssetTargetLookupFailureIs500(t *testing.T) {
	user := &access.User{UserID: "u2", Email: "u2@example.com"}
	h := newTestServer(Config{
		Threads: &mockThreadStore{getResult: &threads.Thread{
			ID: "thr_1", TargetType: portaldomain.TargetTypeAsset, AssetID: "asset_1",
		}},
		Assets: &mockAssetStore{getAsset: &portaldomain.Asset{ID: "asset_1", OwnerID: "someone-else"}},
		Shares: &mockShareStore{listByAssetE: errors.New("db down")},
	}, user)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/portal/threads/thr_1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
}
