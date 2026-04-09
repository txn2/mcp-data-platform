package embedding

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewNoopProvider_DefaultDimension(t *testing.T) {
	t.Parallel()

	p := NewNoopProvider(0)
	assert.Equal(t, DefaultDimension, p.Dimension())
}

func TestNewNoopProvider_NegativeDimension(t *testing.T) {
	t.Parallel()

	p := NewNoopProvider(-5)
	assert.Equal(t, DefaultDimension, p.Dimension())
}

func TestNewNoopProvider_CustomDimension(t *testing.T) {
	t.Parallel()

	p := NewNoopProvider(256)
	assert.Equal(t, 256, p.Dimension())
}

func TestNoopProvider_Embed(t *testing.T) {
	t.Parallel()

	p := NewNoopProvider(4)
	emb, err := p.Embed(context.Background(), "anything")
	require.NoError(t, err)
	require.Len(t, emb, 4)

	for i, v := range emb {
		assert.Equal(t, float32(0), v, "expected zero at index %d", i)
	}
}

func TestNoopProvider_Embed_DefaultDim(t *testing.T) {
	t.Parallel()

	p := NewNoopProvider(0)
	emb, err := p.Embed(context.Background(), "test")
	require.NoError(t, err)
	assert.Len(t, emb, DefaultDimension)
}

func TestNoopProvider_EmbedBatch(t *testing.T) {
	t.Parallel()

	p := NewNoopProvider(3)
	texts := []string{"one", "two", "three"}

	results, err := p.EmbedBatch(context.Background(), texts)
	require.NoError(t, err)
	require.Len(t, results, 3)

	for i, emb := range results {
		require.Len(t, emb, 3, "batch result %d should have 3 dimensions", i)
		for j, v := range emb {
			assert.Equal(t, float32(0), v, "result[%d][%d] should be zero", i, j)
		}
	}
}

func TestNoopProvider_EmbedBatch_Empty(t *testing.T) {
	t.Parallel()

	p := NewNoopProvider(4)
	results, err := p.EmbedBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestNoopProvider_Dimension(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		dim  int
		want int
	}{
		{"zero uses default", 0, DefaultDimension},
		{"negative uses default", -1, DefaultDimension},
		{"positive kept", 128, 128},
		{"one", 1, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := NewNoopProvider(tt.dim)
			assert.Equal(t, tt.want, p.Dimension())
		})
	}
}
