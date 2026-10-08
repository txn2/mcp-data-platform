package portalstore

import (
	"context"
	"io"

	"github.com/txn2/mcp-data-platform/internal/objectobs"
	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// ExportS3Client is the portal's blob backend for the export tools
// (trino_export, api_export, graphql_export): every operation is reported as
// an export (internal/objectobs). Nil wherever S3Client is nil.
func (h *Handle) ExportS3Client() portal.S3Client {
	return h.s3ClientFor(observability.StoragePurposeExports)
}

// ScriptS3Client is the portal's blob backend for a managed script's outputs:
// every operation is reported as a script output. Nil wherever S3Client is nil.
func (h *Handle) ScriptS3Client() portal.S3Client {
	return h.s3ClientFor(observability.StoragePurposeScriptOutputs)
}

// s3ClientFor returns the portal's blob backend with every operation reported
// under purpose, for a writer that stores into the portal's bucket for
// something other than an asset a person saved. Nil wherever S3Client is nil,
// so a caller's "no blob storage" check still holds.
func (h *Handle) s3ClientFor(purpose string) portal.S3Client {
	c := h.S3Client()
	if c == nil {
		return nil
	}
	return purposedS3{c: c, purpose: purpose}
}

// purposedS3 names its purpose on the context of every call it passes on.
type purposedS3 struct {
	c       portal.S3Client
	purpose string
}

func (p purposedS3) ctx(ctx context.Context) context.Context {
	return objectobs.WithPurpose(ctx, p.purpose)
}

// PutObject stores one object.
func (p purposedS3) PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error {
	return p.c.PutObject(p.ctx(ctx), bucket, key, data, contentType) //nolint:wrapcheck // a pass-through
}

// PutObjectStream streams one object.
func (p purposedS3) PutObjectStream(ctx context.Context, bucket, key string, body io.Reader, contentType string) (int64, error) {
	return p.c.PutObjectStream(p.ctx(ctx), bucket, key, body, contentType) //nolint:wrapcheck // a pass-through
}

// GetObject reads one object.
func (p purposedS3) GetObject(ctx context.Context, bucket, key string) (body []byte, contentType string, err error) {
	return p.c.GetObject(p.ctx(ctx), bucket, key) //nolint:wrapcheck // a pass-through
}

// DeleteObject removes one object.
func (p purposedS3) DeleteObject(ctx context.Context, bucket, key string) error {
	return p.c.DeleteObject(p.ctx(ctx), bucket, key) //nolint:wrapcheck // a pass-through
}

// Close is a no-op: the view does not own the client, the Handle does, and a
// consumer closing its view must not close the portal's.
func (purposedS3) Close() error { return nil }
