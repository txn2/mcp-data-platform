package portal

import (
	"context"
	"fmt"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// s3ClientAdapter wraps an mcp-s3 Client to implement portal.S3Client.
type s3ClientAdapter struct {
	client *s3client.Client
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
