package portal

import (
	"context"
	"io"
)

// S3Client abstracts the S3 operations needed by the portal toolkit.
type S3Client interface {
	PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
	// PutObjectStream uploads body to bucket/key without buffering the
	// whole payload in memory (multipart via the AWS transfer manager),
	// returning the number of bytes written. Callers that need a size
	// limit wrap body in a reader that errors past the limit; the
	// transfer manager aborts the incomplete multipart upload on that
	// read error, so no partial object or orphaned parts remain.
	PutObjectStream(ctx context.Context, bucket, key string, body io.Reader, contentType string) (size int64, err error)
	GetObject(ctx context.Context, bucket, key string) ([]byte, string, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	Close() error
}
