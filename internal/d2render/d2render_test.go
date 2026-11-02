package d2render

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderDrawsTheDiagramInEitherTheme(t *testing.T) {
	src := "vpc: {\n  web -> api: http\n  api -> db\n}\nusers -> vpc.web\n"
	light, err := Render(context.Background(), src, false)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(string(light)), "<?xml") || strings.Contains(string(light), "<svg"))
	for _, label := range []string{"vpc", "web", "api", "db", "users", "http"} {
		assert.Contains(t, string(light), label)
	}
	dark, err := Render(context.Background(), src, true)
	require.NoError(t, err)
	assert.NotEqual(t, string(light), string(dark), "the dark drawing is its own")

	again, err := Render(context.Background(), src, false)
	require.NoError(t, err)
	assert.Equal(t, light, again, "served from the cache")
}

func TestRenderRefuses(t *testing.T) {
	_, err := Render(context.Background(), strings.Repeat("a", MaxSourceBytes+1), false)
	require.ErrorIs(t, err, ErrTooLarge)

	_, err = Render(context.Background(), "a: {shape: nosuchshape}", false)
	require.Error(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Render(ctx, "x -> y\n", false)
	require.ErrorIs(t, err, ErrTimeout, "a caller that is gone gets no render")
}

func TestLRUEvictsTheOldest(t *testing.T) {
	c := newLRU(2)
	c.put("a", []byte("1"))
	c.put("b", []byte("2"))
	_, _ = c.get("a")
	c.put("c", []byte("3"))
	_, okA := c.get("a")
	_, okB := c.get("b")
	_, okC := c.get("c")
	assert.True(t, okA, "a was used most recently")
	assert.False(t, okB, "b was the oldest")
	assert.True(t, okC)
}
