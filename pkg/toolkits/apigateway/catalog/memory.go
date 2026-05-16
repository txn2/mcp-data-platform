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
	// embeddings is keyed catalog_id -> spec_name -> operation_id ->
	// row. Three levels because the embeddings table's primary key
	// is (catalog_id, spec_name, operation_id) and the toolkit's
	// read path filters by the first two.
	embeddings map[string]map[string]map[string]OperationEmbedding
}

// NewMemoryStore returns an empty in-memory Store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		catalogs:   make(map[string]Catalog),
		specs:      make(map[string]map[string]SpecEntry),
		embeddings: make(map[string]map[string]map[string]OperationEmbedding),
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
// behavior matches the Postgres FK). Operation embeddings keyed
// on (catalog_id, spec_name) are dropped at the same time so the
// in-memory backend matches the Postgres ON DELETE CASCADE chain
// (api_catalogs → api_catalog_specs → api_catalog_operation_embeddings).
func (s *MemoryStore) DeleteCatalog(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.catalogs[id]; !ok {
		return ErrNotFound
	}
	delete(s.catalogs, id)
	delete(s.specs, id)
	delete(s.embeddings, id)
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

// DeleteSpec removes one spec from the catalog. Associated
// embedding rows are dropped at the same time so the in-memory
// backend mirrors the Postgres FK CASCADE from api_catalog_specs
// down to api_catalog_operation_embeddings.
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
	if specBucket, ok := s.embeddings[catalogID]; ok {
		delete(specBucket, specName)
	}
	return nil
}

// ReferencingConnections always returns nil — the in-memory store
// doesn't know about connection_instances. Use Postgres in
// production / integration tests where this matters.
func (*MemoryStore) ReferencingConnections(_ context.Context, _ string) ([]ConnectionRef, error) {
	return nil, nil
}

// UpsertOperationEmbeddings replaces every embedding row for the
// given (catalogID, specName) pair. The MemoryStore mirrors the
// Postgres backend's all-or-nothing semantics by clearing the
// existing rows before writing the new set.
func (s *MemoryStore) UpsertOperationEmbeddings(_ context.Context, catalogID, specName string, rows []OperationEmbedding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.specs[catalogID]; !ok {
		return ErrNotFound
	}
	if _, ok := s.specs[catalogID][specName]; !ok {
		return ErrNotFound
	}
	specBucket, ok := s.embeddings[catalogID]
	if !ok {
		specBucket = make(map[string]map[string]OperationEmbedding)
		s.embeddings[catalogID] = specBucket
	}
	bucket := make(map[string]OperationEmbedding, len(rows))
	for _, r := range rows {
		bucket[r.OperationID] = cloneEmbeddingRow(r)
	}
	specBucket[specName] = bucket
	return nil
}

// ListOperationEmbeddings returns every embedding row for the
// (catalogID, specName) pair. Empty slice (not error) when the
// spec has no vectors yet.
func (s *MemoryStore) ListOperationEmbeddings(_ context.Context, catalogID, specName string) ([]OperationEmbedding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	specBucket, ok := s.embeddings[catalogID]
	if !ok {
		return nil, nil
	}
	bucket, ok := specBucket[specName]
	if !ok {
		return nil, nil
	}
	out := make([]OperationEmbedding, 0, len(bucket))
	for _, r := range bucket {
		out = append(out, cloneEmbeddingRow(r))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OperationID < out[j].OperationID })
	return out, nil
}

// SetOperationCount updates the operation_count on one spec row.
// Returns ErrNotFound when (catalogID, specName) does not exist.
// Mirrors the Postgres backend's behavior so the embedjobs
// worker tests can run against either store.
func (s *MemoryStore) SetOperationCount(_ context.Context, catalogID, specName string, count int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	bucket, ok := s.specs[catalogID]
	if !ok {
		return ErrNotFound
	}
	spec, ok := bucket[specName]
	if !ok {
		return ErrNotFound
	}
	spec.OperationCount = count
	bucket[specName] = spec
	return nil
}

// DeleteOperationEmbeddings removes every embedding row for the
// (catalogID, specName) pair. No-op when no rows exist.
func (s *MemoryStore) DeleteOperationEmbeddings(_ context.Context, catalogID, specName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	specBucket, ok := s.embeddings[catalogID]
	if !ok {
		return nil
	}
	delete(specBucket, specName)
	return nil
}

// cloneEmbeddingRow returns a deep copy. The embedding and hash
// slices alias caller-owned memory otherwise, which would let a
// later mutation of the returned slice corrupt the store's
// internal state (and vice-versa).
func cloneEmbeddingRow(r OperationEmbedding) OperationEmbedding {
	hash := make([]byte, len(r.TextHash))
	copy(hash, r.TextHash)
	vec := make([]float32, len(r.Embedding))
	copy(vec, r.Embedding)
	return OperationEmbedding{
		OperationID: r.OperationID,
		TextHash:    hash,
		Embedding:   vec,
		Model:       r.Model,
		Dim:         r.Dim,
	}
}
