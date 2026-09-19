package resourcewrite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sort"
	"sync"
	"time"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// memStore is an in-memory managed-resource store that also keeps the content
// revision trail, which is what a Postgres deployment has: one store satisfying
// both interfaces. Modeling them as one object is not a convenience -- the
// writer type-asserts Store for VersionStore to decide whether content can be
// replaced at all, and two separate fakes would make that assertion untestable.
type memStore struct {
	mu        sync.Mutex
	resources map[string]*resource.Resource
	versions  map[string][]resource.Version
	// insertErr, if set, is what the next Insert returns.
	insertErr error
	// getErr, if set, is what Get returns for any id.
	getErr error
	// getByURIErr, if set, is what GetByURI returns for any address. It is
	// separate from getErr because the two reads answer different questions and
	// a caller acts differently on each failing.
	getByURIErr error
	// addRevisionErr, if set, is what the next AddRevision returns.
	addRevisionErr error
	// listErr, if set, is what List returns: a store that could not answer a
	// listing.
	listErr error
	// listVersionsErr, if set, is what ListVersions returns, which is the
	// version trail a delete reads to reclaim superseded blobs.
	listVersionsErr error
	// aliases maps every address a resource has vacated by being moved to the
	// resource that vacated it, which is what the Postgres store records inside
	// a move's transaction (resource_uri_aliases). GetByURI consults it after a
	// live miss, so a citation written before a move keeps resolving; without it
	// here a test would assert an answer production does not give.
	aliases map[string]string
}

func newMemStore() *memStore {
	return &memStore{
		resources: map[string]*resource.Resource{},
		versions:  map[string][]resource.Version{},
		aliases:   map[string]string{},
	}
}

func (m *memStore) Insert(_ context.Context, r resource.Resource) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.insertErr != nil {
		return m.insertErr
	}
	for _, existing := range m.resources {
		if existing.URI == r.URI {
			return errors.New("duplicate key value violates unique constraint")
		}
	}
	now := time.Now().UTC()
	r.CreatedAt, r.UpdatedAt = now, now
	stored := r
	m.resources[r.ID] = &stored
	return nil
}

func (m *memStore) Get(_ context.Context, id string) (*resource.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return nil, m.getErr
	}
	r, ok := m.resources[id]
	if !ok {
		return nil, fmt.Errorf("resource %s: %w", id, errNoRow)
	}
	copied := *r
	return &copied, nil
}

// errNoRow models what the Postgres store answers for a missing row: a wrapped
// sql.ErrNoRows, which is what resource.IsNotFound tests for. A fake that
// returned a bare error would make every missing row indistinguishable from a
// store that could not answer -- the exact distinction the writer draws.
var errNoRow = fmt.Errorf("resource not found: %w", sql.ErrNoRows)

func (m *memStore) GetByIDs(_ context.Context, ids []string) (map[string]*resource.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]*resource.Resource{}
	for _, id := range ids {
		if r, ok := m.resources[id]; ok {
			copied := *r
			out[id] = &copied
		}
	}
	return out, nil
}

// GetByURI answers by live address first and by the alias trail second, which is
// the order the Postgres store reads them in: whoever holds an address now wins,
// and a vacated address still reaches the file that left it.
func (m *memStore) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getByURIErr != nil {
		return nil, m.getByURIErr
	}
	for _, r := range m.resources {
		if r.URI == uri {
			copied := *r
			return &copied, nil
		}
	}
	if id, aliased := m.aliases[uri]; aliased {
		if r, ok := m.resources[id]; ok {
			copied := *r
			return &copied, nil
		}
	}
	return nil, fmt.Errorf("resource %s: %w", uri, errNoRow)
}

// List applies the filter the way the Postgres store's WHERE clause does: the
// visible libraries, the folder prefix, then the page, with the total counted
// before the page is cut.
//
// The filter is honored rather than ignored because it is the whole of what the
// listing path decides: a fake that answered every row would let a test pass
// while the caller was being shown another library's files.
func (m *memStore) List(_ context.Context, f resource.Filter) ([]resource.Resource, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listErr != nil {
		return nil, 0, m.listErr
	}
	out := make([]resource.Resource, 0, len(m.resources))
	for _, r := range m.resources {
		if !f.AllScopes && !visibleIn(f.Scopes, r) {
			continue
		}
		if !resource.PathUnder(r.Path, f.Path) {
			continue
		}
		out = append(out, *r)
	}
	// Newest first, the store's default ordering; the id breaks a tie so the
	// order is stable across runs rather than map-order.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	total := len(out)
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return []resource.Resource{}, total, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && len(out) > f.Limit {
		out = out[:f.Limit]
	}
	return out, total, nil
}

// visibleIn reports whether a resource is in one of the libraries a listing was
// narrowed to.
func visibleIn(scopes []resource.ScopeFilter, r *resource.Resource) bool {
	for _, s := range scopes {
		if s.Scope != r.Scope {
			continue
		}
		if s.Scope == resource.ScopeGlobal || s.ScopeID == r.ScopeID {
			return true
		}
	}
	return false
}

func (*memStore) Update(_ context.Context, _ string, _ resource.Update) error { return nil }

// Move refiles the resources named, the way the Postgres store does in one
// transaction: the address is rewritten along with the library and the folder,
// and an address another row already holds is a conflict rather than a
// silently-shared URI.
//
// The uploader columns are deliberately untouched, which is the property #1576
// turns on: a move rewrites where the file is filed and never who filed it, so
// the row a run is later judged against still names the person who uploaded it.
func (m *memStore) Move(_ context.Context, moves []resource.Move) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Resolved and checked in full before anything is written, because the
	// Postgres store does the batch in one transaction: a fake that mutated the
	// first rows and then reported the batch failed would let a test observe a
	// half-applied move the real store cannot produce.
	//
	// Addresses are compared against the rows the batch does NOT touch. The real
	// store parks every moved row's address first so a batch can shuffle
	// addresses among its own members (renaming a/b to a); that only arises for
	// a folder rename, which this package does not perform, and it is modeled
	// here as "a member of the batch is not an occupant" rather than left as a
	// difference that would refuse what production accepts.
	moving := make(map[string]bool, len(moves))
	for _, mv := range moves {
		moving[mv.ID] = true
	}
	staged := make([]*resource.Resource, 0, len(moves))
	for _, mv := range moves {
		r, ok := m.resources[mv.ID]
		if !ok {
			return fmt.Errorf("resource not found: %s", mv.ID)
		}
		for id, other := range m.resources {
			if !moving[id] && other.URI == mv.URI {
				return resource.ErrURIConflict
			}
		}
		staged = append(staged, r)
	}
	for i, mv := range moves {
		r := staged[i]
		// The vacated address becomes an alias of the resource that left it, and
		// the address it now holds stops being an alias of anything: both halves
		// of what the Postgres store does inside the move's transaction.
		if r.URI != "" && r.URI != mv.URI {
			m.aliases[r.URI] = mv.ID
		}
		delete(m.aliases, mv.URI)
		r.Scope, r.ScopeID, r.Path, r.URI = mv.Scope, mv.ScopeID, mv.Path, mv.URI
	}
	return nil
}

func (m *memStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.resources, id)
	return nil
}

// AddRevision records the revision and moves the resource head onto its blob,
// the way the Postgres store does in one transaction. A fake that recorded the
// row without moving the head would let a test pass while the resource still
// served its old bytes.
func (m *memStore) AddRevision(_ context.Context, rev resource.Revision) (*resource.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.addRevisionErr != nil {
		return nil, m.addRevisionErr
	}
	r, ok := m.resources[rev.ResourceID]
	if !ok {
		return nil, fmt.Errorf("resource %s: %w", rev.ResourceID, errNoRow)
	}
	v := resource.Version{
		ResourceID: rev.ResourceID, Version: len(m.versions[rev.ResourceID]) + 1,
		MIMEType: rev.MIMEType, SizeBytes: rev.SizeBytes, S3Key: rev.S3Key,
		UploaderSub: rev.UploaderSub, UploaderEmail: rev.UploaderEmail,
		RestoredFrom: rev.RestoredFrom, ChangeSummary: rev.ChangeSummary,
		CreatedAt: time.Now().UTC(),
	}
	m.versions[rev.ResourceID] = append(m.versions[rev.ResourceID], v)
	r.S3Key, r.MIMEType, r.SizeBytes = rev.S3Key, rev.MIMEType, rev.SizeBytes
	r.UpdatedAt = v.CreatedAt
	return &v, nil
}

func (m *memStore) ListVersions(_ context.Context, resourceID string) ([]resource.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.listVersionsErr != nil {
		return nil, m.listVersionsErr
	}
	stored := m.versions[resourceID]
	out := make([]resource.Version, len(stored))
	copy(out, stored)
	sort.Slice(out, func(i, j int) bool { return out[i].Version > out[j].Version })
	return out, nil
}

func (m *memStore) GetVersion(_ context.Context, resourceID string, version int) (*resource.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.versions[resourceID] {
		if v.Version == version {
			copied := v
			return &copied, nil
		}
	}
	return nil, fmt.Errorf("version %d: %w", version, errNoRow)
}

func (m *memStore) PruneVersions(_ context.Context, resourceID string, keep int) ([]resource.Version, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	stored := m.versions[resourceID]
	if keep <= 0 || len(stored) <= keep {
		return nil, nil
	}
	pruned := stored[:len(stored)-keep]
	m.versions[resourceID] = stored[len(stored)-keep:]
	return pruned, nil
}

// metadataOnlyStore is a resource store with no revision trail, which is what a
// deployment whose store does not implement VersionStore has. The methods are
// written out rather than promoted from an embedded memStore, because
// embedding would promote AddRevision too and the store would satisfy
// VersionStore after all -- the exact assertion under test.
type metadataOnlyStore struct{ inner *memStore }

func (s metadataOnlyStore) Insert(ctx context.Context, r resource.Resource) error {
	return s.inner.Insert(ctx, r)
}

func (s metadataOnlyStore) Get(ctx context.Context, id string) (*resource.Resource, error) {
	return s.inner.Get(ctx, id)
}

func (s metadataOnlyStore) GetByIDs(ctx context.Context, ids []string) (map[string]*resource.Resource, error) {
	return s.inner.GetByIDs(ctx, ids)
}

func (s metadataOnlyStore) GetByURI(ctx context.Context, uri string) (*resource.Resource, error) {
	return s.inner.GetByURI(ctx, uri)
}

func (s metadataOnlyStore) List(ctx context.Context, f resource.Filter) ([]resource.Resource, int, error) {
	return s.inner.List(ctx, f)
}

func (s metadataOnlyStore) Update(ctx context.Context, id string, u resource.Update) error {
	return s.inner.Update(ctx, id, u)
}

// Move is refused for the same reason Update is delegated: this store models a
// deployment without version support, not one that refiles resources.
func (metadataOnlyStore) Move(context.Context, []resource.Move) error {
	return errors.New("metadataOnlyStore does not move resources")
}

func (s metadataOnlyStore) Delete(ctx context.Context, id string) error {
	return s.inner.Delete(ctx, id)
}

// memBlobs is an in-memory blob backend.
type memBlobs struct {
	mu      sync.Mutex
	objects map[string][]byte
	// putErr, if set, is what the next write returns, streaming or not.
	putErr error
	// deleted records the keys DeleteObject was asked to remove, so a test can
	// assert that a failed write cleaned up after itself.
	deleted []string
	// deleteErr, if set, is what DeleteObject returns, which is a storage
	// backend that will not let go of an object.
	deleteErr error
}

func newMemBlobs() *memBlobs { return &memBlobs{objects: map[string][]byte{}} }

func (b *memBlobs) PutObject(_ context.Context, bucket, key string, data []byte, _ string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.putErr != nil {
		return b.putErr
	}
	stored := make([]byte, len(data))
	copy(stored, data)
	b.objects[bucket+"/"+key] = stored
	return nil
}

func (b *memBlobs) GetObject(_ context.Context, bucket, key string) (body []byte, contentType string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[bucket+"/"+key]
	if !ok {
		return nil, "", errors.New("NoSuchKey")
	}
	return data, "", nil
}

func (b *memBlobs) DeleteObject(_ context.Context, bucket, key string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.deleted = append(b.deleted, bucket+"/"+key)
	if b.deleteErr != nil {
		return b.deleteErr
	}
	delete(b.objects, bucket+"/"+key)
	return nil
}

// PutObjectStream models the real client's streaming write: it draws the
// reader to its end, keeps what it read, and reports that count, so a caller
// that never streams the body cannot pass (#1631).
func (b *memBlobs) PutObjectStream(
	_ context.Context, bucket, key string, body io.Reader, _ string,
) (int64, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("reading the streamed body: %w", err)
	}
	if b.putErr != nil {
		return 0, b.putErr
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[bucket+"/"+key] = data
	return int64(len(data)), nil
}

var (
	_ resource.Store        = metadataOnlyStore{}
	_ resource.Store        = (*memStore)(nil)
	_ resource.VersionStore = (*memStore)(nil)
	_ resource.S3Client     = (*memBlobs)(nil)
)

// Folders is not exercised here: this fake stands in for the read paths a
// metadataOnlyStore uses, and none of them lists a library's folders.
func (metadataOnlyStore) Folders(_ context.Context, _ resource.Filter) ([]resource.Folder, error) {
	return nil, nil
}

// Folders is not exercised here: this fake stands in for the read paths a
// memStore uses, and none of them lists a library's folders.
func (*memStore) Folders(_ context.Context, _ resource.Filter) ([]resource.Folder, error) {
	return nil, nil
}

// Tags is not exercised here: this fake stands in for the read paths a
// metadataOnlyStore uses, and none of them lists a library's tags.
func (metadataOnlyStore) Tags(_ context.Context, _ resource.Filter) ([]string, error) {
	return nil, nil
}

// Tags is not exercised here: this fake stands in for the read paths a
// memStore uses, and none of them lists a library's tags.
func (*memStore) Tags(_ context.Context, _ resource.Filter) ([]string, error) {
	return nil, nil
}

// The capture routes are not exercised here: this fake stands in for the read
// paths a metadataOnlyStore uses, and none of them captures or lists a thumbnail.
func (metadataOnlyStore) SetThumbnail(_ context.Context, _ string, _ resource.ThumbnailCapture) error {
	return nil
}

func (metadataOnlyStore) ClearThumbnail(_ context.Context, _, _ string) error { return nil }

// The capture routes are not exercised here: this fake stands in for the read
// paths a memStore uses, and none of them captures or lists a thumbnail.
func (*memStore) SetThumbnail(_ context.Context, _ string, _ resource.ThumbnailCapture) error {
	return nil
}

func (*memStore) ClearThumbnail(_ context.Context, _, _ string) error { return nil }
