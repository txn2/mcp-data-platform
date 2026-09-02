package trino

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
)

// connectExportServer registers trino_export on a real mcp.Server and returns
// a client session over an in-memory transport, so a test exercises the SDK's
// schema validation and structured-result write rather than the handler alone.
func connectExportServer(t *testing.T, tk *Toolkit) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := mcp.NewServer(&mcp.Implementation{Name: "export-test", Version: "v0"}, nil)
	tk.registerExportTool(server)
	t1, t2 := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, t1, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, nil)
	sess, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// firstTextBlock returns the text of the first text block of a result.
func firstTextBlock(res *mcp.CallToolResult) string {
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	return ""
}

// newFullFlowExportToolkit is an export toolkit whose query returns two rows
// through a sqlmock-backed Trino client, with an owner resolved on every call.
func newFullFlowExportToolkit(t *testing.T) *Toolkit {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(1, "Alice").AddRow(2, "Bob"))

	tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})
	tk.client = trinoclient.NewWithDB(db, trinoclient.Config{Timeout: time.Minute})
	tk.exportDeps.Config.DefaultTimeout = time.Minute
	tk.exportDeps.Config.MaxTimeout = time.Minute
	return tk
}

// TestExportRegistration_StructuredResultCarriesOutput is the #1589 unit
// acceptance: a trino_export call through the registered tool returns its
// output as the structured result, the object the platform's appended blocks
// merge into, and the same object as its one text block.
func TestExportRegistration_StructuredResultCarriesOutput(t *testing.T) {
	sess := connectExportServer(t, newFullFlowExportToolkit(t))
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: exportToolName,
		Arguments: map[string]any{
			"sql": "SELECT id, name FROM users", "format": "csv", "name": "User Export",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool error: %s", firstTextBlock(res))

	structured, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok, "structured result = %T; want the export's own object", res.StructuredContent)
	assert.NotEmpty(t, structured["asset_id"])
	assert.NotEmpty(t, structured["portal_url"])
	assert.Equal(t, "csv", structured["format"])
	assert.EqualValues(t, 2, structured["row_count"])
	assert.Greater(t, structured["size_bytes"], float64(0))
	assert.Equal(t, "Exported 2 rows as csv.", structured["message"])

	require.Len(t, res.Content, 1, "the export's own response is one text block")
	var text map[string]any
	require.NoError(t, json.Unmarshal([]byte(firstTextBlock(res)), &text))
	assert.Equal(t, structured, text, "the text block and the structured result are the same object")
}

// TestExportRegistration_RefusalHasNoStructuredResult holds the error path to
// the same shape api_export has: an in-band refusal is a text block alone,
// not a zero-valued output presented as the structured result.
func TestExportRegistration_RefusalHasNoStructuredResult(t *testing.T) {
	sess := connectExportServer(t, newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{}))
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      exportToolName,
		Arguments: map[string]any{"sql": "DROP TABLE users", "format": "csv", "name": "x"},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, firstTextBlock(res), "write operations not allowed")
	assert.Nil(t, res.StructuredContent)
}

// TestExportRegistration_IdempotentReplayIsStructured covers the second
// success path: a repeated idempotency key returns the existing asset, and
// that reply is a structured result too.
func TestExportRegistration_IdempotentReplayIsStructured(t *testing.T) {
	store := &mockExportAssetStore{
		idempotencyHit: &ExportAssetRef{ID: "asset-existing", SizeBytes: 316},
	}
	tk := newTestExportToolkit(store, &mockExportVersionStore{}, &mockExportS3Client{})
	sess := connectExportServer(t, tk)
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{
		Name: exportToolName,
		Arguments: map[string]any{
			"sql": "SELECT 1", "format": "csv", "name": "rows", "idempotency_key": "k1",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tool error: %s", firstTextBlock(res))
	structured, ok := res.StructuredContent.(map[string]any)
	require.True(t, ok, "structured result = %T", res.StructuredContent)
	assert.Equal(t, "asset-existing", structured["asset_id"])
	assert.EqualValues(t, 316, structured["size_bytes"])
	assert.Equal(t, "https://example.com/portal/assets/asset-existing", structured["portal_url"])
}
