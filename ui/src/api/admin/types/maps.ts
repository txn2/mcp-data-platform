// Street maps (#2068): the basemap a map asset reads from this deployment.
// A region is cut from an OpenStreetMap build into the operator's bucket and
// served by range at /portal/maps/{id}.pmtiles.

export interface MapBounds {
  min_lon: number;
  min_lat: number;
  max_lon: number;
  max_lat: number;
}

export type MapRegionState = "queued" | "fetching" | "ready" | "failed";

/** MapArchive is the archive a region serves. */
export interface MapArchive {
  size_bytes: number;
  build: string;
  min_zoom: number;
  max_zoom: number;
  bounds: MapBounds;
  ready_at: string;
}

export interface MapRegion {
  id: string;
  name: string;
  preset?: string;
  origin: "fetch" | "upload";
  bounds: MapBounds;
  max_zoom: number;
  state: MapRegionState;
  progress_bytes: number;
  total_bytes: number;
  error?: string;
  requested_at: string;
  started_at?: string;
  finished_at?: string;
  archive?: MapArchive;
  created_by?: string;
}

export interface MapPreset {
  id: string;
  name: string;
  bounds: MapBounds;
}

export interface MapSettings {
  enabled: boolean;
  s3_connection: string;
  bucket: string;
  max_zoom: number;
  source_url: string;
  resolved_bucket: string;
  updated_by?: string;
  updated_at?: string;
}

/** MapsView is GET /settings/maps: the settings, every region, the presets. */
export interface MapsView {
  settings: MapSettings;
  regions: MapRegion[];
  presets: MapPreset[];
  upload_prefix: string;
  default_source: string;
}

export type MapSettingsInput = Pick<
  MapSettings,
  "enabled" | "s3_connection" | "bucket" | "max_zoom" | "source_url"
>;

/** MapRegionInput is a preset by id, or an id, a name and a box. */
export interface MapRegionInput {
  preset?: string;
  id?: string;
  name?: string;
  bounds?: MapBounds;
}

export interface MapEstimate {
  size_bytes: number;
  tiles: number;
  build: string;
  min_zoom: number;
  max_zoom: number;
  bounds: MapBounds;
}
