package sitegw

import (
	"testing"

	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

// A gateway built with NoDiskCache keeps no file on disk whatever directory
// the configuration names, so a file past what memory holds is served by
// range rather than read whole (#2721).
func TestNew_NoDiskCacheKeepsNoDirectory(t *testing.T) {
	dir := isolatedCacheDir()
	cfg := siteconfig.Config{PagesDomain: testPagesDomain, Cache: siteconfig.CacheConfig{Dir: dir}}
	if g := New(Deps{Config: cfg}); g.cache.dir != dir {
		t.Fatalf("a gateway with a disk cache keeps it at %q, want %q", g.cache.dir, dir)
	}
	if g := New(Deps{Config: cfg, NoDiskCache: true}); g.cache.dir != "" {
		t.Fatalf("a gateway with no disk cache keeps one at %q", g.cache.dir)
	}
}
