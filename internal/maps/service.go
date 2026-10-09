package maps

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/internal/bgloop"
	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/pmtiles"
	"github.com/txn2/mcp-data-platform/pkg/portal/s3adapter"
)

// Objects is what the maps surface does with the bucket.
type Objects interface {
	PutObjectStream(ctx context.Context, bucket, key string, body io.Reader, contentType string) (int64, error)
	GetObjectRange(ctx context.Context, bucket, key string, offset, length int64) ([]byte, int64, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	ListDirectory(ctx context.Context, bucket, prefix string) ([]s3adapter.ObjectEntry, bool, error)
}

// Bucket is an opened bucket: its client and its name.
type Bucket struct {
	Objects Objects
	Name    string
}

// OpenBucket opens the bucket the settings name, resolving their empty
// connection and bucket to the managed-resources ones.
type OpenBucket func(ctx context.Context, s Settings) (Bucket, error)

// Where archives live in the bucket. A fetched region's archive is named by
// its build and the time it was written, so a refresh writes beside the
// archive being served and the swap is a row update; an uploaded archive is
// any .pmtiles file directly under the uploads prefix.
const (
	ObjectPrefix = "maps/"
	UploadPrefix = ObjectPrefix + "uploads/"
	regionPrefix = ObjectPrefix + "regions/"
	archiveType  = "application/vnd.pmtiles"
)

// FetchLockKey is the advisory lock a fetch pass holds, so one replica does
// the work. Distinct from every other advisory lock key the platform takes.
const FetchLockKey int64 = 4713210301

// Deps is what a Service is built from.
type Deps struct {
	DB       *sql.DB
	Settings SettingsStore
	Regions  RegionStore
	Open     OpenBucket
	// Client is the outbound client a fetch reads its source through.
	Client *http.Client
	// Changed is called after anything the maps knowledge page shows has
	// changed: the settings, or a region becoming ready or going away.
	Changed func(ctx context.Context)
	// Every is the wait between fetch passes on a replica no one woke.
	Every time.Duration
	// IndexURL and BuildURL locate the Protomaps builds; empty is Protomaps'.
	IndexURL string
	BuildURL string
	// RetryPause is the wait before a failed range read is tried again; nil
	// is 1s, 4s, 9s.
	RetryPause func(attempt int) time.Duration
}

// Service is the maps surface: the settings and regions an administrator
// manages, the background fetch, and the reads the public routes make.
type Service struct {
	d      Deps
	wake   chan struct{}
	stop   chan struct{}
	once   sync.Once
	wg     sync.WaitGroup
	locker func(ctx context.Context) (unlock func(), ok bool, err error)

	mu     sync.Mutex
	cancel context.CancelFunc
}

// DefaultEvery is the wait between fetch passes. A save on this replica wakes
// its pass at once; another replica's save is picked up within this.
const DefaultEvery = 30 * time.Second

// New returns the maps service.
func New(d Deps) *Service {
	if d.Every <= 0 {
		d.Every = DefaultEvery
	}
	if d.IndexURL == "" {
		d.IndexURL = ProtomapsIndexURL
	}
	if d.BuildURL == "" {
		d.BuildURL = ProtomapsBuildURL
	}
	if d.RetryPause == nil {
		d.RetryPause = defaultRetryPause
	}
	if d.Changed == nil {
		d.Changed = func(context.Context) {}
	}
	s := &Service{d: d, wake: make(chan struct{}, 1), stop: make(chan struct{})}
	s.locker = s.advisoryLock
	return s
}

// Start runs the fetch loop until Stop.
func (s *Service) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()
	s.wg.Go(func() {
		bgloop.Run(ctx, bgloop.Loop{
			Name: bgloop.NameMapFetch, Every: s.d.Every, Immediate: true,
			Wake: s.wake, Stop: s.stop, Body: s.Pass,
		})
	})
}

// Stop ends the fetch loop and waits for it. A fetch in flight is canceled
// and left fetching, so the next pass on any replica queues it again; the
// part it wrote is removed.
func (s *Service) Stop() {
	s.once.Do(func() {
		s.mu.Lock()
		if s.cancel != nil {
			s.cancel()
		}
		s.mu.Unlock()
		close(s.stop)
	})
	s.wg.Wait()
}

// poke starts a pass on this replica now.
func (s *Service) poke() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// advisoryLock takes the fetch lock on a session of its own, so the lock
// lives exactly as long as the pass.
func (s *Service) advisoryLock(ctx context.Context) (unlock func(), ok bool, err error) {
	conn, err := s.d.DB.Conn(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("maps: opening a lock session: %w", err)
	}
	var got bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", FetchLockKey).Scan(&got); err != nil {
		_ = conn.Close()
		return nil, false, fmt.Errorf("maps: taking the fetch lock: %w", err)
	}
	if !got {
		_ = conn.Close()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock($1)", FetchLockKey)
		_ = conn.Close()
	}, true, nil
}

// Pass is one iteration of the fetch loop: under the fetch lock, it records
// what the uploads prefix holds and fetches every queued region.
func (s *Service) Pass(ctx context.Context) error {
	settings, err := s.d.Settings.Get(ctx)
	if err != nil {
		return err //nolint:wrapcheck // the store's error names what it was reading
	}
	if !settings.Enabled {
		return nil
	}
	unlock, ok, err := s.locker(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return bgloop.ErrSkipped
	}
	defer unlock()
	return s.passLocked(ctx, settings)
}

// passLocked is a pass's work, done while this replica holds the fetch lock.
func (s *Service) passLocked(ctx context.Context, settings Settings) error {
	bucket, err := s.d.Open(ctx, settings)
	if err != nil {
		return fmt.Errorf("maps: opening the bucket: %w", err)
	}
	if err := s.d.Regions.ResetStale(ctx); err != nil {
		return err //nolint:wrapcheck // the store's error names what it was doing
	}
	if err := s.scanUploads(ctx, bucket); err != nil {
		slog.WarnContext(ctx, "maps: reading uploaded archives", "error", logsan.SanitizeForLog(err.Error()))
	}
	return s.fetchQueued(ctx, settings, bucket)
}

// fetchQueued fetches queued regions, longest-waiting first, until none is
// left. A failed fetch is recorded on its region and the next is claimed.
func (s *Service) fetchQueued(ctx context.Context, settings Settings, bucket Bucket) error {
	for {
		region, err := s.d.Regions.ClaimNext(ctx)
		if err != nil || region == nil {
			return err //nolint:wrapcheck // the store's error names what it was doing
		}
		err = bgloop.Unit(ctx, bgloop.NameMapFetchRegion, func(ctx context.Context) error {
			return s.fetch(ctx, settings, bucket, *region)
		})
		if err != nil && ctx.Err() != nil {
			return ctx.Err() //nolint:wrapcheck // the loop's own cancellation
		}
	}
}

// progressEvery is how often a fetch in flight records how far it has got.
const progressEvery = 2 * time.Second

// fetch extracts one region into the bucket and makes it the region's
// archive. A failure is recorded on the region and leaves the archive being
// served untouched.
func (s *Service) fetch(ctx context.Context, settings Settings, bucket Bucket, r Region) error {
	key, archive, err := s.extract(ctx, settings, bucket, r)
	if err != nil {
		if key != "" {
			removeObject(ctx, bucket.Objects, bucket.Name, key)
		}
		return s.recordFailure(ctx, r, err)
	}
	prev, ok, err := s.d.Regions.Finish(ctx, r.ID, archive)
	if err != nil {
		removeObject(ctx, bucket.Objects, bucket.Name, key)
		return err //nolint:wrapcheck // the store's error names what it was doing
	}
	if !ok {
		// Deleted while it was fetched: nothing serves what was written.
		removeObject(ctx, bucket.Objects, bucket.Name, key)
		return nil
	}
	if prev != nil && (prev.Key != archive.Key || prev.Bucket != archive.Bucket) {
		removeObject(ctx, bucket.Objects, prev.Bucket, prev.Key)
	}
	s.d.Changed(ctx)
	return nil
}

// recordFailure records why a fetch stopped short. A fetch stopped because
// the platform is stopping is not a failure: the region stays fetching, and
// the next pass on any replica queues it again.
func (s *Service) recordFailure(ctx context.Context, r Region, err error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("maps: fetching %s: %w", r.ID, ctx.Err())
	}
	reason := failureReason(err)
	slog.WarnContext(ctx, "maps: fetch failed", "region", r.ID, "error", logsan.SanitizeForLog(reason))
	if ferr := s.d.Regions.Fail(context.WithoutCancel(ctx), r.ID, reason); ferr != nil {
		return fmt.Errorf("maps: fetching %s: %w", r.ID, errors.Join(err, ferr))
	}
	return err
}

// extract plans and writes one region's archive, returning the key written
// (set once the upload started, so a failure can remove it) and the archive.
func (s *Service) extract(ctx context.Context, settings Settings, bucket Bucket, r Region) (string, Archive, error) {
	ref, err := resolveSource(ctx, s.d.Client, settings, s.d.IndexURL, s.d.BuildURL)
	if err != nil {
		return "", Archive{}, err
	}
	src := newHTTPSource(s.d.Client, ref.url, s.d.RetryPause)
	plan, err := pmtiles.NewPlan(ctx, src, pmtiles.Request{Bounds: r.Bounds, MaxZoom: uint8(r.MaxZoom)}) // #nosec G115 -- validated to 0..15
	if err != nil {
		return "", Archive{}, err //nolint:wrapcheck // the extract's errors name what failed
	}
	size := int64(plan.Size()) // #nosec G115 -- an archive is far below 2^63 bytes
	if err := s.d.Regions.Progress(ctx, r.ID, 0, size); err != nil {
		return "", Archive{}, err //nolint:wrapcheck // the store's error names what it was doing
	}
	build := ref.build
	if meta, err := plan.Metadata(); err == nil {
		build = buildDate(meta, ref.build)
	}
	key := fmt.Sprintf("%s%s/%s-%d.pmtiles", regionPrefix, r.ID, safeName(ref.build), time.Now().UnixNano())

	pr, pw := io.Pipe()
	go func() {
		last := time.Now()
		err := plan.WriteTo(ctx, src, pw, func(written uint64) {
			if time.Since(last) < progressEvery {
				return
			}
			last = time.Now()
			_ = s.d.Regions.Progress(ctx, r.ID, int64(written), size) // #nosec G115 -- see size
		})
		_ = pw.CloseWithError(err)
	}()
	n, err := bucket.Objects.PutObjectStream(ctx, bucket.Name, key, pr, archiveType)
	_ = pr.CloseWithError(err)
	if err != nil {
		return key, Archive{}, fmt.Errorf("writing the archive to the bucket: %w", err)
	}
	if n != size {
		return key, Archive{}, fmt.Errorf("wrote %d bytes of a %d-byte archive", n, size)
	}
	h := plan.Header()
	return key, Archive{
		Bucket: bucket.Name, Key: key, Size: size, Build: build,
		MinZoom: int(h.MinZoom), MaxZoom: int(h.MaxZoom), Bounds: h.Bounds(),
	}, nil
}

// safeName keeps an object key to characters every store accepts.
func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.', r == '_':
			_, _ = b.WriteRune(r)
		default:
			_, _ = b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "build"
	}
	return b.String()
}

// maxReasonLen bounds a failure reason shown to the operator.
const maxReasonLen = 500

func failureReason(err error) string {
	msg := err.Error()
	if errors.Is(err, pmtiles.ErrNoOverlap) {
		msg = "the source archive holds nothing in this region at these zooms"
	}
	if r := []rune(msg); len(r) > maxReasonLen {
		msg = string(r[:maxReasonLen])
	}
	return msg
}

func removeObject(ctx context.Context, objs Objects, bucket, key string) {
	if err := objs.DeleteObject(context.WithoutCancel(ctx), bucket, key); err != nil {
		slog.WarnContext(ctx, "maps: removing an archive", "key", logsan.SanitizeForLog(key),
			"error", logsan.SanitizeForLog(err.Error()))
	}
}

// scanUploads records every .pmtiles file directly under the uploads prefix
// as a region named by its file name, reading each new or changed file's
// header, and forgets the uploaded regions whose file is gone.
func (s *Service) scanUploads(ctx context.Context, bucket Bucket) error {
	entries, truncated, err := bucket.Objects.ListDirectory(ctx, bucket.Name, UploadPrefix)
	if err != nil {
		return fmt.Errorf("listing uploaded archives: %w", err)
	}
	byID, err := s.regionsByID(ctx)
	if err != nil {
		return err
	}
	keep, changed, err := s.recordUploads(ctx, bucket, entries, byID)
	if err != nil {
		return err
	}
	if truncated {
		// The listing stopped short: a file past it is not gone, and
		// forgetting its region would take a working basemap away.
		if changed {
			s.d.Changed(ctx)
		}
		return errUploadsTruncated
	}
	gone, err := s.d.Regions.DeleteUploadsExcept(ctx, keep)
	if err != nil {
		return err //nolint:wrapcheck // the store's error names what it was doing
	}
	if changed || len(gone) > 0 {
		s.d.Changed(ctx)
	}
	return nil
}

// recordUploads records each listed archive that is new or changed and
// returns every listed archive's key and whether any region changed.
func (s *Service) recordUploads(ctx context.Context, bucket Bucket, entries []s3adapter.ObjectEntry,
	byID map[string]Region,
) (keep []string, changed bool, err error) {
	keep = make([]string, 0, len(entries))
	for _, e := range entries {
		id, ok := uploadID(e.Key)
		if !ok {
			continue
		}
		keep = append(keep, e.Key)
		prev, exists := byID[id]
		if !uploadChanged(prev, exists, e) {
			continue
		}
		if err := s.d.Regions.PutUpload(ctx, readUpload(ctx, bucket, id, e)); err != nil {
			return nil, false, err //nolint:wrapcheck // the store's error names what it was doing
		}
		changed = true
	}
	return keep, changed, nil
}

// regionsByID returns every region, keyed by id.
func (s *Service) regionsByID(ctx context.Context) (map[string]Region, error) {
	known, err := s.d.Regions.List(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the store's error names what it was doing
	}
	byID := make(map[string]Region, len(known))
	for _, r := range known {
		byID[r.ID] = r
	}
	return byID, nil
}

// uploadChanged reports whether an uploaded file needs its header read: it is
// new, or its size differs from what was recorded. A fetched region of the
// same name is left alone.
func uploadChanged(prev Region, exists bool, e s3adapter.ObjectEntry) bool {
	switch {
	case !exists:
		return true
	case prev.Origin == OriginFetch:
		return false
	case prev.Archive != nil:
		return prev.Archive.Key != e.Key || prev.Archive.Size != e.Size
	default:
		return prev.TotalBytes != e.Size
	}
}

// errUploadsTruncated is an uploads prefix holding more files than one
// listing returns. The files listed are recorded; none is forgotten.
var errUploadsTruncated = errors.New("maps: the uploads prefix holds more files than one listing returns; keep at most 100 archives there")

// uploadID names the region an uploaded file becomes: its file name without
// the extension, when that is a valid region id.
func uploadID(key string) (string, bool) {
	name := path.Base(key)
	if !strings.HasSuffix(name, ".pmtiles") || path.Dir(key)+"/" != UploadPrefix {
		return "", false
	}
	id := strings.TrimSuffix(name, ".pmtiles")
	if !ValidRegionID(id) {
		return "", false
	}
	return id, true
}

// readUpload reads an uploaded archive's header and metadata into the region
// it describes, or a failed region saying why it could not be read.
func readUpload(ctx context.Context, bucket Bucket, id string, e s3adapter.ObjectEntry) Region {
	r := Region{
		ID: id, Name: id, Origin: OriginUpload, State: StateFailed, TotalBytes: e.Size,
		Archive: &Archive{Bucket: bucket.Name, Key: e.Key, Size: e.Size},
	}
	head, _, err := bucket.Objects.GetObjectRange(ctx, bucket.Name, e.Key, 0, pmtiles.RootFetchLen)
	if err != nil {
		r.Error = "the file could not be read: " + failureReason(err)
		return r
	}
	h, err := pmtiles.ParseHeader(head)
	if err != nil {
		r.Error = "the file is not a PMTiles version 3 archive"
		return r
	}
	r.Archive.Build = id
	if meta, err := readMetadata(ctx, bucket, e.Key, h); err == nil {
		r.Archive.Build = buildDate(meta, id)
	}
	b := h.Bounds()
	r.State, r.Bounds, r.MaxZoom = StateReady, b, int(h.MaxZoom)
	r.Archive.MinZoom, r.Archive.MaxZoom, r.Archive.Bounds = int(h.MinZoom), int(h.MaxZoom), b
	return r
}

// maxMetadataBytes bounds the metadata read from an uploaded archive.
const maxMetadataBytes = 1 << 20

func readMetadata(ctx context.Context, bucket Bucket, key string, h pmtiles.Header) (map[string]any, error) {
	if h.MetadataLength == 0 || h.MetadataLength > maxMetadataBytes {
		return map[string]any{}, nil
	}
	raw, _, err := bucket.Objects.GetObjectRange(ctx, bucket.Name, key,
		int64(h.MetadataOffset), int64(h.MetadataLength)) // #nosec G115 -- bounded above
	if err != nil {
		return nil, fmt.Errorf("reading archive metadata: %w", err)
	}
	return pmtiles.DecodeMetadata(raw, h.InternalCompression) //nolint:wrapcheck // names what failed
}
