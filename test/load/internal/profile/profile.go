// Package profile captures Go pprof profiles from a running platform binary's
// debug pprof listener (enabled with PPROF_ADDR). Profiles complement the
// Prometheus scrapes: the scrape shows THAT goroutines/heap moved; the profile
// shows WHERE, which feeds the "what degrades first" saturation narrative and
// makes the soak flat-memory assertion diagnosable.
package profile

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Capturer fetches pprof profiles from a base URL (e.g. http://localhost:6060)
// and writes them into an output directory.
type Capturer struct {
	BaseURL string
	OutDir  string
	Client  *http.Client
}

// New returns a Capturer. A nil client uses a long-timeout default (CPU
// profiles stream for their whole duration). An empty baseURL disables capture:
// every method becomes a no-op returning an empty path.
func New(baseURL, outDir string, client *http.Client) *Capturer {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Capturer{BaseURL: baseURL, OutDir: outDir, Client: client}
}

// Enabled reports whether a pprof base URL was configured.
func (c *Capturer) Enabled() bool {
	return c != nil && c.BaseURL != ""
}

// CaptureCPU fetches a CPU profile sampled over d and writes it as
// <prefix>-cpu.pprof. It blocks for approximately d (the endpoint samples for
// `seconds`). Returns the written file path.
func (c *Capturer) CaptureCPU(ctx context.Context, prefix string, d time.Duration) (string, error) {
	if !c.Enabled() {
		return "", nil
	}
	secs := max(int(d.Seconds()), 1)
	url := fmt.Sprintf("%s/debug/pprof/profile?seconds=%d", strings.TrimRight(c.BaseURL, "/"), secs)
	return c.fetch(ctx, url, prefix+"-cpu.pprof")
}

// CaptureHeap writes the current heap profile as <prefix>-heap.pprof.
func (c *Capturer) CaptureHeap(ctx context.Context, prefix string) (string, error) {
	if !c.Enabled() {
		return "", nil
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/debug/pprof/heap"
	return c.fetch(ctx, url, prefix+"-heap.pprof")
}

// CaptureGoroutine writes the current goroutine profile as
// <prefix>-goroutine.pprof.
func (c *Capturer) CaptureGoroutine(ctx context.Context, prefix string) (string, error) {
	if !c.Enabled() {
		return "", nil
	}
	url := strings.TrimRight(c.BaseURL, "/") + "/debug/pprof/goroutine"
	return c.fetch(ctx, url, prefix+"-goroutine.pprof")
}

// fetch downloads url and writes the body to OutDir/name, returning the path.
func (c *Capturer) fetch(ctx context.Context, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("building pprof request: %w", err)
	}
	resp, err := c.Client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetching %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetching %s: status %d", url, resp.StatusCode)
	}
	if err := os.MkdirAll(c.OutDir, 0o750); err != nil {
		return "", fmt.Errorf("creating profile dir: %w", err)
	}
	path := filepath.Join(c.OutDir, name)
	f, err := os.Create(path) //nolint:gosec // path derived from a fixed prefix + harness-controlled OutDir
	if err != nil {
		return "", fmt.Errorf("creating profile file: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, resp.Body); err != nil {
		return "", fmt.Errorf("writing profile: %w", err)
	}
	return path, nil
}
