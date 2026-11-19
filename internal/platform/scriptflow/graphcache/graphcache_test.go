package graphcache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCache_EvictsTheLeastRecentlyUsed(t *testing.T) {
	c := New[int](2)
	k := func(b byte) Key { return Key{b} }
	c.Put(k(1), 1)
	c.Put(k(2), 2)
	_, _ = c.Get(k(1))
	c.Put(k(3), 3)
	c.Put(k(3), 3)
	_, ok := c.Get(k(2))
	assert.False(t, ok, "2 was least recently used")
	v, ok := c.Get(k(1))
	assert.True(t, ok)
	assert.Equal(t, 1, v)
}

func TestKeyOf(t *testing.T) {
	assert.Equal(t, KeyOf("s", nil), KeyOf("s", map[string]string{}))
	a := KeyOf("s", map[string]string{"lib:a@1": "x", "lib:b@1": "y"})
	assert.Equal(t, a, KeyOf("s", map[string]string{"lib:b@1": "y", "lib:a@1": "x"}), "order does not matter")
	assert.NotEqual(t, a, KeyOf("s", map[string]string{"lib:a@1": "x", "lib:b@1": "z"}))
	assert.NotEqual(t, a, KeyOf("s", nil))
}
