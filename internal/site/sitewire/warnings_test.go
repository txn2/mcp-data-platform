package sitewire

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/site"
)

// captureWarnings routes slog at WARN into a buffer for the test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

type listedSites struct {
	sites []site.Site
	err   error
}

func (l listedSites) List(context.Context) ([]site.Site, error) { return l.sites, l.err }

// TestWarnReservedLabels pins #2611: a site already named with two
// characters is named in a configuration warning at startup, with the reason,
// and no other site is; with no pages domain no site has such an address.
func TestWarnReservedLabels(t *testing.T) {
	ctx := context.Background()
	sites := listedSites{sites: []site.Site{{Name: "ab"}, {Name: "docs"}, {Name: "a"}}}

	logs := captureWarnings(t)
	warnReservedLabels(ctx, sites, "")
	assert.Empty(t, logs.String(), "no pages domain, no address to warn about")

	warnReservedLabels(ctx, sites, "pages.example.net")
	out := logs.String()
	assert.Contains(t, out, "code=site_reserved_label")
	assert.Contains(t, out, "site=ab")
	assert.Contains(t, out, "ab--prod.pages.example.net")
	assert.NotContains(t, out, "site=docs")
	assert.NotContains(t, out, "site=a ")

	logs.Reset()
	warnReservedLabels(ctx, listedSites{err: errors.New("db down")}, "pages.example.net")
	assert.Contains(t, logs.String(), "listing sites for the startup checks failed")
	assert.NotContains(t, logs.String(), "code=site_reserved_label")
}
