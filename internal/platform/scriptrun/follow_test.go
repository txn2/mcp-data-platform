package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// What a write did to the tables registered over the file it wrote reaches a
// script and its run log (#1536): an export's record carries it and the script
// reads it back, and a platform.call whose result carries it is echoed into
// the log the same way. The run that put a table behind its file says so in
// its history whether or not the script prints anything.

// followingExporter answers every write with one table report.
type followingExporter struct {
	recordingExporter
	tables []string
}

func (e *followingExporter) Export(ctx context.Context, req ExportRequest) (*ExportResult, error) {
	res, err := e.recordingExporter.Export(ctx, req)
	if err != nil {
		return nil, err
	}
	res.Tables = e.tables
	return res, nil
}

func (e *followingExporter) PublishData(ctx context.Context, req PublishRequest) (*ExportResult, error) {
	res, err := e.recordingExporter.PublishData(ctx, req)
	if err != nil {
		return nil, err
	}
	res.Tables = e.tables
	return res, nil
}

func TestRun_ExportCarriesTheTableReportToTheScriptAndTheLog(t *testing.T) {
	exporter := &followingExporter{tables: []string{"scratch.uploads.t on scratch now reads version 1."}}
	result, err := exporterRun(t, `
out = platform.export(name="daily", rows=[{"a": 1}], format="json")
print(out["tables"][0])
res = platform.publish_data("dash", {"k": 1})
print(len(res["tables"]))
`, exporter)
	require.NoError(t, err)

	require.Len(t, result.Exports, 2)
	assert.Equal(t, []string{"scratch.uploads.t on scratch now reads version 1."}, result.Exports[0].Tables)
	assert.Equal(t, []string{"scratch.uploads.t on scratch now reads version 1."}, result.Exports[1].Tables)
	assert.Equal(t,
		"tables: daily: scratch.uploads.t on scratch now reads version 1.\n"+
			"scratch.uploads.t on scratch now reads version 1.\n"+
			"tables: dash: scratch.uploads.t on scratch now reads version 1.\n"+
			"1\n",
		result.Log, "the host writes the report into the log before the script's own line")
}

func TestRun_ExportWithNoTablesSaysNothing(t *testing.T) {
	result, err := exporterRun(t, `
out = platform.export(name="daily", rows=[{"a": 1}], format="json")
print("tables" in out)
`, &recordingExporter{})
	require.NoError(t, err)
	assert.Equal(t, "False\n", result.Log)
	assert.Nil(t, result.Exports[0].Tables)
}

// tablesCaller answers a tool call the way manage_resource replace_content
// does: a result carrying the sentences about the tables over the file.
type tablesCaller struct {
	recordingCaller
	tables []any
}

func (c *tablesCaller) CallTool(ctx context.Context, name string, args map[string]any) (map[string]any, error) {
	out, err := c.recordingCaller.CallTool(ctx, name, args)
	if err != nil {
		return nil, err
	}
	out["tables"] = c.tables
	out["message"] = "Content replaced and recorded as version 7."
	return out, nil
}

func TestRun_CallEchoesATableReportIntoTheLog(t *testing.T) {
	caller := &tablesCaller{tables: []any{
		"scratch.uploads.t on scratch now reads version 7.",
		"scratch.uploads.s on scratch is pinned to the version it was registered over and is now behind this file.",
		42, // a value that is not a sentence is skipped, not printed
	}}
	result, err := execute(t, `
res = platform.call("manage_resource", {"action": "replace_content", "reference": "mcp:resource:r1", "content": "a,b"})
print(res["message"])
`, caller, nil)
	require.NoError(t, err)

	assert.Equal(t,
		"tables: manage_resource: scratch.uploads.t on scratch now reads version 7.\n"+
			"tables: manage_resource: scratch.uploads.s on scratch is pinned to the version it was registered"+
			" over and is now behind this file.\n"+
			"Content replaced and recorded as version 7.\n",
		result.Log)
}

func TestTableSentences(t *testing.T) {
	assert.Nil(t, tableSentences(map[string]any{}))
	assert.Nil(t, tableSentences(map[string]any{"tables": "not a list"}))
	assert.Equal(t, []string{"a", "b"}, tableSentences(map[string]any{"tables": []any{"a", 1, "b"}}))
}
