package embedding

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/observability"
)

type scriptedProvider struct {
	err error
}

func (p scriptedProvider) Embed(context.Context, string) ([]float32, error) {
	if p.err != nil {
		return nil, p.err
	}
	return []float32{1, 0}, nil
}

func (p scriptedProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	if p.err != nil {
		return nil, p.err
	}
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{1, 0}
	}
	return out, nil
}
func (scriptedProvider) Dimension() int     { return 2 }
func (scriptedProvider) Kind() string       { return KindOllama }
func (scriptedProvider) Model() string      { return "test-model" }
func (scriptedProvider) MaxInputBytes() int { return 7 }

func scrapeMetrics(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

// TestWithMetrics_CountsEveryCallAndTheFallback: the query path and the
// batch path are each counted under the model; a failed query-time call is
// counted as a fallback by the search helper; the optional surfaces read
// through the wrapper.
func TestWithMetrics_CountsEveryCallAndTheFallback(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	ctx := context.Background()

	ok := WithMetrics(scriptedProvider{}, m)
	_, err = ok.Embed(ctx, "q")
	require.NoError(t, err)
	_, err = ok.EmbedBatch(ctx, []string{"a", "b"})
	require.NoError(t, err)
	assert.Equal(t, "test-model", ModelName(ok))
	capped, isCapped := ok.(inputCapped)
	require.True(t, isCapped)
	assert.Equal(t, 7, capped.MaxInputBytes())
	assert.NotNil(t, EmbedForSearch(ctx, ok, "q"))

	failing := WithMetrics(scriptedProvider{err: errors.New("ollama down")}, m)
	assert.Nil(t, EmbedForSearch(ctx, failing, "q"), "the search falls back")
	assert.Nil(t, EmbedChunksForSearch(ctx, failing, []string{"a"}))

	body := scrapeMetrics(t, m)
	for _, want := range []string{
		`embedding_calls_total{model="test-model",status="ok"} 3`,
		`embedding_calls_total{model="test-model",status="upstream_err"} 2`,
		`embedding_call_duration_seconds_count{model="test-model"} 5`,
		`embedding_fallbacks_total{model="test-model"} 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing %q", want)
		}
	}
}

func TestWithMetrics_PassesThroughWhenNothingToRecord(t *testing.T) {
	p := scriptedProvider{}
	assert.Equal(t, Provider(p), WithMetrics(p, nil), "no recorder")
	noop := NewNoopProvider(2)
	m, err := observability.New(observability.Config{Enabled: true, ListenAddr: ":0"})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	assert.Equal(t, noop, WithMetrics(noop, m), "a provider that is not configured is not measured")
	assert.Equal(t, 0, (&measured{Provider: NewNoopProvider(2)}).MaxInputBytes(), "a provider with no cap reads as none")
}
