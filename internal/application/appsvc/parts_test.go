package appsvc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

type fakeSchedules map[string]*script.Schedule

func (f fakeSchedules) GetSchedule(_ context.Context, id string) (*script.Schedule, error) {
	if s, ok := f[id]; ok {
		return s, nil
	}
	return nil, script.ErrScheduleNotFound
}

type fakeActivity map[string]int64

func (f fakeActivity) AcceptedSince(_ context.Context, name string, _ time.Time) (int64, error) {
	return f[name], nil
}

type fakeLister struct {
	scripts  []script.Script
	assets   []portaldomain.Asset
	sources  []string
	filters  []script.ListFilter
	afilters []portaldomain.AssetFilter
	fail     error
}

func (f *fakeLister) ListScripts(_ context.Context, filter script.ListFilter) ([]script.Script, error) {
	f.filters = append(f.filters, filter)
	if f.fail != nil {
		return nil, f.fail
	}
	out := []script.Script{}
	for _, s := range f.scripts {
		if (filter.OwnerEmail == "" || s.OwnerEmail == filter.OwnerEmail) && strings.Contains(strings.ToLower(s.Name), strings.ToLower(filter.Search)) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (f *fakeLister) ListAssets(_ context.Context, filter portaldomain.AssetFilter) ([]portaldomain.Asset, int, error) {
	f.afilters = append(f.afilters, filter)
	out := []portaldomain.Asset{}
	for _, a := range f.assets {
		if (len(filter.Owners) == 0 || a.OwnerEmail == filter.Owners[0]) && strings.Contains(strings.ToLower(a.Name), strings.ToLower(filter.Search)) {
			out = append(out, a)
		}
	}
	return out, len(out), nil
}

func (f *fakeLister) ListSources(_ context.Context, query string, _ int) ([]string, error) {
	out := []string{}
	for _, s := range f.sources {
		if strings.Contains(s, query) {
			out = append(out, s)
		}
	}
	return out, nil
}

// TestDescribeNamesEachPart is #2527's server half: a part reads by name with
// the facts its row is built from, an asset the caller cannot open is not
// named, a part that is gone says so, and a webhook source's activity is told
// to administrators only.
func TestDescribeNamesEachPart(t *testing.T) {
	svc, _, scripts, assets, _ := fixture()
	ctx := context.Background()
	scripts.scripts[scriptA].DisplayName = "Shop API"
	scripts.scripts[scriptA].Description = "Answers the storefront"
	scripts.scripts[scriptA].Version = 4
	assets.assets["as1"].ContentType = "text/html"
	assets.assets["as1"].CurrentVersion = 7
	svc.Schedules = fakeSchedules{scriptA: {CronSpec: "0 2 * * *", Timezone: "UTC", Enabled: true}}
	svc.Activity = fakeActivity{"orders-hook": 1284}

	parts := []application.Part{
		{Kind: application.KindScript, ID: scriptA, AddedBy: jane.Email},
		{Kind: application.KindScript, ID: "33333333-3333-3333-3333-333333333333"},
		{Kind: application.KindAsset, ID: "as1"},
		{Kind: application.KindAsset, ID: "gone"},
		{Kind: application.KindSource, ID: "orders-hook"},
		{Kind: application.KindSource, ID: "deleted-hook"},
	}
	sees := func(context.Context, *portaldomain.Asset) bool { return true }
	got := svc.Describe(ctx, joe, parts, sees)
	require.Len(t, got, 6)

	assert.Equal(t, PartOK, got[0].State)
	assert.Equal(t, "Shop API", got[0].Name, "the display name is the name")
	assert.Equal(t, "Answers the storefront", got[0].Description)
	require.NotNil(t, got[0].LatestVersion)
	assert.Equal(t, 4, *got[0].LatestVersion)
	require.NotNil(t, got[0].Schedule)
	assert.Equal(t, "0 2 * * *", got[0].Schedule.CronSpec)
	assert.Equal(t, jane.Email, got[0].AddedBy)

	assert.Equal(t, PartDeleted, got[1].State)
	assert.Empty(t, got[1].Name)
	assert.Nil(t, got[1].LatestVersion)

	assert.Equal(t, PartOK, got[2].State)
	assert.Equal(t, "storefront", got[2].Name)
	assert.Equal(t, "text/html", got[2].ContentType)
	assert.Equal(t, 7, *got[2].LatestVersion)
	assert.Equal(t, PartDeleted, got[3].State)

	assert.Equal(t, "orders-hook", got[4].Name)
	assert.Nil(t, got[4].RequestsLastDay, "a webhook source's activity is for administrators")
	assert.Nil(t, got[4].LatestVersion)
	assert.Equal(t, PartDeleted, got[5].State)

	hidden := svc.Describe(ctx, joe, parts[2:3], func(context.Context, *portaldomain.Asset) bool { return false })
	assert.Equal(t, PartNotVisible, hidden[0].State)
	assert.Empty(t, hidden[0].Name, "an asset the caller cannot open is not named")
	assert.Empty(t, hidden[0].ContentType)
	assert.Nil(t, hidden[0].LatestVersion)
	assert.Equal(t, PartNotVisible, svc.Describe(ctx, joe, parts[2:3], nil)[0].State, "no viewer opens nothing")

	byAdmin := svc.Describe(ctx, admin, parts, nil)
	assert.Equal(t, PartOK, byAdmin[2].State, "an administrator opens every asset")
	require.NotNil(t, byAdmin[4].RequestsLastDay)
	assert.Equal(t, int64(1284), *byAdmin[4].RequestsLastDay)
}

// TestFindCandidates lists what the caller may add by name: their own scripts
// and assets, every one and the webhook sources for an administrator, leaving
// out this application's parts and marking another application's.
func TestFindCandidates(t *testing.T) {
	svc, store, _, _, _ := fixture()
	ctx := context.Background()
	lister := &fakeLister{
		scripts: []script.Script{
			{ID: scriptA, Name: "revenue-close", OwnerEmail: jane.Email},
			{ID: scriptB, Name: "revenue-forecast", OwnerEmail: joe.Email},
			{ID: "s3", Name: "revenue-export", OwnerEmail: jane.Email},
			{ID: "s4", Name: "unrelated", OwnerEmail: jane.Email},
		},
		assets: []portaldomain.Asset{
			{ID: "as1", Name: "Revenue by region", OwnerEmail: jane.Email},
			{ID: "as2", Name: "Revenue deleted", OwnerEmail: jane.Email, DeletedAt: new(time.Now())},
		},
		sources: []string{"revenue-events", "orders-hook"},
	}
	svc.Lister = lister
	shop, err := svc.Create(ctx, jane, application.Application{Name: "shop"})
	require.NoError(t, err)
	billing, err := svc.Create(ctx, jane, application.Application{Name: "billing"})
	require.NoError(t, err)
	require.NoError(t, store.AddPart(ctx, shop.ID, application.Part{Kind: application.KindScript, ID: scriptA}))
	require.NoError(t, store.AddPart(ctx, billing.ID, application.Part{Kind: application.KindScript, ID: "s3"}))

	got, err := svc.FindCandidates(ctx, jane, "shop", " revenue ", "")
	require.NoError(t, err)
	assert.Equal(t, "revenue", lister.filters[0].Search, "the query is trimmed")
	assert.Equal(t, jane.Email, lister.filters[0].OwnerEmail, "a person lists their own scripts")
	assert.Equal(t, []string{jane.Email}, lister.afilters[0].Owners)
	ids := map[string]*AppRef{}
	for _, c := range got.Candidates {
		ids[c.ID] = c.Application
	}
	assert.NotContains(t, ids, scriptA, "a part of this application is not listed")
	assert.NotContains(t, ids, scriptB, "another person's script is not theirs to add")
	assert.NotContains(t, ids, "as2", "a deleted asset is not listed")
	require.Contains(t, ids, "s3")
	require.NotNil(t, ids["s3"])
	assert.Equal(t, "billing", ids["s3"].Name, "a part of another application names it")
	assert.Contains(t, ids, "as1")
	assert.Equal(t, map[string]int{application.KindScript: 1, application.KindAsset: 1}, got.Counts,
		"no webhook sources for someone who is not an administrator")

	onlyAssets, err := svc.FindCandidates(ctx, jane, "shop", "revenue", application.KindAsset)
	require.NoError(t, err)
	require.Len(t, onlyAssets.Candidates, 1)
	assert.Equal(t, "as1", onlyAssets.Candidates[0].ID)
	assert.Equal(t, got.Counts, onlyAssets.Counts, "the kind narrows the list, not the counts")

	byAdmin, err := svc.FindCandidates(ctx, admin, "shop", "revenue", "")
	require.NoError(t, err)
	assert.Empty(t, lister.filters[len(lister.filters)-1].OwnerEmail, "an administrator lists every script")
	assert.Equal(t, 2, byAdmin.Counts[application.KindScript])
	assert.Equal(t, 1, byAdmin.Counts[application.KindSource])

	_, err = svc.FindCandidates(ctx, joe, "shop", "", "")
	require.ErrorIs(t, err, application.ErrNotAllowed, "only the owner and administrators add parts")

	lister.fail = errors.New("db down")
	_, err = svc.FindCandidates(ctx, jane, "shop", "", "")
	require.Error(t, err)

	svc.Lister = nil
	none, err := svc.FindCandidates(ctx, jane, "shop", "", "")
	require.NoError(t, err)
	assert.Empty(t, none.Candidates)
	assert.NotNil(t, none.Candidates, "an empty list is [], never null")
}
