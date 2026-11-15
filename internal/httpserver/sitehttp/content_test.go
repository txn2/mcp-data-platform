package sitehttp

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

const contentRoute = siteBase + "/file/content"

// rangedObjects is the objects store with the range read the portal's own
// store has, so the route serves by range rather than whole.
type rangedObjects struct{ *memObjects }

func (o rangedObjects) GetObjectRange(_ context.Context, _, key string, offset, length int64) (body []byte, size int64, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	data := o.put[key]
	end := min(int64(len(data)), offset+length)
	if offset >= int64(len(data)) {
		return nil, int64(len(data)), nil
	}
	return data[offset:end], int64(len(data)), nil
}

// mediaFiles files release n of dev holding the viewer's file kinds.
func (f *fixture) mediaFiles(n int, logo string) {
	id := f.release(n, site.EnvDev)
	files := make([]site.File, 0, 4)
	for _, file := range []struct{ path, ct, body string }{
		{"img/logo.png", "image/png", logo},
		{"media/intro.mp4", "video/mp4", strings.Repeat("v", 4096)},
		{"logo.svg", "image/svg+xml", `<svg><script>alert(1)</script></svg>`},
		{"index.html", "text/html; charset=utf-8", "<script>alert(1)</script>"},
	} {
		sf := site.File{Path: file.path, SHA256: "sum-" + strconv.Itoa(n) + file.path, ContentType: file.ct, Size: int64(len(file.body))}
		f.objects.put[sitesvc.ObjectKey(siteID, sf, "")] = []byte(file.body)
		files = append(files, sf)
	}
	f.store.files[id] = files
}

func (f *fixture) get(t *testing.T, target string, u *portal.User, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	maps.Copy(req.Header, header)
	if u != nil {
		req = req.WithContext(portal.ContextWithUser(req.Context(), u))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

// TestFileContent pins #2629's route: bytes through blobserve, scriptable
// kinds as attachments, ranges answered, a release's own bytes, the refusal
// /file gives, and no path outside the tree.
func TestFileContent(t *testing.T) {
	f := newFixture(t)
	f.rt.Service.Objects = rangedObjects{f.objects}
	f.mediaFiles(1, "PNG-ONE")
	f.mediaFiles(2, "PNG-TWO")

	rec := f.get(t, contentRoute+"?path=img/logo.png", userOwner, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "inline")
	assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox")
	assert.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
	assert.Contains(t, rec.Header().Get("Cache-Control"), "private")
	assert.Equal(t, "PNG-TWO", rec.Body.String(), "without release, dev's file")

	rec = f.get(t, contentRoute+"?path=img/logo.png&release=1", userOwner, nil)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "PNG-ONE", rec.Body.String(), "with release, that release's bytes")

	rec = f.get(t, contentRoute+"?path=media/intro.mp4", userOwner, http.Header{"Range": {"bytes=0-99"}})
	require.Equal(t, http.StatusPartialContent, rec.Code)
	assert.Equal(t, 100, rec.Body.Len())

	for _, p := range []string{"logo.svg", "index.html"} {
		rec = f.get(t, contentRoute+"?path="+p, userOwner, nil)
		require.Equal(t, http.StatusOK, rec.Code, p)
		assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment", p)
		assert.Contains(t, rec.Header().Get("Content-Security-Policy"), "sandbox", p)
	}

	assert.Equal(t, http.StatusForbidden, f.get(t, contentRoute+"?path=img/logo.png", userStranger, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.get(t, contentRoute+"?path=img/logo.png&release=1", userStranger, nil).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, f.get(t, contentRoute+"?path=../x", userOwner, nil).Code)
	assert.Equal(t, http.StatusUnprocessableEntity, f.get(t, contentRoute+"?path=missing.png", userOwner, nil).Code)
	assert.Equal(t, http.StatusBadRequest, f.get(t, contentRoute+"?path=img/logo.png&release=x", userOwner, nil).Code)
	assert.Equal(t, http.StatusUnauthorized, f.get(t, contentRoute+"?path=img/logo.png", nil, nil).Code)

	rec = f.get(t, contentRoute+"?path=_headers", userOwner, nil)
	require.Equal(t, http.StatusOK, rec.Code, "the _headers file answers its rendered text")
}

// TestFileContent_AViewerReadsWhatIsServedToThem: someone dev is shared with
// reads dev's file, and a release only while an environment they can open
// serves it.
func TestFileContent_AViewerReadsWhatIsServedToThem(t *testing.T) {
	f := newFixture(t)
	f.mediaFiles(1, "PNG-ONE")
	f.mediaFiles(2, "PNG-TWO")
	rec := f.get(t, contentRoute+"?path=img/logo.png", userViewer, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "PNG-TWO", rec.Body.String(), "the store without range reads serves the whole object")
	assert.Equal(t, http.StatusOK, f.get(t, contentRoute+"?path=img/logo.png&release=2", userViewer, nil).Code)
	assert.Equal(t, http.StatusForbidden, f.get(t, contentRoute+"?path=img/logo.png&release=1", userViewer, nil).Code,
		"a release no environment they open serves")
}
