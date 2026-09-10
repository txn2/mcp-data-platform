package trino

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// fakeLander stands in for the platform's managed-resource writer, recording
// what it was asked to land and answering with a version that climbs per path.
type fakeLander struct {
	checked  []toolkit.ResourceDestination
	landed   []toolkit.ResourceDestination
	bodies   []string
	types    []string
	versions map[string]int
	checkErr error
	landErr  error
}

func newFakeLander() *fakeLander { return &fakeLander{versions: map[string]int{}} }

func (f *fakeLander) CheckResourceDestination(_ context.Context, dest toolkit.ResourceDestination) error {
	f.checked = append(f.checked, dest)
	return f.checkErr
}

func (f *fakeLander) LandResource(
	_ context.Context, dest toolkit.ResourceDestination, content io.Reader, contentType string,
) (*toolkit.ResourceLanding, error) {
	body, err := io.ReadAll(content)
	if err != nil {
		return nil, fmt.Errorf("reading the content the caller streamed: %w", err)
	}
	if f.landErr != nil {
		return nil, f.landErr
	}
	f.landed = append(f.landed, dest)
	f.bodies = append(f.bodies, string(body))
	f.types = append(f.types, contentType)
	address := dest.Path + "/" + dest.Filename
	f.versions[address]++
	version := f.versions[address]
	return &toolkit.ResourceLanding{
		ResourceID: "res-1", Reference: "mcp:resource:res-1",
		URI: "mcp://user/user-123/" + address, Filename: dest.Filename, Path: dest.Path,
		ContentType: contentType, SizeBytes: int64(len(body)), Version: version,
		Created: version == 1, Message: "Landed.",
	}, nil
}

// resultText reads one tool result's text, for an assertion about what a refusal
// does NOT say.
func resultText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	require.Len(t, result.Content, 1)
	tc, ok := result.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", result.Content[0])
	return tc.Text
}

func salesDestination() *toolkit.ResourceDestination {
	return &toolkit.ResourceDestination{Path: "datasets", Filename: "sales.csv"}
}

// The formatted result lands in the file at the path, and the result carries the
// reference and version rather than an asset id.
func TestLandExportWritesTheResultToTheLibrary(t *testing.T) {
	lander := newFakeLander()
	tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})
	tk.exportDeps.ResourceLander = lander

	out, errResult := tk.landExport(context.Background(), tk.exportDeps, exportInput{
		Name: "Weekly sales", Description: "Rolled up by region", Format: formatCSV,
		Resource: salesDestination(),
	}, landedResult{
		body: []byte("region,total\nwest,10\n"), contentType: "text/csv",
		tags: []string{"sales", "_sys-confidential"}, rowCount: 1,
	})

	require.Nil(t, errResult)
	require.NotNil(t, out.Resource)
	assert.Empty(t, out.AssetID, "a resource export writes no asset")
	assert.Equal(t, "mcp:resource:res-1", out.Resource.Reference)
	assert.Equal(t, 1, out.Resource.Version)
	assert.True(t, out.Resource.Created)
	assert.Equal(t, int64(len("region,total\nwest,10\n")), out.SizeBytes)
	assert.Equal(t, 1, out.RowCount)
	assert.Contains(t, out.Message, "Exported 1 rows as csv")
	assert.Contains(t, out.Message, "Landed.")

	require.Len(t, lander.landed, 1)
	assert.Equal(t, "Weekly sales", lander.landed[0].DisplayName)
	assert.Equal(t, "Rolled up by region", lander.landed[0].Description)
	// The sensitivity tags inherited from the queried tables travel with the
	// file, so a landing from restricted data carries the markings the asset
	// would have carried.
	assert.Equal(t, []string{"sales", "_sys-confidential"}, lander.landed[0].Tags)
	assert.Equal(t, "region,total\nwest,10\n", lander.bodies[0])
}

func TestLandExportReportsAFailure(t *testing.T) {
	lander := newFakeLander()
	lander.landErr = assert.AnError
	tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})
	tk.exportDeps.ResourceLander = lander

	out, errResult := tk.landExport(context.Background(), tk.exportDeps, exportInput{
		Name: "Weekly sales", Format: formatCSV, Resource: salesDestination(),
	}, landedResult{body: []byte("a\n"), contentType: "text/csv"})

	assert.Nil(t, out)
	require.NotNil(t, errResult)
	assert.True(t, errResult.IsError)
}

// The destination is settled before the query runs: a refused one costs no query
// and writes nothing.
func TestExportChecksTheDestinationBeforeTheQuery(t *testing.T) {
	cases := map[string]struct {
		args map[string]any
		want string
	}{
		"no library on this deployment": {
			map[string]any{
				"sql": "SELECT 1", "format": "csv", "name": "sales",
				"resource": map[string]any{"path": "datasets", "filename": "sales.csv"},
			},
			"managed-resource library",
		},
		"an idempotency key": {
			map[string]any{
				"sql": "SELECT 1", "format": "csv", "name": "sales", "idempotency_key": "k1",
				"resource": map[string]any{"path": "datasets", "filename": "sales.csv"},
			},
			"idempotency_key",
		},
		"a public link": {
			map[string]any{
				"sql": "SELECT 1", "format": "csv", "name": "sales", "create_public_link": true,
				"resource": map[string]any{"path": "datasets", "filename": "sales.csv"},
			},
			"create_public_link",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})
			if name != "no library on this deployment" {
				tk.exportDeps.ResourceLander = newFakeLander()
			}
			result, _ := callExport(t, tk, tc.args)
			assert.True(t, result.IsError)
			assertResultContains(t, result, tc.want)
			// The query never ran: the refusal is not the "no Trino client" one
			// every call that reaches execution earns here.
			assert.NotContains(t, resultText(t, result), "no Trino client")
		})
	}
}

// A destination the platform refuses is refused in the platform's own words,
// before the query.
func TestExportReportsARefusedDestination(t *testing.T) {
	lander := newFakeLander()
	lander.checkErr = assert.AnError
	tk := newTestExportToolkit(&mockExportAssetStore{}, &mockExportVersionStore{}, &mockExportS3Client{})
	tk.exportDeps.ResourceLander = lander

	result, _ := callExport(t, tk, map[string]any{
		"sql": "SELECT 1", "format": "csv", "name": "sales",
		"resource": map[string]any{"path": "datasets", "filename": "sales.csv"},
	})

	assert.True(t, result.IsError)
	assertResultContains(t, result, assert.AnError.Error())
	assert.Len(t, lander.checked, 1)
	assert.Empty(t, lander.landed)
}

// The published destination schema is the shared one, so the four export tools
// describe one capability in one set of words.
func TestExportPublishesTheSharedDestinationSchema(t *testing.T) {
	var want map[string]any
	require.NoError(t, json.Unmarshal([]byte(toolkit.ResourceDestinationSchema), &want))
	assert.Equal(t, want, resourceDestinationSchema())

	props, ok := exportInputSchema()[propProperties].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, want, props[propResource])
}
