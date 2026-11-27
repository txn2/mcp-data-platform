package resourcedownload

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/contenturl"
	"github.com/txn2/mcp-data-platform/pkg/portal/shareguest"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

var master = []byte("0123456789abcdef0123456789abcdef")

type fakeStore map[string]*resource.Resource

func (f fakeStore) Get(_ context.Context, id string) (*resource.Resource, error) {
	if r, ok := f[id]; ok {
		return r, nil
	}
	return nil, errors.New("resource not found")
}

type fakeObjects struct {
	objects map[string][]byte
}

func (*fakeObjects) PutObject(context.Context, string, string, []byte, string) error { return nil }
func (*fakeObjects) PutObjectStream(context.Context, string, string, io.Reader, string) (int64, error) {
	return 0, nil
}

func (f *fakeObjects) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	b, ok := f.objects[key]
	if !ok {
		return nil, "", errors.New("no such key")
	}
	return b, "", nil
}
func (*fakeObjects) DeleteObject(context.Context, string, string) error { return nil }

// memUses records single downloads in memory.
type memUses struct {
	used map[string]bool
	err  error
}

func (m *memUses) claim(_ context.Context, id, _ string, _, _ time.Time) (bool, error) {
	if m.err != nil {
		return false, m.err
	}
	if m.used[id] {
		return false, nil
	}
	m.used[id] = true
	return true, nil
}

func newService(t *testing.T, now time.Time) (*Service, *http.ServeMux) {
	t.Helper()
	store := fakeStore{
		"r1": {ID: "r1", Filename: "invoice 7.pdf", MIMEType: "application/pdf", S3Key: "k1", SizeBytes: 8},
		"r2": {ID: "r2", Filename: "page.html", MIMEType: "text/html", S3Key: "k2", SizeBytes: 6},
		"r3": {ID: "r3", Filename: "gone.pdf", MIMEType: "application/pdf", S3Key: "missing"},
	}
	objects := &fakeObjects{objects: map[string][]byte{"k1": []byte("%PDF-1.7"), "k2": []byte("<html>")}}
	s := New(Options{Master: master, Base: "https://data.example.com/", Store: store, Objects: objects, Bucket: "resources", Uses: &memUses{used: map[string]bool{}}})
	require.NotNil(t, s)
	s.Now = func() time.Time { return now }
	mux := http.NewServeMux()
	s.Mount(mux)
	return s, mux
}

func get(mux *http.ServeMux, link string) *httptest.ResponseRecorder {
	return send(mux, http.MethodGet, link, "")
}

func send(mux *http.ServeMux, method, link, rangeHeader string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), method, strings.TrimPrefix(link, "https://data.example.com"), http.NoBody)
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

// TestMint_ASingleDownload: the default link is on the public address, ends
// in the file's name, serves the file as a download with no session once,
// lets a HEAD request and byte-range reads through before that without
// spending it, refuses a second full download, and ends after an hour if it
// is never downloaded.
func TestMint_ASingleDownload(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	s, mux := newService(t, now)
	link, err := s.Mint(&resource.Resource{ID: "r1", Filename: "invoice 7.pdf"}, "")
	require.NoError(t, err)
	assert.Equal(t, SingleDownload, link.Expires)
	assert.True(t, strings.HasPrefix(link.URL, "https://data.example.com"+RoutePrefix), link.URL)
	assert.True(t, strings.HasSuffix(link.URL, "/invoice%207.pdf"), link.URL)
	assert.Equal(t, now.Add(time.Hour), link.ExpiresAt)

	assert.Equal(t, http.StatusOK, send(mux, http.MethodHead, link.URL, "").Code, "a HEAD request")
	assert.Equal(t, http.StatusPartialContent, send(mux, http.MethodGet, link.URL, "bytes=0-3").Code, "a byte-range read")
	rec := get(mux, link.URL)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "%PDF-1.7", rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	rec = get(mux, link.URL)
	assert.Equal(t, http.StatusForbidden, rec.Code, "a second full download")
	assert.Contains(t, rec.Body.String(), "has been used")

	unused, err := s.Mint(&resource.Resource{ID: "r1", Filename: "invoice 7.pdf"}, SingleDownload)
	require.NoError(t, err)
	s.Now = func() time.Time { return now.Add(time.Hour) }
	rec = get(mux, unused.URL)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Contains(t, rec.Body.String(), "expired")
}

// TestMint_SixHours: a six-hour link serves every download until it expires,
// and a kind there is none of is refused.
func TestMint_SixHours(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	s, mux := newService(t, now)
	link, err := s.Mint(&resource.Resource{ID: "r1", Filename: "a.pdf"}, SixHours)
	require.NoError(t, err)
	assert.Equal(t, now.Add(6*time.Hour), link.ExpiresAt)
	link.URL = strings.Replace(link.URL, "/a.pdf", "/invoice%207.pdf", 1)
	for range 3 {
		assert.Equal(t, http.StatusOK, get(mux, link.URL).Code)
	}
	s.Now = func() time.Time { return now.Add(6 * time.Hour) }
	assert.Equal(t, http.StatusForbidden, get(mux, link.URL).Code)
	_, err = s.Mint(&resource.Resource{ID: "r1", Filename: "a.pdf"}, "2h")
	require.ErrorIs(t, err, ErrBadExpiry)
}

// TestServe_ARecordThatFails: a single download whose use cannot be recorded
// is not served, and says to try again.
func TestServe_ARecordThatFails(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	s, mux := newService(t, now)
	s.uses = &memUses{err: errors.New("db down")}
	link, err := s.Mint(&resource.Resource{ID: "r1", Filename: "invoice 7.pdf"}, "")
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, get(mux, link.URL).Code)
}

// TestServe_Refusals: an altered token, a token of another key (an asset
// content URL's), a name that is not the file's, and a file since deleted are
// refused; HTML is served as a download, never rendered.
func TestServe_Refusals(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	s, mux := newService(t, now)
	link, err := s.Mint(&resource.Resource{ID: "r1", Filename: "invoice 7.pdf"}, "")
	require.NoError(t, err)
	token, _, _ := strings.Cut(strings.TrimPrefix(link.URL, "https://data.example.com"+RoutePrefix), "/")

	altered := RoutePrefix + token[:len(token)-2] + "AA/invoice%207.pdf"
	assert.Equal(t, http.StatusForbidden, get(mux, altered).Code)

	asset := contenturl.Sign(shareguest.DeriveKey(master, contenturl.KeyLabel), contenturl.Target{ID: "r1", Expires: now.Add(time.Hour)})
	assert.Equal(t, http.StatusForbidden, get(mux, RoutePrefix+asset+"/invoice%207.pdf").Code, "an asset link is not a download link")

	assert.Equal(t, http.StatusNotFound, get(mux, RoutePrefix+token+"/other.pdf").Code)

	gone, err := s.Mint(&resource.Resource{ID: "r9", Filename: "x.pdf"}, "")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, get(mux, gone.URL).Code)

	missing, err := s.Mint(&resource.Resource{ID: "r3", Filename: "gone.pdf"}, "")
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, get(mux, missing.URL).Code, "an object missing from storage")

	page, err := s.Mint(&resource.Resource{ID: "r2", Filename: "page.html"}, "")
	require.NoError(t, err)
	rec := get(mux, page.URL)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
}

// TestNew_NeedsAKeyAndAnAddress: a deployment with no signing key or no
// public address hands out no links, and says what it needs.
func TestNew_NeedsAKeyAndAnAddress(t *testing.T) {
	store := fakeStore{}
	uses := &memUses{used: map[string]bool{}}
	assert.Nil(t, New(Options{Base: "https://x", Store: store, Uses: uses}))
	assert.Nil(t, New(Options{Master: master, Base: " ", Store: store, Uses: uses}))
	assert.Nil(t, New(Options{Master: master, Base: "https://x", Uses: uses}))
	assert.Nil(t, New(Options{Master: master, Base: "https://x", Store: store, Uses: NewUses(nil)}), "no database to record single downloads in")
	var s *Service
	_, err := s.Mint(&resource.Resource{ID: "r1"}, "")
	require.ErrorIs(t, err, ErrUnavailable)
	assert.Contains(t, err.Error(), "portal.public_base_url")
	s.Mount(http.NewServeMux())

	noBlobs := New(Options{Master: master, Base: "https://x", Store: fakeStore{"r1": {ID: "r1", Filename: "a.pdf", S3Key: "k"}}, Uses: uses})
	mux := http.NewServeMux()
	noBlobs.Mount(mux)
	link, err := noBlobs.Mint(&resource.Resource{ID: "r1", Filename: "a.pdf"}, "")
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, strings.TrimPrefix(link.URL, "https://x"), http.NoBody))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
