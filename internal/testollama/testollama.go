//go:build integration

// Package testollama provides a process-singleton Ollama container for
// integration tests. The platform's embedding-related code paths
// (api-gateway spec indexing, memory writes, future semantic-search
// consumers) all depend on a real network-backed embedder. Synthetic
// delays in test stubs do not exercise actual Ollama timeout behavior,
// which is exactly the gap that let the batched /api/embed timeout
// regression in v1.64.0 (#445) ship undetected.
//
// Usage:
//
//	func TestSomethingThatNeedsRealEmbedder(t *testing.T) {
//	    if testing.Short() { t.Skip("integration test") }
//	    ollama := testollama.Get(t)
//	    prov := ollama.Provider()
//	    ...
//	}
//
// The container starts once per test process and is reused across every
// test that calls Get. testcontainers' ryuk reaper cleans it up at
// process exit. The model pull happens once on the first Get call
// (typically 30-60s for nomic-embed-text); subsequent tests pay only
// the connection cost.
package testollama

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	tcollama "github.com/testcontainers/testcontainers-go/modules/ollama"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// DefaultModel is the embedding model the helper pulls. nomic-embed-text
// matches the platform's default (DefaultDimension = 768) and is the
// model every existing consumer assumes. Tests that need a different
// model should construct their own provider against the shared URL.
const DefaultModel = "nomic-embed-text"

// DefaultImage pins the Ollama server version. Older releases predate
// the /api/embed batch endpoint that the platform now uses; pinning
// here keeps test reality aligned with what the prod code expects.
const DefaultImage = "ollama/ollama:0.4.6"

// startupTimeout bounds the cold-start path: container boot + model
// pull. 5 minutes covers a slow first download on CI runners.
const startupTimeout = 5 * time.Minute

// Instance is the shared handle returned by Get.
type Instance struct {
	URL   string
	Model string
}

var (
	once    sync.Once
	shared  *Instance
	initErr error
)

// Get returns the process-singleton Ollama instance with the default
// embedding model pre-pulled. Subsequent calls reuse the same
// container.
func Get(t *testing.T) *Instance {
	t.Helper()
	once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
		defer cancel()

		c, err := tcollama.Run(ctx, DefaultImage)
		if err != nil {
			initErr = fmt.Errorf("testollama: run container: %w", err)
			return
		}
		if _, _, err := c.Exec(ctx, []string{"ollama", "pull", DefaultModel}); err != nil {
			initErr = fmt.Errorf("testollama: pull %s: %w", DefaultModel, err)
			return
		}
		url, err := c.ConnectionString(ctx)
		if err != nil {
			initErr = fmt.Errorf("testollama: connection string: %w", err)
			return
		}
		shared = &Instance{URL: url, Model: DefaultModel}
	})
	if initErr != nil {
		t.Fatalf("testollama init: %v", initErr)
	}
	return shared
}

// Provider returns a fresh embedding.Provider pointed at the shared
// Ollama instance, with the supplied HTTP timeout.
//
// Tests should pass the timeout that matches the production code path
// under test:
//   - 30s (embedding.DefaultTimeout * time.Second) for request-path
//     callers (memory_recall, memory_manage, capture_insight, apigateway
//     query-vector) where the shared Provider is constructed from
//     memory.embedding.ollama with no override.
//   - 5m or more for the api-gateway embed-jobs worker, which constructs
//     its own Provider with apigateway.embed_jobs.embed_timeout.
//
// Passing zero falls back to the embedding package's default.
func (i *Instance) Provider(timeout time.Duration) embedding.Provider {
	return embedding.NewOllamaProvider(embedding.OllamaConfig{
		URL:     i.URL,
		Model:   i.Model,
		Timeout: timeout,
	})
}
