package draftview

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/exporttable"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptdraft"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/runstate"
)

// TestIncompleteNote covers every shape of the gap statement. The note exists
// because a list that silently omitted a computed name would be a false
// statement, so which gap is named has to be right in all four cases.
func TestIncompleteNote(t *testing.T) {
	tests := []struct {
		name   string
		report scriptrun.Report
		want   string
	}{
		{name: "nothing computed", report: scriptrun.Report{}, want: ""},
		{
			name:   "a computed connection",
			report: scriptrun.Report{DynamicConnections: true},
			want:   "the connection list is incomplete",
		},
		{
			name:   "a computed destination",
			report: scriptrun.Report{DynamicDestinations: true},
			want:   "the destination list is incomplete",
		},
		{
			name:   "both, each gap named rather than collapsed into one",
			report: scriptrun.Report{DynamicConnections: true, DynamicDestinations: true},
			want:   "the connection list is incomplete; and at least one platform.export",
		},
		{
			name:   "a computed refresh target",
			report: scriptrun.Report{DynamicRefreshTargets: true},
			want:   "the refresh-target list is incomplete",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IncompleteNote(tt.report)
			if tt.want == "" {
				assert.Empty(t, got)
				return
			}
			assert.Contains(t, got, tt.want)
		})
	}
}

// TestDraftOutputs_SaysWhatWasWrittenAndWhere is the editor's half of #1822: an
// output a dry run allowed to write wrote is marked written and names the
// resource or asset it wrote and the table registered over it, while a preview
// names neither.
func TestOutputs_SaysWhatWasWrittenAndWhere(t *testing.T) {
	out := Outputs(&scriptdraft.Outcome{Result: &scriptrun.Result{Exports: []scriptrun.ExportRecord{
		{
			Name: "staging", Destination: "resources", Format: "jsonl", RowCount: 3, ResourceRef: "mcp:resource:r1",
			Table: &exporttable.Table{QueryTable: "scratch.uploads.analyst_staging"},
		},
		{Name: "daily", Destination: "portal", Format: "csv", AssetID: "a1"},
		{
			Name: "preview", Destination: "portal", Format: "csv", Preview: true,
			Table: &exporttable.Table{Preview: true},
		},
	}}})
	require.Len(t, out, 3)
	assert.True(t, out[0].Written)
	assert.Equal(t, "mcp:resource:r1", out[0].Reference)
	assert.Equal(t, "scratch.uploads.analyst_staging", out[0].Table)
	assert.True(t, out[1].Written)
	assert.Equal(t, "mcp:asset:a1", out[1].Reference)
	assert.False(t, out[2].Written)
	assert.Empty(t, out[2].Reference)
	assert.Empty(t, out[2].Table, "a preview registered nothing, so it names no table")
}

// TestDryRunFailureMessage_ByCause: only a script failure that repeats sends
// the author to the script (#1935); an upstream or a declared temporary
// failure says to try again.
func TestFailureMessage_ByCause(t *testing.T) {
	assert.Contains(t, FailureMessage(&scriptrun.WriteRecord{Tool: "manage_asset"}, runstate.CauseScript), "allow_writes")
	assert.Contains(t, FailureMessage(nil, runstate.CauseUpstream), "outside the script")
	assert.Contains(t, FailureMessage(nil, runstate.CauseTransient), "outside the script")
	assert.Contains(t, FailureMessage(nil, runstate.CauseMemory), "append=True")
	assert.Contains(t, FailureMessage(nil, runstate.CauseScript), "fails the same way again, fix the script")
}

func TestMetrics_CarriesThePeak(t *testing.T) {
	m := Metrics(&scriptrun.Result{Steps: 5, PeakMemory: 4096})
	assert.Equal(t, int64(4096), m.PeakMemoryBytes)
}
