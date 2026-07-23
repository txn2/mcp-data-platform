package blobserve_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

func serve(t *testing.T, opts blobserve.Options, rangeHeader string) *http.Response {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/content", http.NoBody)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	rec := httptest.NewRecorder()
	blobserve.Serve(rec, req, opts)
	return rec.Result()
}

func TestServeSetsHardeningHeaders(t *testing.T) {
	t.Parallel()

	res := serve(t, blobserve.Options{
		Name:        "results.json",
		ContentType: "application/json",
		Data:        []byte(`{"a":1}`),
	}, "")
	defer func() { _ = res.Body.Close() }()

	require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
	require.Equal(t, "application/json", res.Header.Get("Content-Type"))
	require.Equal(t, "bytes", res.Header.Get("Accept-Ranges"))
	require.Equal(t, `inline; filename="results.json"`, res.Header.Get("Content-Disposition"))
}

func TestServeSanitizesContentType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		given string
		want  string
	}{
		{"parameters stripped", "text/csv; charset=utf-8", "text/csv"},
		{"alias canonicalized", "TEXT/JSON", "application/json"},
		{"empty falls back", "", "application/octet-stream"},
		{"unparseable falls back", "not a media type at all", "application/octet-stream"},
		{"header injection attempt is neutralized", "text/plain\r\nX-Evil: 1", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := serve(t, blobserve.Options{Name: "f", ContentType: tt.given, Data: []byte("x")}, "")
			defer func() { _ = res.Body.Close() }()
			require.Equal(t, tt.want, res.Header.Get("Content-Type"))
			require.Empty(t, res.Header.Get("X-Evil"))
		})
	}
}

// TestServeForcesAttachmentForActiveTypes is the serving half of the
// active-type rule: HTML, JSX, SVG and JavaScript must never be offered for
// inline rendering on the platform's own origin.
func TestServeForcesAttachmentForActiveTypes(t *testing.T) {
	t.Parallel()

	for _, ct := range []string{"text/html", "text/jsx", "image/svg+xml", "application/javascript"} {
		t.Run(ct, func(t *testing.T) {
			t.Parallel()
			res := serve(t, blobserve.Options{Name: "page", ContentType: ct, Data: []byte("<b>x</b>")}, "")
			defer func() { _ = res.Body.Close() }()
			require.True(t, strings.HasPrefix(res.Header.Get("Content-Disposition"), "attachment;"),
				"active type %q served as %q", ct, res.Header.Get("Content-Disposition"))
			require.Equal(t, "nosniff", res.Header.Get("X-Content-Type-Options"))
		})
	}
}

func TestServeInlineForPassiveTypes(t *testing.T) {
	t.Parallel()

	for _, ct := range []string{"application/json", "image/png", "audio/mpeg", "video/mp4", "application/pdf", "text/plain"} {
		t.Run(ct, func(t *testing.T) {
			t.Parallel()
			res := serve(t, blobserve.Options{Name: "blob", ContentType: ct, Data: []byte("x")}, "")
			defer func() { _ = res.Body.Close() }()
			require.True(t, strings.HasPrefix(res.Header.Get("Content-Disposition"), "inline;"))
		})
	}
}

func TestServeForceAttachment(t *testing.T) {
	t.Parallel()

	res := serve(t, blobserve.Options{
		Name: "export.csv", ContentType: "text/csv", Data: []byte("a,b\n"), ForceAttachment: true,
	}, "")
	defer func() { _ = res.Body.Close() }()
	require.Equal(t, `attachment; filename="export.csv"`, res.Header.Get("Content-Disposition"))
}

// TestServeRange proves audio and video seek works: a Range request returns 206
// with just the requested bytes and a Content-Range describing them.
func TestServeRange(t *testing.T) {
	t.Parallel()

	data := []byte("0123456789abcdef")

	t.Run("partial range", func(t *testing.T) {
		t.Parallel()
		res := serve(t, blobserve.Options{
			Name: "clip.mp4", ContentType: "video/mp4", Data: data, ModTime: time.Unix(1700000000, 0),
		}, "bytes=4-9")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusPartialContent, res.StatusCode)
		require.Equal(t, "bytes 4-9/16", res.Header.Get("Content-Range"))
		body := make([]byte, 6)
		_, err := res.Body.Read(body)
		require.NoError(t, err)
		require.Equal(t, "456789", string(body))
	})

	t.Run("suffix range", func(t *testing.T) {
		t.Parallel()
		res := serve(t, blobserve.Options{Name: "clip.mp3", ContentType: "audio/mpeg", Data: data}, "bytes=-4")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusPartialContent, res.StatusCode)
		require.Equal(t, "bytes 12-15/16", res.Header.Get("Content-Range"))
	})

	t.Run("unsatisfiable range", func(t *testing.T) {
		t.Parallel()
		res := serve(t, blobserve.Options{Name: "clip.mp3", ContentType: "audio/mpeg", Data: data}, "bytes=999-1200")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusRequestedRangeNotSatisfiable, res.StatusCode)
	})

	t.Run("no range serves the whole object", func(t *testing.T) {
		t.Parallel()
		res := serve(t, blobserve.Options{Name: "clip.mp4", ContentType: "video/mp4", Data: data}, "")
		defer func() { _ = res.Body.Close() }()

		require.Equal(t, http.StatusOK, res.StatusCode)
		require.Equal(t, "16", res.Header.Get("Content-Length"))
	})
}

func TestServeFilenameSanitization(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{"plain.json", `inline; filename="plain.json"`},
		{"has\"quote.json", `inline; filename="has_quote.json"`},
		{"a/b/c.json", `inline; filename="a_b_c.json"`},
		{"inject\r\nX-Evil: 1", `inline; filename="injectX-Evil: 1"`},
		{"", "inline"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := serve(t, blobserve.Options{Name: tt.name, ContentType: "application/json", Data: []byte("{}")}, "")
			defer func() { _ = res.Body.Close() }()
			require.Equal(t, tt.want, res.Header.Get("Content-Disposition"))
			require.Empty(t, res.Header.Get("X-Evil"))
		})
	}
}

func TestServeEmptyData(t *testing.T) {
	t.Parallel()

	res := serve(t, blobserve.Options{Name: "empty.txt", ContentType: "text/plain", Data: nil}, "")
	defer func() { _ = res.Body.Close() }()

	require.Equal(t, http.StatusOK, res.StatusCode)
	require.Equal(t, "0", res.Header.Get("Content-Length"))
}
