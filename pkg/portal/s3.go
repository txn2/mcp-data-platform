package portal

import "context"

// S3Client abstracts the S3 operations needed by the portal toolkit.
type S3Client interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	Close() error
}
