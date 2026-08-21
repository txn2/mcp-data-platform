package script_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// runnable returns a script in the one state the run gate admits.
func runnable() *script.Script {
	return &script.Script{
		ID: "script_1", Name: "daily-sales", OwnerEmail: "jane@example.com", Enabled: true, Status: script.StatusActive,
		Version: 3,
	}
}

// TestRefuseRun is the run gate. Each case is a state in which the platform
// must not run a script on its own, and the first is the one state in which it
// may.
func TestRefuseRun(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*script.Script)
		wantErr string
	}{
		{"admitted", func(*script.Script) {}, ""},
		{"disabled", func(sc *script.Script) {
			sc.Enabled = false
		}, "disabled"},
		{"superseded", func(sc *script.Script) {
			sc.Status, sc.SupersededBy = script.StatusSuperseded, "daily-sales-v2"
		}, "superseded by"},
		{"deprecated", func(sc *script.Script) {
			sc.Status = script.StatusDeprecated
		}, "deprecated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := runnable()
			tt.mutate(sc)
			err := script.RefuseRun(sc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

// TestRefuseRunRejectsAMissingScript proves the nil case is an answer, not a
// panic: every discovery surface asks the gate about whatever it just read.
func TestRefuseRunRejectsAMissingScript(t *testing.T) {
	require.Error(t, script.RefuseRun(nil))
}

func TestRun_Terminal(t *testing.T) {
	for status, terminal := range map[string]bool{
		script.RunStatusPending:   false,
		script.RunStatusRunning:   false,
		script.RunStatusSucceeded: true,
		script.RunStatusFailed:    true,
	} {
		assert.Equal(t, terminal, (&script.Run{Status: status}).Terminal(), status)
	}
}

// TestRun_OutputIsTheReclaimGuard pins what stops a reclaimed run from writing
// an output twice: the run's own record of what it already wrote.
func TestRun_OutputIsTheReclaimGuard(t *testing.T) {
	run := &script.Run{Outputs: []script.RunOutput{
		// The first row carries no destination, which is how every output
		// recorded before delivery existed reads: the portal was the only place
		// an output could go.
		{Name: "daily", AssetID: "asset_1", AssetVersion: 4},
		{Name: "daily", Destination: "acme-drop", Bucket: "exports", Key: "weekly/daily.csv"},
	}}
	found := run.Output("daily", script.DestinationPortal)
	require.NotNil(t, found)
	assert.Equal(t, "asset_1", found.AssetID)
	assert.Equal(t, 4, found.AssetVersion)

	// The same name at another destination is a different write, not a repeat
	// of this one: matching on the name alone would report the delivery as
	// already done and silently skip it.
	delivered := run.Output("daily", "acme-drop")
	require.NotNil(t, delivered)
	assert.Equal(t, "weekly/daily.csv", delivered.Key)

	assert.Nil(t, run.Output("daily", "somewhere-else"))
	assert.Nil(t, run.Output("weekly", script.DestinationPortal))
	assert.Nil(t, (&script.Run{}).Output("daily", script.DestinationPortal))
}

// TestRun_LeaseIsTheFencingToken pins the triple a write is fenced on: a worker
// whose claim was taken over carries a stale worker name and attempt number.
func TestRun_LeaseIsTheFencingToken(t *testing.T) {
	run := &script.Run{ID: "dpx_1", LockedBy: "worker-a", Attempt: 2}
	assert.Equal(t, script.RunLease{RunID: "dpx_1", Worker: "worker-a", Attempt: 2}, run.Lease())

	reclaimed := &script.Run{ID: "dpx_1", LockedBy: "worker-b", Attempt: 3}
	assert.NotEqual(t, run.Lease(), reclaimed.Lease())
}

func TestScript_Principal(t *testing.T) {
	sc := &script.Script{Name: "daily-sales"}
	assert.Equal(t, "script:daily-sales", sc.Principal())
	assert.True(t, len(sc.Principal()) > len(script.PrincipalPrefix))
}

// TestRefuseDraftRun is the gate a DRAFT crosses, which is not the run gate: a
// draft executes as its author, inline, while they iterate — but a script
// taken out of service must still not run.
func TestRefuseDraftRun(t *testing.T) {
	tests := []struct {
		name    string
		sc      *script.Script
		wantErr string
	}{
		{
			name: "a deprecated script may still be dry-run by the person fixing it",
			sc:   &script.Script{Enabled: true, Status: script.StatusDeprecated},
		},
		{
			name:    "a disabled script runs nothing, including a draft",
			sc:      &script.Script{Enabled: false, Status: script.StatusActive},
			wantErr: "disabled",
		},
		{
			name: "a superseded script names its replacement",
			sc: &script.Script{
				Enabled: true, Status: script.StatusSuperseded, SupersededBy: "daily-v2",
			},
			wantErr: "daily-v2",
		},
		{
			name:    "a script that does not exist",
			sc:      nil,
			wantErr: "does not exist",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := script.RefuseDraftRun(tt.sc)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
