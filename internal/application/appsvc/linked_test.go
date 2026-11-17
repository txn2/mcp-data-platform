package appsvc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/site"
)

// contractSites is fakeSites with release contracts: what each release
// declares, and what each environment serves.
type contractSites struct {
	*fakeSites
	served    map[string]int
	contracts map[int]*site.Contract
}

func (c *contractSites) ServedContract(_ context.Context, _, environment string) (int, *site.Contract, error) {
	n := c.served[environment]
	return n, c.contracts[n], nil
}

func (c *contractSites) ReleaseContract(_ context.Context, _ string, number int) (*site.Contract, error) {
	return c.contracts[number], nil
}

func linkedFixture(t *testing.T) (*Service, *memStore, *contractSites) {
	t.Helper()
	svc, store, sites := siteFixture(t)
	cs := &contractSites{fakeSites: sites, served: map[string]int{"prod": 2}, contracts: map[int]*site.Contract{}}
	svc.Sites, svc.Contracts = cs, cs
	return svc, store, cs
}

// TestAddPart_ASitePartIsLinkedUnlessAsked: a new site part is linked; one
// asked for coupled is coupled; a mode on another kind, or an unknown one, is
// refused.
func TestAddPart_ASitePartIsLinkedUnlessAsked(t *testing.T) {
	svc, _, _ := linkedFixture(t)
	ctx := context.Background()
	a, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	assert.Equal(t, application.ModeLinked, a.Parts[0].ReleaseMode)
	_, err = svc.AddPartMode(ctx, jane, "shop", application.Part{Kind: application.KindScript, ID: scriptA, ReleaseMode: application.ModeCoupled})
	require.Error(t, err, "a script part has no mode")
	_, err = svc.AddPartMode(ctx, jane, "shop", application.Part{Kind: application.KindSite, ID: "marketing/stage", ReleaseMode: "sometimes"})
	require.Error(t, err)
	b, err := svc.SetPartMode(ctx, jane, "shop", sitePart, application.ModeCoupled)
	require.NoError(t, err)
	assert.Equal(t, application.ModeCoupled, b.Parts[0].ReleaseMode)
	_, err = svc.SetPartMode(ctx, jane, "shop", "nowhere/prod", application.ModeLinked)
	require.Error(t, err)
	_, err = svc.SetPartMode(ctx, jane, "shop", sitePart, "x")
	require.Error(t, err)
}

// TestRelease_ALinkedPartStaysPut: a release moves no linked part it does not
// name, and its rollback leaves it alone; a release naming it moves it one
// step and its rollback returns it.
func TestRelease_ALinkedPartStaysPut(t *testing.T) {
	svc, _, sites := linkedFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	_, err = svc.AddPart(ctx, jane, "shop", application.KindScript, scriptA)
	require.NoError(t, err)

	rel, err := svc.Release(ctx, admin, "shop", "backend only")
	require.NoError(t, err)
	assert.NotContains(t, rel.Pins, application.Pin{Kind: application.KindSite, PartID: sitePart, Version: 3})
	assert.Empty(t, sites.pins, "a linked part is not moved by a release that does not name it")

	moved, err := svc.ReleaseWith(ctx, admin, "shop", ReleaseOptions{Note: "with the front end", MoveSites: []string{sitePart}})
	require.NoError(t, err)
	assert.Contains(t, moved.Pins, application.Pin{Kind: application.KindSite, PartID: sitePart, Version: 3})
	require.Len(t, sites.pins, 1)
	assert.Equal(t, 3, sites.pins[0].number)
	assert.Contains(t, moved.Changes.Parts, application.PinChange{Kind: application.KindSite, PartID: sitePart, From: 2, To: 3},
		"the move records what the environment served before it, which a rollback returns it to")

	_, err = svc.ReleaseWith(ctx, admin, "shop", ReleaseOptions{MoveSites: []string{"other/prod"}})
	require.Error(t, err, "move_sites names only the application's site parts")
}

// TestRollback_ReturnsOnlyWhatTheReleaseMoved: rolling back a release that
// moved a linked part returns it to the release it served before.
func TestRollback_ReturnsOnlyWhatTheReleaseMoved(t *testing.T) {
	a := &application.Application{Parts: []application.Part{
		{Kind: application.KindSite, ID: "web/prod", ReleaseMode: application.ModeLinked},
		{Kind: application.KindSite, ID: "web/dev", ReleaseMode: application.ModeLinked},
		{Kind: application.KindSite, ID: "admin/stage", ReleaseMode: application.ModeCoupled},
	}}
	current := &application.Release{Changes: application.Changes{Parts: []application.PinChange{
		{Kind: application.KindSite, PartID: "web/prod", From: 7, To: 9},
		{Kind: application.KindSite, PartID: "web/dev", From: 0, To: 4},
	}}}
	target := &application.Release{Pins: []application.Pin{
		{Kind: application.KindSite, PartID: "web/prod", Version: 5},
		{Kind: application.KindSite, PartID: "admin/stage", Version: 2},
		{Kind: application.KindScript, PartID: "s", Version: 1},
	}}
	got := rollbackSitePins(a, current, target)
	assert.ElementsMatch(t, []application.Pin{
		{Kind: application.KindSite, PartID: "admin/stage", Version: 2},
		{Kind: application.KindScript, PartID: "s", Version: 1},
		{Kind: application.KindSite, PartID: "web/prod", Version: 7},
	}, got, "the coupled part takes the target's pin, the linked part moved by the release goes back, and nothing else")
}

// TestRelease_AContractThatBreaksAFrontEndIsRefused: a release whose contract
// a linked part's release does not accept is refused naming it, and an
// administrator's force releases it and records the part.
func TestRelease_AContractThatBreaksAFrontEndIsRefused(t *testing.T) {
	svc, store, sites := linkedFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	sites.contracts[2] = &site.Contract{Application: "shop", Range: "=2"}
	four := 4
	_, err = svc.ReleaseWith(ctx, admin, "shop", ReleaseOptions{Contract: &four})
	var r *RefusedError
	require.ErrorAs(t, err, &r)
	assert.Contains(t, r.Msg, "marketing/prod would serve release 2, which works with contract =2, not 4")
	a, _ := store.GetByName(ctx, "shop")
	assert.Zero(t, a.ContractVersion, "a refused release declares nothing")

	rel, err := svc.ReleaseWith(ctx, admin, "shop", ReleaseOptions{Contract: &four, Force: true})
	require.NoError(t, err)
	assert.Len(t, rel.Changes.Forced, 1)
	a, _ = store.GetByName(ctx, "shop")
	assert.Equal(t, 4, a.ContractVersion)

	two := 2
	_, err = svc.ReleaseWith(ctx, admin, "shop", ReleaseOptions{Contract: &two})
	require.NoError(t, err, "a contract the front end accepts releases")
	sites.contracts[2] = &site.Contract{Application: "other", Range: "=9"}
	_, err = svc.ReleaseWith(ctx, admin, "shop", ReleaseOptions{Contract: &four})
	require.NoError(t, err, "a release declaring another application's contract says nothing about this one")
	neg := -1
	_, err = svc.ReleaseWith(ctx, admin, "shop", ReleaseOptions{Contract: &neg})
	require.Error(t, err)
}

// TestSetContract: the owner sets the contract version; a negative one is
// refused; someone else is refused.
func TestSetContract(t *testing.T) {
	svc, _, _ := linkedFixture(t)
	ctx := context.Background()
	a, err := svc.SetContract(ctx, jane, "shop", 3)
	require.NoError(t, err)
	assert.Equal(t, 3, a.ContractVersion)
	_, err = svc.SetContract(ctx, jane, "shop", -1)
	require.Error(t, err)
	_, err = svc.SetContract(ctx, application.Actor{Email: "bob@example.com"}, "shop", 1)
	require.Error(t, err)
}

// TestStatusOf_NamesEachSitePart: the status reads each site part's mode,
// what it serves, where a release would move it, and whether it works with
// the contract.
func TestStatusOf_NamesEachSitePart(t *testing.T) {
	svc, _, sites := linkedFixture(t)
	ctx := context.Background()
	_, err := svc.AddPart(ctx, jane, "shop", application.KindSite, sitePart)
	require.NoError(t, err)
	_, err = svc.SetContract(ctx, jane, "shop", 3)
	require.NoError(t, err)
	sites.contracts[2] = &site.Contract{Application: "shop", Range: "=2"}
	st, err := svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	require.Len(t, st.SiteParts, 1)
	assert.Equal(t, SitePartView{PartID: sitePart, Mode: application.ModeLinked, Serves: 2, Step: 3, Contract: "=2", Compatible: false}, st.SiteParts[0])
	assert.Empty(t, st.Pending, "a linked part a release would not move is not pending")

	svc.Contracts = nil
	st, err = svc.StatusOf(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, []SitePartView{}, st.SiteParts, "[] never null")
}
