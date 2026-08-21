package scriptrun

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingExporter stands in for the portal writer, recording what it was
// asked to persist.
type recordingExporter struct {
	requests  []ExportRequest
	published []PublishRequest
	err       error
}

func (e *recordingExporter) Export(_ context.Context, req ExportRequest) (*ExportResult, error) {
	e.requests = append(e.requests, req)
	if e.err != nil {
		return nil, e.err
	}
	return &ExportResult{AssetID: "asset_1", AssetVersion: len(e.requests), Bytes: 512}, nil
}

func (e *recordingExporter) PublishData(_ context.Context, req PublishRequest) (*ExportResult, error) {
	e.published = append(e.published, req)
	if e.err != nil {
		return nil, e.err
	}
	return &ExportResult{AssetID: "asset_1", AssetVersion: len(e.published), Bytes: 256}, nil
}

// exporterRun executes source under a platform run's shape: an exporter that
// persists what the script produces.
func exporterRun(t *testing.T, source string, exporter Exporter) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "dpx_1", FireTime: fireTime,
		Caller: &recordingCaller{}, Exporter: exporter,
	})
}

// draftPublish executes source as a draft: no exporter, so every
// platform.publish_data call previews.
func draftPublish(t *testing.T, source string) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "dpx_1", FireTime: fireTime,
		Caller: &recordingCaller{},
	})
}

func TestPublishData_PersistsThroughTheExporter(t *testing.T) {
	// Acceptance: a platform run's publish_data reaches the exporter with the
	// converted payload, and the script reads back where the version landed.
	exporter := &recordingExporter{}
	result, err := exporterRun(t,
		`res = platform.publish_data("dash", {"regions": [{"name": "west", "total": 12}]})
print(res["asset_version"])`,
		exporter)
	require.NoError(t, err)

	require.Len(t, exporter.published, 1)
	req := exporter.published[0]
	assert.Equal(t, "dash", req.Name)
	payload, ok := req.Data.(map[string]any)
	require.True(t, ok, "the payload crosses as plain Go values")
	assert.Contains(t, payload, "regions")

	require.Len(t, result.Exports, 1)
	record := result.Exports[0]
	assert.True(t, record.Refresh)
	assert.False(t, record.Preview)
	assert.Equal(t, "json", record.Format)
	assert.Equal(t, "asset_1", record.AssetID)
	assert.Equal(t, 1, record.AssetVersion)
	assert.Equal(t, 256, record.Bytes)
	assert.Contains(t, result.Log, "1")
}

func TestPublishData_DraftPreviewsWithTheRealSerializedSize(t *testing.T) {
	// Acceptance: a draft writes nothing and reports the byte size the one
	// shared serializer produces for the same payload.
	result, err := draftPublish(t, `platform.publish_data("dash", {"a": 1})`)
	require.NoError(t, err)

	require.Len(t, result.Exports, 1)
	record := result.Exports[0]
	assert.True(t, record.Preview)
	assert.True(t, record.Refresh)
	assert.Empty(t, record.AssetID, "a preview persisted nothing")

	want, ferr := FormatDataPayload("dash", map[string]any{"a": int64(1)})
	require.NoError(t, ferr)
	assert.Equal(t, len(want), record.Bytes)
}

func TestPublishData_RowCountIsHonest(t *testing.T) {
	// A list payload reports its length; a dict reports zero rather than a
	// number that is not a row count.
	result, err := draftPublish(t, `platform.publish_data("dash", [{"a": 1}, {"a": 2}])`)
	require.NoError(t, err)
	assert.Equal(t, 2, result.Exports[0].RowCount)

	result, err = draftPublish(t, `platform.publish_data("dash", {"rows": [1, 2, 3]})`)
	require.NoError(t, err)
	assert.Equal(t, 0, result.Exports[0].RowCount)
}

func TestPublishData_ArgumentRefusals(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			name:    "scalar payload",
			source:  `platform.publish_data("dash", "just a string")`,
			wantErr: "data must be a dict or a list",
		},
		{
			name:    "blank name",
			source:  `platform.publish_data("  ", {"a": 1})`,
			wantErr: "name is required",
		},
		{
			name:    "same name twice",
			source:  "platform.publish_data(\"dash\", {\"a\": 1})\nplatform.publish_data(\"dash\", {\"a\": 2})",
			wantErr: "already written",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := draftPublish(t, tt.source)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestPublishData_SharesTheOutputBudgetAndNameSpaceWithExport(t *testing.T) {
	// An export and a refresh of one name aim at one asset, so the pair is
	// refused exactly as two exports of one name are.
	exporter := &recordingExporter{}
	_, err := exporterRun(t,
		"platform.export(\"dash\", \"<html><body/></html>\", \"html\")\nplatform.publish_data(\"dash\", {\"a\": 1})",
		exporter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already written")
}

func TestFormatDataPayload_DeterministicEscapedAndBounded(t *testing.T) {
	// The serializer sorts keys, escapes the characters that could terminate a
	// script element, and holds the same ceiling every export is held to.
	out, err := FormatDataPayload("dash", map[string]any{
		"b": "</script><script>alert(1)</script>", "a": 1,
	})
	require.NoError(t, err)
	assert.NotContains(t, string(out), "</script>",
		"the payload can never terminate the element it lands inside")
	assert.Less(t, bytes.Index(out, []byte(`"a"`)), bytes.Index(out, []byte(`"b"`)),
		"keys serialize in sorted order, so the bytes are deterministic")

	_, err = FormatDataPayload("dash", map[string]any{"x": strings.Repeat("y", MaxOutputBytes)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "over the")
}
