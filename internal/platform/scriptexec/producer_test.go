package scriptexec

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedby"
)

// TestExportNamesTheScriptAsProducer covers the arm the MCP middleware cannot
// reach: the output writer stores through the portal's write funnels directly,
// so it names the script on the context it hands them, by id.
func TestExportNamesTheScriptAsProducer(t *testing.T) {
	h := newWriterHarness(t)
	var seen producedby.Producer

	h.versions.onCreateCtx = func(ctx context.Context) {
		p, ok := producedby.From(ctx)
		require.True(t, ok, "the store must be handed a producer")
		seen = p
	}

	_, err := h.writer.Export(context.Background(), csvRequest("regions"))
	require.NoError(t, err)

	assert.Equal(t, producedby.KindScript, seen.Kind)
	assert.Equal(t, h.writer.script.ID, seen.ID)
	assert.Equal(t, h.writer.script.Name, seen.Label,
		"the name is the label; the id is what survives a rename")
}

// TestProducingIsTheOneStamp keeps the two write entry points naming the
// producer the same way.
func TestProducingIsTheOneStamp(t *testing.T) {
	h := newWriterHarness(t)
	got, ok := producedby.From(h.writer.producing(context.Background()))
	require.True(t, ok)
	assert.Equal(t, h.writer.script.ID, got.ID)
}
