package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// landingExporter is the exporter arm of a platform run whose output goes to the
// managed-resource library: it answers with the file a landing would have
// produced, so the engine's half of the contract can be read off the record the
// script receives.
type landingExporter struct {
	requests []ExportRequest
	versions map[string]int
}

func newLandingExporter() *landingExporter {
	return &landingExporter{versions: map[string]int{}}
}

func (e *landingExporter) Export(_ context.Context, req ExportRequest) (*ExportResult, error) {
	e.requests = append(e.requests, req)
	e.versions[req.Key]++
	version := e.versions[req.Key]
	return &ExportResult{
		Key:             req.Key,
		ResourceID:      "res-1",
		ResourceRef:     "mcp:resource:res-1",
		ResourceURI:     "mcp://user/jane@example.com/" + req.Key,
		ResourceVersion: version,
		Bytes:           64,
		Tables:          []string{"scratch.orders followed onto version 1."},
	}, nil
}

func (*landingExporter) PublishData(_ context.Context, _ PublishRequest) (*ExportResult, error) {
	return &ExportResult{AssetID: "asset_1", AssetVersion: 1, Bytes: 32}, nil
}

// "resources" resolves with no configuration at all, like the portal: both are
// the platform's own stores, and neither is an entry in scripts.destinations.
func TestExport_TheLibraryDestinationIsBuiltIn(t *testing.T) {
	exporter := newLandingExporter()
	_, err := destinationRun(t,
		`platform.export(name="orders", rows=[{"a": 1}], format="csv", destination="resources", key="datasets/orders.csv")`,
		nil, exporter)
	require.NoError(t, err)

	require.Len(t, exporter.requests, 1)
	assert.Equal(t, script.ResourcesDestination(), exporter.requests[0].Destination)
	assert.Equal(t, "datasets/orders.csv", exporter.requests[0].Key)
}

// The record the script receives carries the file it wrote: the reference and the
// uri to cite it by, and the version this run recorded.
func TestExport_TheLibraryRecordNamesTheFile(t *testing.T) {
	exporter := newLandingExporter()
	result, err := destinationRun(t, `
out = platform.export(name="orders", rows=[{"a": 1}], format="csv", destination="resources", key="datasets/orders.csv")
print(out["reference"], out["uri"], out["version"])
`, nil, exporter)
	require.NoError(t, err)

	require.Len(t, result.Exports, 1)
	record := result.Exports[0]
	assert.Equal(t, script.DestinationResources, record.Destination)
	assert.Equal(t, "res-1", record.ResourceID)
	assert.Equal(t, "mcp:resource:res-1", record.ResourceRef)
	assert.Equal(t, "mcp://user/jane@example.com/datasets/orders.csv", record.ResourceURI)
	assert.Equal(t, 1, record.ResourceVersion)
	assert.Empty(t, record.AssetID)
	assert.Empty(t, record.Bucket)
	assert.Contains(t, result.Log, "mcp:resource:res-1 mcp://user/jane@example.com/datasets/orders.csv 1")
	// What the write did to the tables over the file is in the run's own log,
	// whether or not the script printed it (#1536).
	assert.Contains(t, result.Log, "tables: orders: scratch.orders followed onto version 1.")
}

// The key is the file's identity across runs, so a library output without one has
// no address to be the same file at. The refusal says what to write.
func TestExport_TheLibraryDestinationNeedsAKey(t *testing.T) {
	exporter := newLandingExporter()
	_, err := destinationRun(t,
		`platform.export(name="orders", rows=[{"a": 1}], destination="resources")`,
		nil, exporter)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a key")
	assert.Contains(t, err.Error(), "datasets/orders.csv")
	assert.Empty(t, exporter.requests, "the refusal precedes the write")
}

// A key that is not an address in the library is refused at the call, before the
// run has written anything.
func TestExport_ALibraryKeyIsAFolderAndAFilename(t *testing.T) {
	cases := map[string]struct {
		key  string
		want string
	}{
		"no folder":         {"orders.csv", "names a file but no folder"},
		"a leading slash":   {"/datasets/orders.csv", "cannot start with '/'"},
		"a relative walk":   {"datasets/../../etc/passwd", "'.' or '..' segments"},
		"an empty segment":  {"datasets//orders.csv", "empty path segment"},
		"a trailing slash":  {"datasets/orders.csv/", "empty path segment"},
		"padded whitespace": {"datasets/ orders.csv", "leading or trailing whitespace"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			exporter := newLandingExporter()
			_, err := destinationRun(t,
				`platform.export(name="orders", rows=[{"a": 1}], destination="resources", key="`+tc.key+`")`,
				nil, exporter)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, exporter.requests)
		})
	}
}

// A draft run previews a library output the way it previews every other one: the
// content is serialized to measure it and nothing is written, so an author sees
// the size before anything persists.
func TestExport_ADraftPreviewsALibraryOutput(t *testing.T) {
	result, err := destinationRun(t,
		`platform.export(name="orders", rows=[{"a": 1}], format="csv", destination="resources", key="datasets/orders.csv")`,
		nil, nil)
	require.NoError(t, err)

	require.Len(t, result.Exports, 1)
	assert.True(t, result.Exports[0].Preview)
	assert.Equal(t, script.DestinationResources, result.Exports[0].Destination)
	assert.Positive(t, result.Exports[0].Bytes)
	assert.Empty(t, result.Exports[0].ResourceID, "a preview wrote no file")
}

// A draft refuses the same key a platform run refuses, so an author learns at the
// desk rather than at the first scheduled fire.
func TestExport_ADraftRefusesAnUnusableLibraryKey(t *testing.T) {
	_, err := destinationRun(t,
		`platform.export(name="orders", rows=[{"a": 1}], destination="resources")`,
		nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a key")
}

// One result to the library and to the portal is two calls sharing one name, the
// same as the portal-and-bucket pair.
func TestExport_OneNameReachesTheLibraryAndThePortal(t *testing.T) {
	exporter := newLandingExporter()
	result, err := destinationRun(t, `
rows = [{"a": 1}]
platform.export(name="orders", rows=rows, format="csv", destination="resources", key="datasets/orders.csv")
platform.export(name="orders", rows=rows, format="csv")
`, nil, exporter)
	require.NoError(t, err)
	require.Len(t, result.Exports, 2)
	assert.Equal(t, script.DestinationResources, result.Exports[0].Destination)
	assert.Equal(t, script.DestinationPortal, result.Exports[1].Destination)
}

// A delivered object's record carries both halves of where it went, which is
// the other arm of the same dict the library output fills.
func TestExport_ADeliveredRecordCarriesTheBucketAndTheKey(t *testing.T) {
	exporter := &deliveringExporter{}
	result, err := destinationRun(t, `
out = platform.export(name="daily", rows=[{"a": 1}], format="csv", destination="acme-drop", key="daily/out.csv")
print(out["bucket"], out["key"])
`, []script.Destination{acmeDrop()}, exporter)
	require.NoError(t, err)

	require.Len(t, result.Exports, 1)
	assert.Equal(t, "acme-exports", result.Exports[0].Bucket)
	assert.Equal(t, "daily/out.csv", result.Exports[0].Key)
	assert.Empty(t, result.Exports[0].ResourceID)
	assert.Contains(t, result.Log, "acme-exports daily/out.csv")
}

// deliveringExporter answers as a bucket delivery does: the object's address and
// nothing about an asset or a file.
type deliveringExporter struct{}

func (*deliveringExporter) Export(_ context.Context, req ExportRequest) (*ExportResult, error) {
	return &ExportResult{Bucket: req.Destination.Bucket, Key: req.Key, Bytes: 32}, nil
}

func (*deliveringExporter) PublishData(_ context.Context, _ PublishRequest) (*ExportResult, error) {
	return &ExportResult{AssetID: "asset_1", AssetVersion: 1, Bytes: 32}, nil
}

// The static validator reads the destination off the source, so a review surface
// states that a script writes the library before it is ever run (#1415).
func TestValidate_ReadsTheLibraryDestinationFromTheSource(t *testing.T) {
	report := Validate(`platform.export(name="orders", rows=[], destination="resources", key="datasets/orders.csv")`)
	assert.Contains(t, report.Destinations, script.DestinationResources)
	assert.True(t, report.OK, "the built-in destination is not a finding: %+v", report.Findings)

	checked := WithDestinationCheck(report, nil)
	assert.True(t, checked.OK, "a built-in destination needs no configuration: %+v", checked.Findings)
}
