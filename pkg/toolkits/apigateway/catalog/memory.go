package catalog

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryStore is an in-memory Store implementation. Used by tests
// to avoid spinning up Postgres for table-driven cases that only
// exercise the Store contract. Safe for concurrent use.
//
// MemoryStore does NOT track connection references — calls to
// ReferencingConnections always return nil. The admin handler that
// uses ReferencingConnections to gate catalog deletion is exercised
// against the Postgres implementation in integration tests, which
// has the real cross-table query.
type MemoryStore struct {
	mu       sync.Mutex
	catalogs map[string]Catalog
	specs    map[string]map[string]SpecEntry // catalog_id -> spec_name -> entry
}

// NewMemoryStore returns an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		catalogs: make(map[string]Catalog),
		specs:    make(map[string]map[string]SpecEntry),
	}
}

// Compile-time interface check.
var _ Store = (*MemoryStore)(nil)

// CreateCatalog adds a new catalog. Returns ErrInvalidID when the
// id fails the slug check or ErrConflict when (id) or (name,
// version) is already taken.
func (s *MemoryStore) CreateCatalog(_ context.Context, c Catalog) error {
	if err := ValidateID(c.ID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.catalogs[c.ID]; exists {
		return ErrConflict
	}
	for _, existing := range s.catalogs {
		if existing.Name == c.Name && existing.Version == c.Version {
			return ErrConflict
		}
	}
	now := time.Now()
	c.CreatedAt = now
	c.UpdatedAt = now
	s.catalogs[c.ID] = c
	return nil
}

// GetCatalog returns the catalog by id or ErrNotFound.
func (s *MemoryStore) GetCatalog(_ context.Context, id string) (*Catalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.catalogs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &c, nil
}

// ListCatalogs returns every catalog sorted by (name, version, id).
func (s *MemoryStore) ListCatalogs(_ context.Context) ([]Catalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Catalog, 0, len(s.catalogs))
	for _, c := range s.catalogs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		if out[i].Version != out[j].Version {
			return out[i].Version < out[j].Version
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// UpdateCatalog applies the partial update. Returns ErrNotFound
// when id is unknown, ErrConflict when the edit would collide on
// (name, version).
func (s *MemoryStore) UpdateCatalog(_ context.Context, id string, u Update) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.catalogs[id]
	if !ok {
		return ErrNotFound
	}
	newName, newVersion := c.Name, c.Version
	if u.Name != nil {
		newName = *u.Name
	}
	if u.Version != nil {
		newVersion = *u.Version
	}
	for otherID, other := range s.catalogs {
		if otherID == id {
			continue
		}
		if other.Name == newName && other.Version == newVersion {
			return ErrConflict
		}
	}
	c.Name = newName
	c.Version = newVersion
	if u.DisplayName != nil {
		c.DisplayName = *u.DisplayName
	}
	if u.Description != nil {
		c.Description = *u.Description
	}
	c.UpdatedAt = time.Now()
	s.catalogs[id] = c
	return nil
}

// DeleteCatalog removes the catalog and all its specs (CASCADE
// behavior matches the Postgres FK).
func (s *MemoryStore) DeleteCatalog(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.catalogs[id]; !ok {
		return ErrNotFound
	}
	delete(s.catalogs, id)
	delete(s.specs, id)
	return nil
}

// UpsertSpec inserts or replaces a spec entry. Returns ErrNotFound
// when catalog_id is unknown.
func (s *MemoryStore) UpsertSpec(_ context.Context, catalogID string, spec SpecEntry) error {
	if err := ValidateSpecName(spec.SpecName); err != nil {
		return err
	}
	if err := ValidateSourceKind(spec.SourceKind); err != nil {
		return err
	}
	normalizedBasePath, err := NormalizeBasePath(spec.BasePath)
	if err != nil {
		return err
	}
	spec.BasePath = normalizedBasePath
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.catalogs[catalogID]; !ok {
		return ErrNotFound
	}
	bucket, ok := s.specs[catalogID]
	if !ok {
		bucket = make(map[string]SpecEntry)
		s.specs[catalogID] = bucket
	}
	now := time.Now()
	if existing, ok := bucket[spec.SpecName]; ok {
		spec.CreatedAt = existing.CreatedAt
	} else {
		spec.CreatedAt = now
	}
	spec.UpdatedAt = now
	bucket[spec.SpecName] = spec
	return nil
}

// GetSpec returns one spec from the catalog or ErrNotFound.
func (s *MemoryStore) GetSpec(_ context.Context, catalogID, specName string) (*SpecEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, ok := s.specs[catalogID]
	if !ok {
		return nil, ErrNotFound
	}
	spec, ok := bucket[specName]
	if !ok {
		return nil, ErrNotFound
	}
	return &spec, nil
}

// ListSpecs returns every spec in the catalog, sorted by spec name.
func (s *MemoryStore) ListSpecs(_ context.Context, catalogID string) ([]SpecEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, ok := s.specs[catalogID]
	if !ok {
		return nil, nil
	}
	out := make([]SpecEntry, 0, len(bucket))
	for _, e := range bucket {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SpecName < out[j].SpecName })
	return out, nil
}

// DeleteSpec removes one spec from the catalog.
func (s *MemoryStore) DeleteSpec(_ context.Context, catalogID, specName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, ok := s.specs[catalogID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := bucket[specName]; !ok {
		return ErrNotFound
	}
	delete(bucket, specName)
	return nil
}

// ReferencingConnections always returns nil — the in-memory store
// doesn't know about connection_instances. Use Postgres in
// production / integration tests where this matters.
func (*MemoryStore) ReferencingConnections(_ context.Context, _ string) ([]ConnectionRef, error) {
	return nil, nil
}
