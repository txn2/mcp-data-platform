package exportwire

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestMountFor_NoPlatformMountsNothing: with no platform there is nothing to
// export from, and no route is mounted.
func TestMountFor_NoPlatformMountsNothing(t *testing.T) {
	mux := http.NewServeMux()
	MountFor(mux, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/portal/assets/a1/standalone", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("a route answered %d with no platform", rec.Code)
	}
}
