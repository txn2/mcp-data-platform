package scriptexec

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A version a script writes moves its asset's head, so the tables registered
// over the output follow it (#1536). Both writes -- an export and a data
// refresh -- hand the version they recorded to the follower, record its answer
// on the run's output, and return it to the host so the script and the run log
// see it.

type followRecorder struct {
	asked  []string
	answer []string
}

func (f *followRecorder) follow(_ context.Context, assetID string, version int) []string {
	f.asked = append(f.asked, assetID+"@"+strconv.Itoa(version))
	return f.answer
}

func TestOutputWriter_ExportFollowsTheTablesOverTheOutput(t *testing.T) {
	h := newWriterHarness(t)
	follow := &followRecorder{answer: []string{"scratch.uploads.t on scratch now reads version 1."}}
	h.writer.deps.FollowTables = follow.follow

	result, err := h.writer.Export(context.Background(), csvRequest("daily"))
	require.NoError(t, err)

	assert.Equal(t, []string{"scratch.uploads.t on scratch now reads version 1."}, result.Tables)
	require.Len(t, h.assets.inserted, 1)
	assert.Equal(t, []string{h.assets.inserted[0].ID + "@1"}, follow.asked,
		"the follower is given the asset and the version the write recorded")
	require.Len(t, h.runs.outputs, 1)
	assert.Equal(t, result.Tables, h.runs.outputs[0].Tables, "the run's own record carries the report")
}

func TestOutputWriter_PublishDataFollowsTheTablesOverTheOutput(t *testing.T) {
	h := publishHarness(t, "dash", "html", htmlDashboard)
	follow := &followRecorder{answer: []string{"scratch.uploads.t on scratch is pinned"}}
	h.writer.deps.FollowTables = follow.follow

	res, err := h.writer.PublishData(context.Background(), publishRequest("dash",
		map[string]any{"regions": []any{map[string]any{"name": "west"}}}))
	require.NoError(t, err)

	assert.Equal(t, []string{"scratch.uploads.t on scratch is pinned"}, res.Tables)
	require.Len(t, follow.asked, 1)
	assert.Contains(t, follow.asked[0], "@2", "the refresh is the asset's next version")
	require.Len(t, h.run.Outputs, 1)
	assert.Equal(t, res.Tables, h.run.Outputs[0].Tables)
}

func TestOutputWriter_WithoutAFollowerSaysNothingAboutTables(t *testing.T) {
	h := newWriterHarness(t)
	result, err := h.writer.Export(context.Background(), csvRequest("daily"))
	require.NoError(t, err)
	assert.Nil(t, result.Tables)
	assert.Nil(t, h.runs.outputs[0].Tables)
}
