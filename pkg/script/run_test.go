package script_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// executable returns a script, its approved version, and a run queued against
// that version — the state the execution gate must admit.
func executable() (*script.Script, *script.Version, *script.Run) {
	approvedAt := time.Now().UTC()
	v := &script.Version{
		ID: "sver_1", ScriptID: "script_1", Version: 3,
		Source: "print(1)", ApprovedBy: "admin@example.com", ApprovedAt: &approvedAt,
		Grants: fullGrant(),
	}
	sc := &script.Script{
		ID: "script_1", Name: "daily-sales", Scope: script.ScopePersonal,
		OwnerEmail: "jane@example.com", Enabled: true, Status: script.StatusActive,
		ApprovedVersionID: v.ID, Version: v.Version,
	}
	run := &script.Run{
		ID: "dpx_1", ScriptID: sc.ID, VersionID: v.ID, Version: v.Version,
		Trigger: script.TriggerTool, Status: script.RunStatusRunning,
	}
	return sc, v, run
}

// TestRefuseRun is the execution gate. Each case is a state in which the
// platform must not run a script on its own, and the last is the one state in
// which it may.
func TestRefuseRun(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*script.Script, *script.Version, *script.Run)
		wantErr string
	}{
		{"admitted", func(*script.Script, *script.Version, *script.Run) {}, ""},
		{"disabled", func(sc *script.Script, _ *script.Version, _ *script.Run) {
			sc.Enabled = false
		}, "disabled"},
		{"superseded", func(sc *script.Script, _ *script.Version, _ *script.Run) {
			sc.Status, sc.SupersededBy = script.StatusSuperseded, "daily-sales-v2"
		}, "superseded by"},
		{"deprecated", func(sc *script.Script, _ *script.Version, _ *script.Run) {
			sc.Status = script.StatusDeprecated
		}, "deprecated"},
		{"no approved version", func(sc *script.Script, _ *script.Version, _ *script.Run) {
			sc.ApprovedVersionID = ""
		}, "no approved version"},
		{"approval moved to another version", func(sc *script.Script, _ *script.Version, _ *script.Run) {
			sc.ApprovedVersionID = "sver_2"
		}, "is not any more"},
		{"version carries no approval stamp", func(_ *script.Script, v *script.Version, _ *script.Run) {
			v.ApprovedAt = nil
		}, "no approval grant"},
		{"version carries an empty grant", func(_ *script.Script, v *script.Version, _ *script.Run) {
			v.Grants = script.Grants{}
		}, "no approval grant"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc, v, run := executable()
			tt.mutate(sc, v, run)
			err := script.RefuseRun(sc, v, run)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
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

// TestRefuseDraftRun is the gate a DRAFT crosses, which is not the approved-run
// gate: a draft executes as its author with no grant, so approval has nothing
// to say about it — but a script taken out of service must still not run.
func TestRefuseDraftRun(t *testing.T) {
	tests := []struct {
		name    string
		sc      *script.Script
		wantErr string
	}{
		{
			name: "an unapproved script may still be dry-run: that is the whole point",
			sc:   &script.Script{Enabled: true, Status: script.StatusDraft},
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
