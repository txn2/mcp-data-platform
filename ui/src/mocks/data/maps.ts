import type { MapPreset, MapRegion, MapsView } from "@/api/admin/types";

// The maps settings section (#2068) in the dev mock: one region in each state
// a fetch moves through, so the settings page shows all four.

const usBounds = { min_lon: -125, min_lat: 24.4, max_lon: -66.9, max_lat: 49.4 };

export const mockMapPresets: MapPreset[] = [
  { id: "united-states", name: "United States (contiguous)", bounds: usBounds },
  { id: "alaska", name: "Alaska", bounds: { min_lon: -179.2, min_lat: 51.2, max_lon: -129.9, max_lat: 71.5 } },
  { id: "hawaii", name: "Hawaii", bounds: { min_lon: -160.3, min_lat: 18.9, max_lon: -154.8, max_lat: 22.3 } },
  { id: "europe", name: "Europe", bounds: { min_lon: -25, min_lat: 34.5, max_lon: 45, max_lat: 71.5 } },
];

const sfBounds = { min_lon: -122.52, min_lat: 37.7, max_lon: -122.35, max_lat: 37.83 };

export const mockMapRegions: MapRegion[] = [
  {
    id: "united-states", name: "United States (contiguous)", preset: "united-states", origin: "fetch",
    bounds: usBounds, max_zoom: 14, state: "fetching", progress_bytes: 3_412_000_000,
    total_bytes: 8_830_000_000, requested_at: "2026-10-09T08:40:00Z", started_at: "2026-10-09T08:40:02Z",
  },
  {
    id: "sf", name: "San Francisco", origin: "fetch", bounds: sfBounds, max_zoom: 14, state: "ready",
    progress_bytes: 4_126_605, total_bytes: 4_126_605, requested_at: "2026-10-09T08:10:00Z",
    archive: { size_bytes: 4_126_605, build: "2026-10-08", min_zoom: 0, max_zoom: 14, bounds: sfBounds, ready_at: "2026-10-09T08:10:09Z" },
  },
  {
    id: "seattle", name: "Seattle", origin: "fetch",
    bounds: { min_lon: -122.46, min_lat: 47.48, max_lon: -122.22, max_lat: 47.74 }, max_zoom: 14, state: "failed",
    progress_bytes: 0, total_bytes: 0, requested_at: "2026-10-09T08:30:00Z",
    error: "pmtiles: reading the header: reading the source archive: HTTP 503",
    archive: { size_bytes: 6_210_440, build: "2026-10-01", min_zoom: 0, max_zoom: 14,
      bounds: { min_lon: -122.46, min_lat: 47.48, max_lon: -122.22, max_lat: 47.74 }, ready_at: "2026-10-01T09:00:00Z" },
  },
  {
    id: "denver", name: "Denver", origin: "fetch",
    bounds: { min_lon: -105.11, min_lat: 39.61, max_lon: -104.6, max_lat: 39.91 }, max_zoom: 14, state: "queued",
    progress_bytes: 0, total_bytes: 0, requested_at: "2026-10-09T08:45:00Z",
  },
];

export const mockMaps: MapsView = {
  settings: {
    enabled: true, s3_connection: "", bucket: "", max_zoom: 14, source_url: "",
    resolved_bucket: "managed-resources", updated_by: "sarah.chen@example.com", updated_at: "2026-10-09T08:39:00Z",
  },
  regions: mockMapRegions,
  presets: mockMapPresets,
  upload_prefix: "maps/uploads/",
  default_source: "https://build.protomaps.com/ (newest daily build)",
};
