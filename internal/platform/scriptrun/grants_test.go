package scriptrun

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// recordingExporter stands in for the portal writer, recording what it was
// asked to persist.
type recordingExporter struct {
	requests []ExportRequest
	err      error
}

func (e *recordingExporter) Export(_ context.Context, req ExportRequest) (*ExportResult, error) {
	e.requests = append(e.requests, req)
	if e.err != nil {
		return nil, e.err
	}
	return &ExportResult{AssetID: "asset_1", AssetVersion: len(e.requests), Bytes: 512}, nil
}

// grantedRun executes source under an approved run's shape: a grant that
// confines it, and an exporter that persists what it produces.
func grantedRun(t *testing.T, source string, grants *script.Grants, exporter Exporter) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "dpx_1", FireTime: fireTime,
		Caller: &recordingCaller{}, Grants: grants, Exporter: exporter,
	})
}

// fullGrant permits everything the platform implements.
func fullGrant() *script.Grants {
	return &script.Grants{
		Roles:        []string{"analyst"},
		Connections:  []string{"warehouse"},
		Capabilities: script.Capabilities,
		Destinations: []script.Destination{script.PortalDestination()},
	}
}

// TestGrantEnforcement_RefusesWhatWasNotApproved covers the host facade's half
// of the layered check: every axis of a grant, refused inside the interpreter
// with a message naming what was granted.
func TestGrantEnforcement_RefusesWhatWasNotApproved(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		mutate  func(*script.Grants)
		wantErr string
	}{
		{
			name:    "capability not granted",
			source:  `platform.query(connection="warehouse", sql="SELECT 1")`,
			mutate:  func(g *script.Grants) { g.Capabilities = []string{script.CapabilityExport} },
			wantErr: "the platform.query binding is not in this script's approved grant",
		},
		{
			name:    "connection not granted",
			source:  `platform.query(connection="finance", sql="SELECT 1")`,
			mutate:  func(*script.Grants) {},
			wantErr: `connection "finance" is not in this script's approved grant`,
		},
		{
			name:    "connection not named",
			source:  `platform.query(sql="SELECT 1")`,
			mutate:  func(*script.Grants) {},
			wantErr: "must name the connection to query",
		},
		{
			name:    "export capability not granted",
			source:  `platform.export(name="daily", rows=[])`,
			mutate:  func(g *script.Grants) { g.Capabilities = []string{script.CapabilityQuery} },
			wantErr: "the platform.export binding is not in this script's approved grant",
		},
		{
			name:    "no destination granted",
			source:  `platform.export(name="daily", rows=[])`,
			mutate:  func(g *script.Grants) { g.Destinations = nil },
			wantErr: "approved with no output destinations",
		},
		{
			name:   "destination not granted",
			source: `platform.export(name="daily", rows=[])`,
			mutate: func(g *script.Grants) {
				g.Destinations = []script.Destination{{
					Name: "elsewhere", Kind: script.DestinationKindS3,
					Connection: "acme-s3", Bucket: "exports",
				}}
			},
			wantErr: `destination "portal" is not in this script's approved grant`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			grants := fullGrant()
			tt.mutate(grants)
			_, err := grantedRun(t, tt.source, grants, &recordingExporter{})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestGrantEnforcement_DraftRunsCarryNoGrantLayer pins the distinction between
// "nothing granted" and "no grant applies": a draft runs as its own author, so
// there is nothing to narrow, and the same calls that a grant would refuse go
// through.
func TestGrantEnforcement_DraftRunsCarryNoGrantLayer(t *testing.T) {
	result, err := grantedRun(t, `
res = platform.query(sql="SELECT 1")
platform.export(name="daily", rows=[{"a": 1}])
`, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, result.Queries)
	require.Len(t, result.Exports, 1)
	assert.True(t, result.Exports[0].Preview, "a draft writes nothing")
}

// TestGrantEnforcement_EmptyGrantDeniesEverything is the reason Grants is a
// pointer: an approved version with nothing granted must refuse, not permit.
func TestGrantEnforcement_EmptyGrantDeniesEverything(t *testing.T) {
	_, err := grantedRun(t, `platform.query(connection="warehouse", sql="SELECT 1")`,
		&script.Grants{}, &recordingExporter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "granted: none")
}

// TestExport_PersistsThroughTheExporter is preview becoming persistence: the
// same call, the same arguments, and now an asset version the script can name.
func TestExport_PersistsThroughTheExporter(t *testing.T) {
	exporter := &recordingExporter{}
	result, err := grantedRun(t, `
out = platform.export(name="daily", rows=[{"region": "west", "total": 1}], format="csv")
print(json.encode(out))
`, fullGrant(), exporter)
	require.NoError(t, err)

	require.Len(t, exporter.requests, 1)
	assert.Equal(t, "daily", exporter.requests[0].Name)
	assert.Equal(t, "csv", exporter.requests[0].Format)
	assert.Equal(t, []string{"region", "total"}, exporter.requests[0].Columns,
		"the column order the author wrote reaches the writer")

	require.Len(t, result.Exports, 1)
	assert.False(t, result.Exports[0].Preview)
	assert.Equal(t, "asset_1", result.Exports[0].AssetID)
	assert.Equal(t, 1, result.Exports[0].AssetVersion)
	assert.Equal(t, 512, result.Exports[0].Bytes, "the writer's own byte count wins over the estimate")
	assert.Contains(t, result.Log, `"asset_id":"asset_1"`)
	assert.Contains(t, result.Log, `"preview":false`)
}

// TestExport_WriterFailureFailsTheRun pins that a report whose output did not
// persist did not happen.
func TestExport_WriterFailureFailsTheRun(t *testing.T) {
	_, err := grantedRun(t, `platform.export(name="daily", rows=[{"a": 1}])`,
		fullGrant(), &recordingExporter{err: errors.New("s3 unreachable")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "s3 unreachable")
}

// TestColumnOrder_FollowsTheScriptNotTheMap is the determinism property behind
// tabular output: Go maps have no order, so the column order is read from the
// Starlark rows, where the author's insertion order survives.
func TestColumnOrder_FollowsTheScriptNotTheMap(t *testing.T) {
	exporter := &recordingExporter{}
	_, err := grantedRun(t, `
platform.export(name="daily", rows=[
    {"zebra": 1, "apple": 2},
    {"apple": 3, "mango": 4},
])
`, fullGrant(), exporter)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 1)
	assert.Equal(t, []string{"zebra", "apple", "mango"}, exporter.requests[0].Columns,
		"first row's keys in written order, then keys a later row introduces")
}

// TestColumnOrder_NonListAndNonDictRows covers the shapes columnOrder must not
// choke on: rows that are not a list, and entries that are not dicts.
func TestColumnOrder_NonListAndNonDictRows(t *testing.T) {
	exporter := &recordingExporter{}
	_, err := grantedRun(t, `platform.export(name="daily", rows=[{"a": 1}, 7])`,
		fullGrant(), exporter)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 1)
	assert.Equal(t, []string{"a"}, exporter.requests[0].Columns)
	assert.Len(t, exporter.requests[0].Rows, 2, "a non-dict row still counts as a row")
}

// TestApprovedLimits_AreLooserThanADraftOnEveryAxis pins the policy an approved
// run executes under, since a draft's tighter limits would otherwise silently
// govern scheduled work.
func TestApprovedLimits_AreLooserThanADraftOnEveryAxis(t *testing.T) {
	limits := ApprovedLimits()
	assert.Greater(t, limits.MaxSteps, uint64(DraftMaxSteps))
	assert.Greater(t, limits.Timeout, DraftTimeout)
	assert.Greater(t, limits.MaxRows, DraftMaxRows)
	assert.Greater(t, limits.MaxResultBytes, DraftMaxResultBytes)
	assert.Equal(t, MaxLogBytes, limits.MaxLogBytes)
}

// deliveryGrant permits everything fullGrant does, plus one external
// destination.
func deliveryGrant() *script.Grants {
	grants := fullGrant()
	grants.Destinations = append(grants.Destinations, script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	})
	return grants
}

// TestExport_ResolvesTheDestinationItsApprovalPinned pins what the engine hands
// the writer: the address the grant carries, never a name the script could
// point somewhere else.
func TestExport_ResolvesTheDestinationItsApprovalPinned(t *testing.T) {
	exporter := &recordingExporter{}
	_, err := grantedRun(t, `
platform.export(name="daily", rows=[{"a": 1}])
platform.export(name="daily", rows=[{"a": 1}], destination="acme-drop", key="2026/08/sales.csv")
`, deliveryGrant(), exporter)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 2)

	assert.Equal(t, script.PortalDestination(), exporter.requests[0].Destination,
		"an export naming no destination goes to the portal")
	assert.Empty(t, exporter.requests[0].Key)

	delivered := exporter.requests[1]
	assert.Equal(t, "acme-s3", delivered.Destination.Connection)
	assert.Equal(t, "acme-exports", delivered.Destination.Bucket)
	assert.Equal(t, "weekly", delivered.Destination.Prefix)
	assert.Equal(t, "2026/08/sales.csv", delivered.Key)
}

// TestExport_RefusesAKeyThatCouldNotBeWritten covers the two ways a key is
// wrong: aimed at a destination that names its own objects, and shaped so it
// could climb out of the prefix it was granted under.
func TestExport_RefusesAKeyThatCouldNotBeWritten(t *testing.T) {
	tests := []struct {
		name    string
		source  string
		wantErr string
	}{
		{
			"a key aimed at the portal",
			`platform.export(name="daily", rows=[], key="sales.csv")`,
			"stores its own objects and takes no key",
		},
		{
			"a key climbing out of the prefix",
			`platform.export(name="daily", rows=[], destination="acme-drop", key="../elsewhere.csv")`,
			"'.' or '..'",
		},
		{
			"an absolute key",
			`platform.export(name="daily", rows=[], destination="acme-drop", key="/etc/passwd")`,
			"cannot start with '/'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exporter := &recordingExporter{}
			_, err := grantedRun(t, tt.source, deliveryGrant(), exporter)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			assert.Empty(t, exporter.requests, "the write was never issued")
		})
	}
}

// TestExport_DraftPreviewsWhereverItIsAddressed pins that a draft moves no data
// even when it names an external destination: an author iterating on a report
// must not be delivering files to another system while they do it.
func TestExport_DraftPreviewsWhereverItIsAddressed(t *testing.T) {
	result, err := Run(context.Background(), Options{
		Source: `platform.export(name="daily", rows=[{"a": 1}], destination="acme-drop")`,
		Name:   "test", RunID: "dpx_1", FireTime: fireTime, Caller: &recordingCaller{},
	})
	require.NoError(t, err)
	require.Len(t, result.Exports, 1)
	assert.True(t, result.Exports[0].Preview)
	assert.Equal(t, "acme-drop", result.Exports[0].Destination)
	assert.Empty(t, result.Exports[0].Key)
}

// TestExport_ADraftAnswersTheSameArgumentChecks pins the authoring loop against
// the run it is preparing for: a draft resolves no address, but it must refuse
// exactly what an approved run would refuse, or an author finishes a script the
// approval then breaks.
func TestExport_ADraftAnswersTheSameArgumentChecks(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Source: `platform.export(name="daily", rows=[], key="sales.csv")`,
		Name:   "test", RunID: "dpx_1", FireTime: fireTime, Caller: &recordingCaller{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stores its own objects and takes no key")

	_, err = Run(context.Background(), Options{
		Source: `platform.export(name="daily", rows=[], destination="acme-drop", key="../out.csv")`,
		Name:   "test", RunID: "dpx_1", FireTime: fireTime, Caller: &recordingCaller{},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "'.' or '..'")
}

// TestExport_RefusesAPositionalDestination pins the engine half of the same
// rule the validator reports: where a script writes has to be readable from its
// source, so the arguments that decide it must be named.
func TestExport_RefusesAPositionalDestination(t *testing.T) {
	exporter := &recordingExporter{}
	_, err := grantedRun(t, `platform.export("daily", [], "csv", "acme-drop")`,
		deliveryGrant(), exporter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pass destination and key by name")
	assert.Empty(t, exporter.requests)

	// The three that were always positional still are: this refuses the
	// arguments that decide where output goes, not the ones that produce it.
	_, err = grantedRun(t, `platform.export("daily", [{"a": 1}], "csv")`, deliveryGrant(), exporter)
	require.NoError(t, err)
	require.Len(t, exporter.requests, 1)
	assert.Equal(t, script.PortalDestination(), exporter.requests[0].Destination)
}
