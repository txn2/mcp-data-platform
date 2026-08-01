package sharecache_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/sharecache"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareaccess"
)

// TestPubliclyCacheable pins the rule against the whole mode domain: public is
// the only value a shared cache may act on, and the empty mode a pre-column row
// carries fails closed exactly as shareaccess.Authorize does with it.
func TestPubliclyCacheable(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mode shareaccess.Mode
		want bool
	}{
		{shareaccess.ModePublic, true},
		{shareaccess.ModeRestricted, false},
		{shareaccess.ModeAuthenticated, false},
		{shareaccess.Mode(""), false},
		{shareaccess.Mode("Public"), false},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sharecache.PubliclyCacheable(tt.mode))
		})
	}
}

func TestApply(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		mode             shareaccess.Mode
		wantCacheControl string
	}{
		{"restricted gets the private floor", shareaccess.ModeRestricted, "private"},
		{"authenticated gets the private floor", shareaccess.ModeAuthenticated, "private"},
		{"empty mode fails closed", shareaccess.Mode(""), "private"},
		{"public is left to the handler", shareaccess.ModePublic, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			sharecache.Apply(rec, tt.mode)

			assert.Equal(t, tt.wantCacheControl, rec.Header().Get("Cache-Control"))
			assert.Equal(t, "Cookie", rec.Header().Get("Vary"),
				"Vary is unconditional: the viewer page differs by caller in every mode")
		})
	}
}

func TestRefuse(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sharecache.Refuse(rec)

	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

// TestRefuseOverridesTheFloor covers the order the gate uses: Apply runs on
// every request, then Refuse on the ones that are turned away. A refusal that
// kept the `private` floor would still be storable by the caller's own browser
// and replayed to them after they gain access.
func TestRefuseOverridesTheFloor(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sharecache.Apply(rec, shareaccess.ModeRestricted)
	sharecache.Refuse(rec)

	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
}

func TestMaxAge(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time {
		ts := now.Add(d)
		return &ts
	}

	tests := []struct {
		name      string
		expiresAt *time.Time
		want      time.Duration
	}{
		{"no expiry uses the window", nil, sharecache.Window},
		{"expiry beyond the window is capped", at(5 * time.Hour), sharecache.Window},
		{"expiry inside the window clamps", at(10 * time.Minute), 10 * time.Minute},
		{"expiry already past floors at zero", at(-time.Minute), 0},
		{"expiry exactly now floors at zero", at(0), 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, sharecache.MaxAge(tt.expiresAt, now))
		})
	}
}

// TestThumbnail is the acceptance shape of #1070: the same object, the same
// URL, and a directive that follows who the gate would let in.
func TestThumbnail(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	soon := now.Add(10 * time.Minute)

	tests := []struct {
		name      string
		mode      shareaccess.Mode
		expiresAt *time.Time
		want      string
		wantVary  string
	}{
		{"public share is shared-cacheable", shareaccess.ModePublic, nil, "public, max-age=3600", ""},
		{"public share clamped to expiry", shareaccess.ModePublic, &soon, "public, max-age=600", ""},
		{"restricted share is not", shareaccess.ModeRestricted, nil, "private, max-age=3600", "Cookie"},
		{"authenticated share is not", shareaccess.ModeAuthenticated, nil, "private, max-age=3600", "Cookie"},
		{"empty mode is not", shareaccess.Mode(""), nil, "private, max-age=3600", "Cookie"},
		{"restricted share clamped to expiry", shareaccess.ModeRestricted, &soon, "private, max-age=600", "Cookie"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			sharecache.Thumbnail(rec, tt.mode, tt.expiresAt, now)

			require.Equal(t, tt.want, rec.Header().Get("Cache-Control"))
			assert.Equal(t, tt.wantVary, rec.Header().Get("Vary"))
		})
	}
}

// TestThumbnailAfterApply is how the gate and the handler actually compose: the
// floor is written first, and the thumbnail's own directive replaces it without
// losing the Vary the gate set.
func TestThumbnailAfterApply(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)

	for _, mode := range []shareaccess.Mode{shareaccess.ModePublic, shareaccess.ModeRestricted} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			sharecache.Apply(rec, mode)
			sharecache.Thumbnail(rec, mode, nil, now)

			assert.Equal(t, "Cookie", rec.Header().Get("Vary"))
			assert.Contains(t, rec.Header().Get("Cache-Control"), "max-age=3600")
		})
	}
}

// TestApplyWritesNothingElse guards the header set: the gate runs on every
// request to the public surface, so anything it writes ships on every response.
func TestApplyWritesNothingElse(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	sharecache.Apply(rec, shareaccess.ModeRestricted)

	got := make([]string, 0, len(rec.Header()))
	for name := range rec.Header() {
		got = append(got, name)
	}
	assert.ElementsMatch(t, []string{"Vary", "Cache-Control"}, got)
	assert.Equal(t, http.StatusOK, rec.Code, "writing headers must not commit a status")
}
