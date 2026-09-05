package resource

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Issue #1631: the managed-resource write was the one upload path without
// multipart. The whole object was read into a single []byte and handed to blob
// storage as one PUT, which an S3-compatible backend is free to refuse above a
// bound well under the file, and which made the configured ceiling resident
// heap per concurrent upload. It also depended on a writable temp directory,
// because the multipart parser spooled a large part to one -- and the
// published image is built FROM scratch and has none.
//
// The write now streams: the form is walked part by part, and the file part is
// handed to the multipart uploader as a reader.

// streamFields are the metadata a create carries, in the order the route reads
// them: everything before the file part.
var streamFields = []struct{ name, value string }{
	{"scope", "global"},
	{"path", "references"},
	{"display_name", "Reference material"},
	{"description", "Uploaded by the streaming-upload tests."},
}

// streamUploadRequest builds a create whose parts are in the documented order,
// with the file last.
func streamUploadRequest(t *testing.T, filename, declared string, content []byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range streamFields {
		require.NoError(t, w.WriteField(f.name, f.value))
	}
	writeFilePart(t, w, filename, declared, content)
	require.NoError(t, w.Close())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

// writeFilePart appends the file part, with a declared Content-Type only when
// the caller named one -- the two forms a browser and a script each send.
func writeFilePart(t *testing.T, w *multipart.Writer, filename, declared string, content []byte) {
	t.Helper()
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	if declared != "" {
		header.Set("Content-Type", declared)
	}
	part, err := w.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
}

// TestUpload_LargeFileNeedsNoTemporaryDirectory is the criterion the published
// image runs under. ParseMultipartForm spooled any part past its memory budget
// to os.CreateTemp, so on an image with no /tmp every upload above that budget
// failed with "no such file or directory" before blob storage was reached at
// all. Walking the parts spools nothing, so the upload has to succeed with the
// process pointed at a directory that does not exist.
func TestUpload_LargeFileNeedsNoTemporaryDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-directory")
	t.Setenv("TMPDIR", missing)
	require.Equal(t, missing, os.TempDir(),
		"the test has to actually remove the temp directory or it proves nothing")
	_, err := os.CreateTemp("", "spool-")
	require.Error(t, err, "a spool must be impossible here, or this test would pass either way")

	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"},
		okExtractor, nil)

	// Well past the 10 MB budget the old parser spooled at.
	content := bytes.Repeat([]byte("a"), 12<<20)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, streamUploadRequest(t, "large.txt", "text/plain", content))

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	body := decodeJSON(t, rec.Body)
	assert.EqualValues(t, len(content), body["size_bytes"])
	require.Len(t, s3.objects, 1)
	for _, stored := range s3.objects {
		assert.Equal(t, content, stored, "the stored object is the file that was uploaded")
	}
}

// discardingS3 counts what it is given without keeping it, so a test can
// measure what the platform allocated carrying a file rather than what the
// fake allocated holding one.
type discardingS3 struct {
	mockS3
	written int64
}

func (d *discardingS3) PutObjectStream(
	_ context.Context, _, _ string, body io.Reader, _ string,
) (int64, error) {
	n, err := io.Copy(io.Discard, body)
	d.written = n
	if err != nil {
		return 0, fmt.Errorf("reading the streamed body: %w", err)
	}
	return n, nil
}

// TestUpload_AllocatesFarLessThanTheFile is the memory criterion. The measure
// is total bytes allocated across the request rather than a sampled peak: a
// path that assembles the object allocates at least its size (and more, since
// io.ReadAll grows its slice), so an upload that allocates a small fraction of
// the file cannot be holding it.
func TestUpload_AllocatesFarLessThanTheFile(t *testing.T) {
	const size = 64 << 20
	store, s3 := newMockStore(), &discardingS3{mockS3: *newMockS3()}
	h := NewHandler(Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"},
		okExtractor, nil)

	// The body is built before the measurement, so what is counted is the
	// request, not the fixture.
	req := streamUploadRequest(t, "large.bin", "application/octet-stream", bytes.Repeat([]byte("a"), size))

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	runtime.ReadMemStats(&after)

	require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
	require.EqualValues(t, size, s3.written, "the whole file still reached storage")

	allocated := after.TotalAlloc - before.TotalAlloc
	// An eighth of the file. The streaming path allocates the sniff prefix and
	// a copy buffer, which are kilobytes; the bound is loose enough to absorb
	// the request machinery around them and still fail outright on any path
	// that holds the object.
	assert.Less(t, allocated, uint64(size/8),
		"carrying a %d-byte upload allocated %d bytes, which is the shape of a path that holds the file",
		size, allocated)
}

// TestUpload_TypeIsDetectedOffTheStream is the criterion that nothing about
// storage changed for an ordinary file. Detection reads a bounded prefix of a
// part it cannot seek, so the prefix has to be put back in front of the rest:
// the type has to be the type detected from the bytes, and the stored object
// has to be the file, for content longer than the prefix.
func TestUpload_TypeIsDetectedOffTheStream(t *testing.T) {
	// Longer than contenttype.StructuredSniffLen, so the prefix is a fraction
	// of the file and re-prepending it is what makes the object whole.
	var csv bytes.Buffer
	// bytes.Buffer never fails a write, so the errors are discarded explicitly
	// rather than left unchecked.
	_, _ = csv.WriteString("day,high,low\n")
	for i := 0; csv.Len() < 40<<10; i++ {
		_, _ = fmt.Fprintf(&csv, "2026-01-%02d,%d,%d\n", (i%28)+1, 70+i%20, 50+i%15)
	}
	content := csv.Bytes()

	// Prose of the same shape: past the prefix, and not a CSV, so a case that
	// expects plain text is testing the declaration rather than the columns.
	var prose bytes.Buffer
	for prose.Len() < 40<<10 {
		_, _ = prose.WriteString("The registration notes for this dataset run to several pages. ")
	}

	tests := []struct {
		name     string
		declared string
		filename string
		content  []byte
		want     string
	}{
		{"no declaration at all", "", "weather.csv", content, "text/csv"},
		{"a browser's catch-all declaration", mimeTypeOctetStream, "weather.csv", content, "text/csv"},
		{"a specific and wrong declaration", "application/vnd.ms-excel", "weather.csv", content, "text/csv"},
		{"a declaration the content agrees with", "text/plain", "notes.txt", prose.Bytes(), "text/plain"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, s3 := newMockStore(), newMockS3()
			h := NewHandler(Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"},
				okExtractor, nil)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, streamUploadRequest(t, tt.filename, tt.declared, tt.content))

			require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
			body := decodeJSON(t, rec.Body)
			assert.Equal(t, tt.want, body["mime_type"])
			assert.EqualValues(t, len(tt.content), body["size_bytes"])
			require.Len(t, s3.objects, 1)
			for _, stored := range s3.objects {
				assert.Equal(t, tt.content, stored,
					"the object stored must be byte-identical to the file uploaded")
			}
		})
	}
}

// TestUpload_FilePartMustBeLast pins the order the routes read. The file part
// is handed to the uploader where the walk finds it, so a part behind it is
// metadata that would be silently dropped. It is refused instead, and because
// the refusal happens while the file is being read, the uploader aborts and
// neither an object nor a record survives.
func TestUpload_FilePartMustBeLast(t *testing.T) {
	build := func(t *testing.T) *http.Request {
		t.Helper()
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for _, f := range streamFields {
			require.NoError(t, w.WriteField(f.name, f.value))
		}
		writeFilePart(t, w, "weather.csv", "text/csv", []byte("day,high\nmon,71\n"))
		// The part the walk will never reach.
		require.NoError(t, w.WriteField("tags", "weather"))
		require.NoError(t, w.Close())

		req := httptest.NewRequestWithContext(context.Background(),
			http.MethodPost, "/api/v1/resources", &buf)
		req.Header.Set("Content-Type", w.FormDataContentType())
		return req
	}

	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"},
		okExtractor, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, build(t))

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "must be the last part")
	assert.Empty(t, s3.objects, "a refused create must leave no object behind")
	assert.Empty(t, store.resources, "a refused create must leave no record behind")
}

// TestUpload_FieldsAfterTheFileAreRefusedNotDropped is the same rule stated as
// what it protects: a form that put its metadata behind the file is told so
// rather than having that metadata quietly ignored.
func TestUpload_FieldsBeforeTheFileAreRequired(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	writeFilePart(t, w, "weather.csv", "text/csv", []byte("day,high\nmon,71\n"))
	for _, f := range streamFields {
		require.NoError(t, w.WriteField(f.name, f.value))
	}
	require.NoError(t, w.Close())

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"},
		okExtractor, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// The fields never arrived in time to be read, so the create is refused on
	// the first of them the validator reaches -- before any byte is stored.
	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assert.Empty(t, s3.objects, "nothing is stored for a create that was never valid")
}

// TestUpload_AFieldCannotBeAFile bounds a metadata part. The walk reads each
// field into memory, which is only safe because a part labeled as a field is
// held to a size no field reaches.
func TestUpload_AFieldCannotBeAFile(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("scope", "global"))
	require.NoError(t, w.WriteField("description", string(bytes.Repeat([]byte("a"), maxFormFieldBytes+1))))
	writeFilePart(t, w, "weather.csv", "text/csv", []byte("day,high\nmon,71\n"))
	require.NoError(t, w.Close())

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"},
		okExtractor, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "description field is too long")
	assert.Empty(t, s3.objects)
}

// TestUpload_JustOverTheCeilingIsRefusedByTheCeiling covers the gap between the
// ceiling and the body bound above it. A file in that range passes
// MaxBytesReader, so only the ceiling on the file's own bytes can refuse it --
// and it has to refuse by the deployment's number.
func TestUpload_JustOverTheCeilingIsRefusedByTheCeiling(t *testing.T) {
	const ceiling = 1 << 20

	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{
		Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp",
		MaxUploadBytes: ceiling,
	}, okExtractor, nil)

	// Over the ceiling but under ceiling+multipartFramingBytes, so the body
	// bound never fires.
	content := bytes.Repeat([]byte("a"), ceiling+1)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, streamUploadRequest(t, "just-over.txt", "text/plain", content))

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "file exceeds 1 MB limit")
	assert.Empty(t, s3.objects, "a refused upload must not reach storage")
	assert.Empty(t, store.resources, "a refused upload must leave no record")
}

// TestReplaceContent_StreamsAndNeedsNoTemporaryDirectory is the second write
// route under the same two conditions: a file past the old spool budget, and
// no directory to spool it to.
func TestReplaceContent_StreamsAndNeedsNoTemporaryDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-directory")
	t.Setenv("TMPDIR", missing)
	require.Equal(t, missing, os.TempDir())

	fx := newVersionedHandler(t, okExtractor)
	seedVersionedResource(t, fx.store, fx.s3, fx.versions)

	content := bytes.Repeat([]byte("b"), 12<<20)
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	writeFilePart(t, w, "replacement.txt", "text/plain", content)
	require.NoError(t, w.Close())
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/resources/"+seedResourceID+"/content", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	rec := httptest.NewRecorder()
	fx.handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())
	body := decodeJSON(t, rec.Body)
	assert.EqualValues(t, len(content), body["size_bytes"])

	versions := fx.versions.byResource[seedResourceID]
	require.Len(t, versions, 2, "the replacement is recorded as the next version")
	assert.EqualValues(t, len(content), versions[1].SizeBytes)
	assert.Equal(t, content, fx.s3.objects[versions[1].S3Key],
		"the version's object is the file that was uploaded")
}

// TestUpload_ABodyThatRunsOutBeforeTheFileIsRefusedByTheCeiling covers the
// backstop firing while the walk is still reading metadata. The body bound is
// reached before the file part exists, and the refusal still has to name the
// size, not report a form that would not parse: to whoever is uploading, the
// request was too big either way.
func TestUpload_ABodyThatRunsOutBeforeTheFileIsRefusedByTheCeiling(t *testing.T) {
	const ceiling = 1 << 20

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("scope", "global"))
	// Enough near-maximum fields to run the whole body past ceiling plus the
	// framing headroom, without any one of them reaching the field bound.
	filler := string(bytes.Repeat([]byte("a"), maxFormFieldBytes-1))
	for i := range (ceiling+multipartFramingBytes)/(maxFormFieldBytes-1) + 2 {
		require.NoError(t, w.WriteField(fmt.Sprintf("spare-%d", i), filler))
	}
	writeFilePart(t, w, "weather.csv", "text/csv", []byte("day,high\nmon,71\n"))
	require.NoError(t, w.Close())

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{
		Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp",
		MaxUploadBytes: ceiling,
	}, okExtractor, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "file exceeds 1 MB limit")
	assert.Empty(t, s3.objects)
}

// TestUpload_APartWithNoNameIsRefused pins the other way the walk stops before
// the file. Every part these routes read is addressed by name, so a part
// carrying none is refused rather than drawn to find out it was never wanted.
func TestUpload_APartWithNoNameIsRefused(t *testing.T) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	head := make(textproto.MIMEHeader)
	head.Set("Content-Disposition", "form-data")
	part, err := w.CreatePart(head)
	require.NoError(t, err)
	_, err = part.Write([]byte("nowhere to put this"))
	require.NoError(t, err)
	writeFilePart(t, w, "weather.csv", "text/csv", []byte("day,high\nmon,71\n"))
	require.NoError(t, w.Close())

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())

	store, s3 := newMockStore(), newMockS3()
	h := NewHandler(Deps{Store: store, S3Client: s3, S3Bucket: "test-bucket", URIScheme: "mcp"},
		okExtractor, nil)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
	assert.Contains(t, rec.Body.String(), "must carry a name")
	assert.Empty(t, s3.objects)
}
