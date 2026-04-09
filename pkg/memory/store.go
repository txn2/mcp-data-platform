package memory

import "context"

// Store persists and queries memory records.
type Store interface {
	// Insert creates a new memory record.
	Insert(ctx context.Context, record Record) error

	// Get retrieves a single memory record by ID.
	Get(ctx context.Context, id string) (*Record, error)

	// Update modifies fields on an existing memory record.
	Update(ctx context.Context, id string, updates RecordUpdate) error

	// Delete soft-deletes a memory record by setting status to archived.
	Delete(ctx context.Context, id string) error

	// List returns memory records matching the filter with pagination.
	List(ctx context.Context, filter Filter) ([]Record, int, error)

	// VectorSearch performs cosine similarity search over embeddings.
	VectorSearch(ctx context.Context, query VectorQuery) ([]ScoredRecord, error)

	// EntityLookup returns active memories linked to a DataHub URN.
	EntityLookup(ctx context.Context, urn string, persona string) ([]Record, error)

	// MarkStale flags memory records as stale with a reason.
	MarkStale(ctx context.Context, ids []string, reason string) error

	// MarkVerified updates the last_verified timestamp for records.
	MarkVerified(ctx context.Context, ids []string) error

	// Supersede marks an old record as superseded by a new one.
	Supersede(ctx context.Context, oldID, newID string) error
}
