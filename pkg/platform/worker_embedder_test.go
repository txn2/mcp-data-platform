package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"

	"github.com/txn2/mcp-data-platform/internal/platform/memorylayer"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// TestWorkerEmbedder_OllamaUsesEmbedTimeout verifies that when the
// platform's embedder is Ollama, the workerEmbedder helper returns a
// FRESH Provider with the configured embed_timeout. The shared
// p.embeddingProv is not mutated and retains the 30s default request-
// path callers depend on (#445).
func TestWorkerEmbedder_OllamaUsesEmbedTimeout(t *testing.T) {
	t.Parallel()

	shared := embedding.NewOllamaProvider(embedding.OllamaConfig{
		URL:   "http://example.invalid:11434",
		Model: "nomic-embed-text",
	})
	p := &Platform{
		embeddingProv: shared,
		config: &Config{
			Memory: MemoryConfig{
				Embedding: EmbeddingConfig{
					Provider: "ollama",
					Ollama: OllamaEmbedConfig{
						URL:   "http://example.invalid:11434",
						Model: "nomic-embed-text",
					},
				},
			},
			APIGateway: APIGatewayConfig{
				EmbedJobs: APIGatewayEmbedJobsConfig{
					EmbedTimeout: 7 * time.Minute,
				},
			},
		},
	}

	worker := p.workerEmbedder()
	if reflect.ValueOf(worker).Pointer() == reflect.ValueOf(shared).Pointer() {
		t.Fatal("workerEmbedder must return a fresh Provider when Ollama is configured, not the shared instance")
	}

	got := httpTimeoutOf(t, worker)
	if got != 7*time.Minute {
		t.Errorf("worker provider timeout = %v; want %v (from embed_timeout)", got, 7*time.Minute)
	}
	if sharedT := httpTimeoutOf(t, shared); sharedT != embedding.DefaultTimeout*time.Second {
		t.Errorf("shared provider timeout changed to %v; expected to stay at the request-path default %v",
			sharedT, embedding.DefaultTimeout*time.Second)
	}
}

// TestWorkerEmbedder_OllamaDefaultsTo5Min covers the zero-value
// fallback: an operator who does not set embed_timeout gets the
// platform's documented 5-minute default rather than the 30s
// request-path default.
func TestWorkerEmbedder_OllamaDefaultsTo5Min(t *testing.T) {
	t.Parallel()

	p := &Platform{
		embeddingProv: embedding.NewOllamaProvider(embedding.OllamaConfig{}),
		config: &Config{
			Memory: MemoryConfig{
				Embedding: EmbeddingConfig{Provider: "ollama"},
			},
		},
	}
	got := httpTimeoutOf(t, p.workerEmbedder())
	if got != defaultEmbedJobsTimeout {
		t.Errorf("worker provider timeout = %v; want %v (defaultEmbedJobsTimeout)", got, defaultEmbedJobsTimeout)
	}
}

// TestWorkerEmbedder_NoopReturnsShared covers the non-Ollama path:
// when the platform's embedder is the noop placeholder (or any future
// non-Ollama kind), the worker reuses the shared instance rather than
// constructing a redundant Ollama provider.
func TestWorkerEmbedder_NoopReturnsShared(t *testing.T) {
	t.Parallel()

	shared := embedding.NewNoopProvider(768)
	p := &Platform{
		embeddingProv: shared,
		config: &Config{
			Memory: MemoryConfig{Embedding: EmbeddingConfig{Provider: ""}},
		},
	}
	if got := p.workerEmbedder(); reflect.ValueOf(got).Pointer() != reflect.ValueOf(shared).Pointer() {
		t.Errorf("non-Ollama provider should be reused by the worker; got a different instance")
	}
}

// TestWorkerEmbedder_YieldsToInteractiveEmbeds pins the wiring of #1988 end
// to end through the memory layer the platform assembles: the platform's
// embedder is the interactive side of the layer's gate, the worker's Ollama
// provider its background side, and the gate is published over the database.
// While a search's embed is held at the embedding server, a worker call waits
// instead of reaching it; the canceled context makes the wait observable
// without a sleep, since the gate refuses before any request.
func TestWorkerEmbedder_YieldsToInteractiveEmbeds(t *testing.T) {
	t.Parallel()

	entered, release := make(chan struct{}), make(chan struct{})
	var batches atomic.Int64
	ollama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			batches.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		close(entered)
		<-release
		_, _ = w.Write([]byte(`{"embedding":[0.1,0.2]}`))
	}))
	defer ollama.Close()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	// The mark is published as the search starts; its clear follows the
	// gate's linger and is pinned by the gate's own tests.
	mock.ExpectExec("INSERT INTO embed_interactive").WillReturnResult(sqlmock.NewResult(0, 1))

	ollamaCfg := OllamaEmbedConfig{URL: ollama.URL, Model: "nomic-embed-text"}
	handle, err := memorylayer.New(db, nil, memorylayer.Config{
		ToolkitName: "memory", EmbeddingProvider: "ollama",
		Ollama: embedding.OllamaConfig{URL: ollamaCfg.URL, Model: ollamaCfg.Model},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := &Platform{
		memory:        handle,
		embeddingProv: handle.EmbeddingProvider(),
		config:        &Config{Memory: MemoryConfig{Embedding: EmbeddingConfig{Provider: "ollama", Ollama: ollamaCfg}}},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = p.embeddingProv.Embed(context.Background(), "a search")
	}()
	<-entered

	worker := p.workerEmbedder()
	if embedding.ModelName(worker) != "nomic-embed-text" {
		t.Errorf("worker model = %q", embedding.ModelName(worker))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = worker.EmbedBatch(ctx, []string{"row"})
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "embedding gate:") {
		t.Fatalf("worker call returned %v; want the gate's own refusal, before any request", err)
	}
	if n := batches.Load(); n != 0 {
		t.Fatalf("worker reached Ollama %d times while an interactive embed was in flight", n)
	}
	close(release)
	<-done
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("the interactive embed was not published to the other replicas: %v", err)
	}
}

// httpTimeoutOf returns the Timeout on the *http.Client embedded in an
// Ollama provider. It uses reflection to reach the unexported field
// because the embedding package does not expose it; the test sits in
// the same module so this is safe.
func httpTimeoutOf(t *testing.T, p embedding.Provider) time.Duration {
	t.Helper()
	v := reflect.ValueOf(p)
	if v.Kind() != reflect.Ptr {
		return 0
	}
	clientField := v.Elem().FieldByName("client")
	if !clientField.IsValid() {
		return 0
	}
	// clientField is an unexported *http.Client; use unsafe pointer
	// reach via the reflect Value's UnsafePointer().
	clientPtr := (*http.Client)(clientField.UnsafePointer())
	if clientPtr == nil {
		return 0
	}
	return clientPtr.Timeout
}
