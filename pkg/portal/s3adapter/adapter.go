// Package s3adapter adapts an mcp-s3 client to the portal.S3Client interface
// used by the portal handler for blob storage. It lives in its own package so
// the (self-contained) adapter mechanism does not count against the portal
// package's size budget.
package s3adapter

import (
	"context"
	"fmt"
	"io"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// API is the subset of the mcp-s3 *client.Client the adapter calls.
// Declaring it as an interface (satisfied by the concrete client) lets the
// adapter be exercised with an in-memory fake in tests without standing up a
// real S3 endpoint.
type API interface {
	PutObject(ctx context.Context, input *s3client.PutObjectInput) (*s3client.PutObjectOutput, error)
	PutObjectStream(ctx context.Context, input *s3client.PutObjectStreamInput) (*s3client.PutObjectOutput, error)
	GetObject(ctx context.Context, bucket, key string) (*s3client.ObjectContent, error)
	DeleteObject(ctx context.Context, bucket, key string) error
	Close() error
}

// ClientAdapter wraps an mcp-s3 Client to satisfy portal.S3Client. It is
// returned as a concrete type; callers assign it to a portal.S3Client-typed
// field, which Go satisfies structurally (no import back into portal).
type ClientAdapter struct {
	client API
}

// New creates a ClientAdapter backed by an mcp-s3 Client.
func New(client *s3client.Client) *ClientAdapter {
	return &ClientAdapter{client: client}
}

// PutObject uploads data to the given bucket and key.
func (a *ClientAdapter) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error {
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

// PutObjectStream streams body to the given bucket and key, returning the
// number of bytes uploaded.
func (a *ClientAdapter) PutObjectStream(ctx context.Context, bucket, key string, body io.Reader, contentType string) (int64, error) {
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

// GetObject fetches the object at the given bucket and key.
func (a *ClientAdapter) GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error) {
	obj, err := a.client.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, "", fmt.Errorf("s3 get: %w", err)
	}
	return obj.Body, obj.ContentType, nil
}

// DeleteObject removes the object at the given bucket and key.
func (a *ClientAdapter) DeleteObject(ctx context.Context, bucket, key string) error {
	if err := a.client.DeleteObject(ctx, bucket, key); err != nil {
		return fmt.Errorf("s3 delete: %w", err)
	}
	return nil
}

// Close releases the underlying client.
func (a *ClientAdapter) Close() error {
	if err := a.client.Close(); err != nil {
		return fmt.Errorf("closing s3 client: %w", err)
	}
	return nil
}
