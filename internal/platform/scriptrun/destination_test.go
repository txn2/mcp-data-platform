package scriptrun

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// destinationRun executes source with the given configured destination set, so
// a test drives resolveDestination through the same path a real export takes.
func destinationRun(t *testing.T, source string, destinations []script.Destination, exporter Exporter) (*Result, error) {
	t.Helper()
	return Run(context.Background(), Options{
		Source: source, Name: "test", RunID: "dpx_1", FireTime: fireTime,
		Caller: &recordingCaller{}, Destinations: destinations, Exporter: exporter,
	})
}

// acmeDrop is the one configured bucket destination the tests here share.
func acmeDrop() script.Destination {
	return script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}
}

func TestExport_PortalDestinationIsBuiltIn(t *testing.T) {
	// "portal" resolves with no configuration at all: it is the platform's own
	// asset store, never an entry in scripts.destinations.
	exporter := &recordingExporter{}
	result, err := destinationRun(t,
		`platform.export(name="daily", rows=[{"a": 1}], destination="portal")`,
		nil, exporter)
	require.NoError(t, err)

	require.Len(t, exporter.requests, 1)
	assert.Equal(t, script.PortalDestination(), exporter.requests[0].Destination)
	require.Len(t, result.Exports, 1)
	assert.Equal(t, script.DestinationPortal, result.Exports[0].Destination)
}

func TestExport_ConfiguredDestinationResolvesToItsAddress(t *testing.T) {
	// The name a script writes resolves to the full address configuration
	// declares for it — connection, bucket, prefix — and the key the script
	// asked for rides along beneath that prefix.
	exporter := &recordingExporter{}
	result, err := destinationRun(t,
		`platform.export(name="daily", rows=[{"a": 1}], destination="acme-drop", key="daily/out.csv")`,
		[]script.Destination{acmeDrop()}, exporter)
	require.NoError(t, err)

	require.Len(t, exporter.requests, 1)
	assert.Equal(t, acmeDrop(), exporter.requests[0].Destination)
	assert.Equal(t, "daily/out.csv", exporter.requests[0].Key)
	require.Len(t, result.Exports, 1)
	assert.Equal(t, "acme-drop", result.Exports[0].Destination)
}

func TestExport_UnconfiguredDestinationIsRefused(t *testing.T) {
	// A name nothing declares is refused before anything is written, and the
	// refusal names the set the deployment does declare so the author can pick
	// from it.
	exporter := &recordingExporter{}
	_, err := destinationRun(t,
		`platform.export(name="daily", rows=[], destination="elsewhere")`,
		[]script.Destination{acmeDrop()}, exporter)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `destination "elsewhere" is not configured`)
	assert.Contains(t, err.Error(), "acme-drop")
	assert.Contains(t, err.Error(), `"portal" is always available`)
	assert.Empty(t, exporter.requests, "the refusal precedes the write")
}

func TestExport_NoConfiguredDestinationsRefusalNamesThePortal(t *testing.T) {
	// With nothing configured, the refusal says so and names the portal as the
	// only place a script can write, rather than listing an empty set.
	_, err := destinationRun(t,
		`platform.export(name="daily", rows=[], destination="elsewhere")`,
		nil, &recordingExporter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declares no bucket destinations")
	assert.Contains(t, err.Error(), `"portal"`)
}

func TestExport_DraftResolvesThroughTheSameSet(t *testing.T) {
	// A draft run (no Exporter) resolves against the same configured set, so a
	// destination a real run would refuse fails while the author is iterating.
	result, err := destinationRun(t,
		`platform.export(name="daily", rows=[{"a": 1}], destination="acme-drop", key="daily/out.csv")`,
		[]script.Destination{acmeDrop()}, nil)
	require.NoError(t, err)
	require.Len(t, result.Exports, 1)
	assert.True(t, result.Exports[0].Preview)
	assert.Equal(t, "acme-drop", result.Exports[0].Destination)

	_, err = destinationRun(t,
		`platform.export(name="daily", rows=[], destination="elsewhere")`,
		[]script.Destination{acmeDrop()}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `destination "elsewhere" is not configured`)
}

func TestResolveDestination_PortalNameNeverResolvesToABucket(t *testing.T) {
	// Configuration validation refuses a bucket destination named "portal",
	// but resolution does not depend on that: the built-in check runs first,
	// so a list carrying the reserved name can never turn a portal write into
	// a bucket delivery.
	h := &hostState{opts: Options{Destinations: []script.Destination{{
		Name: script.DestinationPortal, Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports",
	}}}}
	resolved, err := h.resolveDestination(script.DestinationPortal)
	require.NoError(t, err)
	assert.Equal(t, script.PortalDestination(), resolved)
}

func TestExport_PortalTakesNoKey(t *testing.T) {
	// The portal names its own objects: an output's identity there is its
	// name, so a key aimed at it is a misunderstanding worth reporting.
	_, err := destinationRun(t,
		`platform.export(name="daily", rows=[], destination="portal", key="daily/out.json")`,
		nil, &recordingExporter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "takes no key")
}
