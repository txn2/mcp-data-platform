package epcollect

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// fakeFiles is a library that records what it saved and removed.
type fakeFiles struct {
	mu      sync.Mutex
	saved   map[string]File
	removed []string
	failOn  string
}

func (*fakeFiles) FileURI(src whsource.Source, file File) string {
	return "mcp://persona/admin/webhooks/" + src.Name + "/attachments/" + file.EventID + "/" + file.Field + "/" + file.Name
}

func (f *fakeFiles) PutFile(_ context.Context, src whsource.Source, file File) (uri, id string, err error) {
	if file.Name == f.failOn {
		return "", "", errors.New("bucket unavailable")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saved == nil {
		f.saved = map[string]File{}
	}
	id = "res-" + file.Field + "-" + file.Name
	f.saved[id] = file
	return f.FileURI(src, file), id, nil
}

func (f *fakeFiles) DeleteFile(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, id)
	delete(f.saved, id)
	return nil
}

var pngData = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")

func fileSpec() epform.Spec {
	return epform.Spec{Fields: []epform.Field{
		{Name: "name", Type: epform.TypeText, MaxLength: 100},
		{Name: "photos", Type: epform.TypeFile, MaxFiles: 3, MaxBytes: 1 << 20},
	}}
}

// checked is a submission whose files passed their spec, as eprun hands it on.
func checked(t *testing.T, uploads []epform.Upload) Submission {
	t.Helper()
	values, errs, err := epform.Check(fileSpec(), time.Unix(0, 0), map[string][]string{"name": {"Ana"}}, uploads, time.Unix(60, 0))
	require.NoError(t, err)
	require.Empty(t, errs)
	return Submission{ScriptID: "scr_1", Endpoint: "apply", Version: 1, Spec: fileSpec(), Values: values}
}

// #2333: a submission's files are given their addresses before form_submit
// runs, saved when it is stored, and named by the stored event.
func TestFiles_PrepareThenStore(t *testing.T) {
	files := &fakeFiles{}
	w := &fakeWriter{}
	at := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	c := &Collector{Sources: fakeSources{src: formSrc()}, Writer: w, Files: files, Now: func() time.Time { return at }}
	uploads := []epform.Upload{
		{Field: "photos", Name: "Site Logo.PNG", Data: pngData},
		{Field: "photos", Name: "site logo.png", Data: pngData},
	}
	sub := checked(t, uploads)

	errs, err := c.Prepare(t.Context(), &sub, uploads)
	require.NoError(t, err)
	require.Empty(t, errs)
	require.NotEmpty(t, sub.EventID)
	assert.Equal(t, at, sub.ReceivedAt)
	require.Len(t, sub.Files, 2)
	assert.Equal(t, "site-logo.png", sub.Files[0].Name)
	assert.Equal(t, "site-logo-2.png", sub.Files[1].Name, "two files of one field never share an address")
	assert.Equal(t, "image/png", sub.Files[0].ContentType)
	prepared, _ := sub.Values["photos"].([]any)
	first, _ := prepared[0].(map[string]any)
	wantURI := "mcp://persona/admin/webhooks/form-apply/attachments/" + sub.EventID + "/photos/site-logo.png"
	assert.Equal(t, wantURI, first[epform.FileKeyURI], "form_submit is handed the address the file is saved at")
	assert.Empty(t, files.saved, "nothing is saved before form_submit accepts")

	require.NoError(t, c.Store(t.Context(), sub))
	assert.Len(t, files.saved, 2)
	require.Len(t, w.events, 1)
	assert.Equal(t, sub.EventID, w.events[0].EventID)
	assert.Equal(t, at, w.events[0].ReceivedAt)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(w.events[0].Payload, &payload))
	values, ok := payload[KeyValues].(map[string]any)
	require.True(t, ok)
	photos, ok := values["photos"].([]any)
	require.True(t, ok)
	stored, ok := photos[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, wantURI, stored[epform.FileKeyURI])
	assert.Equal(t, "res-photos-site-logo.png", stored[epform.FileKeyResourceID])
	assert.Equal(t, "Site Logo.PNG", stored[epform.FileKeyName], "the name the browser sent is kept")
	assert.NotContains(t, string(w.events[0].Payload), "PNG\\r", "the event carries no bytes")
}

// #2333: a submission that is not stored keeps none of its files.
func TestFiles_NotStoredKeepsNone(t *testing.T) {
	uploads := []epform.Upload{{Field: "photos", Name: "a.png", Data: pngData}, {Field: "photos", Name: "b.png", Data: pngData}}

	files := &fakeFiles{}
	c := &Collector{Sources: fakeSources{src: formSrc()}, Writer: &fakeWriter{err: errors.New("spool full")}, Files: files}
	sub := checked(t, uploads)
	_, err := c.Prepare(t.Context(), &sub, uploads)
	require.NoError(t, err)
	require.ErrorContains(t, c.Store(t.Context(), sub), "spool full")
	assert.Empty(t, files.saved, "the event was not stored, so its files are removed")
	assert.Len(t, files.removed, 2)

	failing := &fakeFiles{failOn: "b.png"}
	c = &Collector{Sources: fakeSources{src: formSrc()}, Writer: &fakeWriter{}, Files: failing}
	sub = checked(t, uploads)
	_, err = c.Prepare(t.Context(), &sub, uploads)
	require.NoError(t, err)
	require.ErrorContains(t, c.Store(t.Context(), sub), `saving file "b.png" of field "photos": bucket unavailable`)
	assert.Empty(t, failing.saved, "a file that was saved is removed when another is not")

	c = &Collector{Sources: fakeSources{src: formSrc()}, Writer: &fakeWriter{}}
	require.ErrorIs(t, c.Store(t.Context(), sub), ErrCannotSaveFiles)
}

func TestFiles_PrepareRefusals(t *testing.T) {
	uploads := []epform.Upload{{Field: "photos", Name: "a.png", Data: pngData}}

	var none *Collector
	assert.False(t, none.CanSaveFiles())
	sub := checked(t, uploads)
	_, err := none.Prepare(t.Context(), &sub, uploads)
	require.ErrorIs(t, err, ErrCannotSaveFiles)

	c := &Collector{Sources: fakeSources{}, Writer: &fakeWriter{}, Files: &fakeFiles{}}
	_, err = c.Prepare(t.Context(), &sub, uploads)
	require.ErrorIs(t, err, ErrNoSource)

	c = &Collector{Sources: fakeSources{src: formSrc()}, Writer: &fakeWriter{}, Files: &fakeFiles{}}
	errs, err := c.Prepare(t.Context(), &sub, nil)
	require.NoError(t, err)
	assert.Empty(t, errs, "no files, nothing to prepare")

	denied := []epform.Upload{{Field: "photos", Name: "run.exe", Data: pngData}}
	sub = checked(t, denied)
	errs, err = c.Prepare(t.Context(), &sub, denied)
	require.NoError(t, err)
	assert.Contains(t, errs["photos"], `"run.exe" cannot be saved`)
	assert.Empty(t, sub.Files)

	unchecked := Submission{ScriptID: "scr_1", Values: map[string]any{}}
	_, err = c.Prepare(t.Context(), &unchecked, uploads)
	require.ErrorContains(t, err, "was not checked")
}
