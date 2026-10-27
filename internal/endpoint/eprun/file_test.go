package eprun

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcollect"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// photoForm takes up to two images and refuses a submission named refuse;
// it confirms with the address its first photo is saved at.
const photoForm = `
def form_spec(req):
    """The photo form."""
    return {"fields": [
        {"name": "name", "type": "text", "required": True},
        {"name": "photos", "type": "file", "max_files": 2, "max_bytes": 4096, "accept": ["image/*"]},
    ]}

def form_submit(req, values):
    """Refuses one name; confirms with the first photo's address."""
    if values["name"] == "refuse":
        return {"errors": {"name": "not this one"}}
    photos = values["photos"]
    return {"confirmation": photos[0]["uri"] if photos else "none"}
`

var pngFile = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

// library saves files in memory.
type library struct {
	saved   map[string][]byte
	removed int
}

func (*library) FileURI(_ whsource.Source, f epcollect.File) string {
	return "mcp://persona/admin/" + f.EventID + "/" + f.Field + "/" + f.Name
}

func (l *library) PutFile(_ context.Context, src whsource.Source, f epcollect.File) (uri, id string, err error) {
	if l.saved == nil {
		l.saved = map[string][]byte{}
	}
	l.saved[f.Name] = f.Data
	return l.FileURI(src, f), "res-" + f.Name, nil
}

func (l *library) DeleteFile(context.Context, string) error { l.removed++; return nil }

func photoFixture(t *testing.T, c *epcollect.Collector) (*fixture, *time.Time) {
	t.Helper()
	f := newFixture(t, photoForm)
	now := time.Unix(1_800_000_000, 0)
	f.runner.FormKey, f.runner.Now = formKey, func() time.Time { return now }
	f.runner.SetCollector(c)
	return f, &now
}

func multipartSubmission(t *testing.T, token, name string, files map[string][]byte) (contentType string, body []byte) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("name", name))
	require.NoError(t, w.WriteField("form_token", token))
	for fname, data := range files {
		part, err := w.CreateFormFile("photos", fname)
		require.NoError(t, err)
		_, err = part.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return w.FormDataContentType(), buf.Bytes()
}

func postMultipart(t *testing.T, f *fixture, ct string, body []byte) (resp Response, out map[string]any) {
	t.Helper()
	resp = f.serve(t, Request{Method: "POST", Header: http.Header{"Content-Type": {ct}}, Body: body})
	return resp, decode(t, resp.Body)
}

// #2333: an accepted submission's files are saved as managed resources, the
// stored event names them, and form_submit was handed the same addresses; a
// refused one saves nothing.
func TestFiles_ASubmissionsFilesAreSavedOnlyWhenAccepted(t *testing.T) {
	lib := &library{}
	d := &deliveries{}
	f, now := photoFixture(t, &epcollect.Collector{Sources: formSource{found: true}, Writer: d, Files: lib, MaxFileBytes: 1 << 20})
	_, token := showForm(t, f, nil)
	*now = now.Add(5 * time.Second)

	ct, body := multipartSubmission(t, token, "refuse", map[string][]byte{"logo.png": pngFile})
	resp, out := postMultipart(t, f, ct, body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.Status, string(resp.Body))
	assert.Equal(t, map[string]any{"name": "not this one"}, out["errors"])
	assert.Empty(t, lib.saved, "a submission form_submit refuses keeps no file")
	assert.Empty(t, d.events)

	ct, body = multipartSubmission(t, token, "Ana", map[string][]byte{"logo.png": pngFile})
	resp, out = postMultipart(t, f, ct, body)
	require.Equal(t, http.StatusOK, resp.Status, string(resp.Body))
	require.Len(t, d.events, 1)
	uri := "mcp://persona/admin/" + d.events[0].EventID + "/photos/logo.png"
	assert.Equal(t, uri, out["confirmation"], "form_submit was handed the address the file is saved at")
	assert.Equal(t, pngFile, lib.saved["logo.png"])
	var payload map[string]any
	require.NoError(t, json.Unmarshal(d.events[0].Payload, &payload))
	values, ok := payload["values"].(map[string]any)
	require.True(t, ok)
	photos, ok := values["photos"].([]any)
	require.True(t, ok)
	photo, ok := photos[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{"name": "logo.png", "content_type": "image/png", "size": float64(len(pngFile)), "uri": uri, "resource_id": "res-logo.png"}, photo)

	ct, body = multipartSubmission(t, token, "Ana", map[string][]byte{"run.exe": pngFile})
	resp, out = postMultipart(t, f, ct, body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	assert.Contains(t, fieldError(t, out, "photos"), `"run.exe" cannot be saved`)

	ct, body = multipartSubmission(t, token, "Ana", map[string][]byte{"notes.txt": []byte("plain text")})
	resp, out = postMultipart(t, f, ct, body)
	require.Equal(t, http.StatusUnprocessableEntity, resp.Status)
	assert.Contains(t, fieldError(t, out, "photos"), "which this field does not take")
	assert.Len(t, lib.saved, 1, "only the accepted submission's file was saved")
}

// #2333: a submission whose files cannot be saved here is answered 503, and a
// form with a file field is not shown where its files cannot be saved or past
// the deployment's upload limit.
func TestFiles_WhereFilesCannotBeSaved(t *testing.T) {
	f, now := photoFixture(t, &epcollect.Collector{Sources: formSource{}, Writer: &deliveries{}, Files: &library{}})
	_, token := showForm(t, f, nil)
	*now = now.Add(5 * time.Second)
	ct, body := multipartSubmission(t, token, "Ana", map[string][]byte{"logo.png": pngFile})
	resp, _ := postMultipart(t, f, ct, body)
	assert.Equal(t, http.StatusServiceUnavailable, resp.Status, "the form has no source to save its files through")

	noFiles, _ := photoFixture(t, &epcollect.Collector{Sources: formSource{found: true}, Writer: &deliveries{}})
	assert.Equal(t, http.StatusInternalServerError, noFiles.serve(t, Request{Method: "GET"}).Status)

	small, _ := photoFixture(t, &epcollect.Collector{Sources: formSource{found: true}, Writer: &deliveries{}, Files: &library{}, MaxFileBytes: 1024})
	assert.Equal(t, http.StatusInternalServerError, small.serve(t, Request{Method: "GET"}).Status, "max_bytes past the upload limit")
}

// fieldError is the error an answer names for field.
func fieldError(t *testing.T, out map[string]any, field string) string {
	t.Helper()
	errs, ok := out["errors"].(map[string]any)
	require.True(t, ok, "%v", out)
	msg, _ := errs[field].(string)
	return msg
}
