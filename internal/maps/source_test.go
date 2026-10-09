package maps

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveSource(t *testing.T) {
	ctx := context.Background()
	got, err := resolveSource(ctx, http.DefaultClient, Settings{SourceURL: "https://mirror.example.com/osm/planet-v4.pmtiles"}, "", "")
	require.NoError(t, err)
	assert.Equal(t, sourceRef{url: "https://mirror.example.com/osm/planet-v4.pmtiles", build: "planet-v4"}, got,
		"a mirror is read as given, and its file name names the build")

	index := func(body string, status int) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return srv.URL
	}
	got, err = resolveSource(ctx, http.DefaultClient, Settings{},
		index(`[{"key":"20261006.pmtiles"},{"key":"20261008.pmtiles"},{"key":"v4.15.2.pmtiles"},{"key":"20261007.pmtiles"}]`, 200),
		"https://build.example.com/")
	require.NoError(t, err)
	assert.Equal(t, sourceRef{url: "https://build.example.com/20261008.pmtiles", build: "2026-10-08"}, got,
		"the newest daily build, not a versioned one")

	for body, status := range map[string]int{
		`[{"key":"v4.pmtiles"}]`: 200,
		`not json`:               200,
		`[]`:                     503,
	} {
		_, err := resolveSource(ctx, http.DefaultClient, Settings{}, index(body, status), "x")
		require.Error(t, err, body)
	}
	_, err = resolveSource(ctx, http.DefaultClient, Settings{}, "http://127.0.0.1:1/builds.json", "x")
	require.Error(t, err)
	_, err = resolveSource(ctx, http.DefaultClient, Settings{}, "::", "x")
	require.Error(t, err)
}

func TestBuildDate(t *testing.T) {
	assert.Equal(t, "2026-10-08", buildDate(map[string]any{"planetiler:osm:osmosisreplicationtime": "2026-10-08T04:00:00Z"}, "x"))
	assert.Equal(t, "x", buildDate(map[string]any{"planetiler:osm:osmosisreplicationtime": "yesterday"}, "x"))
	assert.Equal(t, "x", buildDate(nil, "x"))
}

func noPause(int) time.Duration { return 0 }

func TestHTTPSource_RetriesWhatIsWorthRetrying(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		assert.Equal(t, "bytes=2-5", r.Header.Get("Range"))
		w.Header().Set("ETag", `"a"`)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = io.WriteString(w, "2345")
	}))
	defer srv.Close()
	src := newHTTPSource(http.DefaultClient, srv.URL, noPause)
	b, err := src.ReadRange(context.Background(), 2, 4)
	require.NoError(t, err)
	assert.Equal(t, "2345", string(b))
	assert.Equal(t, int32(3), calls.Load())
	assert.Equal(t, `"a"`, src.tag())
}

func TestHTTPSource_Refusals(t *testing.T) {
	serve := func(status int, body string) *httpSource {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		}))
		t.Cleanup(srv.Close)
		return newHTTPSource(http.DefaultClient, srv.URL, noPause)
	}
	ctx := context.Background()

	_, err := serve(http.StatusNotFound, "").ReadRange(ctx, 0, 10)
	require.ErrorContains(t, err, "HTTP 404", "a 404 is not retried")

	_, err = serve(http.StatusPreconditionFailed, "").ReadRange(ctx, 0, 10)
	require.ErrorIs(t, err, errSourceChanged)

	b, err := serve(http.StatusRequestedRangeNotSatisfiable, "").ReadRange(ctx, 100, 10)
	require.NoError(t, err)
	assert.Empty(t, b, "a read past the end returns nothing")

	b, err = serve(http.StatusOK, "short").ReadRange(ctx, 0, 100)
	require.NoError(t, err)
	assert.Equal(t, "short", string(b), "a whole file shorter than the read is the read")

	_, err = serve(http.StatusOK, strings.Repeat("x", 200)).ReadRange(ctx, 0, 100)
	require.ErrorContains(t, err, "does not answer byte ranges")

	_, err = serve(http.StatusOK, "x").ReadRange(ctx, 10, 5)
	require.ErrorContains(t, err, "HTTP 200", "a 200 to a read past the start is not the range asked for")

	_, err = serve(http.StatusTooManyRequests, "").ReadRange(ctx, 0, 1)
	require.ErrorContains(t, err, "HTTP 429")

	_, err = newHTTPSource(http.DefaultClient, "::", noPause).ReadRange(ctx, 0, 1)
	require.Error(t, err)
}

func TestHTTPSource_StopsWithItsContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	src := newHTTPSource(http.DefaultClient, srv.URL, func(int) time.Duration { cancel(); return time.Hour })
	_, err := src.ReadRange(ctx, 0, 1)
	require.ErrorIs(t, err, context.Canceled)
}
