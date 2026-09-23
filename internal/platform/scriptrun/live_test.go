package scriptrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptlive"
)

// TestRun_HandsBackTheResultAndLastProgress runs both #1845/#1847 bindings
// through the engine, the way a draft does: no Live passed, the run keeps its
// own, and the result carries both.
func TestRun_HandsBackTheResultAndLastProgress(t *testing.T) {
	result, err := execute(t, `
for i in range(3):
    platform.progress("row", done=i + 1, total=3)
print("done")
platform.result({"total": 42})
`, &recordingCaller{}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{"total": 42}`, string(result.Return))
	require.NotNil(t, result.Progress)
	assert.Equal(t, int64(3), *result.Progress.Done)
	assert.Equal(t, "done\n", result.Log)
}

// TestRun_WritesIntoTheLiveItIsGiven is the platform-run path: the worker
// hands in a Live and reads the log from it while and after the run executes.
func TestRun_WritesIntoTheLiveItIsGiven(t *testing.T) {
	live := scriptlive.New(MaxLogBytes, 0)
	opts := RunLimits(PlatformLimits{})
	opts.Source, opts.Name, opts.Live = "print(\"hello\")\nplatform.progress(\"half\", done=1, total=2)\n", "test", live
	_, err := Run(t.Context(), opts)
	require.NoError(t, err)
	snap, _ := live.Snapshot()
	assert.Equal(t, "hello\n", snap.Log)
	assert.Equal(t, "half", snap.Progress.Message)
}

func TestValidate_KnowsProgressAndResult(t *testing.T) {
	report := Validate("platform.progress(\"x\")\nplatform.result(1)\n")
	assert.True(t, report.OK, report.Findings)
	assert.Contains(t, report.Capabilities, scriptlive.CapabilityProgress)
	assert.Contains(t, report.Capabilities, scriptlive.CapabilityResult)
}
