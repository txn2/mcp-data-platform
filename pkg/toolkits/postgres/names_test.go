package postgres

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNameClash(t *testing.T) {
	holds := func(kinds ...string) func(string) bool {
		return func(k string) bool { return slices.Contains(kinds, k) }
	}
	assert.Contains(t, NameClash(Kind, "acme", holds("trino")), `"acme" is already a Trino connection`)
	assert.Contains(t, NameClash("trino", "orders", holds(Kind)), `"orders" is already an application database`)
	assert.Empty(t, NameClash(Kind, "orders", holds(Kind)), "the same kind is an update")
	assert.Empty(t, NameClash("s3", "orders", holds("trino", Kind)), "another kind is never compared")
	assert.Empty(t, NameClash(Kind, "free", holds()))
}
