package sitepath

import (
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/site"
)

func TestCleanPath(t *testing.T) {
	for in, want := range map[string]string{"./a/b.html": "a/b.html", "\tindex.html": "index.html", ".well-known/x": ".well-known/x", ".nvmrc": ".nvmrc", ".eslintrc.cjs": ".eslintrc.cjs"} {
		got, err := CleanPath(in)
		if err != nil || got != want {
			t.Errorf("CleanPath(%q) = %q, %v", in, got, err)
		}
	}
	for in, want := range map[string]string{
		"": "cannot be served", "a\\b": "cannot be served", "a\x01b": "control character", "/abs": "relative and clean",
		"a/../b": "relative and clean", ".env": "dotted segment", "x/.git/config": "dotted segment", ".npmrc": "registry token",
		"x/.npmrc": "registry token", strings.Repeat("a", 1025): "cannot be served",
	} {
		if _, err := CleanPath(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("CleanPath(%q) = %v, want %q", in, err, want)
		}
	}
}

func TestObjectKeyAndDigest(t *testing.T) {
	f := site.File{Path: "a.css", SHA256: "abc"}
	if got := ObjectKey("s1", f, ""); got != "sites/s1/objects/abc" {
		t.Errorf("ObjectKey = %s", got)
	}
	if got := ObjectKey("s1", f, "br"); got != "sites/s1/objects/abc.br" {
		t.Errorf("ObjectKey br = %s", got)
	}
	f.StoredIn = "rel1"
	if got := ObjectKey("s1", f, "gz"); got != "sites/s1/releases/rel1/gz/a.css" {
		t.Errorf("ObjectKey legacy = %s", got)
	}
	a := Digest([]site.File{{Path: "b", SHA256: "2"}, {Path: "a", SHA256: "1"}})
	b := Digest([]site.File{{Path: "a", SHA256: "1"}, {Path: "b", SHA256: "2"}})
	if a != b || len(a) != 64 {
		t.Errorf("Digest depends on order: %s %s", a, b)
	}
}
