// Package s3 provides an S3 implementation of the storage provider.
package s3

import (
	"context"
	"fmt"
	"strings"

	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/storage"
)

// Config holds S3 adapter configuration.
type Config struct {
	Region         string
	Endpoint       string
	AccessKeyID    string
	SecretKey      string
	BucketPrefix   string
	ConnectionName string
	ReadOnly       bool
}

// S3Client defines the interface for S3 operations used by the adapter.
// This interface allows for mocking in tests.
type S3Client interface {
	ListObjects(ctx context.Context, bucket, prefix, delimiter string, maxKeys int32, continueToken string) (*s3client.ListObjectsOutput, error)
	Close() error
}

// Adapter implements storage.Provider using S3.
type Adapter struct {
	cfg    Config
	client S3Client
}

// New creates a new S3 adapter with an existing client.
func New(cfg Config, client S3Client) (*Adapter, error) {
	if client == nil {
		return nil, fmt.Errorf("s3 client is required")
	}
	return &Adapter{
		cfg:    cfg,
		client: client,
	}, nil
}

// NewFromConfig creates a new S3 adapter with a new client from config.
func NewFromConfig(cfg Config) (*Adapter, error) {
	clientCfg := &s3client.Config{
		Region:          cfg.Region,
		Endpoint:        cfg.Endpoint,
		AccessKeyID:     cfg.AccessKeyID,
		SecretAccessKey: cfg.SecretKey,
		Name:            cfg.ConnectionName,
	}

	client, err := s3client.New(context.Background(), clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating s3 client: %w", err)
	}

	return &Adapter{
		cfg:    cfg,
		client: client,
	}, nil
}

// Name returns the provider name.
func (a *Adapter) Name() string {
	return "s3"
}

// ResolveDataset converts a URN to an S3 dataset identifier.
func (a *Adapter) ResolveDataset(_ context.Context, urn string) (*storage.DatasetIdentifier, error) {
	// Parse URN format: urn:li:dataset:(urn:li:dataPlatform:s3,bucket/prefix,PROD)
	if !strings.HasPrefix(urn, "urn:li:dataset:") {
		return nil, fmt.Errorf("invalid dataset URN: %s", urn)
	}

	// Extract the name part (bucket/prefix)
	start := strings.Index(urn, ",")
	end := strings.LastIndex(urn, ",")
	if start == -1 || end == -1 || start == end {
		return nil, fmt.Errorf("invalid URN format: %s", urn)
	}

	path := urn[start+1 : end]
	parts := strings.SplitN(path, "/", 2)

	dataset := &storage.DatasetIdentifier{
		Bucket:     parts[0],
		Connection: a.cfg.ConnectionName,
	}
	if len(parts) > 1 {
		dataset.Prefix = parts[1]
	}

	return dataset, nil
}

// GetDatasetAvailability checks if a dataset is available in S3.
func (a *Adapter) GetDatasetAvailability(ctx context.Context, urn string) (*storage.DatasetAvailability, error) {
	dataset, err := a.ResolveDataset(ctx, urn)
	if err != nil {
		return &storage.DatasetAvailability{
			Available: false,
			Error:     err.Error(),
		}, nil
	}

	// List objects to verify the dataset exists and get stats
	result, err := a.client.ListObjects(ctx, dataset.Bucket, dataset.Prefix, "", 1000, "")
	if err != nil {
		return &storage.DatasetAvailability{
			Available: false,
			Error:     err.Error(),
		}, nil
	}

	// Calculate totals
	var totalSize int64
	for _, obj := range result.Objects {
		totalSize += obj.Size
	}

	return &storage.DatasetAvailability{
		Available:   true,
		Bucket:      dataset.Bucket,
		Prefix:      dataset.Prefix,
		Connection:  a.cfg.ConnectionName,
		ObjectCount: int64(result.KeyCount),
		TotalSize:   totalSize,
	}, nil
}

// GetAccessExamples returns examples for accessing an S3 dataset.
func (a *Adapter) GetAccessExamples(ctx context.Context, urn string) ([]storage.AccessExample, error) {
	dataset, err := a.ResolveDataset(ctx, urn)
	if err != nil {
		return nil, err
	}

	s3Path := "s3://" + dataset.Bucket
	if dataset.Prefix != "" {
		s3Path += "/" + dataset.Prefix
	}

	return []storage.AccessExample{
		{
			Description: "List objects using AWS CLI",
			Command:     fmt.Sprintf("aws s3 ls %s/", s3Path),
		},
		{
			Description: "Sync to local directory",
			Command:     fmt.Sprintf("aws s3 sync %s ./local-dir", s3Path),
		},
		{
			Description: "Copy single file",
			Command:     fmt.Sprintf("aws s3 cp %s/filename.ext ./", s3Path),
		},
	}, nil
}

// ListObjects lists objects in an S3 dataset prefix.
func (a *Adapter) ListObjects(ctx context.Context, dataset storage.DatasetIdentifier, limit int) ([]storage.ObjectInfo, error) {
	if limit <= 0 {
		limit = 100
	}
	// S3 API limits maxKeys to 1000, ensure we stay within int32 bounds
	const maxLimit = 1000
	if limit > maxLimit {
		limit = maxLimit
	}

	result, err := a.client.ListObjects(ctx, dataset.Bucket, dataset.Prefix, "", int32(limit), "") // #nosec G115 -- bounds checked above
	if err != nil {
		return nil, fmt.Errorf("listing objects: %w", err)
	}

	objects := make([]storage.ObjectInfo, 0, len(result.Objects))
	for _, obj := range result.Objects {
		info := storage.ObjectInfo{
			Key:          obj.Key,
			Bucket:       dataset.Bucket,
			Size:         obj.Size,
			LastModified: &obj.LastModified,
		}
		objects = append(objects, info)
	}

	return objects, nil
}

// Close releases resources.
func (a *Adapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

// Verify interface compliance.
var _ storage.Provider = (*Adapter)(nil)
