package connscope

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRefConnections(t *testing.T) {
	assert.Nil(t, RefConnections(nil, "mcp:glossary_term:revenue"), "a definition names no connection")
	assert.Nil(t, RefConnections(nil, "not a reference"))
	assert.Equal(t, []string{"warehouse"}, RefConnections(nil, "mcp:dataset:(trino,warehouse,a.b.c)"),
		"an instance no toolkit serves is matched by its own name")
}
