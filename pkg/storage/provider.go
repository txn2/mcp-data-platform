package storage

import "context"

// Provider provides storage availability context for metadata entities.
// S3 implements this. Future storage systems (GCS, Azure Blob) can too.
type Provider interface {
	// Name returns the provider name.
	Name() string

	// ResolveDataset converts a URN to a storage dataset identifier.
	ResolveDataset(ctx context.Context, urn string) (*DatasetIdentifier, error)

	// GetDatasetAvailability checks if a dataset is available in storage.
	GetDatasetAvailability(ctx context.Context, urn string) (*DatasetAvailability, error)

	// GetAccessExamples returns examples for accessing a dataset.
	GetAccessExamples(ctx context.Context, urn string) ([]AccessExample, error)

	// ListObjects lists objects in a dataset prefix.
	ListObjects(ctx context.Context, dataset DatasetIdentifier, limit int) ([]ObjectInfo, error)

	// Close releases resources.
	Close() error
}
