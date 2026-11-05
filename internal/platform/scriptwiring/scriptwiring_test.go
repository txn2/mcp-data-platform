package scriptwiring

import (
	"context"
	"testing"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epwire"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptadmit"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptexec"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsave"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstmt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
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
	assert.Equal(t, scriptrun.PlatformLimits{Timeout: 20 * time.Minute, MaxSteps: 9, MaxRows: 11, MaxStatementTimeout: 90 * time.Second},
		runLimits(cfg, scriptstmt.Config{MaxTimeoutSeconds: 90}))
	assert.Equal(t, scriptstmt.ToolMaxTimeout, runLimits(cfg, scriptstmt.Config{}).MaxStatementTimeout,
		"an unset scripts.sql ceiling is the tools' own cap")
	assert.Equal(t, uint64(0), runLimits(scriptadmit.Config{MaxSteps: -5}, scriptstmt.Config{}).MaxSteps,
		"a negative step count is unset, not a huge unsigned one")
	assert.Equal(t, 3, admission(cfg).Fixed)
	assert.True(t, admission(scriptadmit.Config{Concurrency: "lots"}).Adaptive(),
		"a value validation would have refused runs adaptive")
}

// A deployment with a database keeps recordings in it, and one without keeps
// none (#1939).
func TestRecordingsOver(t *testing.T) {
	assert.Nil(t, recordingsOver(nil, nil))
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	assert.NotNil(t, recordingsOver(db, nil))
	// With the platform's encryptor the recordings are sealed (#2402).
	assert.NotNil(t, recordingsOver(db, fieldcrypt.NewRestFieldEncryptor(nil)))
}

func TestScriptLineage(t *testing.T) {
	assert.Nil(t, scriptLineage(Deps{}), "no database, no lineage")
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	assert.Nil(t, scriptLineage(Deps{DB: db}), "no toolkits, no lineage")
	assert.NotNil(t, scriptLineage(Deps{DB: db, Toolkits: registry.NewRegistry()}))
}

// Applications are assembled from the same stores and the endpoint commands
// the script layer has (#2411, #2412), and a store a deployment lacks stays
// nil rather than an interface holding a nil.
func TestApplicationDeps(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	bare := applicationDeps(Deps{DB: db, Bucket: "portal", Prefix: "p"}, nil, nil)
	assert.Same(t, db, bare.DB)
	assert.Equal(t, "portal", bare.Bucket)
	assert.Nil(t, bare.Assets)
	assert.Nil(t, bare.AssetVersions)
	assert.Nil(t, bare.Blobs)
	assert.Nil(t, bare.Endpoints)
	assert.NotNil(t, bare.Claims)
	assert.Nil(t, bare.SaveGate, "no gate stays nil, never an interface holding a nil")

	gate := &scriptsave.Gate{}
	with := applicationDeps(Deps{DB: db}, &epwire.Endpoints{Commands: &epcmd.Handler{}}, gate)
	assert.NotNil(t, with.Endpoints, "an import binds endpoints through the endpoint commands")
	assert.Same(t, gate, with.SaveGate, "an import saves each script through the layer's gate (#2476)")
}
