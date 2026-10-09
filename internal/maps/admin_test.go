package maps

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

func TestAddRegion(t *testing.T) {
	ts := newTestService(t, nil)
	ctx := context.Background()

	r, err := ts.AddRegion(ctx, RegionInput{Preset: DefaultPresetID}, "a@example.com")
	require.NoError(t, err)
	assert.Equal(t, "united-states", r.ID)
	assert.Equal(t, "United States (contiguous)", r.Name)
	assert.Equal(t, StateQueued, r.State)
	assert.Equal(t, 3, r.MaxZoom, "the fetch is at the zoom the settings name")

	named, err := ts.AddRegion(ctx, RegionInput{Preset: "hawaii", ID: "islands", Name: "The islands"}, "a")
	require.NoError(t, err)
	assert.Equal(t, "islands", named.ID)
	assert.Equal(t, "hawaii", named.Preset)

	box, err := ts.AddRegion(ctx, RegionInput{ID: "box", Bounds: &sf}, "a")
	require.NoError(t, err)
	assert.Equal(t, "box", box.Name, "a box with no name is named by its id")

	for _, tc := range []struct {
		in   RegionInput
		want string
	}{
		{RegionInput{Preset: "atlantis"}, `unknown preset "atlantis"`},
		{RegionInput{ID: "x"}, "name a preset or give bounds"},
		{RegionInput{ID: "x", Bounds: &pmtiles.Bounds{MinLon: 5, MaxLon: 1, MinLat: 0, MaxLat: 1}}, "bounds: min_lon"},
		{RegionInput{ID: "Bad Id", Bounds: &sf}, "id must be lowercase"},
		{RegionInput{Preset: DefaultPresetID}, `a region with id "united-states" already exists`},
	} {
		_, err := ts.AddRegion(ctx, tc.in, "a")
		var ie *InputError
		require.ErrorAs(t, err, &ie, "%+v", tc.in)
		assert.Contains(t, ie.Error(), tc.want)
	}

	ts.settings.err = errors.New("down")
	_, err = ts.AddRegion(ctx, RegionInput{ID: "y", Bounds: &sf}, "a")
	require.ErrorContains(t, err, "down")
	ts.settings.err = nil
	ts.regions.failNext = errors.New("insert failed")
	_, err = ts.AddRegion(ctx, RegionInput{ID: "y", Bounds: &sf}, "a")
	require.ErrorContains(t, err, "insert failed")
}

func TestRefreshRegion(t *testing.T) {
	ts := newTestService(t, nil)
	ctx := context.Background()
	addSF(t, ts)

	ts.regions.rows["sf"].State = StateFetching
	_, err := ts.RefreshRegion(ctx, "sf")
	require.ErrorIs(t, err, ErrRegionBusy)

	ts.regions.rows["sf"].State = StateFailed
	ts.settings.s.MaxZoom = 5
	r, err := ts.RefreshRegion(ctx, "sf")
	require.NoError(t, err)
	assert.Equal(t, StateQueued, r.State)
	assert.Equal(t, 5, r.MaxZoom, "a refresh fetches at the zoom the settings name now")

	_, err = ts.RefreshRegion(ctx, "nowhere")
	require.ErrorIs(t, err, ErrRegionNotFound)

	ts.regions.rows["up"] = &Region{ID: "up", Origin: OriginUpload, State: StateReady}
	_, err = ts.RefreshRegion(ctx, "up")
	var ie *InputError
	require.ErrorAs(t, err, &ie)

	ts.settings.err = errors.New("down")
	_, err = ts.RefreshRegion(ctx, "sf")
	require.ErrorContains(t, err, "down")
}

func TestDeleteRegion_RemovesItsArchive(t *testing.T) {
	ts := newTestService(t, nil)
	ctx := context.Background()
	ts.objects.objects["bucket/maps/regions/sf/a.pmtiles"] = []byte("x")
	ts.regions.rows["sf"] = &Region{
		ID: "sf", Origin: OriginFetch, State: StateReady,
		Archive: &Archive{Bucket: "bucket", Key: "maps/regions/sf/a.pmtiles"},
	}
	require.NoError(t, ts.DeleteRegion(ctx, "sf"))
	assert.Empty(t, ts.objects.keys("bucket/"))
	assert.Equal(t, 1, ts.changed)

	ts.objects.objects["bucket/"+UploadPrefix+"bad.pmtiles"] = []byte("x")
	ts.regions.rows["bad"] = &Region{ID: "bad", Origin: OriginUpload, State: StateFailed}
	require.NoError(t, ts.DeleteRegion(ctx, "bad"))
	assert.Empty(t, ts.objects.keys("bucket/"), "an uploaded file is deleted with its region")

	require.ErrorIs(t, ts.DeleteRegion(ctx, "nowhere"), ErrRegionNotFound)
}

func TestSaveSettings(t *testing.T) {
	ts := newTestService(t, nil)
	ctx := context.Background()
	require.NoError(t, ts.SaveSettings(ctx, SettingsInput{Enabled: true, MaxZoom: 12, SourceURL: "https://mirror.example.com/planet.pmtiles"}, "a@example.com"))
	got, _ := ts.settings.Get(ctx)
	assert.Equal(t, 12, got.MaxZoom)
	assert.Equal(t, "a@example.com", got.UpdatedBy)
	assert.Equal(t, 1, ts.changed, "turning maps on or off rewrites the knowledge page")

	var ie *InputError
	require.ErrorAs(t, ts.SaveSettings(ctx, SettingsInput{MaxZoom: 16}, "a"), &ie)
	require.ErrorAs(t, ts.SaveSettings(ctx, SettingsInput{MaxZoom: 0}, "a"), &ie, "an omitted zoom is refused, not read as zoom 0")
	require.ErrorAs(t, ts.SaveSettings(ctx, SettingsInput{MaxZoom: 14, SourceURL: "ftp://x/y"}, "a"), &ie)
	ts.settings.err = errors.New("down")
	require.ErrorContains(t, ts.SaveSettings(ctx, SettingsInput{MaxZoom: 14}, "a"), "down")
}

func TestView(t *testing.T) {
	ts := newTestService(t, nil)
	ctx := context.Background()
	addSF(t, ts)
	v, err := ts.View(ctx, func(Settings) string { return "managed-resources" })
	require.NoError(t, err)
	assert.Equal(t, "managed-resources", v.Settings.ResolvedBucket)
	assert.Len(t, v.Regions, 1)
	assert.Len(t, v.Presets, len(presets))
	assert.Equal(t, UploadPrefix, v.UploadPrefix)
	assert.Nil(t, v.Settings.UpdatedAt)

	require.NoError(t, ts.SaveSettings(ctx, SettingsInput{Enabled: true, MaxZoom: 3}, "a"))
	v, err = ts.View(ctx, nil)
	require.NoError(t, err)
	assert.NotNil(t, v.Settings.UpdatedAt)

	ts.regions.failNext = errors.New("list failed")
	_, err = ts.View(ctx, nil)
	require.ErrorContains(t, err, "list failed")
	ts.settings.err = errors.New("down")
	_, err = ts.View(ctx, nil)
	require.ErrorContains(t, err, "down")
}

func TestStateAndServedArchive(t *testing.T) {
	ts := newTestService(t, nil)
	ctx := context.Background()
	ts.regions.rows["sf"] = &Region{
		ID: "sf", Name: "San Francisco", State: StateReady,
		Archive: &Archive{Bucket: "bucket", Key: "k", Size: 10, Build: "2026-10-08", MaxZoom: 13, Bounds: sf},
	}
	ts.regions.rows["la"] = &Region{ID: "la", Name: "Los Angeles", State: StateQueued}

	st, err := ts.State(ctx)
	require.NoError(t, err)
	assert.True(t, st.Enabled)
	require.Len(t, st.Regions, 1, "a region with no archive is not listed")
	assert.Equal(t, "/portal/maps/sf.pmtiles", st.Regions[0].URL)

	a, objs, err := ts.ServedArchive(ctx, "sf")
	require.NoError(t, err)
	assert.Equal(t, "k", a.Key)
	assert.NotNil(t, objs)

	for _, id := range []string{"la", "nowhere", "../etc"} {
		_, _, err = ts.ServedArchive(ctx, id)
		require.ErrorIs(t, err, ErrUnavailable, id)
	}

	ts.settings.s.Enabled = false
	st, err = ts.State(ctx)
	require.NoError(t, err)
	assert.False(t, st.Enabled)
	assert.Empty(t, st.Regions)
	assert.NotNil(t, st.Regions, "an empty list is [], never null")
	_, _, err = ts.ServedArchive(ctx, "sf")
	require.ErrorIs(t, err, ErrUnavailable, "a disabled deployment serves nothing")

	ts.settings.s.Enabled = true
	ts.d.Open = func(context.Context, Settings) (Bucket, error) { return Bucket{}, errors.New("no s3") }
	_, _, err = ts.ServedArchive(ctx, "sf")
	require.ErrorContains(t, err, "no s3")
	ts.regions.failNext = errors.New("read failed")
	_, _, err = ts.ServedArchive(ctx, "sf")
	require.ErrorContains(t, err, "read failed")
	ts.regions.failNext = errors.New("list failed")
	_, err = ts.State(ctx)
	require.ErrorContains(t, err, "list failed")
	ts.settings.err = errors.New("down")
	_, err = ts.State(ctx)
	require.ErrorContains(t, err, "down")
	_, _, err = ts.ServedArchive(ctx, "sf")
	require.ErrorContains(t, err, "down")
}

func TestEstimate(t *testing.T) {
	src := newSourceServer(t, map[string][]byte{"20261008.pmtiles": testArchive(t, 3)})
	ts := newTestService(t, src)
	est, err := ts.Estimate(context.Background(), EstimateInput{Bounds: &sf})
	require.NoError(t, err)
	assert.Positive(t, est.SizeBytes)
	assert.Equal(t, uint64(4), est.Tiles, "one tile at each of zooms 0 to 3")
	assert.Equal(t, "2026-10-08", est.Build)
	assert.Empty(t, ts.objects.keys(""), "an estimate writes nothing")

	var ie *InputError
	_, err = ts.Estimate(context.Background(), EstimateInput{Preset: "nowhere"})
	require.ErrorAs(t, err, &ie)

	ts.settings.s.SourceURL = "http://127.0.0.1:1/x.pmtiles"
	_, err = ts.Estimate(context.Background(), EstimateInput{Preset: DefaultPresetID})
	require.Error(t, err)

	ts.settings.err = errors.New("down")
	_, err = ts.Estimate(context.Background(), EstimateInput{Preset: DefaultPresetID})
	require.ErrorContains(t, err, "down")
}

func TestSettingsValidate(t *testing.T) {
	assert.Empty(t, DefaultSettings().Validate())
	assert.Equal(t, DefaultMaxZoom, DefaultSettings().MaxZoom)
	assert.NotEmpty(t, Settings{MaxZoom: 0}.Validate())
	assert.NotEmpty(t, Settings{MaxZoom: 16}.Validate())
	assert.NotEmpty(t, Settings{MaxZoom: 14, SourceURL: "not a url"}.Validate())
	assert.NotEmpty(t, Settings{MaxZoom: 14, SourceURL: "https://"}.Validate())
	assert.Empty(t, Settings{MaxZoom: 14, SourceURL: "http://mirror.internal/planet.pmtiles"}.Validate())
}

func TestPresets(t *testing.T) {
	ps := Presets()
	require.NotEmpty(t, ps)
	assert.Equal(t, DefaultPresetID, ps[0].ID, "the United States is offered first")
	for _, p := range ps {
		require.NoError(t, p.Bounds.Validate(), p.ID)
		assert.True(t, ValidRegionID(p.ID), p.ID)
	}
	ps[0].Name = "changed"
	again, _ := PresetByID(DefaultPresetID)
	assert.NotEqual(t, "changed", again.Name, "Presets hands out a copy")
	_, ok := PresetByID("atlantis")
	assert.False(t, ok)
}

func TestArchiveETagChangesWithTheObject(t *testing.T) {
	a := Archive{Bucket: "b", Key: "maps/regions/sf/1.pmtiles"}
	b := Archive{Bucket: "b", Key: "maps/regions/sf/2.pmtiles"}
	assert.NotEqual(t, a.ETag(), b.ETag())
	assert.Equal(t, a.ETag(), a.ETag())
	assert.Regexp(t, `^"[0-9a-f]+"$`, a.ETag())
}
