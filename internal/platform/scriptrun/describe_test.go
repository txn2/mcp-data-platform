package scriptrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tags= and metadata= reach the exporter on the request (#1848), and a bad
// one fails the run before anything is written.
func TestExport_TagsAndMetadataReachTheExporter(t *testing.T) {
	exporter := &recordingExporter{}
	_, err := registerRun(t,
		`platform.export("x", [{"n": 1}], format="csv", tags=["report:sales"], metadata={"region": "west"})`,
		&registeringCaller{}, exporter, WritesMade)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 1)
	assert.Equal(t, []string{"report:sales"}, exporter.requests[0].Tags)
	assert.Equal(t, map[string]any{"region": "west"}, exporter.requests[0].Metadata)

	exporter = &recordingExporter{}
	_, err = registerRun(t,
		`platform.export("x", [{"n": 1}], format="csv", metadata={"run_id": "mine"})`,
		&registeringCaller{}, exporter, WritesMade)
	require.ErrorContains(t, err, "recorded by the platform")
	assert.Empty(t, exporter.requests)
}
