package graphql

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
)

// flatIntrospectionResult is what an endpoint answers the introspection
// query with. Hand-written rather than captured from a vendor: what is
// being exercised is the platform reading what the specification
// defines, not one server's dialect.
const flatIntrospectionResult = `{
  "data": {
    "__schema": {
      "queryType": {"name": "Query"},
      "mutationType": {"name": "Mutation"},
      "subscriptionType": null,
      "types": [
        {"kind": "OBJECT", "name": "Query", "fields": [
          {"name": "dataset", "description": "Read one dataset.",
           "args": [{"name": "urn", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}}],
           "type": {"kind": "OBJECT", "name": "Dataset"}, "isDeprecated": false}
        ]},
        {"kind": "OBJECT", "name": "Mutation", "fields": [
          {"name": "retire", "args": [{"name": "urn", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}}],
           "type": {"kind": "SCALAR", "name": "Boolean"}, "isDeprecated": false}
        ]},
        {"kind": "OBJECT", "name": "Dataset", "fields": [
          {"name": "urn", "type": {"kind": "NON_NULL", "ofType": {"kind": "SCALAR", "name": "String"}}, "isDeprecated": false},
          {"name": "name", "type": {"kind": "SCALAR", "name": "String"}, "isDeprecated": false}
        ]}
      ],
      "directives": []
    }
  }
}`

// memorySchemaStore is a SchemaStore held in memory: enough to prove the
// toolkit stores what it read and reads back what it stored, without a
// database. The real store's SQL is covered by its own package.
type memorySchemaStore struct {
	mu       sync.Mutex
	schemas  map[string]StoredSchema
	putErr   error
	getErr   error
	putCalls int
}

func newMemorySchemaStore() *memorySchemaStore {
	return &memorySchemaStore{schemas: map[string]StoredSchema{}}
}

func (m *memorySchemaStore) GetSchema(_ context.Context, connection string) (StoredSchema, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.getErr != nil {
		return StoredSchema{}, m.getErr
	}
	s, ok := m.schemas[connection]
	if !ok {
		return StoredSchema{}, fmt.Errorf("connection %s: %w", connection, ErrSchemaNotFound)
	}
	return s, nil
}

func (m *memorySchemaStore) PutSchema(_ context.Context, s StoredSchema) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putCalls++
	if m.putErr != nil {
		return m.putErr
	}
	m.schemas[s.Connection] = s
	return nil
}

func (m *memorySchemaStore) DeleteSchema(_ context.Context, connection string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.schemas, connection)
	return nil
}

// memoryVectors is a VectorReader over a map.
type memoryVectors struct {
	vectors map[string][]float32
	err     error
}

func (m memoryVectors) LoadVectors(context.Context, string, string) (map[string][]float32, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.vectors, nil
}

// wordEmbedder is a deterministic embedder: each distinct lowercased
// word gets a fixed unit vector and a text embeds to their normalized
// average. Crude, but enough for "these two texts are closer than those
// two", which is all the ranking asks of it.
type wordEmbedder struct {
	dim int
	err error
	// zero makes the provider answer with an all-zero vector, which is
	// what a provider that cannot reach its model returns.
	zero bool
}

func (w wordEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	if w.err != nil {
		return nil, w.err
	}
	if w.zero {
		return make([]float32, w.dim), nil
	}
	return wordVector(text, w.dim), nil
}

func (w wordEmbedder) EmbedBatch(ctx context.Context, texts []string) (vectors [][]float32, err error) {
	out := make([][]float32, 0, len(texts))
	for _, t := range texts {
		vec, err := w.Embed(ctx, t)
		if err != nil {
			return nil, err
		}
		out = append(out, vec)
	}
	return out, nil
}

func (w wordEmbedder) Dimension() int { return w.dim }
func (wordEmbedder) Kind() string     { return "test" }

var _ embedding.Provider = wordEmbedder{}

// wordVector hashes each word of a text into one dimension and returns
// the L2-normalized sum, so texts sharing words point the same way.
func wordVector(text string, dim int) []float32 {
	if dim <= 0 {
		dim = 8
	}
	vec := make([]float32, dim)
	word := 0
	for _, r := range text {
		if r == ' ' || r == '\n' || r == '.' || r == ':' {
			word = 0
			continue
		}
		word = word*31 + int(r)
		vec[((word%dim)+dim)%dim]++
	}
	var norm float32
	for _, v := range vec {
		norm += v * v
	}
	if norm == 0 {
		return vec
	}
	scale := float32(1) / sqrt32(norm)
	for i := range vec {
		vec[i] *= scale
	}
	return vec
}

func sqrt32(v float32) float32 {
	x := v
	for range 20 {
		x = (x + v/x) / 2
	}
	return x
}

// fakeAssets is an export asset store that records what it was handed.
type fakeAssets struct {
	mu         sync.Mutex
	inserted   []ExportAsset
	versions   []ExportVersion
	existing   *ExportAssetRef
	insertErr  error
	lookupErr  error
	shareURL   string
	shareErr   error
	shareCalls int
}

func (f *fakeAssets) InsertExportAsset(_ context.Context, a ExportAsset) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertErr != nil {
		return f.insertErr
	}
	f.inserted = append(f.inserted, a)
	return nil
}

func (f *fakeAssets) GetByIdempotencyKey(context.Context, string, string) (ref *ExportAssetRef, err error) {
	if f.lookupErr != nil {
		return nil, f.lookupErr
	}
	return f.existing, nil
}

func (f *fakeAssets) CreateExportVersion(_ context.Context, v ExportVersion) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versions = append(f.versions, v)
	return len(f.versions), nil
}

func (f *fakeAssets) CreatePublicShare(context.Context, string, string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shareCalls++
	if f.shareErr != nil {
		return "", f.shareErr
	}
	return f.shareURL, nil
}

// errNotOneObject reports a test that expected exactly one stored object.
var errNotOneObject = errors.New("want exactly one stored object")

// fakeBlobs is an object store that keeps what was written.
type fakeBlobs struct {
	mu      sync.Mutex
	objects map[string][]byte
	err     error
}

func newFakeBlobs() *fakeBlobs { return &fakeBlobs{objects: map[string][]byte{}} }

func (f *fakeBlobs) PutObjectStream(_ context.Context, bucket, key string, body io.Reader, _ string) (size int64, err error) {
	if f.err != nil {
		return 0, f.err
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		return 0, fmt.Errorf("fake blobs: reading the body: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[bucket+"/"+key] = raw
	return int64(len(raw)), nil
}

// only returns the single stored object, failing when there is not
// exactly one.
func (f *fakeBlobs) only() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.objects) != 1 {
		return nil, errNotOneObject
	}
	for _, v := range f.objects {
		return v, nil
	}
	return nil, errNotOneObject
}

// allowPolicy allows everything and records what it was asked.
type allowPolicy struct {
	mu    sync.Mutex
	asked [][3]string
}

func (p *allowPolicy) Allow(_ context.Context, connection, method, path, _ string) (allowed bool, reason string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.asked = append(p.asked, [3]string{connection, method, path})
	return true, ""
}

// denyPolicy refuses whatever its predicate matches.
type denyPolicy struct {
	deny func(method, path string) bool
}

func (p denyPolicy) Allow(_ context.Context, _, method, path, _ string) (allowed bool, reason string) {
	if p.deny(method, path) {
		return false, "denied by test policy"
	}
	return true, ""
}
