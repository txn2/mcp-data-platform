package connset

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errNotFound = errors.New("not found")

type conn struct{ url string }

type fakeStore map[string]map[string]any

func (f fakeStore) GetConnection(_ context.Context, name string) (map[string]any, error) {
	if c, ok := f[name]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("connection %s: %w", name, errNotFound)
}

func newSet(released *[]string) *Set[*conn] {
	return New("caldav", errNotFound,
		func(_ string, cfg map[string]any) (*conn, error) {
			u, _ := cfg["url"].(string)
			if u == "" {
				return nil, errors.New("url is required")
			}
			return &conn{url: u}, nil
		},
		func(c *conn) { *released = append(*released, c.url) })
}

func TestSet_Lifecycle(t *testing.T) {
	var released []string
	s := newSet(&released)
	ctx := context.Background()

	require.NoError(t, s.Add("work", map[string]any{"url": "a"}))
	assert.ErrorIs(t, s.Add("work", map[string]any{"url": "b"}), ErrExists)
	assert.Equal(t, []string{"b"}, released, "the duplicate's build is released")
	assert.Error(t, s.Add("bad", map[string]any{}))

	require.NoError(t, s.Update("work", map[string]any{"url": "c"}))
	assert.Equal(t, []string{"b", "a"}, released, "the replaced connection is released")
	assert.ErrorIs(t, s.Update("nope", map[string]any{"url": "d"}), errNotFound)
	assert.Error(t, s.Update("work", map[string]any{}))

	c, err := s.Get(ctx, "work")
	require.NoError(t, err)
	assert.Equal(t, "c", c.url)
	c, err = s.Get(ctx, "")
	require.NoError(t, err, "the only connection is the default")
	assert.Equal(t, "c", c.url)

	require.NoError(t, s.Adopt("home", map[string]any{"url": "h"}))
	_, err = s.Get(ctx, "")
	assert.ErrorContains(t, err, "name a caldav connection; this deployment has home, work")
	assert.Equal(t, []string{"home", "work"}, s.Names())

	var seen []string
	s.Each(func(n string, _ *conn) { seen = append(seen, n) })
	assert.Equal(t, []string{"home", "work"}, seen)

	s.Put("home", &conn{url: "h2"})
	c, err = s.Get(ctx, "home")
	require.NoError(t, err)
	assert.Equal(t, "h2", c.url)
	assert.Contains(t, released, "h", "the connection Put replaced is released")

	require.NoError(t, s.Remove("home"))
	assert.ErrorIs(t, s.Remove("home"), errNotFound)
	s.Close()
	assert.Empty(t, s.Names())
	_, err = s.Get(ctx, "")
	assert.ErrorContains(t, err, "no caldav connection is configured")
}

func TestSet_CatchesUpFromTheStore(t *testing.T) {
	var released []string
	s := newSet(&released)
	ctx := context.Background()
	_, err := s.Get(ctx, "saved-elsewhere")
	assert.ErrorIs(t, err, errNotFound, "without a store a miss is a miss")

	s.SetStore(fakeStore{"saved-elsewhere": {"url": "x"}})
	c, err := s.Get(ctx, "saved-elsewhere")
	require.NoError(t, err)
	assert.Equal(t, "x", c.url)
	_, err = s.Get(ctx, "never-saved")
	assert.ErrorIs(t, err, errNotFound)
}
