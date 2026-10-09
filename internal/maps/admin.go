package maps

import (
	"context"
	"errors"
	"time"

	"github.com/txn2/mcp-data-platform/internal/pmtiles"
)

// View is the maps section as the settings page shows it.
type View struct {
	Settings SettingsView `json:"settings"`
	Regions  []Region     `json:"regions"`
	Presets  []Preset     `json:"presets"`
	// UploadPrefix is where in the bucket an operator puts a .pmtiles file to
	// serve it without a fetch.
	UploadPrefix string `json:"upload_prefix"`
	// DefaultSource is the source a fetch reads when source_url is empty.
	DefaultSource string `json:"default_source"`
}

// SettingsView is the stored settings with the defaults they resolve to.
type SettingsView struct {
	Enabled      bool   `json:"enabled"`
	S3Connection string `json:"s3_connection"`
	Bucket       string `json:"bucket"`
	MaxZoom      int    `json:"max_zoom"`
	SourceURL    string `json:"source_url"`
	// ResolvedBucket is the bucket archives are written to: Bucket, or the
	// managed-resources bucket when that is empty.
	ResolvedBucket string     `json:"resolved_bucket"`
	UpdatedBy      string     `json:"updated_by,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
}

// Estimate is what a fetch of a region would write.
type Estimate struct {
	SizeBytes int64          `json:"size_bytes"`
	Tiles     uint64         `json:"tiles"`
	Build     string         `json:"build"`
	MinZoom   int            `json:"min_zoom"`
	MaxZoom   int            `json:"max_zoom"`
	Bounds    pmtiles.Bounds `json:"bounds"`
}

// ResolvedBucket names the bucket archives go to under s.
type ResolvedBucket func(s Settings) string

// View returns the settings and every region.
func (s *Service) View(ctx context.Context, bucketOf ResolvedBucket) (View, error) {
	settings, err := s.d.Settings.Get(ctx)
	if err != nil {
		return View{}, err //nolint:wrapcheck // the store's error names what it was reading
	}
	regions, err := s.d.Regions.List(ctx)
	if err != nil {
		return View{}, err //nolint:wrapcheck // the store's error names what it was reading
	}
	v := SettingsView{
		Enabled: settings.Enabled, S3Connection: settings.S3Connection, Bucket: settings.Bucket,
		MaxZoom: settings.MaxZoom, SourceURL: settings.SourceURL, UpdatedBy: settings.UpdatedBy,
	}
	if bucketOf != nil {
		v.ResolvedBucket = bucketOf(settings)
	}
	if !settings.UpdatedAt.IsZero() {
		v.UpdatedAt = &settings.UpdatedAt
	}
	return View{
		Settings: v, Regions: regions, Presets: Presets(),
		UploadPrefix: UploadPrefix, DefaultSource: s.d.BuildURL + " (newest daily build)",
	}, nil
}

// SaveSettings stores the settings and wakes the fetch.
func (s *Service) SaveSettings(ctx context.Context, in SettingsInput, author string) error {
	settings := Settings{
		Enabled: in.Enabled, S3Connection: in.S3Connection, Bucket: in.Bucket,
		MaxZoom: in.MaxZoom, SourceURL: in.SourceURL,
	}
	if msg := settings.Validate(); msg != "" {
		return inputErr("%s", msg)
	}
	if err := s.d.Settings.Set(ctx, settings, author); err != nil {
		return err //nolint:wrapcheck // the store's error names what it was writing
	}
	s.d.Changed(ctx)
	s.poke()
	return nil
}

// AddRegion saves a region and queues its fetch at the settings' zoom.
func (s *Service) AddRegion(ctx context.Context, in RegionInput, author string) (*Region, error) {
	p, err := resolveRegion(in.Preset, in.Bounds)
	if err != nil {
		return nil, err
	}
	r := Region{ID: in.ID, Name: in.Name, Bounds: p.Bounds, CreatedBy: author}
	if in.Preset != "" {
		r.Preset = p.ID
		if r.ID == "" {
			r.ID = p.ID
		}
		if r.Name == "" {
			r.Name = p.Name
		}
	}
	if !ValidRegionID(r.ID) {
		return nil, inputErr("id must be lowercase letters, digits and dashes, starting with a letter or digit, at most 63 characters")
	}
	if r.Name == "" {
		r.Name = r.ID
	}
	settings, err := s.d.Settings.Get(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's error names what it was reading
	}
	r.MaxZoom = settings.MaxZoom
	if err := s.d.Regions.Create(ctx, r); err != nil {
		if errors.Is(err, ErrRegionExists) {
			return nil, inputErr("a region with id %q already exists", r.ID)
		}
		return nil, err //nolint:wrapcheck // the store's error names what it was writing
	}
	s.poke()
	return s.d.Regions.Get(ctx, r.ID) //nolint:wrapcheck // the store's error names what it was reading
}

// RefreshRegion queues a fetch of the newest build, at the settings' zoom.
// The archive being served keeps serving until the new one is complete.
func (s *Service) RefreshRegion(ctx context.Context, id string) (*Region, error) {
	settings, err := s.d.Settings.Get(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's error names what it was reading
	}
	r, err := s.d.Regions.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's error names what it was reading
	}
	if r.Origin == OriginUpload {
		return nil, inputErr("an uploaded archive is replaced by uploading a new file, not fetched")
	}
	if err := s.d.Regions.Requeue(ctx, id, settings.MaxZoom); err != nil {
		return nil, err //nolint:wrapcheck // the store's error names what it was writing
	}
	s.poke()
	return s.d.Regions.Get(ctx, id) //nolint:wrapcheck // the store's error names what it was reading
}

// DeleteRegion removes a region and its archive. An uploaded region is
// removed by deleting its file; deleting the row alone would bring it back on
// the next scan.
func (s *Service) DeleteRegion(ctx context.Context, id string) error {
	r, err := s.d.Regions.Get(ctx, id)
	if err != nil {
		return err //nolint:wrapcheck // the store's error names what it was reading
	}
	settings, err := s.d.Settings.Get(ctx)
	if err != nil {
		return err //nolint:wrapcheck // the store's error names what it was reading
	}
	if _, err := s.d.Regions.Delete(ctx, id); err != nil {
		return err //nolint:wrapcheck // the store's error names what it was writing
	}
	key := ""
	bucketName := ""
	switch {
	case r.Archive != nil:
		key, bucketName = r.Archive.Key, r.Archive.Bucket
	case r.Origin == OriginUpload:
		key = UploadPrefix + r.ID + ".pmtiles"
	}
	if key != "" {
		if bucket, err := s.d.Open(ctx, settings); err == nil {
			if bucketName == "" {
				bucketName = bucket.Name
			}
			removeObject(ctx, bucket.Objects, bucketName, key)
		}
	}
	s.d.Changed(ctx)
	return nil
}

// estimateTimeout bounds an estimate, which reads the source's directories
// for the region: seconds for a country, a few for a continent.
const estimateTimeout = 2 * time.Minute

// Estimate plans an extract without writing it and reports its size.
func (s *Service) Estimate(ctx context.Context, in EstimateInput) (Estimate, error) {
	p, err := resolveRegion(in.Preset, in.Bounds)
	if err != nil {
		return Estimate{}, err
	}
	settings, err := s.d.Settings.Get(ctx)
	if err != nil {
		return Estimate{}, err //nolint:wrapcheck // the store's error names what it was reading
	}
	ctx, cancel := context.WithTimeout(ctx, estimateTimeout)
	defer cancel()
	ref, err := resolveSource(ctx, s.d.Client, settings, s.d.IndexURL, s.d.BuildURL)
	if err != nil {
		return Estimate{}, err
	}
	plan, err := pmtiles.NewPlan(ctx, newHTTPSource(s.d.Client, ref.url, s.d.RetryPause),
		pmtiles.Request{Bounds: p.Bounds, MaxZoom: uint8(settings.MaxZoom)}) // #nosec G115 -- validated to 0..15
	if err != nil {
		return Estimate{}, err //nolint:wrapcheck // the extract's errors name what failed
	}
	build := ref.build
	if meta, err := plan.Metadata(); err == nil {
		build = buildDate(meta, ref.build)
	}
	h := plan.Header()
	return Estimate{
		SizeBytes: int64(plan.Size()), Tiles: h.AddressedTiles, Build: build, // #nosec G115 -- far below 2^63
		MinZoom: int(h.MinZoom), MaxZoom: int(h.MaxZoom), Bounds: h.Bounds(),
	}, nil
}
