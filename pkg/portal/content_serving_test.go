package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// getAssetContent issues a GET against the authenticated raw-content endpoint,
// optionally with a Range header.
func getAssetContent(t *testing.T, asset *Asset, data []byte, ct, rangeHeader string) *httptest.ResponseRecorder {
	t.Helper()

	h := newTestHandler(
		&mockAssetStore{getAsset: asset},
		&mockShareStore{},
		&mockS3Client{getData: data, getCT: ct},
		&User{UserID: asset.OwnerID},
	)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/api/v1/portal/assets/"+asset.ID+"/content", http.NoBody)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestAssetContentSetsNosniff covers the serving-hardening acceptance criterion
// of issue #1007: every raw content response refuses browser content sniffing,
// so a stored type can never be reinterpreted as something executable.
func TestAssetContentSetsNosniff(t *testing.T) {
	for _, ct := range []string{"application/json", "text/plain", "image/png", "text/html", ""} {
		t.Run("content type "+ct, func(t *testing.T) {
			asset := &Asset{ID: "a1", OwnerID: "u1", Name: "thing", S3Bucket: "b", S3Key: "k", ContentType: ct}
			w := getAssetContent(t, asset, []byte("payload"), ct, "")

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		})
	}
}

// TestAssetContentSanitizesContentType proves a stored type cannot inject a
// header or arrive with parameters attached.
func TestAssetContentSanitizesContentType(t *testing.T) {
	tests := []struct {
		name   string
		stored string
		want   string
	}{
		{"parameters stripped", "text/csv; charset=utf-8", "text/csv"},
		{"alias canonicalized", "text/json", "application/json"},
		{"garbage falls back", "not a type", "application/octet-stream"},
		{"injection attempt neutralized", "text/plain\r\nX-Evil: 1", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			asset := &Asset{ID: "a1", OwnerID: "u1", Name: "thing", S3Bucket: "b", S3Key: "k"}
			w := getAssetContent(t, asset, []byte("payload"), tt.stored, "")

			assert.Equal(t, tt.want, w.Header().Get("Content-Type"))
			assert.Empty(t, w.Header().Get("X-Evil"))
		})
	}
}

// TestAssetContentActiveTypeIsAlwaysAttachment keeps stored HTML, JSX and SVG
// from rendering inline on the platform's own origin.
func TestAssetContentActiveTypeIsAlwaysAttachment(t *testing.T) {
	for _, ct := range []string{"text/html", "text/jsx", "image/svg+xml"} {
		t.Run(ct, func(t *testing.T) {
			asset := &Asset{ID: "a1", OwnerID: "u1", Name: "page", S3Bucket: "b", S3Key: "k", ContentType: ct}
			w := getAssetContent(t, asset, []byte("<b>x</b>"), ct, "")

			assert.True(t, strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;"),
				"disposition = %q", w.Header().Get("Content-Disposition"))
		})
	}
}

// TestAssetContentRangeRequest is the acceptance criterion for audio and video
// seek: the content endpoint answers a byte range with 206 and just those bytes.
func TestAssetContentRangeRequest(t *testing.T) {
	data := []byte("0123456789abcdef")
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "clip.mp4", S3Bucket: "b", S3Key: "k",
		ContentType: "video/mp4", UpdatedAt: time.Unix(1700000000, 0).UTC(),
	}

	t.Run("advertises range support", func(t *testing.T) {
		w := getAssetContent(t, asset, data, "video/mp4", "")
		assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("serves a partial range", func(t *testing.T) {
		w := getAssetContent(t, asset, data, "video/mp4", "bytes=4-9")
		assert.Equal(t, http.StatusPartialContent, w.Code)
		assert.Equal(t, "bytes 4-9/16", w.Header().Get("Content-Range"))
		assert.Equal(t, "456789", w.Body.String())
		assert.Equal(t, "video/mp4", w.Header().Get("Content-Type"))
	})

	t.Run("rejects an unsatisfiable range", func(t *testing.T) {
		w := getAssetContent(t, asset, data, "video/mp4", "bytes=900-999")
		assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, w.Code)
	})
}

// publicViewContentData renders a public share and pulls the JSON payload the
// viewer bundle reads out of the page.
func publicViewContentData(t *testing.T, asset *Asset, data []byte) map[string]any {
	t.Helper()

	share := &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: asset.ID, Token: "tok1"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: data, getCT: asset.ContentType},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	body := w.Body.String()
	start := strings.Index(body, `id="content-data"`)
	require.GreaterOrEqual(t, start, 0, "content-data script tag not found")
	open := strings.Index(body[start:], ">") + start + 1
	end := strings.Index(body[open:], "</script>") + open

	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(body[open:end]), &payload))
	return payload
}

// TestPublicViewerServesBinaryFromURL covers the binary-serving requirement of
// issue #1007: the page embeds text content as a JSON string, which cannot
// carry arbitrary bytes, so binary families must be handed a content URL
// instead of embedded content.
func TestPublicViewerServesBinaryFromURL(t *testing.T) {
	now := time.Now()

	binary := []struct {
		name string
		ct   string
	}{
		{"png", "image/png"},
		{"mp3", "audio/mpeg"},
		{"mp4", "video/mp4"},
		{"pdf", "application/pdf"},
		{"unknown binary", "application/octet-stream"},
	}

	for _, tt := range binary {
		t.Run(tt.name, func(t *testing.T) {
			asset := &Asset{
				ID: "a1", OwnerID: "u1", Name: "blob", ContentType: tt.ct,
				Tags: []string{}, CreatedAt: now, UpdatedAt: now, SizeBytes: 8,
			}
			payload := publicViewContentData(t, asset, []byte{0x00, 0x01, 0x02})

			assert.Equal(t, true, payload["serveFromURL"], "binary asset must render from a URL")
			assert.Empty(t, payload["content"], "binary bytes must not be embedded in the page")
			assert.Equal(t, "/portal/view/tok1/content", payload["contentURL"])
		})
	}
}

// TestPublicViewerEmbedsTextContent is the counterpart: text families keep the
// existing embedded-content path, so no extra request is needed to render them.
func TestPublicViewerEmbedsTextContent(t *testing.T) {
	now := time.Now()

	for _, ct := range []string{"text/markdown", "application/json", "text/csv", "text/html", "application/xml"} {
		t.Run(ct, func(t *testing.T) {
			asset := &Asset{
				ID: "a1", OwnerID: "u1", Name: "doc", ContentType: ct,
				Tags: []string{}, CreatedAt: now, UpdatedAt: now, SizeBytes: 5,
			}
			payload := publicViewContentData(t, asset, []byte("hello"))

			assert.Equal(t, false, payload["serveFromURL"])
			assert.Equal(t, "hello", payload["content"])
		})
	}
}

// TestPublicCSPAllowsMedia covers the CSP change the audio and video viewers
// need: without media-src the browser blocks the element's source outright.
func TestPublicCSPAllowsMedia(t *testing.T) {
	now := time.Now()
	asset := &Asset{
		ID: "a1", OwnerID: "u1", Name: "clip", ContentType: "video/mp4",
		Tags: []string{}, CreatedAt: now, UpdatedAt: now,
	}
	share := &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	csp := w.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "media-src 'self' blob: data:")
	assert.Contains(t, csp, "object-src 'self'")
}

// TestPublicAssetContentSupportsRange proves the public share's content
// endpoint seeks too, which is what makes a shared video playable.
func TestPublicAssetContentSupportsRange(t *testing.T) {
	data := []byte("0123456789abcdef")
	asset := &Asset{ID: "a1", OwnerID: "u1", Name: "clip.mp4", ContentType: "video/mp4", S3Bucket: "b", S3Key: "k"}
	share := &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1"}
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: asset},
		ShareStore: &mockShareStore{getByTokenRes: share},
		S3Client:   &mockS3Client{getData: data, getCT: "video/mp4"},
	}, nil)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/content", http.NoBody)
	req.Header.Set("Range", "bytes=8-11")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusPartialContent, w.Code)
	assert.Equal(t, "89ab", w.Body.String())
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
}

// TestPublicAssetContentCannotScriptTheOrigin covers issue #1068 on the surface
// that made it reachable: a single-asset public share serves stored bytes with
// no authentication, on the same origin whose REST API is cookie-authenticated.
// A document uploaded there must arrive unable to run script — as a download
// rather than a rendered document, and under a policy that denies script even
// if the disposition were wrong or the family unrecognized.
//
// The assertion is on the response headers rather than on browser behavior,
// because the headers are what the platform controls.
func TestPublicAssetContentCannotScriptTheOrigin(t *testing.T) {
	const payload = `<html xmlns="http://www.w3.org/1999/xhtml"><script>fetch("/api/v1/portal/assets")</script></html>`

	for _, ct := range []string{
		"application/xhtml+xml", "application/xml", "text/xml", "text/html", "image/svg+xml",
	} {
		t.Run(ct, func(t *testing.T) {
			asset := &Asset{
				ID: "a1", OwnerID: "u1", Name: "doc", ContentType: ct, S3Bucket: "b", S3Key: "k",
			}
			share := &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1"}
			h := NewHandler(Deps{
				AssetStore: &mockAssetStore{getAsset: asset},
				ShareStore: &mockShareStore{getByTokenRes: share},
				S3Client:   &mockS3Client{getData: []byte(payload), getCT: ct},
			}, nil)

			req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/content", http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			assert.True(t, strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment"),
				"%q must not be offered for inline rendering, got %q", ct, w.Header().Get("Content-Disposition"))
			assert.Equal(t, "default-src 'none'; sandbox", w.Header().Get("Content-Security-Policy"))
			assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
		})
	}
}

// TestPublicAssetContentAlwaysCarriesCSP is the unconditional half: the policy
// is on the response whatever the family, so a type nobody classified is
// contained rather than exploitable.
func TestPublicAssetContentAlwaysCarriesCSP(t *testing.T) {
	for _, ct := range []string{"text/plain", "image/png", "application/pdf", "application/vnd.acme.unknown", ""} {
		t.Run("content type "+ct, func(t *testing.T) {
			asset := &Asset{ID: "a1", OwnerID: "u1", Name: "blob", ContentType: ct, S3Bucket: "b", S3Key: "k"}
			share := &Share{AccessMode: AccessModePublic, ID: "s1", AssetID: "a1", Token: "tok1"}
			h := NewHandler(Deps{
				AssetStore: &mockAssetStore{getAsset: asset},
				ShareStore: &mockShareStore{getByTokenRes: share},
				S3Client:   &mockS3Client{getData: []byte("payload"), getCT: ct},
			}, nil)

			req := httptest.NewRequestWithContext(context.Background(), "GET", "/portal/view/tok1/content", http.NoBody)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, "default-src 'none'; sandbox", w.Header().Get("Content-Security-Policy"))
		})
	}
}
