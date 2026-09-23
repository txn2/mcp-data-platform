package scriptwiring

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptadmit"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The stores stand in for the run queue and the script records; building the
// handle only asks that there are some.
type (
	runStore     struct{ script.RunStore }
	scriptReader struct{ scriptexec.ScriptReader }
	versionStore struct{ scriptexec.VersionReader }
)

// TestDraftExports adapts the handle's draft writer to the runner's hook
// (#1822): a deployment with nowhere to keep runs gives drafts no writer, and
// one with a handle gives each draft a writer that files outputs under the
// draft's script and refuses what that writer refuses.
func TestDraftExports(t *testing.T) {
	assert.Nil(t, draftExports(nil), "no handle, no writer: every draft export previews")

	scripts := scriptexec.New(scriptexec.Config{
		Runs: runStore{}, Scripts: scriptReader{}, Versions: versionStore{}, WorkerDisabled: true,
	})
	require.NotNil(t, scripts)
	exports := draftExports(scripts)
	require.NotNil(t, exports)

	w := exports(scriptdraft.Target{
		Script: &script.Script{Name: "brand-new"}, RunID: "dpx_1",
		Identity: scriptdraft.Identity{UserID: "sub-1", Email: "jane@example.com", Roles: []string{"analyst"}},
	})
	require.NotNil(t, w)
	_, err := w.Export(context.Background(), scriptrun.ExportRequest{
		Name: "daily", Format: "csv", Destination: script.PortalDestination(),
	})
	require.Error(t, err, "an unsaved script's portal output is refused by the draft writer")
	assert.Contains(t, err.Error(), "not saved yet")
}

// TestRunLimits_AndAdmission convert the scripts.worker settings into what
// the engine and the worker take (#1843).
func TestRunLimits_AndAdmission(t *testing.T) {
	cfg := scriptadmit.Config{
		Concurrency: "3", RunTimeout: 20 * time.Minute, MaxSteps: 9, MaxQueryRows: 11,
	}
	assert.Equal(t, scriptrun.PlatformLimits{Timeout: 20 * time.Minute, MaxSteps: 9, MaxRows: 11}, runLimits(cfg))
	assert.Equal(t, uint64(0), runLimits(scriptadmit.Config{MaxSteps: -5}).MaxSteps,
		"a negative step count is unset, not a huge unsigned one")
	assert.Equal(t, 3, admission(cfg).Fixed)
	assert.True(t, admission(scriptadmit.Config{Concurrency: "lots"}).Adaptive(),
		"a value validation would have refused runs adaptive")
}
