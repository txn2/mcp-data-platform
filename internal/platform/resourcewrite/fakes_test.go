package resourcewrite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
	// addRevisionErr, if set, is what the next AddRevision returns.
	addRevisionErr error
}

func newMemStore() *memStore {
	return &memStore{
		resources: map[string]*resource.Resource{},
		versions:  map[string][]resource.Version{},
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

func (m *memStore) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.resources {
		if r.URI == uri {
			copied := *r
			return &copied, nil
		}
	}
	return nil, fmt.Errorf("resource %s: %w", uri, errNoRow)
}

func (m *memStore) List(_ context.Context, _ resource.Filter) ([]resource.Resource, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]resource.Resource, 0, len(m.resources))
	for _, r := range m.resources {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, len(out), nil
}

func (*memStore) Update(_ context.Context, _ string, _ resource.Update) error { return nil }

// Move is refused: the write path under test creates and revises resources and
// never refiles one, so accepting a move would model a store this package does
// not exercise.
func (*memStore) Move(_ context.Context, _ []resource.Move) error {
	return errors.New("memStore does not move resources")
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
	// putErr, if set, is what the next PutObject returns.
	putErr error
	// deleted records the keys DeleteObject was asked to remove, so a test can
	// assert that a failed write cleaned up after itself.
	deleted []string
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
	delete(b.objects, bucket+"/"+key)
	return nil
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

func (metadataOnlyStore) PendingThumbnails(_ context.Context, _ resource.Filter, _ int) ([]resource.Resource, error) {
	return nil, nil
}

// The capture routes are not exercised here: this fake stands in for the read
// paths a memStore uses, and none of them captures or lists a thumbnail.
func (*memStore) SetThumbnail(_ context.Context, _ string, _ resource.ThumbnailCapture) error {
	return nil
}

func (*memStore) ClearThumbnail(_ context.Context, _, _ string) error { return nil }

func (*memStore) PendingThumbnails(_ context.Context, _ resource.Filter, _ int) ([]resource.Resource, error) {
	return nil, nil
}
