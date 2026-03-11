package contentviewer

import (
	"testing"
	"testing/fstest"
)

func TestEmbedInitDefaultsEmpty(t *testing.T) {
	// In a clean checkout (or after embed-clean) the dist/ directory contains
	// only .gitkeep, so init() should leave JS and CSS as empty strings.
	// When the frontend has been built, they will be non-empty; both are valid.
	if JS != "" && CSS != "" {
		if len(JS) < 10 {
			t.Errorf("JS bundle is present but suspiciously short (%d bytes)", len(JS))
		}
		if len(CSS) < 10 {
			t.Errorf("CSS bundle is present but suspiciously short (%d bytes)", len(CSS))
		}
		return
	}

	if JS != "" {
		t.Errorf("expected JS to be empty in clean dist, got %d bytes", len(JS))
	}
	if CSS != "" {
		t.Errorf("expected CSS to be empty in clean dist, got %d bytes", len(CSS))
	}
}

func TestDistFSReadable(t *testing.T) {
	entries, err := distFS.ReadDir("dist")
	if err != nil {
		t.Fatalf("failed to read embedded dist directory: %v", err)
	}
	if len(entries) == 0 {
		t.Error("embedded dist directory is empty — expected at least .gitkeep")
	}
}

func TestLoadBundlesWithFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"dist/content-viewer.js":  {Data: []byte("console.log('viewer');")},
		"dist/content-viewer.css": {Data: []byte(".root { color: red; }")},
	}

	js, css := loadBundles(fsys)

	if js != "console.log('viewer');" {
		t.Errorf("JS = %q, want %q", js, "console.log('viewer');")
	}
	if css != ".root { color: red; }" {
		t.Errorf("CSS = %q, want %q", css, ".root { color: red; }")
	}
}

func TestLoadBundlesEmpty(t *testing.T) {
	fsys := fstest.MapFS{
		"dist/.gitkeep": {Data: []byte{}},
	}

	js, css := loadBundles(fsys)

	if js != "" {
		t.Errorf("expected JS to be empty, got %q", js)
	}
	if css != "" {
		t.Errorf("expected CSS to be empty, got %q", css)
	}
}

func TestLoadBundlesPartial(t *testing.T) {
	fsys := fstest.MapFS{
		"dist/content-viewer.js": {Data: []byte("var x = 1;")},
	}

	js, css := loadBundles(fsys)

	if js != "var x = 1;" {
		t.Errorf("JS = %q, want %q", js, "var x = 1;")
	}
	if css != "" {
		t.Errorf("expected CSS to be empty, got %q", css)
	}
}
