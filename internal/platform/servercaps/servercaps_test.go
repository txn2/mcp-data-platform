package servercaps

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/mcptask"
)

// Tools and logging are always declared; resources, prompts and completions
// as configured; the Tasks extension where it is offered (#2009).
func TestBuild(t *testing.T) {
	bare := Build(Input{})
	assert.NotNil(t, bare.Tools)
	assert.True(t, bare.Tools.ListChanged)
	assert.NotNil(t, bare.Logging) //nolint:staticcheck // still declared during the deprecation window
	assert.Nil(t, bare.Resources)
	assert.Nil(t, bare.Prompts)
	assert.Nil(t, bare.Completions, "nothing to complete")
	assert.Empty(t, bare.Extensions)

	resources := Build(Input{Resources: true})
	assert.True(t, resources.Resources.ListChanged)
	assert.NotNil(t, resources.Completions)

	prompts := Build(Input{Prompts: true})
	assert.True(t, prompts.Prompts.ListChanged)
	assert.NotNil(t, prompts.Completions)

	tasks := Build(Input{Tasks: true})
	assert.Contains(t, tasks.Extensions, mcptask.ExtensionID)
}
