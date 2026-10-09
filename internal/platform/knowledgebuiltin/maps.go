package knowledgebuiltin

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/maps"
)

// mapRegionsPlaceholder marks where the maps page receives the regions this
// deployment serves (#2068). The page is the one an agent reads before drawing
// a map, so it says what is here, or plainly that nothing is, rather than
// leaving the agent to discover a 404.
const mapRegionsPlaceholder = "{{MAP_REGIONS}}"

// MapState reads what the maps surface offers a map. *maps.Reader satisfies
// it; nil is a deployment with no database, so no maps.
type MapState interface {
	State(ctx context.Context) (maps.State, error)
}

// MapsOf returns the map state over db, or nil without a database.
func MapsOf(db *sql.DB) MapState {
	if db == nil {
		return nil
	}
	return maps.NewReader(db)
}

// mapStateOf reads the state the maps page is written with, or nil when it
// could not be read.
func mapStateOf(ctx context.Context, ms MapState) *maps.State {
	if ms == nil {
		return &maps.State{Regions: []maps.Ready{}}
	}
	state, err := ms.State(ctx)
	if err != nil {
		slog.WarnContext(ctx, "built-in knowledge pages: reading the map regions",
			"error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return &state
}

// mapRegionsSection renders the regions part of the maps page.
func mapRegionsSection(state *maps.State) string {
	live := "A document can read the list as it is now from `" + maps.RegionsPath +
		"`, which answers `{\"enabled\": ..., \"regions\": [...]}` with each region's `id`, `name`, `url`, " +
		"`bounds` (`min_lon`, `min_lat`, `max_lon`, `max_lat`), `min_zoom`, `max_zoom` and `build`."
	switch {
	case state == nil:
		return "The regions this deployment serves could not be read when this page was written. " + live
	case !state.Enabled:
		return "**This deployment has no basemap.** Maps are turned off in Admin > Settings > Maps, so no " +
			"region is served and `/portal/maps/` answers 404. A street map cannot be drawn here. Say so, and " +
			"offer what can be drawn without one: a state or county map from the boundaries below, or the data " +
			"as a table or chart. An administrator can turn maps on and add a region."
	case len(state.Regions) == 0:
		return "**Maps are on, but no region is ready yet.** A region is being fetched, or none has been " +
			"added. Until one is ready, draw what needs no basemap (the state and county boundaries below). " + live
	}
	var b strings.Builder
	_, _ = b.WriteString("These regions were ready when this page was last written. " + live + "\n\n")
	_, _ = b.WriteString("| Region | Archive | Bounds (west, south, east, north) | Zooms | Built from |\n|---|---|---|---|---|\n")
	for _, r := range state.Regions {
		_, _ = fmt.Fprintf(&b, "| %s (`%s`) | `%s` | %.4f, %.4f, %.4f, %.4f | %d to %d | OpenStreetMap, %s |\n",
			r.Name, r.ID, r.URL, r.Bounds.MinLon, r.Bounds.MinLat, r.Bounds.MaxLon, r.Bounds.MaxLat,
			r.MinZoom, r.MaxZoom, r.Build)
	}
	return strings.TrimRight(b.String(), "\n")
}
