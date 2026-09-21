package scriptwiring

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
