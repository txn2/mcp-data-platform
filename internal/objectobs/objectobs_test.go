package objectobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/smithy-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

func installMetrics(t *testing.T) *observability.Metrics {
	t.Helper()
	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	prev := outbound.Metrics()
	outbound.SetDefaultMetrics(m)
	t.Cleanup(func() {
		outbound.SetDefaultMetrics(prev)
		_ = m.Shutdown(context.Background())
	})
	return m
}

func scrape(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

// A put is counted under the adapter's purpose with the bytes it stored, a
// failure under its reason class, and a context's purpose wins over the
// adapter's.
func TestDo_RecordsOperations(t *testing.T) {
	m := installMetrics(t)
	ctx := context.Background()

	n, err := Do(ctx, observability.StoragePurposePortalAssets, OpPut, "assets", func(context.Context) (int64, error) {
		return 42, nil
	})
	require.NoError(t, err)
	assert.Equal(t, int64(42), n)

	denied := &smithy.GenericAPIError{Code: "AccessDenied", Message: "Access Denied"}
	_, err = Do(ctx, observability.StoragePurposePortalAssets, OpGet, "assets", func(context.Context) (int64, error) {
		return 0, fmt.Errorf("s3: %w", denied)
	})
	require.ErrorIs(t, err, denied)

	_, err = Do(WithPurpose(ctx, observability.StoragePurposeThumbnails), observability.StoragePurposePortalAssets, OpDelete, "assets",
		func(context.Context) (int64, error) { return 0, nil })
	require.NoError(t, err)

	body := scrape(t, m)
	assert.Contains(t, body, `storage_operations_total{operation="put",purpose="portal_assets",result="ok"} 1`)
	assert.Contains(t, body, `storage_bytes_written_total{purpose="portal_assets"} 42`)
	assert.Contains(t, body, `storage_operations_total{operation="get",purpose="portal_assets",reason="access_denied",result="error"} 1`)
	assert.Contains(t, body, `storage_operations_total{operation="delete",purpose="thumbnails",result="ok"} 1`)
	assert.Contains(t, body, `storage_operation_duration_seconds_count{operation="put",purpose="portal_assets"} 1`)
}

// Inside a trace, an operation is a storage.<operation> client span under the
// caller's span, carrying the purpose and, on a failure, the reason.
func TestDo_SpanUnderCaller(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tr := observability.NewTracerFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)), observability.TracingConfig{Enabled: true})
	ctx, root := tr.Start(context.Background(), "tool_call")
	_, _ = Do(ctx, observability.StoragePurposeResources, OpPut, "res", func(context.Context) (int64, error) {
		return 0, errors.New("operation error S3: PutObject, api error NoSuchBucket: The specified bucket does not exist")
	})
	root.End()

	var span sdktrace.ReadOnlySpan
	for _, s := range sr.Ended() {
		if s.Name() == "storage.put" {
			span = s
		}
	}
	require.NotNil(t, span)
	assert.Equal(t, root.SpanContext().SpanID(), span.Parent().SpanID())
	attrs := map[string]string{}
	for _, kv := range span.Attributes() {
		attrs[string(kv.Key)] = kv.Value.String()
	}
	assert.Equal(t, "resources", attrs[attrPurpose])
	assert.Equal(t, "bucket_missing", attrs[attrReason])
	assert.Equal(t, "res", attrs[attrBucket])
}

func TestReason(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"typed bucket", &smithy.GenericAPIError{Code: "NoSuchBucket"}, observability.StorageReasonBucketMissing},
		{"typed key", fmt.Errorf("get: %w", &smithy.GenericAPIError{Code: "NoSuchKey"}), observability.StorageReasonNotFound},
		{"typed quota", &smithy.GenericAPIError{Code: "XMinioStorageFull"}, observability.StorageReasonQuotaExceeded},
		{"typed unknown code falls to text", &smithy.GenericAPIError{Code: "SlowDown", Message: "reduce your rate"}, observability.StorageReasonOther},
		{"text signature", errors.New("api error SignatureDoesNotMatch: bad"), observability.StorageReasonAccessDenied},
		{"text 403", errors.New("https response error StatusCode: 403, RequestID: x"), observability.StorageReasonAccessDenied},
		{"text 404", errors.New("unexpected status code: 404"), observability.StorageReasonNotFound},
		{"text quota", errors.New("QuotaExceeded: bucket full"), observability.StorageReasonQuotaExceeded},
		{"outage", errors.New("dial tcp 10.0.0.1:9000: connect: connection refused"), observability.StorageReasonOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, Reason(c.err))
		})
	}
}
