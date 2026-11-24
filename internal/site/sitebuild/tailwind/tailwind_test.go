package tailwind

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/txn2/mcp-data-platform/internal/site/sitebuild"
)

// fakeLib is a stand-in for Tailwind's compiler with its shape: compile
// answers a promise of something whose build takes the candidates, and reads
// imports through loadStylesheet.
const fakeLib = `exports.compile = async function (css, opts) {
  var m = css.match(/@import "([^"]+)"/);
  var imported = m ? (await opts.loadStylesheet(m[1], opts.base)).content : "";
  if (css.indexOf("@boom") >= 0) { throw new Error("unknown at-rule @boom"); }
  if (css.indexOf("@spin") >= 0) { while (true) {} }
  return { build: function (c) { return imported + "\n" + c.filter(function (x) { return /^[a-z]+-[0-9]+$/.test(x); }).map(function (x) { return "." + x + "{}"; }).join("\n"); } };
};`

func fakePackage(t *testing.T) *Package {
	t.Helper()
	p, err := Prepare("4.1.13", map[string][]byte{"dist/lib.js": []byte(fakeLib), "index.css": []byte("/* tw index */")})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompileStylesheet(t *testing.T) {
	src := fstest.MapFS{
		"app.tsx":         {Data: []byte(`<div className="p-4 m-2 flex">`)},
		"e2e/x.ts":        {Data: []byte(`"z-9"`)},
		"src/extra.css":   {Data: []byte("/* extra */")},
		"vendor/lib.html": {Data: []byte(`<p class="w-1">`)},
	}
	c := NewCompiler(fakePackage(t), src, "", Limits{})
	out, changed, err := c.CompileStylesheet(context.Background(), "app.css", []byte(`@import "tailwindcss"; @source not "e2e";`))
	if err != nil || !changed {
		t.Fatal(err)
	}
	css := string(out)
	for _, want := range []string{"/* tw index */", ".p-4{}", ".m-2{}", ".w-1{}"} {
		if !strings.Contains(css, want) {
			t.Errorf("output lacks %s:\n%s", want, css)
		}
	}
	if strings.Contains(css, "z-9") {
		t.Errorf("@source not was ignored:\n%s", css)
	}
	if again, _, _ := c.CompileStylesheet(context.Background(), "app.css", []byte(`@import "tailwindcss"; @source not "e2e";`)); string(again) != css {
		t.Error("a second compile differs")
	}
	if _, changed, _ := c.CompileStylesheet(context.Background(), "plain.css", []byte("body{}")); changed {
		t.Error("a stylesheet without tailwindcss was compiled")
	}
	out, _, err = c.CompileStylesheet(context.Background(), "local.css", []byte(`@import "./src/extra.css";`+"\n@import 'tailwindcss/theme';"))
	if err != nil || !strings.Contains(string(out), "/* extra */") {
		t.Errorf("local import: %v %s", err, out)
	}
}

func TestRefusals(t *testing.T) {
	c := NewCompiler(fakePackage(t), fstest.MapFS{}, "", Limits{Time: 200 * time.Millisecond})
	for css, want := range map[string]string{
		`@import "tailwindcss"; @plugin "daisyui";`:              `@plugin "daisyui" is not supported`,
		`@import "tailwindcss"; @config "./tailwind.config.js";`: `@config "./tailwind.config.js" is not supported`,
		`@import "tailwindcss"; @boom;`:                          "unknown at-rule @boom",
		`@import "tailwindcss"; @spin;`:                          "limit on time",
		`@import "../outside.css"; @import "tailwindcss";`:       "outside the site",
		`@import "lodash"; @import "tailwindcss";`:               "imported by a path in the site",
	} {
		_, _, err := c.CompileStylesheet(context.Background(), "app.css", []byte(css))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", css, err, want)
		}
	}
	small := NewCompiler(fakePackage(t), fstest.MapFS{"a.ts": {Data: []byte("p-1 p-2 p-3")}}, "", Limits{Candidates: 2})
	var le *LimitError
	if _, _, err := small.CompileStylesheet(context.Background(), "app.css", []byte(`@import "tailwindcss";`)); !errors.As(err, &le) {
		t.Errorf("candidate limit: %v", err)
	}
	onlyInput := NewCompiler(fakePackage(t), fstest.MapFS{}, "src/app.css", Limits{})
	if _, changed, _ := onlyInput.CompileStylesheet(context.Background(), "other.css", []byte(`@import "tailwindcss";`)); changed {
		t.Error("tailwind_input was not the only stylesheet compiled")
	}
	if _, err := Prepare("4.0.0", map[string][]byte{}); err == nil {
		t.Error("a package without dist/lib.js prepared")
	}
}

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: "package/" + name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

func TestLoaderFetchesAndChecks(t *testing.T) {
	tgz := tarball(t, map[string]string{"dist/lib.js": fakeLib, "index.css": "/* idx */"})
	sum := sha512.Sum512(tgz)
	integrity := "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
	fetches := 0
	serve := func(meta string) Fetch {
		return func(_ context.Context, u string) ([]byte, string, error) {
			fetches++
			switch u {
			case Registry + "4.1.13":
				return []byte(meta), "application/json", nil
			case "https://registry.npmjs.org/tailwindcss/-/tailwindcss-4.1.13.tgz":
				return tgz, "application/octet-stream", nil
			}
			return nil, "", errors.New("404 Not Found")
		}
	}
	good := `{"version":"4.1.13","dist":{"tarball":"https://registry.npmjs.org/tailwindcss/-/tailwindcss-4.1.13.tgz","integrity":"` + integrity + `"}}`
	l := &Loader{Fetch: serve(good)}
	if _, err := l.Load(context.Background(), "4.1.13"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Load(context.Background(), "4.1.13"); err != nil || fetches != 2 {
		t.Errorf("a loaded version was fetched again: %d %v", fetches, err)
	}
	for meta, want := range map[string]string{
		strings.Replace(good, integrity, "sha512-AAAA", 1):                                             "integrity check",
		strings.Replace(good, "https://registry.npmjs.org/tailwindcss/-/", "https://evil.example/", 1): "outside the registry",
		strings.Replace(good, `"integrity":"`+integrity+`"`, `"integrity":"sha1-x"`, 1):                "no sha512",
	} {
		_, err := (&Loader{Fetch: serve(meta)}).Load(context.Background(), "4.1.13")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", want, err)
		}
	}
	if _, err := (&Loader{Fetch: serve(good)}).Load(context.Background(), "4.9.9"); err == nil || !strings.Contains(err.Error(), "no such version") {
		t.Errorf("missing version: %v", err)
	}
	factory := (&Loader{Fetch: serve(good)}).Factory(Limits{})
	cfg, err := sitebuild.Parse([]byte(`{"entries":["app.tsx"],"tailwind":"4.1.13"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := factory(context.Background(), cfg, fstest.MapFS{}); err != nil {
		t.Error(err)
	}
	if _, err := (&Loader{}).Load(context.Background(), "4.1.13"); err == nil {
		t.Error("a loader with no fetch loaded")
	}
}
