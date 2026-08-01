package resource

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pdfBytes is a minimal PDF header, enough for http.DetectContentType.
var pdfBytes = []byte("%PDF-1.7\n1 0 obj\n<<>>\nendobj\n")

// TestUploadContentTypeDenylist drives the real upload endpoint across the
// boundary the resource library keeps: a denylist, not an allowlist. A resource
// is human-uploaded reference material, so the long tail of document, archive
// and vendor formats has to get through; what is refused is the executable set
// and the one document family a browser renders natively with script.
func TestUploadContentTypeDenylist(t *testing.T) {
	tests := []struct {
		name     string
		declared string
		filename string
		content  []byte
		accept   bool
	}{
		{name: "pdf", declared: "application/pdf", filename: "report.pdf", content: pdfBytes, accept: true},
		{
			name:     "an office document nothing in the platform models",
			declared: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			filename: "template.docx",
			content:  []byte("PK\x03\x04 not really a docx"),
			accept:   true,
		},
		{
			name:     "a vendor format the platform has never heard of",
			declared: "image/vnd.dwg",
			filename: "floorplan.dwg",
			content:  []byte("AC1027 drawing bytes"),
			accept:   true,
		},
		{
			name:     "undeclared type is detected and accepted",
			declared: "",
			filename: "report.pdf",
			content:  pdfBytes,
			accept:   true,
		},
		{
			name:     "xhtml refused",
			declared: "application/xhtml+xml",
			filename: "page.xhtml",
			content:  []byte("<html><body/></html>"),
			accept:   false,
		},
		{
			name:     "xhtml refused with a charset parameter",
			declared: "application/xhtml+xml; charset=utf-8",
			filename: "page.xhtml",
			content:  []byte("<html><body/></html>"),
			accept:   false,
		},
		{
			name:     "windows executable refused",
			declared: "application/x-msdownload",
			filename: "setup.dat",
			content:  []byte("MZ payload"),
			accept:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := newMockStore()
			s3 := newMockS3()
			h := newTestHandler(store, s3, okExtractor)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, uploadRequest(t, tt.declared, tt.filename, tt.content))

			if tt.accept {
				require.Equal(t, http.StatusCreated, rec.Code, "body: %s", rec.Body.String())
				assert.Len(t, s3.objects, 1, "accepted upload must reach storage")
				return
			}
			require.Equal(t, http.StatusBadRequest, rec.Code, "body: %s", rec.Body.String())
			assert.Empty(t, s3.objects, "refused upload must not reach storage")
		})
	}
}

// uploadRequest builds a resource-create request whose file part declares
// declared, or declares nothing when it is empty.
func uploadRequest(t *testing.T, declared, filename string, content []byte) *http.Request {
	t.Helper()

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range map[string]string{
		"scope":        "global",
		"category":     "references",
		"display_name": "Reference material",
		"description":  "Uploaded for the content-type test.",
	} {
		require.NoError(t, w.WriteField(k, v))
	}

	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	if declared != "" {
		header.Set("Content-Type", declared)
	}
	part, err := w.CreatePart(header)
	require.NoError(t, err)
	_, err = part.Write(content)
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/v1/resources", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}
