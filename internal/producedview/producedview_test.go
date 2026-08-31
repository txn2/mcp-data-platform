package producedview

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fakeProducers answers the by-producer listing.
type fakeProducers struct {
	rows  []producedby.Row
	err   error
	limit int
}

func (*fakeProducers) Record(context.Context, producedby.Write) error { return nil }

func (*fakeProducers) ListByTarget(context.Context, string, string) ([]producedby.Row, error) {
	return nil, nil
}

func (f *fakeProducers) ListByProducer(_ context.Context, _, _ string, limit int) ([]producedby.Row, error) {
	f.limit = limit
	return f.rows, f.err
}

type fakeAssets struct {
	found map[string]*portaldomain.Asset
	err   error
}

func (f *fakeAssets) GetByIDs(context.Context, []string) (map[string]*portaldomain.Asset, error) {
	return f.found, f.err
}

type fakeResources struct {
	res *resource.Resource
	err error
}

func (f *fakeResources) Get(context.Context, string) (*resource.Resource, error) {
	return f.res, f.err
}

type fakeScripts struct {
	byID map[string]*script.Script
	err  error
}

func (f *fakeScripts) GetByID(_ context.Context, id string) (*script.Script, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byID[id], nil
}

func assetRow(id string) producedby.Row {
	at := time.Now().UTC()
	return producedby.Row{
		TargetKind: producedby.TargetAsset, TargetID: id,
		Producer:     producedby.Producer{Kind: producedby.KindScript, ID: "script-1"},
		Created:      true,
		FirstWriteAt: at, LastWriteAt: at, WriteCount: 3, LastVersion: 3,
	}
}

func resourceRow(id string) producedby.Row {
	at := time.Now().UTC()
	return producedby.Row{
		TargetKind: producedby.TargetResource, TargetID: id,
		Producer:     producedby.Producer{Kind: producedby.KindScript, ID: "script-1"},
		FirstWriteAt: at, LastWriteAt: at, WriteCount: 1,
	}
}

func TestNewWithoutProducersIsNil(t *testing.T) {
	assert.Nil(t, New(nil, nil, nil, nil))
}

func TestProducedNamesAssetsAndResources(t *testing.T) {
	producers := &fakeProducers{rows: []producedby.Row{assetRow("asset-1"), resourceRow("res-1")}}
	assets := &fakeAssets{found: map[string]*portaldomain.Asset{"asset-1": {ID: "asset-1", Name: "Daily sales"}}}
	resources := &fakeResources{res: &resource.Resource{ID: "res-1", DisplayName: "Region map"}}

	r := New(producers, assets, resources, nil)
	items, err := r.Produced(context.Background(), "script-1", 0)
	require.NoError(t, err)
	require.Len(t, items, 2)

	assert.Equal(t, "Daily sales", items[0].Name)
	assert.True(t, items[0].Created)
	assert.False(t, items[0].Deleted)
	assert.Equal(t, 3, items[0].WriteCount)

	assert.Equal(t, "Region map", items[1].Name)
	assert.False(t, items[1].Created, "a file the script only modified is not marked as created")
}

// TestProducedReportsADeletedTarget is acceptance criterion 8 read from the
// script's end: a file the script wrote and that has since gone stays listed.
func TestProducedReportsADeletedTarget(t *testing.T) {
	gone := time.Now().UTC()
	producers := &fakeProducers{rows: []producedby.Row{
		assetRow("asset-gone"), assetRow("asset-soft"), resourceRow("res-gone"),
	}}
	assets := &fakeAssets{found: map[string]*portaldomain.Asset{
		"asset-soft": {ID: "asset-soft", Name: "Removed", DeletedAt: &gone},
	}}
	resources := &fakeResources{err: sql.ErrNoRows}

	items, err := New(producers, assets, resources, nil).Produced(context.Background(), "script-1", 0)
	require.NoError(t, err)
	require.Len(t, items, 3)
	for _, it := range items {
		assert.True(t, it.Deleted, "%s should read as deleted", it.TargetID)
		assert.Empty(t, it.Name)
	}
}

func TestProducedWithoutStoresLeavesFilesUnnamed(t *testing.T) {
	producers := &fakeProducers{rows: []producedby.Row{assetRow("asset-1"), resourceRow("res-1")}}
	items, err := New(producers, nil, nil, nil).Produced(context.Background(), "script-1", 0)
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, it := range items {
		assert.Empty(t, it.Name)
		assert.False(t, it.Deleted, "an unresolved file is not a deleted one")
	}
}

func TestProducedAssetLookupFailureLeavesNothingDeleted(t *testing.T) {
	producers := &fakeProducers{rows: []producedby.Row{assetRow("asset-1")}}
	assets := &fakeAssets{err: errors.New("boom")}
	items, err := New(producers, assets, nil, nil).Produced(context.Background(), "script-1", 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].Deleted)
}

func TestProducedResourceReadFailureIsNotADeletion(t *testing.T) {
	producers := &fakeProducers{rows: []producedby.Row{resourceRow("res-1")}}
	resources := &fakeResources{err: errors.New("database down")}
	items, err := New(producers, nil, resources, nil).Produced(context.Background(), "script-1", 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].Deleted)
}

func TestProducedOnlyResourcesSkipsTheAssetRead(t *testing.T) {
	producers := &fakeProducers{rows: []producedby.Row{resourceRow("res-1")}}
	assets := &fakeAssets{err: errors.New("must not be called")}
	items, err := New(producers, assets, &fakeResources{res: &resource.Resource{DisplayName: "r"}}, nil).
		Produced(context.Background(), "script-1", 0)
	require.NoError(t, err)
	assert.Equal(t, "r", items[0].Name)
}

func TestProducedPropagatesTheListingFailure(t *testing.T) {
	producers := &fakeProducers{err: errors.New("boom")}
	_, err := New(producers, nil, nil, nil).Produced(context.Background(), "script-1", 0)
	assert.Error(t, err)
}

func TestProducedPassesTheLimitThrough(t *testing.T) {
	producers := &fakeProducers{}
	_, err := New(producers, nil, nil, nil).Produced(context.Background(), "script-1", 12)
	require.NoError(t, err)
	assert.Equal(t, 12, producers.limit)
}

func TestNamesResolvesLiveScriptsOnly(t *testing.T) {
	scripts := &fakeScripts{byID: map[string]*script.Script{"live": {ID: "live", Name: "daily-sales"}}}
	names, err := New(&fakeProducers{}, nil, nil, scripts).
		Names(context.Background(), []string{"live", "gone", "live"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"live": "daily-sales"}, names)
}

func TestNamesWithoutLookupResolvesNone(t *testing.T) {
	names, err := New(&fakeProducers{}, nil, nil, nil).Names(context.Background(), []string{"a"})
	require.NoError(t, err)
	assert.Empty(t, names)
}

// TestNamesReportsALookupFailure keeps a briefly unavailable database from
// reading as "every script was deleted".
func TestNamesReportsALookupFailure(t *testing.T) {
	scripts := &fakeScripts{err: errors.New("database down")}
	_, err := New(&fakeProducers{}, nil, nil, scripts).Names(context.Background(), []string{"a"})
	assert.Error(t, err)
}
