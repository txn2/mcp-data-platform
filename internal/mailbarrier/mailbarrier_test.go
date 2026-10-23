package mailbarrier

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/toolwrite"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type recCaller struct {
	calls    []string
	readOnly map[string]bool
}

func (r *recCaller) CallTool(_ context.Context, name string, _ map[string]any) (map[string]any, error) {
	r.calls = append(r.calls, name)
	return map[string]any{}, nil
}

func (r *recCaller) DeclaresReadOnly(_ context.Context, name string) (readOnly, known bool) {
	v, ok := r.readOnly[name]
	return v, ok
}

type plainCaller struct{}

func (plainCaller) CallTool(context.Context, string, map[string]any) (map[string]any, error) {
	return map[string]any{}, nil
}

type recExporter struct {
	exports, publishes int
	recorded           []script.RunOutput
}

func (r *recExporter) RecordToolOutput(_ context.Context, out script.RunOutput) {
	r.recorded = append(r.recorded, out)
}

func (r *recExporter) Export(context.Context, scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	r.exports++
	return &scriptrun.ExportResult{}, nil
}

func (r *recExporter) PublishData(context.Context, scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	r.publishes++
	return &scriptrun.ExportResult{}, nil
}

func TestBarrier_WritesBeforeMailAreAllowed(t *testing.T) {
	next, exp := &recCaller{}, &recExporter{}
	c, e := Guard(false, toolwrite.Classifier{}, next, exp)
	ctx := context.Background()
	_, err := c.CallTool(ctx, "trino_execute", nil)
	require.NoError(t, err, "a script that has not read mail writes as before")
	_, err = e.Export(ctx, scriptrun.ExportRequest{})
	require.NoError(t, err)
}

func TestBarrier_RefusesWritesAfterMailUnlessAllowed(t *testing.T) {
	ctx := context.Background()
	next, exp := &recCaller{readOnly: map[string]bool{"vendor__lookup": true}}, &recExporter{}
	c, e := Guard(false, toolwrite.Classifier{}, next, exp)

	_, err := c.CallTool(ctx, "mail_search", nil)
	require.NoError(t, err)
	_, err = c.CallTool(ctx, "trino_query", nil)
	require.NoError(t, err, "reads stay allowed")
	_, err = c.CallTool(ctx, "vendor__lookup", nil)
	require.NoError(t, err, "a gateway tool advertised read-only is a read")

	_, err = c.CallTool(ctx, "notify", map[string]any{"action": "send"})
	var refused *RefusalError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "mail_search", refused.MailTool)
	assert.Contains(t, err.Error(), "mail_actions=true")
	_, err = c.CallTool(ctx, "vendor__create", nil)
	assert.ErrorAs(t, err, &refused, "an unknown tool is a write")
	_, err = e.Export(ctx, scriptrun.ExportRequest{})
	assert.ErrorContains(t, err, "platform.export")
	_, err = e.PublishData(ctx, scriptrun.PublishRequest{})
	assert.ErrorContains(t, err, "platform.publish_data")
	assert.Equal(t, []string{"mail_search", "trino_query", "vendor__lookup"}, next.calls)
	assert.Zero(t, exp.exports+exp.publishes)

	declarer, ok := c.(scriptrun.ReadOnlyDeclarer)
	require.True(t, ok)
	ro, known := declarer.DeclaresReadOnly(ctx, "vendor__lookup")
	assert.True(t, ro && known)
}

func TestBarrier_AllowedScriptActsOnMail(t *testing.T) {
	ctx := context.Background()
	next, exp := &recCaller{}, &recExporter{}
	c, e := Guard(true, toolwrite.Classifier{}, next, exp)
	_, _ = c.CallTool(ctx, "mail_message", nil)
	_, err := c.CallTool(ctx, "save_asset", nil)
	require.NoError(t, err)
	_, err = e.PublishData(ctx, scriptrun.PublishRequest{})
	require.NoError(t, err)
}

func TestBarrier_CapsMailCallsPerRun(t *testing.T) {
	c, _ := Guard(true, toolwrite.Classifier{}, &recCaller{}, nil)
	ctx := context.Background()
	for range MaxMailCallsPerRun {
		_, err := c.CallTool(ctx, "mail_message", nil)
		require.NoError(t, err)
	}
	_, err := c.CallTool(ctx, "mail_message", nil)
	assert.ErrorContains(t, err, "the most one run makes")
}

func TestExporter_PassesToolOutputsThrough(t *testing.T) {
	exp := &recExporter{}
	_, e := Guard(false, toolwrite.Classifier{}, plainCaller{}, exp)
	rec, ok := e.(scriptrun.ToolOutputRecorder)
	require.True(t, ok, "an output an export tool wrote is still listed on the run")
	rec.RecordToolOutput(context.Background(), script.RunOutput{Name: "big-report"})
	assert.Len(t, exp.recorded, 1)
	_, e = Guard(false, toolwrite.Classifier{}, plainCaller{}, &noRecord{})
	still, ok := e.(scriptrun.ToolOutputRecorder)
	require.True(t, ok)
	still.RecordToolOutput(context.Background(), script.RunOutput{})
}

type noRecord struct{ recExporter }

func (*noRecord) RecordToolOutput() {}

func TestGuard_NilsStayNil(t *testing.T) {
	c, e := Guard(false, toolwrite.Classifier{}, nil, nil)
	assert.Nil(t, c)
	assert.Nil(t, e)
	c, e = Guard(false, toolwrite.Classifier{}, plainCaller{}, nil)
	assert.NotNil(t, c)
	assert.Nil(t, e)
	declarer, ok := c.(scriptrun.ReadOnlyDeclarer)
	require.True(t, ok)
	_, known := declarer.DeclaresReadOnly(context.Background(), "x")
	assert.False(t, known)
	_, _ = c.CallTool(context.Background(), "mail_search", nil)
	_, err := c.CallTool(context.Background(), "unknown_writer", nil)
	assert.True(t, errors.As(err, new(*RefusalError)), "with no declarer an unknown tool is a write")
}
