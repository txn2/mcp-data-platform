package portalstore

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/objectobs"
	"github.com/txn2/mcp-data-platform/internal/outbound"
	"github.com/txn2/mcp-data-platform/pkg/observability"
)

// observedS3 stands in for the portal's adapter: it reports each operation
// the way the adapter does, under the portal's assets unless the context
// names another purpose, and closes nothing it was not asked to.
type observedS3 struct{ closed bool }

func (*observedS3) op(ctx context.Context, op string) error {
	_, err := objectobs.Do(ctx, observability.StoragePurposePortalAssets, op, "b",
		func(context.Context) (int64, error) { return 0, nil })
	return err //nolint:wrapcheck // test double
}

func (o *observedS3) PutObject(ctx context.Context, _, _ string, _ []byte, _ string) error {
	return o.op(ctx, objectobs.OpPut)
}

func (o *observedS3) PutObjectStream(ctx context.Context, _, _ string, _ io.Reader, _ string) (int64, error) {
	return 0, o.op(ctx, objectobs.OpPut)
}

func (o *observedS3) GetObject(ctx context.Context, _, _ string) (body []byte, contentType string, err error) {
	return nil, "", o.op(ctx, objectobs.OpGet)
}

func (o *observedS3) DeleteObject(ctx context.Context, _, _ string) error {
	return o.op(ctx, objectobs.OpDelete)
}

func (o *observedS3) Close() error { o.closed = true; return nil }

// The export and script views report their operations under their own
// purpose, never close the shared client, and are nil where the portal has no
// blob storage.
func TestPurposedClients(t *testing.T) {
	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	prev := outbound.Metrics()
	outbound.SetDefaultMetrics(m)
	t.Cleanup(func() { outbound.SetDefaultMetrics(prev); _ = m.Shutdown(context.Background()) })

	assert.Nil(t, (&Handle{}).ExportS3Client())
	assert.Nil(t, (*Handle)(nil).ScriptS3Client())

	inner := &observedS3{}
	h := &Handle{s3Client: inner}
	ctx := context.Background()
	exp := h.ExportS3Client()
	require.NoError(t, exp.PutObject(ctx, "b", "k", nil, ""))
	_, err = exp.PutObjectStream(ctx, "b", "k", strings.NewReader(""), "")
	require.NoError(t, err)
	_, _, err = exp.GetObject(ctx, "b", "k")
	require.NoError(t, err)
	require.NoError(t, exp.DeleteObject(ctx, "b", "k"))
	require.NoError(t, exp.Close())
	assert.False(t, inner.closed, "a view closed the portal's client")
	require.NoError(t, h.ScriptS3Client().PutObject(ctx, "b", "k", nil, ""))
	require.NoError(t, h.S3Client().PutObject(ctx, "b", "k", nil, ""))

	srv := httptest.NewServer(m.Handler())
	defer srv.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck // test cleanup
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	body := string(raw)
	assert.Contains(t, body, `storage_operations_total{operation="put",purpose="exports",result="ok"} 2`)
	assert.Contains(t, body, `storage_operations_total{operation="get",purpose="exports",result="ok"} 1`)
	assert.Contains(t, body, `storage_operations_total{operation="delete",purpose="exports",result="ok"} 1`)
	assert.Contains(t, body, `storage_operations_total{operation="put",purpose="script_outputs",result="ok"} 1`)
	assert.Contains(t, body, `storage_operations_total{operation="put",purpose="portal_assets",result="ok"} 1`)
}
