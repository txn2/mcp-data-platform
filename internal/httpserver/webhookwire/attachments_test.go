package webhookwire

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcollect"
	"github.com/txn2/mcp-data-platform/internal/webhook/whimap"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

var inbox = whsource.Source{Name: "inbox", Config: whsource.Config{Input: whsource.InputIMAP, Persona: "analyst"}}

func TestPutFile_CreatesThenReplaces(t *testing.T) {
	h, w := newWindowResources(fakeByURI{err: sql.ErrNoRows})
	uri, id, err := h.PutFile(ctx, inbox, whimap.File{Folder: "webhooks/inbox/attachments/2026-10-21/inbox-7-3", Name: "minutes.pdf", ContentType: "application/pdf", Data: []byte("%PDF")})
	require.NoError(t, err)
	assert.Equal(t, "new", id)
	assert.Empty(t, uri, "the fake writer stamps no address")
	require.Len(t, w.created, 1)
	got := w.created[0]
	assert.Equal(t, resource.ScopePersona, got.Scope)
	assert.Equal(t, "analyst", got.ScopeID, "an attachment lands in the source persona's library")
	assert.Equal(t, "webhooks/inbox/attachments/2026-10-21/inbox-7-3", got.Path)
	assert.Equal(t, []string{"webhook", "mail", "inbox"}, got.Tags)
	assert.Equal(t, "minutes.pdf", got.DisplayName)

	h.byURI = fakeByURI{found: &resource.Resource{ID: "new"}}
	_, id, err = h.PutFile(ctx, inbox, whimap.File{Folder: "webhooks/inbox/attachments/2026-10-21/inbox-7-3", Name: "minutes.pdf", ContentType: "application/pdf", Data: []byte("%PDF-2")})
	require.NoError(t, err)
	assert.Equal(t, "new", id)
	assert.Equal(t, []string{"new"}, w.replaced, "the same message read again is a new version, not a second file")
}

func TestPutFile_Refusals(t *testing.T) {
	h, w := newWindowResources(fakeByURI{err: errBoom})
	_, _, err := h.PutFile(ctx, inbox, whimap.File{Folder: "f", Name: "a.txt", ContentType: "text/plain", Data: nil})
	require.ErrorIs(t, err, errBoom)

	h.byURI = fakeByURI{err: sql.ErrNoRows}
	w.err["create"] = errBoom
	_, _, err = h.PutFile(ctx, inbox, whimap.File{Folder: "f", Name: "a.txt", ContentType: "text/plain", Data: nil})
	require.ErrorIs(t, err, errBoom)

	h.byURI = fakeByURI{found: &resource.Resource{ID: "gone"}}
	_, _, err = h.PutFile(ctx, inbox, whimap.File{Folder: "f", Name: "a.txt", ContentType: "text/plain", Data: nil})
	require.Error(t, err, "a replace of a resource that vanished is reported")
}

func TestDeleteAttachments_RemovesTheFolder(t *testing.T) {
	h, w := newWindowResources(fakeByURI{})
	for i := range attachmentsPage + 5 {
		id := fmt.Sprintf("a%03d", i)
		w.rows[id] = &resource.Resource{ID: id}
	}
	var asked []resource.Filter
	h.byURI = fakeByURI{list: func(f resource.Filter) ([]resource.Resource, error) {
		asked = append(asked, f)
		ids := make([]string, 0, len(w.rows))
		for id := range w.rows {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		page := ids[:min(f.Limit, len(ids))]
		out := make([]resource.Resource, 0, len(page))
		for _, id := range page {
			out = append(out, *w.rows[id])
		}
		return out, nil
	}}
	require.NoError(t, h.DeleteAttachments(ctx, inbox))
	assert.Empty(t, w.rows)
	require.Len(t, asked, 2, "a full page is followed by another listing")
	assert.Equal(t, "webhooks/inbox/attachments", asked[0].Path)
	assert.Equal(t, []resource.ScopeFilter{{Scope: resource.ScopePersona, ScopeID: "analyst"}}, asked[0].Scopes)
}

func TestDeleteAttachments_Refusals(t *testing.T) {
	h, w := newWindowResources(fakeByURI{list: func(resource.Filter) ([]resource.Resource, error) { return nil, errBoom }})
	require.ErrorIs(t, h.DeleteAttachments(ctx, inbox), errBoom)

	w.rows["a"] = &resource.Resource{ID: "a"}
	h.byURI = fakeByURI{list: func(resource.Filter) ([]resource.Resource, error) { return []resource.Resource{{ID: "a"}}, nil }}
	w.err["delete"] = errBoom
	require.ErrorIs(t, h.DeleteAttachments(ctx, inbox), errBoom)

	delete(w.err, "delete")
	stuck := make([]resource.Resource, attachmentsPage)
	for i := range stuck {
		stuck[i] = resource.Resource{ID: fmt.Sprintf("s%03d", i)}
	}
	h.byURI = fakeByURI{list: func(resource.Filter) ([]resource.Resource, error) { return stuck, nil }}
	assert.ErrorContains(t, h.DeleteAttachments(ctx, inbox), "still listed after its delete",
		"a listing that never empties is reported rather than read forever")
}

var contactForm = whsource.Source{Name: "form-contact", Config: whsource.Config{Input: whsource.InputForm}}

// #2333: a form's file is saved in its source's library under the folder the
// source's attachments are removed from, at the address it was promised.
func TestFormFiles(t *testing.T) {
	h, w := newWindowResources(fakeByURI{})
	h.adminPersona = "admin"
	files := formFiles{r: h}
	f := epcollect.File{
		EventID: "ev1", At: time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC), Field: "photos",
		Name: "logo.png", ContentType: "image/png", Data: []byte("png"),
	}
	folder := "webhooks/form-contact/attachments/2026-10-26/ev1/photos"
	assert.Equal(t, resource.BuildURI("mcp", resource.ScopePersona, "admin", folder, "logo.png"), files.FileURI(contactForm, f))
	assert.True(t, strings.HasPrefix(folder, whimap.AttachmentsFolder(contactForm.Name)+"/"), "removed with the source")

	_, id, err := files.PutFile(ctx, contactForm, f)
	require.NoError(t, err)
	require.Len(t, w.created, 1)
	got := w.created[0]
	assert.Equal(t, folder, got.Path)
	assert.Equal(t, "logo.png", got.Filename)
	assert.Equal(t, "image/png", got.MIMEType)
	assert.Equal(t, []string{"webhook", "form", "form-contact"}, got.Tags)
	assert.Contains(t, got.Description, "the form contact")

	require.NoError(t, files.DeleteFile(ctx, id))
	assert.Empty(t, w.rows)

	w.err["create"] = errBoom
	_, _, err = files.PutFile(ctx, contactForm, f)
	require.ErrorIs(t, err, errBoom)
}
