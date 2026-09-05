package resource

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1628: the upload ceiling was the compiled-in MaxUploadBytes, so a
// deployment whose reference material runs larger had no way to say so. It is
// now resources.managed.max_upload_bytes, reaching the write routes as
// Deps.MaxUploadBytes.

func TestNormalizeMaxUploadBytes(t *testing.T) {
	tests := []struct {
		name       string
		configured int64
		want       int64
	}{
		{"absent selects the shipped default", 0, MaxUploadBytes},
		{"a negative value selects the default rather than refusing every upload", -1, MaxUploadBytes},
		{"a large negative value does the same", -1 << 40, MaxUploadBytes},
		{"a configured ceiling is what applies", 262144000, 262144000},
		{"a ceiling below the default is honored, not raised", 1 << 20, 1 << 20},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeMaxUploadBytes(tt.configured))
		})
	}
}

// TestDescribeUploadLimit pins the phrasing a refusal carries. It has to match
// the browser's formatBytes (ui/src/lib/format.ts) unit for unit, since the
// file chooser beside the refusal renders the same number.
func TestDescribeUploadLimit(t *testing.T) {
	tests := []struct {
		limit int64
		want  string
	}{
		{MaxUploadBytes, "100 MB"},
		{262144000, "250 MB"},
		{0, "0 B"},
		{512, "512 B"},
		{1 << 20, "1 MB"},
		{1<<20 + 1<<19, "1.5 MB"},
		{2 << 30, "2 GB"},
		{3 << 40, "3 TB"},
		// Beyond the last unit the value keeps scaling in it rather than
		// naming a unit that is not there.
		{4 << 50, "4096 TB"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			assert.Equal(t, tt.want, DescribeUploadLimit(tt.limit))
		})
	}
}

// TestCreate_CeilingIsTheDeploymentsOwn drives the real create route: a file at
// the configured ceiling is stored, and one byte over is refused by a message
// naming that deployment's number.
//
// The at-the-ceiling case is the one that was broken before the ceiling was
// configurable at all: the request body was bounded at exactly the ceiling,
// which the multipart framing around the file already exceeds, so a file of
// exactly the limit was refused as a malformed form -- a 400 naming neither
// the size nor the limit.
func TestCreate_CeilingIsTheDeploymentsOwn(t *testing.T) {
	const ceiling = 4 << 20

	tests := []struct {
		name    string
		size    int64
		created bool
	}{
		{"a byte under the ceiling", ceiling - 1, true},
		{"exactly the ceiling", ceiling, true},
		{"a byte over the ceiling", ceiling + 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, s3 := newMockStore(), newMockS3()
			h := newTestHandler(store, s3, okExtractor)
			h.deps.MaxUploadBytes = ceiling

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, uploadRequest(t, "text/plain", "reference.txt", bytes.Repeat([]byte("a"), int(tt.size))))

			if !tt.created {
				require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
				assert.Contains(t, rec.Body.String(), "file exceeds 4 MB limit",
					"the refusal must name the deployment's ceiling")
				assert.Empty(t, s3.objects, "a refused upload must not reach storage")
				return
			}
			require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())

			var body map[string]any
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
			assert.Equal(t, float64(tt.size), body["size_bytes"], "the whole file must be stored")
			require.Len(t, s3.objects, 1)
		})
	}
}

// TestCreate_UnsetCeilingRefusesByTheDefault pins that a deployment configuring
// nothing refuses by MaxUploadBytes and says so. Driven through the size the
// multipart header declares rather than through a hundred megabytes of body:
// the route reads that header before it reads any bytes.
func TestCreate_UnsetCeilingRefusesByTheDefault(t *testing.T) {
	store, s3 := newMockStore(), newMockS3()
	h := newTestHandler(store, s3, okExtractor)
	require.Zero(t, h.deps.MaxUploadBytes, "this case is a deployment that configures no ceiling")
	assert.Equal(t, int64(MaxUploadBytes), h.maxUploadBytes())
	assert.Equal(t, "100 MB", DescribeUploadLimit(h.maxUploadBytes()),
		"the refusal a stock deployment writes is unchanged")
}

// TestReplaceContent_HonorsTheCeiling pins that the revision route applies the
// same ceiling the create route does. It had its own copy of the constant, so
// a configured ceiling that reached only one of them would let a file be
// created and then refused on replacement, or the reverse.
func TestReplaceContent_HonorsTheCeiling(t *testing.T) {
	const ceiling = 2 << 20

	tests := []struct {
		name   string
		size   int64
		revise bool
	}{
		{"exactly the ceiling is a revision", ceiling, true},
		{"a byte over is refused", ceiling + 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newVersionedHandler(t, okExtractor)
			seedVersionedResource(t, fx.store, fx.s3, fx.versions)
			fx.handler.deps.MaxUploadBytes = ceiling

			req := buildMultipartRequest(t, nil, bytes.Repeat([]byte("b"), int(tt.size)), "f.csv")
			req.URL.Path = "/api/v1/resources/res-1/content"
			rec := httptest.NewRecorder()
			fx.handler.ServeHTTP(rec, req)

			// The seeded resource is already version 1, so a revision is the
			// second row and a refusal leaves the first alone.
			if !tt.revise {
				require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
				assert.Contains(t, rec.Body.String(), "file exceeds 2 MB limit")
				assert.Len(t, fx.versions.byResource["res-1"], 1, "a refused replacement records no version")
				return
			}
			require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
			require.Len(t, fx.versions.byResource["res-1"], 2, "an accepted replacement records a version")
			assert.Equal(t, tt.size, fx.versions.byResource["res-1"][1].SizeBytes)
			assert.Equal(t, tt.size, fx.store.resources["res-1"].SizeBytes, "the revision is the live content")
		})
	}
}

// TestUpload_OversizeBodyIsRefusedByTheCeiling covers the refusal a plainly
// oversize upload actually produces. The body bound cuts the request off
// before the multipart parser has read the file part, so the size check on the
// part header never runs: without naming the ceiling here, the only thing such
// a caller is told is "invalid multipart form", which states neither the size
// they sent nor the size they may send.
//
// Both routes are covered, and the unterminated body proves the bound is still
// a backstop rather than something the caller can talk their way past.
func TestUpload_OversizeBodyIsRefusedByTheCeiling(t *testing.T) {
	const ceiling = 1 << 20

	// wellFormed is a complete create request whose file is far over the
	// ceiling; unterminated is a part that never ends, so only the bound can
	// stop the read.
	wellFormed := func(t *testing.T) *http.Request {
		t.Helper()
		return uploadRequest(t, "text/plain", "big.txt", bytes.Repeat([]byte("a"), 4*ceiling))
	}
	unterminated := func(t *testing.T) *http.Request {
		t.Helper()
		var buf bytes.Buffer
		// bytes.Buffer never fails a write, so the errors are discarded
		// explicitly rather than left unchecked.
		//
		// The metadata parts come first because that is the order the create
		// route reads (#1631): it validates the fields where the file part
		// begins, so a form carrying only the file is refused for the missing
		// scope long before the ceiling is reached, and would not exercise
		// the bound this case is about.
		for _, field := range []string{
			"scope\"\r\n\r\nglobal", "path\"\r\n\r\nsamples",
			"display_name\"\r\n\r\nA file", "description\"\r\n\r\nUploaded by the ceiling test.",
		} {
			_, _ = buf.WriteString("--X\r\nContent-Disposition: form-data; name=\"" + field + "\r\n")
		}
		_, _ = buf.WriteString("--X\r\nContent-Disposition: form-data; name=\"file\"; filename=\"f.txt\"\r\n\r\n")
		_, _ = buf.Write(bytes.Repeat([]byte("a"), ceiling+multipartFramingBytes+1))
		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/v1/resources", &buf)
		req.Header.Set("Content-Type", `multipart/form-data; boundary=X`)
		return req
	}

	tests := []struct {
		name string
		path string
		req  func(*testing.T) *http.Request
	}{
		{"a well-formed body over the ceiling", "/api/v1/resources", wellFormed},
		{"a body that never ends", "/api/v1/resources", unterminated},
		{"a replacement over the ceiling", "/api/v1/resources/res-1/content", func(t *testing.T) *http.Request {
			t.Helper()
			req := wellFormed(t)
			req.URL.Path = "/api/v1/resources/res-1/content"
			return req
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fx := newVersionedHandler(t, okExtractor)
			seedVersionedResource(t, fx.store, fx.s3, fx.versions)
			fx.handler.deps.MaxUploadBytes = ceiling
			before := len(fx.s3.objects)

			rec := httptest.NewRecorder()
			fx.handler.ServeHTTP(rec, tt.req(t))

			require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), "file exceeds 1 MB limit",
				"an oversize body must be refused by the ceiling it passed, not as a malformed form")
			assert.Len(t, fx.s3.objects, before, "a refused upload must not reach storage")
		})
	}
}
