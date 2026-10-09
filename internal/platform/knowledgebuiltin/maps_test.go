package knowledgebuiltin

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/maps"
	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

type stateFunc func(context.Context) (maps.State, error)

func (f stateFunc) State(ctx context.Context) (maps.State, error) { return f(ctx) }

func mapsPage(t *testing.T, state *maps.State) string {
	t.Helper()
	pages, err := PagesWith(state)
	require.NoError(t, err)
	for _, p := range pages {
		if p.Slug == "platform-maps" {
			assert.NotContains(t, p.Body, mapRegionsPlaceholder)
			return p.Body
		}
	}
	t.Fatal("the maps page is not shipped")
	return ""
}

// The maps page says what this deployment serves, or plainly that it serves
// no basemap, so an agent learns it before drawing rather than from a 404.
func TestMapsPage_SaysWhatThisDeploymentServes(t *testing.T) {
	off := mapsPage(t, &maps.State{Regions: []maps.Ready{}})
	assert.Contains(t, off, "This deployment has no basemap.")

	empty := mapsPage(t, &maps.State{Enabled: true, Regions: []maps.Ready{}})
	assert.Contains(t, empty, "no region is ready yet")

	ready := mapsPage(t, &maps.State{Enabled: true, Regions: []maps.Ready{{
		ID: "sf", Name: "San Francisco", URL: maps.ArchivePath("sf"), MinZoom: 0, MaxZoom: 13, Build: "2026-10-08",
		Bounds: pmtiles.Bounds{MinLon: -122.52, MinLat: 37.7, MaxLon: -122.35, MaxLat: 37.83},
	}}})
	assert.Contains(t, ready, "| San Francisco (`sf`) | `/portal/maps/sf.pmtiles` | -122.5200, 37.7000, -122.3500, 37.8300 | 0 to 13 | OpenStreetMap, 2026-10-08 |")
	assert.Contains(t, ready, maps.RegionsPath)
	assert.NotContains(t, ready, "This deployment has no basemap.")

	unknown := mapsPage(t, nil)
	assert.Contains(t, unknown, "could not be read")
}

// Every runtime path the page names is one the portal build serves: the
// vite plugin's list and this page are held together here.
func TestMapsPage_NamesTheServedRuntime(t *testing.T) {
	body := mapsPage(t, &maps.State{Regions: []maps.Ready{}})
	for _, p := range []string{
		"/portal/vendor/maplibre/maplibre-gl.js", "/portal/vendor/maplibre/maplibre-gl.css",
		"/portal/vendor/maplibre/pmtiles.js", "/portal/vendor/maplibre/basemaps.js",
		"/portal/vendor/maplibre/topojson-client.js", "/portal/vendor/maplibre/us-atlas/states-albers-10m.json",
		"/portal/vendor/maplibre/fonts/{fontstack}/{range}.pbf", "MapLibre GL JS 5.24.0",
	} {
		assert.Contains(t, body, p)
	}
	assert.False(t, strings.Contains(body, "unpkg.com") || strings.Contains(body, "cdn.jsdelivr"), "the page names no CDN")
}

func TestMapStateOf(t *testing.T) {
	ctx := context.Background()
	off := mapStateOf(ctx, nil)
	require.NotNil(t, off)
	assert.False(t, off.Enabled)

	on := mapStateOf(ctx, stateFunc(func(context.Context) (maps.State, error) {
		return maps.State{Enabled: true, Regions: []maps.Ready{}}, nil
	}))
	require.NotNil(t, on)
	assert.True(t, on.Enabled)

	assert.Nil(t, mapStateOf(ctx, stateFunc(func(context.Context) (maps.State, error) {
		return maps.State{}, errors.New("down")
	})))
	assert.Nil(t, MapsOf(nil))
}
