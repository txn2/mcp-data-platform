package scriptdraft

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestPersisted(t *testing.T) {
	state := &script.StateWrite{Value: map[string]any{"c": 1}}
	written := scriptrun.ExportRecord{Name: "o"}
	previewed := scriptrun.ExportRecord{Name: "p", Preview: true}
	call := scriptrun.WriteRecord{Tool: "manage_resource", Call: "manage_resource action=create"}

	tests := []struct {
		name    string
		outcome *Outcome
		want    []string
		not     []string
	}{
		{"nil outcome", nil, []string{"Nothing was persisted"}, nil},
		{
			"barred draft", &Outcome{Result: &scriptrun.Result{Exports: []scriptrun.ExportRecord{previewed}}},
			[]string{"Nothing was persisted", "would have been refused"},
			[]string{"allow_writes"},
		},
		{
			"barred draft with state", &Outcome{Result: &scriptrun.Result{State: state}},
			[]string{"Nothing was persisted", "did not save it"},
			nil,
		},
		{
			"allowed, nothing written", &Outcome{AllowWrites: true, Result: &scriptrun.Result{}},
			[]string{"This draft was run with allow_writes. It made no write."},
			[]string{"Nothing was persisted"},
		},
		{
			"allowed, one call", &Outcome{AllowWrites: true, Result: &scriptrun.Result{Writes: []scriptrun.WriteRecord{call}}},
			[]string{"The 1 call listed under writes persisted for real."},
			nil,
		},
		{
			"allowed, two outputs", &Outcome{AllowWrites: true, Result: &scriptrun.Result{
				Exports: []scriptrun.ExportRecord{written, written},
			}},
			[]string{"The 2 outputs listed under exports persisted for real."},
			[]string{"preview"},
		},
		{
			"allowed, calls and outputs", &Outcome{AllowWrites: true, Result: &scriptrun.Result{
				Writes: []scriptrun.WriteRecord{call, call}, Exports: []scriptrun.ExportRecord{written},
			}},
			[]string{"The 2 calls listed under writes, and The 1 output listed under exports, persisted for real."},
			nil,
		},
		{
			"allowed with nowhere to write exports", &Outcome{AllowWrites: true, Result: &scriptrun.Result{
				Exports: []scriptrun.ExportRecord{previewed}, State: state,
			}},
			[]string{"It made no write.", "marked preview reported its shape instead", "did not save it"},
			nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.outcome.Persisted("draft")
			for _, w := range tt.want {
				assert.Contains(t, msg, w)
			}
			for _, n := range tt.not {
				assert.NotContains(t, msg, n)
			}
		})
	}
}
