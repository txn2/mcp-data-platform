//go:build integration

package appstore

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appenv"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/testdb"
)

func envNames(a *application.Application) []string {
	out := []string{}
	for _, e := range a.Environments {
		out = append(out, e.Name)
	}
	return out
}

// Environments (#2696): a release lands in the environment named, only the
// last holds the parts' released versions, a promote is the environment's
// release moved, and a run resolves its environment's pin and variables in
// one statement.
func TestRealDB_EnvironmentsHoldTheirOwnRelease(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s, scripts := New(db), scriptstore.New(db)

	plain := &application.Application{Name: "plain", OwnerEmail: owner, CreatedBy: owner}
	require.NoError(t, s.Create(ctx, plain))
	got, err := s.Get(ctx, plain.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{DefaultEnvironment}, envNames(got), "one environment when none is named")

	app := &application.Application{
		Name: "shop", OwnerEmail: owner, CreatedBy: owner,
		Environments: []application.Environment{{Name: "dev"}, {Name: "prod"}},
	}
	require.NoError(t, s.Create(ctx, app))
	api := newScript(ctx, t, scripts, "shop-api")
	require.NoError(t, s.AddPart(ctx, app.ID, application.Part{Kind: application.KindScript, ID: api.ID, AddedBy: owner}))

	rel := application.Release{
		Action: application.ActionRelease, CreatedBy: owner,
		Pins: []application.Pin{{Kind: application.KindScript, PartID: api.ID, Version: 1}},
	}
	first, err := s.Record(ctx, app.ID, application.Recorded{Release: rel, Environment: "dev"})
	require.NoError(t, err)
	assert.Equal(t, 1, first.Number)
	got, err = s.Get(ctx, app.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.Environments[0].Release)
	assert.Equal(t, 0, got.Environments[1].Release)
	assert.Equal(t, 0, got.CurrentRelease, "a release in dev is not what /endpoints serves")
	held, err := scripts.GetByID(ctx, api.ID)
	require.NoError(t, err)
	assert.Equal(t, 0, held.ReleasedVersion, "dev's release holds no part")

	saveVersion(ctx, t, scripts, api, "def main():\n    return 2\n")
	rel.Pins = []application.Pin{{Kind: application.KindScript, PartID: api.ID, Version: 2}}
	second, err := s.Record(ctx, app.ID, application.Recorded{Release: rel, Environment: "dev", After: 1})
	require.NoError(t, err)
	assert.Equal(t, 2, second.Number)
	_, err = s.Record(ctx, app.ID, application.Recorded{Release: rel, Environment: "dev", After: 1})
	require.ErrorIs(t, err, application.ErrStale)

	// Promote release 1 to prod: prod now holds the script at version 1.
	require.NoError(t, s.SetEnvironmentRelease(ctx, app.ID, application.Move{Environment: "prod", Number: 1, Expect: 0, By: owner}))
	require.ErrorIs(t, s.SetEnvironmentRelease(ctx, app.ID, application.Move{Environment: "prod", Number: 2, Expect: 0, By: owner}), application.ErrStale)
	got, err = s.Get(ctx, app.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, got.CurrentRelease)
	held, err = scripts.GetByID(ctx, api.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, held.ReleasedVersion)

	// A third environment between the two, and variables over the defaults.
	require.NoError(t, s.AddEnvironment(ctx, app.ID, application.Environment{Name: "stage", RequiresApproval: true}, "dev"))
	require.ErrorIs(t, s.AddEnvironment(ctx, app.ID, application.Environment{Name: "stage"}, "dev"), application.ErrNameTaken)
	got, err = s.Get(ctx, app.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"dev", "stage", "prod"}, envNames(got))
	assert.True(t, got.Environments[1].RequiresApproval)
	names, err := s.EnvironmentNames(ctx, api.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{"dev", "stage", "prod"}, names)

	require.NoError(t, s.SetVariable(ctx, app.ID, application.Variable{Name: "API_BASE", Value: "https://sandbox.example.com", UpdatedBy: owner}))
	require.NoError(t, s.SetVariable(ctx, app.ID, application.Variable{Environment: "prod", Name: "API_BASE", Value: "https://pay.example.com", UpdatedBy: owner}))
	require.NoError(t, s.SetVariable(ctx, app.ID, application.Variable{Environment: "stage", Name: "KEY", Value: "{{secret:k}}", UpdatedBy: owner}))
	require.NoError(t, s.SetVariable(ctx, app.ID, application.Variable{Environment: "prod", Name: "API_BASE", Value: "https://pay2.example.com", UpdatedBy: owner}))

	dev, err := s.ForScript(ctx, api.ID, "dev")
	require.NoError(t, err)
	assert.Equal(t, appenv.Script{OK: true, Environment: "dev", Version: 2, Variables: map[string]string{"API_BASE": "https://sandbox.example.com"}}, *dev)
	last, err := s.ForScript(ctx, api.ID, "")
	require.NoError(t, err)
	assert.Equal(t, appenv.Script{OK: true, Environment: "prod", Version: 1, Variables: map[string]string{"API_BASE": "https://pay2.example.com"}}, *last)
	stage, err := s.ForScript(ctx, api.ID, "stage")
	require.NoError(t, err)
	assert.Equal(t, 0, stage.Version, "stage runs no release yet")
	assert.Equal(t, "{{secret:k}}", stage.Variables["KEY"])
	_, err = s.ForScript(ctx, api.ID, "qa")
	require.ErrorIs(t, err, appenv.ErrNoEnvironment)
	none, err := s.ForScript(ctx, "00000000-0000-0000-0000-000000000000", "")
	require.NoError(t, err)
	assert.False(t, none.OK)

	require.NoError(t, s.DeleteVariable(ctx, app.ID, "prod", "API_BASE"))
	last, err = s.ForScript(ctx, api.ID, "")
	require.NoError(t, err)
	assert.Equal(t, "https://sandbox.example.com", last.Variables["API_BASE"], "the default once prod's own is removed")

	require.NoError(t, s.SetEnvironmentApproval(ctx, app.ID, "stage", false, owner))
	require.ErrorIs(t, s.SetEnvironmentApproval(ctx, app.ID, "qa", true, owner), appenv.ErrNoEnvironment)
	require.NoError(t, s.RemoveEnvironment(ctx, app.ID, "stage"))
	require.ErrorIs(t, s.RemoveEnvironment(ctx, app.ID, "stage"), appenv.ErrNoEnvironment)
	vars, err := s.Variables(ctx, app.ID)
	require.NoError(t, err)
	assert.Len(t, vars, 1, "stage's variables go with it")
	list, err := s.List(ctx)
	require.NoError(t, err)
	for _, a := range list {
		if a.ID == app.ID {
			assert.Equal(t, []string{"dev", "prod"}, envNames(&a))
		}
	}
}
