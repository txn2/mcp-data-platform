package sitegw

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/pkg/blobserve"
)

const mediaBucket = "resources"

// fakeMedia is a library holding a few files under one mounted folder.
type fakeMedia struct {
	mounts    []site.Mount
	files     map[string]string // tail under the folder -> content type
	objects   *memObjects
	mountErr  error
	findErr   error
	mountRead int
}

func (f *fakeMedia) Mounts(context.Context, string) ([]site.Mount, error) {
	f.mountRead++
	return f.mounts, f.mountErr
}

func (f *fakeMedia) Find(_ context.Context, m site.Mount, rel string) (*MediaFile, error) {
	if f.findErr != nil {
		return nil, f.findErr
	}
	ct, ok := f.files[m.Folder+"/"+rel]
	if !ok {
		return nil, nil //nolint:nilnil // no file at that path
	}
	return &MediaFile{
		Name: rel, ContentType: ct, ModTime: time.Unix(1700000000, 0),
		Object: blobserve.Object{Store: f.objects, Bucket: mediaBucket, Key: "resources/global/" + m.Folder + "/" + rel},
	}, nil
}

func mediaHarness(t *testing.T) (*harness, *fakeMedia) {
	t.Helper()
	media := &fakeMedia{
		mounts: []site.Mount{{Prefix: "/media/", Scope: "global", Folder: "sites/m"}},
		files:  map[string]string{"sites/m/logo.png": "image/png", "sites/m/page.html": "text/html", "sites/m/index.html": "image/png"},
	}
	h := newHarness(Deps{Media: media})
	media.objects = h.objects
	for tail, body := range map[string]string{"sites/m/logo.png": "PNGBYTES", "sites/m/page.html": "<script>x</script>", "sites/m/index.html": "MOUNTED"} {
		h.objects.bodies[objectName(mediaBucket, "resources/global/"+tail)] = []byte(body)
	}
	return h, media
}

func TestMedia_AMountedFileIsServedByRange(t *testing.T) {
	h, _ := mediaHarness(t)
	h.mapHost(h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML}))

	rec := h.get(testSiteHost, "/media/logo.png")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "PNGBYTES", rec.Body.String())
	assert.Equal(t, "image/png", rec.Header().Get(hdrType))
	assert.Equal(t, cacheRevalidate, rec.Header().Get(hdrCache))
	assert.NotEmpty(t, rec.Header().Get(headerETag))

	ranged := h.do(http.MethodGet, testSiteHost, "/media/logo.png", http.Header{"Range": {"bytes=0-2"}})
	assert.Equal(t, http.StatusPartialContent, ranged.Code)
	assert.Equal(t, "PNG", ranged.Body.String())
}

func TestMedia_AnActiveTypeIsADownload(t *testing.T) {
	h, _ := mediaHarness(t)
	h.mapHost(h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML}))
	rec := h.get(testSiteHost, "/media/page.html")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment", "a media folder is never a second site")
	assert.Equal(t, nosniff, rec.Header().Get(hdrNosniff))
}

func TestMedia_AReleaseFileWinsAndAMissIsTheNotFoundPage(t *testing.T) {
	h, _ := mediaHarness(t)
	h.mapHost(h.addRelease(1, nil,
		testFile{path: indexFile, body: "home", contentType: ctHTML},
		testFile{path: "media/index.html", body: "FROM THE RELEASE", contentType: ctHTML},
		testFile{path: notFound, body: "nothing here", contentType: ctHTML},
	))
	rec := h.get(testSiteHost, "/media/index.html")
	assert.Equal(t, "FROM THE RELEASE", rec.Body.String(), "a release file at the same path wins over a mount")

	for _, p := range []string{"/media/missing.png", "/elsewhere.png"} {
		rec := h.get(testSiteHost, p)
		assert.Equal(t, http.StatusNotFound, rec.Code, p)
		assert.Equal(t, "nothing here", rec.Body.String(), p)
	}
}

func TestMedia_APrivateEnvironmentKeepsItPrivate(t *testing.T) {
	h, _ := mediaHarness(t)
	rel := h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML})
	h.store.hosts[testSiteHost] = &site.Route{SiteID: testSiteID, Site: "marketing", Environment: "prod", Release: rel, Private: true}
	// A private route answers only through an access guard; with none, the
	// mounted file is never served.
	rec := h.get(testSiteHost, "/media/logo.png")
	assert.NotEqual(t, "PNGBYTES", rec.Body.String())
}

func TestMedia_ReadsAreCachedAndFailuresAnswer(t *testing.T) {
	h, media := mediaHarness(t)
	h.mapHost(h.addRelease(1, nil, testFile{path: indexFile, body: "home", contentType: ctHTML}))
	h.get(testSiteHost, "/media/logo.png")
	h.get(testSiteHost, "/media/logo.png")
	assert.Equal(t, 1, media.mountRead, "a site's mounts are read once per route lifetime")
	h.gateway.Invalidate("")
	h.get(testSiteHost, "/media/logo.png")
	assert.Equal(t, 2, media.mountRead, "a change on this replica is read at once")

	media.findErr = errors.New("down")
	assert.Equal(t, http.StatusServiceUnavailable, h.get(testSiteHost, "/media/logo.png").Code)

	media.findErr, media.mountErr = nil, errors.New("down")
	h.gateway.Invalidate("")
	assert.Equal(t, http.StatusNotFound, h.get(testSiteHost, "/media/logo.png").Code, "mounts that cannot be read serve nothing")

	media.mountErr = nil
	h.gateway.Invalidate("")
	delete(h.objects.bodies, objectName(mediaBucket, "resources/global/sites/m/logo.png"))
	rec := h.get(testSiteHost, "/media/logo.png")
	assert.Equal(t, http.StatusNotFound, rec.Code, "a record whose object is gone answers not found")
	assert.Empty(t, rec.Header().Get(hdrCache))
}
