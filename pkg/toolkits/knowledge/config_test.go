package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseConfig_WithStore(t *testing.T) {
	store := NewNoopStore()
	got := ParseConfig(map[string]any{
		"store": store,
	})
	assert.Equal(t, store, got)
}

func TestParseConfig_WithoutStore(t *testing.T) {
	got := ParseConfig(map[string]any{})
	assert.Nil(t, got)
}

func TestParseConfig_NilMap(t *testing.T) {
	got := ParseConfig(nil)
	assert.Nil(t, got)
}

func TestParseConfig_WrongType(t *testing.T) {
	got := ParseConfig(map[string]any{
		"store": "not a store",
	})
	assert.Nil(t, got)
}
