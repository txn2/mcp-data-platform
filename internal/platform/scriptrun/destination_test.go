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

// TestCheckDestinations_FlagsAnUndeclaredName is #1415: the surface whose job
// is answering "would this run" has to answer it, rather than reporting ok and
// leaving the refusal to arrive after the script's queries have executed.
func TestCheckDestinations_FlagsAnUndeclaredName(t *testing.T) {
	report := Validate(`platform.export("top-stores", [], "csv", destination = "drop", key = "t.csv")` + "\n")
	require.True(t, report.OK, "the source itself is fine")
	require.Equal(t, []string{"drop"}, report.Destinations)

	checked := WithDestinationCheck(report, nil)
	assert.False(t, checked.OK)
	require.Len(t, checked.Findings, 1)
	assert.Equal(t, SeverityError, checked.Findings[0].Severity)
	assert.Contains(t, checked.Findings[0].Message, `destination "drop" is not configured`)
	assert.Contains(t, checked.Findings[0].Message, "declares no bucket destinations")
}

// TestCheckDestinations_RefusalIsTheRuntimeRefusal is the point of sharing
// ResolveDestination: an author who reads validate and an author who reads a
// failed run are told the same thing about the same script.
func TestCheckDestinations_RefusalIsTheRuntimeRefusal(t *testing.T) {
	declared := []script.Destination{acmeDrop()}
	source := `platform.export("x", [], "csv", destination = "drop", key = "x.csv")` + "\n"

	findings := CheckDestinations(Validate(source), declared)
	require.Len(t, findings, 1)

	_, err := destinationRun(t, source, declared, &recordingExporter{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), findings[0].Message,
		"validate and the run name the same refusal")
}

// TestCheckDestinations_AcceptsThePortalAndDeclaredNames keeps the check from
// becoming a second, stricter set: whatever a run resolves, validate accepts.
func TestCheckDestinations_AcceptsThePortalAndDeclaredNames(t *testing.T) {
	report := Validate(`platform.export("a", [], "csv")
platform.export("b", [], "csv", destination = "acme-drop", key = "b.csv")
`)
	require.ElementsMatch(t, []string{script.DestinationPortal, "acme-drop"}, report.Destinations)

	checked := WithDestinationCheck(report, []script.Destination{acmeDrop()})
	assert.True(t, checked.OK, "%+v", checked.Findings)
	assert.Empty(t, CheckDestinations(report, []script.Destination{acmeDrop()}))
}

// TestCheckDestinations_IgnoresAComputedDestination states the limit honestly:
// a destination the source computes is not readable from the source, so there
// is nothing to check. DynamicDestinations is what reports it instead.
func TestCheckDestinations_IgnoresAComputedDestination(t *testing.T) {
	report := Validate(`where = "dr" + "op"
platform.export("x", [], "csv", destination = where, key = "x.csv")
`)
	require.True(t, report.DynamicDestinations)
	require.Empty(t, report.Destinations)

	checked := WithDestinationCheck(report, nil)
	assert.True(t, checked.OK, "%+v", checked.Findings)
}

// TestCheckDestinations_NamesTheDeclaredSet is the other refusal wording: a
// deployment that declares destinations lists them, so the author can pick one.
func TestCheckDestinations_NamesTheDeclaredSet(t *testing.T) {
	findings := CheckDestinations(
		Validate(`platform.export("x", [], "csv", destination = "drop", key = "x.csv")`+"\n"),
		[]script.Destination{acmeDrop()})
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "this deployment declares acme-drop")
	assert.Contains(t, findings[0].Hint, "scripts.destinations")
}
