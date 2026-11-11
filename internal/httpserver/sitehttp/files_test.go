package sitehttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
	"github.com/txn2/mcp-data-platform/internal/site/sitetool"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// The Files routes (#2590): every write is a publish to dev from the release
// the editor read, so a stale base is a 409 and nothing is written; a viewer
// reads and is refused writes; a dry run stores nothing.

const (
	filesRoute = siteBase + "/files"
	fileRoute  = siteBase + "/file"
	pageV1     = "<h1>Pricing $12</h1>\n"
)

// devFiles files a release of dev holding pages, each stored so the service
// reads it back.
func (f *fixture) devFiles(n int, pages map[string]string) {
	id := f.release(n, site.EnvDev)
	files := make([]site.File, 0, len(pages))
	for path, body := range pages {
		file := site.File{Path: path, SHA256: "sum-" + strconv.Itoa(n) + "-" + path, ContentType: "text/html; charset=utf-8", Size: int64(len(body))}
		f.objects.put[sitesvc.ObjectKey(siteID, file, "")] = []byte(body)
		files = append(files, file)
	}
	f.store.files[id] = files
}

func TestFiles_ListAndRead(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1, "about.html": "<h1>About</h1>"})

	got := decodeBody[sitesvc.TreeListing](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, filesRoute + "?path=", nil}, userOwner))
	assert.Equal(t, 1, got.Release)
	assert.Equal(t, 2, got.Total)
	f.statusIs(t, http.StatusOK, route{http.MethodGet, filesRoute, nil}, userViewer)
	f.statusIs(t, http.StatusForbidden, route{http.MethodGet, filesRoute, nil}, userStranger)
	f.statusIs(t, http.StatusUnauthorized, route{http.MethodGet, filesRoute, nil}, nil)

	text := decodeBody[FileTextView](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, fileRoute + "?path=index.html", nil}, userOwner))
	assert.Equal(t, pageV1, text.Body)
	assert.Equal(t, 1, text.Release, "the release a save names as its base")
	f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodGet, fileRoute + "?path=missing.html", nil}, userOwner)
	f.statusIs(t, http.StatusForbidden, route{http.MethodGet, fileRoute + "?path=index.html", nil}, userStranger)
	f.statusIs(t, http.StatusUnauthorized, route{http.MethodGet, fileRoute + "?path=index.html", nil}, nil)
}

func TestFiles_AFileThatIsNotTextIsRefusedAsSuch(t *testing.T) {
	f := newFixture(t)
	id := f.release(1, site.EnvDev)
	f.store.files[id] = []site.File{{Path: "logo.png", SHA256: "png", ContentType: "image/png", Size: 4}}
	rec := f.statusIs(t, http.StatusUnprocessableEntity, route{http.MethodGet, fileRoute + "?path=logo.png", nil}, userOwner)
	assert.Contains(t, rec.Body.String(), "image/png")
}

func TestFiles_SaveMakesAReleaseOfDevFromTheOneRead(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})

	out := decodeBody[sitesvc.Published](t, f.statusIs(t, http.StatusOK, route{
		http.MethodPut, fileRoute,
		FileSaveBody{Path: "index.html", Content: "<h1>Pricing $15</h1>\n", Base: 1, Note: "Starter price"},
	}, userManager))
	assert.Equal(t, 2, out.Release.Number)
	assert.Equal(t, 1, out.Release.Base, "derived from the release the editor read")
	assert.Equal(t, "edit index.html", out.Release.Source)
	assert.Equal(t, userManager.Email, out.Release.CreatedBy, "a hand edit is attributed to the person")
	assert.Equal(t, 2, f.store.sites[siteID].Environment(site.EnvDev).Release, "dev serves it at once")
	require.NotEmpty(t, f.audit.events)
	last := f.audit.events[len(f.audit.events)-1]
	assert.Equal(t, sitetool.CmdPublish, last.Parameters["command"])
	assert.Equal(t, "index.html", last.Parameters[paramPath])
}

func TestFiles_ASaveAgainstAnOlderReleaseIsAConflict(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})
	f.devFiles(2, map[string]string{"index.html": "<h1>Someone else</h1>"})

	rec := f.statusIs(t, http.StatusConflict, route{http.MethodPut, fileRoute, FileSaveBody{Path: "index.html", Content: "mine", Base: 1}}, userOwner)
	assert.Contains(t, rec.Body.String(), "does not match the current version 2")
	assert.Equal(t, 2, f.store.sites[siteID].Environment(site.EnvDev).Release, "nothing was written")
	assert.Len(t, f.store.releases[siteID], 2)
}

func TestFiles_SaveRefusals(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})
	for name, c := range map[string]struct {
		user *portal.User
		body any
		want int
	}{
		"no base would replace the whole site": {userOwner, FileSaveBody{Path: "index.html", Content: "x"}, http.StatusBadRequest},
		"not JSON":                             {userOwner, "{", http.StatusBadRequest},
		"not base64":                           {userOwner, FileSaveBody{Path: "a.bin", Content: "%%%", Encoding: "base64", Base: 1}, http.StatusBadRequest},
		"a path no release can hold":           {userOwner, FileSaveBody{Path: "../etc/passwd", Content: "x", Base: 1}, http.StatusUnprocessableEntity},
		"someone dev is shared with":           {userViewer, FileSaveBody{Path: "index.html", Content: "x", Base: 1}, http.StatusForbidden},
		"someone with no access":               {userStranger, FileSaveBody{Path: "index.html", Content: "x", Base: 1}, http.StatusForbidden},
		"nobody signed in":                     {nil, FileSaveBody{Path: "index.html", Content: "x", Base: 1}, http.StatusUnauthorized},
	} {
		t.Run(name, func(t *testing.T) {
			f.statusIs(t, c.want, route{http.MethodPut, fileRoute, c.body}, c.user)
		})
	}
	assert.Equal(t, 1, f.store.sites[siteID].Environment(site.EnvDev).Release, "no refusal wrote a release")
}

func TestFiles_DryRunStoresNothingAndCountsWhatChanges(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})
	before := len(f.audit.events)

	out := decodeBody[sitesvc.Published](t, f.statusIs(t, http.StatusOK, route{
		http.MethodPut, fileRoute,
		FileSaveBody{Path: "index.html", Content: "<h1>new</h1>", Base: 1, DryRun: true},
	}, userOwner))
	assert.Equal(t, 1, out.OutputsChanged)
	assert.Equal(t, 1, f.store.sites[siteID].Environment(site.EnvDev).Release)
	assert.Len(t, f.store.releases[siteID], 1)
	assert.Len(t, f.audit.events, before, "a dry run is not a write")
}

func TestFiles_SaveTakesBase64(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})
	body := []byte{0x89, 'P', 'N', 'G'}
	out := decodeBody[sitesvc.Published](t, f.statusIs(t, http.StatusOK, route{
		http.MethodPut, fileRoute,
		FileSaveBody{Path: "logo.png", Content: base64.StdEncoding.EncodeToString(body), Encoding: "base64", Base: 1},
	}, userOwner))
	assert.Equal(t, 2, out.Release.Number)
	found := false
	for _, data := range f.objects.put {
		found = found || bytes.Equal(data, body)
	}
	assert.True(t, found, "the decoded bytes were stored")
}

func TestFiles_Delete(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1, "old.html": "<h1>old</h1>"})

	f.statusIs(t, http.StatusBadRequest, route{http.MethodDelete, fileRoute + "?path=old.html", nil}, userOwner)
	f.statusIs(t, http.StatusForbidden, route{http.MethodDelete, fileRoute + "?path=old.html&base=1", nil}, userViewer)
	f.statusIs(t, http.StatusUnauthorized, route{http.MethodDelete, fileRoute + "?path=old.html&base=1", nil}, nil)
	out := decodeBody[sitesvc.Published](t, f.statusIs(t, http.StatusOK, route{http.MethodDelete, fileRoute + "?path=old.html&base=1&note=gone", nil}, userOwner))
	assert.Equal(t, 2, out.Release.Number)
	assert.Equal(t, "delete old.html", out.Release.Source)
	f.statusIs(t, http.StatusConflict, route{http.MethodDelete, fileRoute + "?path=index.html&base=1", nil}, userOwner)
}

// upload sends a multipart upload of files into folder.
func (f *fixture) upload(t *testing.T, u *portal.User, base, folder string, files map[string][]byte) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	require.NoError(t, w.WriteField("base", base))
	require.NoError(t, w.WriteField("folder", folder))
	require.NoError(t, w.WriteField("note", "new images"))
	for name, data := range files {
		part, err := w.CreateFormFile("files", name)
		require.NoError(t, err)
		_, err = part.Write(data)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, filesRoute, &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if u != nil {
		req = req.WithContext(portal.ContextWithUser(req.Context(), u))
	}
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	return rec
}

func TestFiles_Upload(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})

	rec := f.upload(t, userOwner, "1", "/images/", map[string][]byte{"hero.webp": []byte("webp bytes"), "../escape.png": []byte("png")})
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	out := decodeBody[sitesvc.Published](t, rec)
	assert.Equal(t, 2, out.Release.Number)
	assert.Equal(t, "upload", out.Release.Source)
	paths := map[string]bool{}
	for _, file := range f.store.files[out.Release.ID] {
		paths[file.Path] = true
	}
	assert.True(t, paths["images/hero.webp"], "filed in the folder")
	assert.True(t, paths["images/escape.png"], "a name is its base name only, so it cannot climb out of the folder")

	for name, r := range map[string]*httptest.ResponseRecorder{
		"no base":                    f.upload(t, userOwner, "", "", map[string][]byte{"a.png": []byte("a")}),
		"no file":                    f.upload(t, userOwner, "2", "", nil),
		"someone dev is shared with": f.upload(t, userViewer, "2", "", map[string][]byte{"a.png": []byte("a")}),
		"an older release":           f.upload(t, userOwner, "1", "", map[string][]byte{"a.png": []byte("a")}),
	} {
		t.Run(name, func(t *testing.T) {
			want := map[string]int{"no base": http.StatusBadRequest, "no file": http.StatusBadRequest, "someone dev is shared with": http.StatusForbidden, "an older release": http.StatusConflict}[name]
			assert.Equal(t, want, r.Code, r.Body.String())
		})
	}
	assert.Equal(t, http.StatusUnauthorized, f.upload(t, nil, "2", "", map[string][]byte{"a.png": []byte("a")}).Code)
}

func TestFiles_UploadRefusesWhatIsNotAFormOrTooLarge(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})
	f.statusIs(t, http.StatusBadRequest, route{http.MethodPost, filesRoute, "plain text"}, userOwner)

	small := siteconfig.Config{PagesDomain: pagesDomain, MaxFileBytes: 4}
	f.rt.Live.Set(small)
	rec := f.upload(t, userOwner, "1", "", map[string][]byte{"big.png": []byte("more than four bytes")})
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "big.png")
}

func TestFiles_Diff(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})
	f.devFiles(2, map[string]string{"index.html": "<h1>Pricing $15</h1>\n"})

	got := decodeBody[FileDiffView](t, f.statusIs(t, http.StatusOK, route{http.MethodGet, fileRoute + "/diff?path=index.html&from=1&to=2", nil}, userOwner))
	assert.Equal(t, 1, got.From)
	assert.Equal(t, 2, got.To)
	assert.Contains(t, got.Diff, "-<h1>Pricing $12</h1>")
	assert.Contains(t, got.Diff, "+<h1>Pricing $15</h1>")
	f.statusIs(t, http.StatusForbidden, route{http.MethodGet, fileRoute + "/diff?path=index.html&from=1&to=2", nil}, userViewer)
	f.statusIs(t, http.StatusUnauthorized, route{http.MethodGet, fileRoute + "/diff?path=index.html", nil}, nil)
}

// unread is a request body that fails the test when anything reads it.
type unread struct{ t *testing.T }

func (u unread) Read([]byte) (int, error) {
	u.t.Error("the body was read before the caller was found allowed to publish")
	return 0, io.EOF
}

func TestFiles_AWriteIsRefusedBeforeItsBodyIsRead(t *testing.T) {
	f := newFixture(t)
	f.devFiles(1, map[string]string{"index.html": pageV1})
	for _, u := range []*portal.User{userViewer, userStranger} {
		for _, r := range []struct{ method, target, contentType string }{
			{http.MethodPut, fileRoute, "application/json"},
			{http.MethodPost, filesRoute, "multipart/form-data; boundary=x"},
			{http.MethodDelete, fileRoute + "?path=index.html&base=1", ""},
		} {
			t.Run(u.Email+" "+r.method+" "+r.target, func(t *testing.T) {
				req := httptest.NewRequestWithContext(portal.ContextWithUser(context.Background(), u), r.method, r.target, unread{t})
				if r.contentType != "" {
					req.Header.Set("Content-Type", r.contentType)
				}
				rec := httptest.NewRecorder()
				f.mux.ServeHTTP(rec, req)
				assert.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
			})
		}
	}
	assert.Equal(t, 1, f.store.sites[siteID].Environment(site.EnvDev).Release)
}

func TestFiles_AWriteToASiteThatDoesNotExistIsNotFoundBeforeTheBody(t *testing.T) {
	f := newFixture(t)
	req := httptest.NewRequestWithContext(portal.ContextWithUser(context.Background(), userOwner), http.MethodPut, base+"/no-such-site/file", unread{t})
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
}
