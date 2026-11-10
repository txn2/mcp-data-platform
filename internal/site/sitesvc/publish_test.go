package sitesvc

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

func TestCleanPath(t *testing.T) {
	cases := []struct {
		name, in, want, refusal string
	}{
		{name: "plain", in: "index.html", want: "index.html"},
		{name: "nested", in: "css/site.3f2a.css", want: "css/site.3f2a.css"},
		{name: "leading dot-slash", in: "./a/b.txt", want: "a/b.txt"},
		{name: "surrounding space", in: "  a.txt ", want: "a.txt"},
		{name: "well-known", in: ".well-known/security.txt", want: ".well-known/security.txt"},
		{name: "well-known nested", in: "app/.well-known/x", want: "app/.well-known/x"},
		{name: "a dot inside a name", in: "a.b/c.d", want: "a.b/c.d"},
		{name: "empty", in: "", refusal: "cannot be served"},
		{name: "blank", in: "   ", refusal: "cannot be served"},
		{name: "too long", in: strings.Repeat("a", maxPathLen+1), refusal: "cannot be served"},
		{name: "not utf-8", in: "a\xff.txt", refusal: "cannot be served"},
		{name: "a backslash", in: `a\b.txt`, refusal: "cannot be served"},
		{name: "a nul", in: "a\x00b", refusal: "cannot be served"},
		{name: "a control character", in: "a\x01b", refusal: "control character"},
		{name: "a newline", in: "a\nb", refusal: "control character"},
		{name: "a delete", in: "a\x7fb", refusal: "control character"},
		{name: "absolute", in: "/etc/passwd", refusal: "relative and clean"},
		{name: "a doubled slash", in: "a//b", refusal: "relative and clean"},
		{name: "an inner dot", in: "a/./b", refusal: "relative and clean"},
		{name: "a parent inside", in: "a/../b", refusal: "relative and clean"},
		{name: "a trailing slash", in: "a/", refusal: "relative and clean"},
		{name: "a parent first", in: "../secret", refusal: "dotted segment"},
		{name: "a parent alone", in: "..", refusal: "dotted segment"},
		{name: "env file", in: ".env", refusal: "dotted segment"},
		{name: "git config", in: ".git/config", refusal: "dotted segment"},
		{name: "a nested dotfile", in: "a/.htpasswd", refusal: "dotted segment"},
		{name: "well-known in the wrong case", in: ".Well-Known/x", refusal: "dotted segment"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CleanPath(tc.in)
			if tc.refusal != "" {
				require.ErrorContains(t, err, tc.refusal)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsJunk(t *testing.T) {
	for _, name := range []string{".DS_Store", "assets/.DS_Store", "Thumbs.db", "a/b/Thumbs.db", "__MACOSX/a/b"} {
		assert.True(t, isJunk(name), name)
	}
	for _, name := range []string{"index.html", "a/__MACOSX/b", ".env", "thumbs.db"} {
		assert.False(t, isJunk(name), name)
	}
}

func TestContentTypeOfAndCompressible(t *testing.T) {
	assert.Equal(t, octetStream, contentTypeOf("no-extension"))
	assert.Equal(t, octetStream, contentTypeOf("a.unknownext"))
	assert.Equal(t, "text/css; charset=utf-8", contentTypeOf("a/b.CSS"), "the extension is read case-insensitively")
	assert.Equal(t, "text/html; charset=utf-8", contentTypeOf("index.html"), "a site's pages are served as pages")
	assert.Equal(t, "text/javascript; charset=utf-8", contentTypeOf("a.js"))

	for _, ct := range []string{"text/html", "text/css", "application/json", "application/javascript", "image/svg+xml", "application/ld+json", "font/ttf"} {
		assert.True(t, compressible(ct), ct)
	}
	for _, ct := range []string{"image/png", "image/jpeg", "application/zip", "font/woff2", octetStream} {
		assert.False(t, compressible(ct), ct)
	}
}

func TestObjectKey(t *testing.T) {
	legacy := site.File{Path: "css/a.css", SHA256: "abc", StoredIn: "r3"}
	assert.Equal(t, "sites/s1/releases/r3/files/css/a.css", ObjectKey("s1", legacy, ""), "a file written before digests stays where it is")
	assert.Equal(t, "sites/s1/releases/r3/gz/css/a.css", ObjectKey("s1", legacy, "gz"))
	f := site.File{Path: "../css/a.css", SHA256: "abc"}
	assert.Equal(t, "sites/s1/objects/abc", ObjectKey("s1", f, ""), "the digest is the key: a caller's path never reaches it")
	assert.Equal(t, "sites/s1/objects/abc.br", ObjectKey("s1", f, "br"))
	assert.NotEqual(t, ObjectKey("s1", f, ""), ObjectKey("s2", f, ""), "objects are per site")
}

func TestDigest(t *testing.T) {
	a := site.File{Path: "a.html", SHA256: "aa"}
	b := site.File{Path: "b.html", SHA256: "bb"}
	files := []site.File{b, a}

	first := Digest(files)
	assert.Equal(t, Digest([]site.File{a, b}), first, "the order the files came in does not change it")
	assert.Equal(t, []site.File{b, a}, files, "the caller's slice is left in its order")
	assert.Len(t, first, 64)

	assert.NotEqual(t, first, Digest([]site.File{{Path: "a.html", SHA256: "changed"}, b}), "a file's content is fixed")
	assert.NotEqual(t, first, Digest([]site.File{{Path: "renamed.html", SHA256: "aa"}, b}), "a file's path is fixed")
	assert.NotEqual(t, first, Digest([]site.File{a}), "the set of files is fixed")
	assert.Equal(t, Digest(nil), Digest([]site.File{}))
}

func publishReq(files ...InputFile) PublishRequest {
	return PublishRequest{Site: "marketing", Files: files}
}

func page(path string) InputFile { return InputFile{Path: path, Body: []byte("<p>" + path + "</p>")} }

func TestPublish_JunkIsDroppedAndTheRestFiled(t *testing.T) {
	svc, store, objects, _ := fixture(t)
	got, err := svc.Publish(context.Background(), owner, publishReq(
		page("index.html"), page(".DS_Store"), page("assets/Thumbs.db"), page("__MACOSX/index.html"),
	))
	require.NoError(t, err)
	assert.Equal(t, 1, got.Release.FileCount, "only the one file that is served is counted")
	assert.Equal(t, []string{"portal/" + ObjectKey(siteID, store.releases[siteID][0].Files[0], "")}, objects.keys())
}

func TestPublish_Refusals(t *testing.T) {
	cases := []struct {
		name  string
		files []InputFile
		want  string
	}{
		{"a parent reference", []InputFile{page("../x.html")}, "dotted segment"},
		{"a dotted segment", []InputFile{page("index.html"), page(".env")}, "dotted segment"},
		{"a control character", []InputFile{page("a\x02.html")}, "control character"},
		{"the same path twice", []InputFile{page("a.html"), page("a.html")}, `"a.html" appears twice`},
		{"the same path spelled two ways", []InputFile{page("a.html"), page("./a.html")}, "appears twice"},
		{"no files", nil, "holds no files"},
		{"only junk", []InputFile{page(".DS_Store")}, "holds no files"},
		{"only a redirect map", []InputFile{{Path: "_redirects", Body: []byte("/a /b\n")}}, "holds no files"},
		{"a bad redirect map", []InputFile{page("a.html"), {Path: "_redirects", Body: []byte("/a /b 200\n")}}, "line 1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, store, objects, _ := fixture(t)
			_, err := svc.Publish(context.Background(), owner, publishReq(tc.files...))
			var r *RefusedError
			require.ErrorAs(t, err, &r)
			assert.Contains(t, r.Msg, tc.want)
			assert.Empty(t, objects.keys(), "a refused publish stores nothing")
			assert.Empty(t, store.releases[siteID], "a refused publish records nothing")
		})
	}
}

func TestPublish_SizeLimits(t *testing.T) {
	body := func(n int) []byte { return bytes.Repeat([]byte("x"), n) }
	cases := []struct {
		name  string
		cfg   siteconfig.Config
		files []InputFile
		want  string
	}{
		{"a file at the bound", siteconfig.Config{MaxFileBytes: 10}, []InputFile{{Path: "a.bin", Body: body(10)}}, ""},
		{"a file past the bound", siteconfig.Config{MaxFileBytes: 10}, []InputFile{{Path: "a.bin", Body: body(11)}}, "sites.max_file_bytes"},
		{"as many files as allowed", siteconfig.Config{MaxFiles: 2}, []InputFile{page("a"), page("b")}, ""},
		{"more files than allowed", siteconfig.Config{MaxFiles: 2}, []InputFile{page("a"), page("b"), page("c")}, "sites.max_release_files"},
		{"a release at the bound", siteconfig.Config{MaxReleaseBytes: 20}, []InputFile{{Path: "a.bin", Body: body(10)}, {Path: "b.bin", Body: body(10)}}, ""},
		{"a release past the bound", siteconfig.Config{MaxReleaseBytes: 20}, []InputFile{{Path: "a.bin", Body: body(10)}, {Path: "b.bin", Body: body(11)}}, "sites.max_release_bytes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, objects, _ := fixture(t)
			svc.Config = tc.cfg
			_, err := svc.Publish(context.Background(), owner, publishReq(tc.files...))
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
			assert.Empty(t, objects.keys())
		})
	}
}

func TestPublish_RedirectsAreReadAndNotServed(t *testing.T) {
	svc, store, objects, _ := fixture(t)
	got, err := svc.Publish(context.Background(), owner, publishReq(
		page("index.html"), InputFile{Path: "_redirects", Body: []byte("# moved\n/old /new 302\n")},
	))
	require.NoError(t, err)
	assert.Equal(t, []site.Redirect{{From: "/old", To: "/new", Status: 302}}, got.Release.Redirects)
	assert.Equal(t, 1, got.Release.FileCount, "the map is not a file of the release")
	assert.NotContains(t, strings.Join(objects.keys(), ","), "_redirects")
	for _, f := range store.releases[siteID][0].Files {
		assert.NotEqual(t, redirectsFile, f.Path)
	}

	noMap, err := svc.Publish(context.Background(), owner, publishReq(page("index.html")))
	require.NoError(t, err)
	assert.NotNil(t, noMap.Release.Redirects, "no redirect is [], never null")
	assert.Empty(t, noMap.Release.Redirects)
}

func TestPublish_CompressedVariantsOnlyWhenWorthwhile(t *testing.T) {
	random := make([]byte, 4096)
	_, err := rand.Read(random)
	require.NoError(t, err)
	compressibleBody := bytes.Repeat([]byte("<p>the same words again</p>\n"), 200)

	files := []InputFile{
		{Path: "big.css", Body: compressibleBody},
		{Path: "small.css", Body: compressibleBody[:minCompressBytes-1]},
		{Path: "photo.png", Body: bytes.Repeat([]byte("<p>other words again</p>\n"), 200)},
		{Path: "noise.css", Body: random},
	}
	svc, store, objects, _ := fixture(t)
	_, err = svc.Publish(context.Background(), owner, publishReq(files...))
	require.NoError(t, err)

	got := map[string]site.File{}
	for _, f := range store.releases[siteID][0].Files {
		got[f.Path] = f
	}
	key := func(enc, name string) string {
		return "portal/" + ObjectKey(siteID, got[name], enc)
	}

	big := got["big.css"]
	assert.Equal(t, int64(len(compressibleBody)), big.Size)
	assert.Positive(t, big.GzipSize)
	assert.Positive(t, big.BrotliSize)
	assert.Equal(t, big.GzipSize, int64(len(objects.put[key("gz", "big.css")])), "the recorded size is the stored object's")
	assert.Equal(t, big.BrotliSize, int64(len(objects.put[key("br", "big.css")])))

	gz, err := gzip.NewReader(bytes.NewReader(objects.put[key("gz", "big.css")]))
	require.NoError(t, err)
	plain, err := io.ReadAll(gz)
	require.NoError(t, err)
	assert.Equal(t, compressibleBody, plain, "the gzip form is the file")
	plain, err = io.ReadAll(brotli.NewReader(bytes.NewReader(objects.put[key("br", "big.css")])))
	require.NoError(t, err)
	assert.Equal(t, compressibleBody, plain, "the brotli form is the file")

	for _, name := range []string{"small.css", "photo.png", "noise.css"} {
		assert.Zero(t, got[name].GzipSize, name)
		assert.Zero(t, got[name].BrotliSize, name)
		assert.False(t, objects.has(key("gz", name)), name)
		assert.False(t, objects.has(key("br", name)), name)
		assert.True(t, objects.has(key("", name)), "the file itself is always stored: "+name)
	}
}

func TestPublish_StoresTheManifestOfWhatItWrote(t *testing.T) {
	svc, store, _, _ := fixture(t)
	got, err := svc.Publish(context.Background(), owner, publishReq(page("b.css"), page("a.css")))
	require.NoError(t, err)
	rec := store.releases[siteID][0]
	assert.Equal(t, Digest(rec.Files), got.Release.ManifestSHA256)
	assert.Equal(t, int64(len("<p>b.css</p>")+len("<p>a.css</p>")), got.Release.TotalBytes)
	assert.Equal(t, "text/css; charset=utf-8", rec.Files[0].ContentType)
	assert.Len(t, rec.Files[0].SHA256, 64)
	assert.Nil(t, got.Release.Files, "the answer does not carry the file list")
}

func TestPublish_AStorageFailureRecordsNothing(t *testing.T) {
	body := bytes.Repeat([]byte("repeat me "), 500)
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	cases := map[string]string{
		"the file":   digest,
		"the gzip":   digest + ".gz",
		"the brotli": digest + ".br",
	}
	for name, suffix := range cases {
		t.Run(name, func(t *testing.T) {
			svc, store, objects, _ := fixture(t)
			objects.fail = suffix
			_, err := svc.Publish(context.Background(), owner, publishReq(InputFile{Path: "big.css", Body: body}))
			require.ErrorContains(t, err, "storing big.css")
			assert.Empty(t, store.releases[siteID])
			assert.Zero(t, store.sites[siteID].Environment(site.EnvDefault).Release, "the environment did not move")
		})
	}
}

func TestPublish_StoreFailures(t *testing.T) {
	boom := errors.New("down")
	t.Run("recording the release", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.failRecord = boom
		_, err := svc.Publish(context.Background(), owner, publishReq(page("a")))
		require.ErrorIs(t, err, boom)
	})
	t.Run("moving the environment", func(t *testing.T) {
		svc, store, _, _ := fixture(t)
		store.failPoint = site.ErrStale
		_, err := svc.Publish(context.Background(), owner, publishReq(page("a")))
		require.ErrorIs(t, err, site.ErrStale)
	})
}

func TestPrepare_ManyFilesAreKeptInOrder(t *testing.T) {
	svc, _, _, _ := fixture(t)
	in := make([]InputFile, 0, 20)
	for i := range 20 {
		in = append(in, page(fmt.Sprintf("p%02d.html", i)))
	}
	b, err := svc.prepare(PublishRequest{Files: in}, newBasis(nil, nil))
	require.NoError(t, err)
	require.Len(t, b.inputs, 20)
	assert.Equal(t, "p07.html", b.inputs[7].Path)
}

func TestPublish_ReportsProgressAndThePhase(t *testing.T) {
	svc, _, _, _ := fixture(t)
	var mu sync.Mutex
	var totals []int
	var phases []string
	_, err := svc.Publish(context.Background(), owner, PublishRequest{
		Site: "marketing", Files: []InputFile{page("a.html"), page("b.html"), page("c.html")},
		Progress: func(done, total int) {
			mu.Lock()
			defer mu.Unlock()
			totals = append(totals, done)
			assert.Equal(t, 3, total)
		},
		Phase: func(p string) { phases = append(phases, p) },
	})
	require.NoError(t, err)
	assert.ElementsMatch(t, []int{1, 2, 3}, totals, "told once per file, counting up")
	assert.Equal(t, []string{"Switching default to the new release"}, phases)
}
