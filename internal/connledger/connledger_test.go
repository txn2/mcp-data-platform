package connledger

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLedger(t *testing.T) {
	var l Ledger
	cfg := map[string]any{"host": "trino", "port": float64(8080), "nested": map[string]any{"b": 1, "a": 2}}
	assert.False(t, l.Holds("c", cfg), "nothing recorded")

	l.Record("c", cfg)
	same := map[string]any{"nested": map[string]any{"a": 2, "b": 1}, "port": float64(8080), "host": "trino"}
	assert.True(t, l.Holds("c", same), "the same configuration in another key order")
	assert.False(t, l.Holds("c", map[string]any{"host": "trino", "port": float64(8081), "nested": map[string]any{"b": 1, "a": 2}}))
	assert.False(t, l.Holds("other", cfg))

	unencodable := map[string]any{"f": func() {}}
	assert.False(t, l.Holds("c", unencodable))
	l.Record("c", unencodable)
	assert.False(t, l.Holds("c", cfg), "an unencodable configuration forgets the recorded one")

	l.Record("c", cfg)
	l.Forget("c")
	assert.False(t, l.Holds("c", cfg))
}
