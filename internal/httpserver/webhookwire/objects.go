package webhookwire

import (
	"context"
	"fmt"

	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/internal/objectobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// listPage is how many keys one listing request asks for.
const listPage = 1000

// purpose is what the webhook objects are reported as (internal/objectobs).
const purpose = observability.StoragePurposeWebhooks

// s3API is the part of the S3 client the webhook objects use.
type s3API interface {
	PutObject(ctx context.Context, input *s3client.PutObjectInput) (*s3client.PutObjectOutput, error)
	GetObject(ctx context.Context, bucket, key string) (*s3client.ObjectContent, error)
	ListObjects(ctx context.Context, bucket, prefix, delimiter string, maxKeys int32, continueToken string) (*s3client.ListObjectsOutput, error)
	DeleteObject(ctx context.Context, bucket, key string) error
}

// objects is what the receiver, the compactor and the source service do in
// the managed-resources bucket: write a segment, read one back, list a
// window's segments across every page, and delete them. Every request is
// observed under the webhooks purpose.
type objects struct {
	c s3API
}

// PutObject writes one object.
func (o objects) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error {
	_, err := objectobs.Do(ctx, purpose, objectobs.OpPut, bucket, func(ctx context.Context) (int64, error) {
		_, err := o.c.PutObject(ctx, &s3client.PutObjectInput{
			Bucket: bucket, Key: key, Body: data, ContentType: contentType,
		})
		return int64(len(data)), err //nolint:wrapcheck // wrapped below, once
	})
	if err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}
	return nil
}

// GetObject reads one object whole.
func (o objects) GetObject(ctx context.Context, bucket, key string) ([]byte, error) {
	var obj *s3client.ObjectContent
	_, err := objectobs.Do(ctx, purpose, objectobs.OpGet, bucket, func(ctx context.Context) (int64, error) {
		var err error
		obj, err = o.c.GetObject(ctx, bucket, key)
		return 0, err //nolint:wrapcheck // wrapped below, once
	})
	if err != nil {
		return nil, fmt.Errorf("s3 get: %w", err)
	}
	return obj.Body, nil
}

// ListKeys returns every key under prefix. A window of a busy source holds a
// segment per second per replica, far more than one page.
func (o objects) ListKeys(ctx context.Context, bucket, prefix string) ([]string, error) {
	var (
		keys  []string
		token string
	)
	for {
		out, err := o.listPage(ctx, bucket, prefix, token)
		if err != nil {
			return nil, err
		}
		for _, obj := range out.Objects {
			if obj.Key != prefix {
				keys = append(keys, obj.Key)
			}
		}
		if !out.IsTruncated || out.NextContinueToken == "" {
			return keys, nil
		}
		token = out.NextContinueToken
	}
}

// listPage reads one page of a listing.
func (o objects) listPage(ctx context.Context, bucket, prefix, token string) (*s3client.ListObjectsOutput, error) {
	var out *s3client.ListObjectsOutput
	_, err := objectobs.Do(ctx, purpose, objectobs.OpList, bucket, func(ctx context.Context) (int64, error) {
		var err error
		out, err = o.c.ListObjects(ctx, bucket, prefix, "", listPage, token)
		return 0, err //nolint:wrapcheck // wrapped below, once
	})
	if err != nil {
		return nil, fmt.Errorf("s3 list: %w", err)
	}
	return out, nil
}

// DeleteObject removes one object.
func (o objects) DeleteObject(ctx context.Context, bucket, key string) error {
	_, err := objectobs.Do(ctx, purpose, objectobs.OpDelete, bucket, func(ctx context.Context) (int64, error) {
		return 0, o.c.DeleteObject(ctx, bucket, key) //nolint:wrapcheck // wrapped below, once
	})
	if err != nil {
		return fmt.Errorf("s3 delete: %w", err)
	}
	return nil
}
