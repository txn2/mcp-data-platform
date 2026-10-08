// Package objectobs observes the object operations the platform makes on the
// buckets it owns (#1896): portal assets, managed resources, thumbnails, a
// script's outputs, exports and webhook segments. Every put, get, list and
// delete is a client span named storage.<operation> and one
// storage_operations_total observation labeled by what the bucket is used for
// (its purpose), with a failure read into a reason class, so a credential or
// bucket-policy mistake reads differently from an outage.
//
// The adapters that hold an S3 client (pkg/portal/s3adapter and the webhook
// objects) call Do around each operation with the purpose they were built
// for. A caller that writes for another purpose through a shared client -- the
// thumbnail worker through the portal's client, an export through it -- names
// that purpose on the context with WithPurpose, and the operation is reported
// under it.
package objectobs

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/aws/smithy-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// The operations an object adapter reports.
const (
	OpPut      = "put"
	OpGet      = "get"
	OpGetRange = "get_range"
	OpList     = "list"
	OpDelete   = "delete"
)

// Span attribute keys. The purpose and operation are the metric's labels; the
// bucket is on the span only, since a deployment names it.
const (
	attrPurpose   = "storage.purpose"
	attrOperation = "storage.operation"
	attrBucket    = "storage.bucket"
	attrReason    = "storage.failure_reason"
)

type purposeKey struct{}

// WithPurpose names the purpose every object operation under ctx is reported
// under, whatever the client it runs through was built for.
func WithPurpose(ctx context.Context, purpose string) context.Context {
	return context.WithValue(ctx, purposeKey{}, purpose)
}

// purposeFrom is the purpose ctx names, or fallback.
func purposeFrom(ctx context.Context, fallback string) string {
	if p, ok := ctx.Value(purposeKey{}).(string); ok && p != "" {
		return p
	}
	return fallback
}

// Do runs one object operation under a client span and records it. fn returns
// the bytes it stored (zero for anything but a put) and its error, which Do
// returns unchanged.
func Do(ctx context.Context, fallback, op, bucket string, fn func(context.Context) (int64, error)) (int64, error) {
	purpose := purposeFrom(ctx, fallback)
	ctx, span := observability.ChildSpan(ctx, "storage."+op,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String(attrPurpose, purpose),
			attribute.String(attrOperation, op),
			attribute.String(attrBucket, bucket),
		))
	start := time.Now()
	written, err := fn(ctx)
	reason := Reason(err)
	outbound.Metrics().RecordStorageOperation(ctx, observability.StorageOperation{
		Purpose: purpose, Operation: op, Reason: reason, Duration: time.Since(start), Written: written,
	})
	status := observability.StatusOK
	if err != nil {
		status = observability.StatusUpstreamErr
		span.SetAttributes(attribute.String(attrReason, reason))
	}
	observability.SetSpanStatus(span, status, err)
	span.End()
	return written, err
}

// Reason classifies a failed object operation: access_denied for a refused
// credential or policy, bucket_missing, quota_exceeded for a store that is
// full, not_found for an absent object, and other for everything else (a
// timeout, a refused connection, a server error). Empty for nil.
//
// The S3 error code is read when the error chain carries one; the mcp-s3
// client does not always wrap with %w, so the code is also looked for in the
// error text, where the SDK writes it.
func Reason(err error) string {
	if err == nil {
		return ""
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		if r := reasonForCode(apiErr.ErrorCode()); r != "" {
			return r
		}
	}
	msg := strings.ToLower(err.Error())
	for _, c := range codeReasons {
		if strings.Contains(msg, strings.ToLower(c.code)) {
			return c.reason
		}
	}
	// The SDK writes "StatusCode: 403"; other layers "status code: 403".
	msg = strings.ReplaceAll(msg, "status code", "statuscode")
	switch {
	case strings.Contains(msg, "statuscode: 403"):
		return observability.StorageReasonAccessDenied
	case strings.Contains(msg, "statuscode: 404"):
		return observability.StorageReasonNotFound
	default:
		return observability.StorageReasonOther
	}
}

// codeReasons maps the S3 error codes (AWS, MinIO, SeaweedFS) to a reason
// class. Ordered: NoSuchBucket is tested before the shorter codes a message
// could also contain.
//
//nolint:gochecknoglobals // a read-only lookup table.
var codeReasons = []struct{ code, reason string }{
	{"NoSuchBucket", observability.StorageReasonBucketMissing},
	{"AccessDenied", observability.StorageReasonAccessDenied},
	{"InvalidAccessKeyId", observability.StorageReasonAccessDenied},
	{"SignatureDoesNotMatch", observability.StorageReasonAccessDenied},
	{"ExpiredToken", observability.StorageReasonAccessDenied},
	{"QuotaExceeded", observability.StorageReasonQuotaExceeded},
	{"XMinioStorageFull", observability.StorageReasonQuotaExceeded},
	{"XMinioAdminBucketQuotaExceeded", observability.StorageReasonQuotaExceeded},
	{"InsufficientStorage", observability.StorageReasonQuotaExceeded},
	{"NoSuchKey", observability.StorageReasonNotFound},
	{"NotFound", observability.StorageReasonNotFound},
}

// reasonForCode is the class of one S3 error code, or empty when the code is
// not one the table names.
func reasonForCode(code string) string {
	for _, c := range codeReasons {
		if strings.EqualFold(c.code, code) {
			return c.reason
		}
	}
	return ""
}
