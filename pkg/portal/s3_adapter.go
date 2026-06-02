package portal

import (
	"context"
	"fmt"
	"io"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// s3API is the subset of the mcp-s3 *client.Client the adapter calls.
// Declaring it as an interface (satisfied by the concrete client) lets
// the adapter be exercised with an in-memory fake in tests without
// standing up a real S3 endpoint.
type s3API interface {
	PutObject(ctx context.Context, input *s3client.PutObjectInput) (*s3client.PutObjectOutput, error)
	PutObjectStream(ctx context.Context, input *s3client.PutObjectStreamInput) (*s3client.PutObjectOutput, error)
	GetObject(ctx context.Context, bucket, key string) (*s3client.ObjectContent, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	Close() error
}

// s3ClientAdapter wraps an mcp-s3 Client to implement portal.S3Client.
type s3ClientAdapter struct {
	client s3API
}

// NewS3ClientAdapter creates an S3Client backed by an mcp-s3 Client.
func NewS3ClientAdapter(client *s3client.Client) S3Client {
	return &s3ClientAdapter{client: client}
}

func (a *s3ClientAdapter) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error { //nolint:revive // interface impl
	_, err := a.client.PutObject(ctx, &s3client.PutObjectInput{
		Bucket:      bucket,
		Key:         key,
		Body:        data,
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	return nil
}

func (a *s3ClientAdapter) PutObjectStream(ctx context.Context, bucket, key string, body io.Reader, contentType string) (int64, error) { //nolint:revive // interface impl
	// Count bytes as the transfer manager pulls them so callers learn the
	// uploaded size (the manager does not report it). Callers enforce any
	// size limit by wrapping body in a reader that errors past the limit;
	// the transfer manager aborts the incomplete multipart upload on that
	// read error.
	counter := &countingReader{r: body}
	_, err := a.client.PutObjectStream(ctx, &s3client.PutObjectStreamInput{
		Bucket:      bucket,
		Key:         key,
		Body:        counter,
		ContentType: contentType,
	})
	if err != nil {
		return counter.n, fmt.Errorf("s3 put stream: %w", err)
	}
	return counter.n, nil
}

// countingReader tallies the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	read, err := c.r.Read(p)
	c.n += int64(read)
	return read, err //nolint:wrapcheck // transparent pass-through of the wrapped reader's error
}

func (a *s3ClientAdapter) GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error) { //nolint:revive // interface impl
	obj, err := a.client.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}
	return obj.Body, obj.ContentType, nil
}

func (a *s3ClientAdapter) DeleteObject(ctx context.Context, bucket, key string) error { //nolint:revive // interface impl
	if err := a.client.DeleteObject(ctx, bucket, key); err != nil {
		return fmt.Errorf("s3 delete: %w", err)
	}
	return nil
}

func (a *s3ClientAdapter) Close() error { //nolint:revive // interface impl
	if err := a.client.Close(); err != nil {
		return fmt.Errorf("closing s3 client: %w", err)
	}
	return nil
}

// Verify interface compliance.
var _ S3Client = (*s3ClientAdapter)(nil)
