package scriptlint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func acmeDrop() script.Destination {
	return script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}
}

// TestCheckDestinations_FlagsAnUndeclaredName is #1415: the surface whose job
// is answering "would this run" has to answer it, rather than reporting ok and
// leaving the refusal to arrive after the script's queries have executed.
func TestCheckDestinations_FlagsAnUndeclaredName(t *testing.T) {
	report := scriptrun.Validate(`platform.export("top-stores", [], "csv", destination = "drop", key = "t.csv")` + "\n")
	require.True(t, report.OK, "the source itself is fine")
	require.Equal(t, []string{"drop"}, report.Destinations)

	checked := WithDestinationCheck(report, nil)
	assert.False(t, checked.OK)
	require.Len(t, checked.Findings, 1)
	assert.Equal(t, scriptrun.SeverityError, checked.Findings[0].Severity)
	assert.Contains(t, checked.Findings[0].Message, `destination "drop" is not configured`)
	assert.Contains(t, checked.Findings[0].Message, "declares no bucket destinations")
}

// TestCheckDestinations_AcceptsThePortalAndDeclaredNames keeps the check from
// becoming a second, stricter set: whatever a run resolves, validate accepts.
func TestCheckDestinations_AcceptsThePortalAndDeclaredNames(t *testing.T) {
	report := scriptrun.Validate(`platform.export("a", [], "csv")
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
	report := scriptrun.Validate(`where = "dr" + run.params["x"]
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
		scriptrun.Validate(`platform.export("x", [], "csv", destination = "drop", key = "x.csv")`+"\n"),
		[]script.Destination{acmeDrop()})
	require.Len(t, findings, 1)
	assert.Contains(t, findings[0].Message, "this deployment declares acme-drop")
	assert.Contains(t, findings[0].Hint, "scripts.destinations")
}

// The built-in resources destination needs no configuration.
func TestCheckDestinations_AcceptsTheLibrary(t *testing.T) {
	report := scriptrun.Validate(`platform.export(name="orders", rows=[], destination="resources", key="datasets/orders.csv")`)
	checked := WithDestinationCheck(report, nil)
	assert.True(t, checked.OK, "a built-in destination needs no configuration: %+v", checked.Findings)
}

// DraftRefusal names the first finding of a source a draft does not run, and
// is empty for one it does.
func TestDraftRefusal(t *testing.T) {
	assert.Empty(t, DraftRefusal("print(1)\n", nil))
	assert.Contains(t, DraftRefusal("def f(:\n", nil), "the source does not pass validation, so it was not run: ")
	assert.Contains(t, DraftRefusal(`platform.export("x", [], "csv", destination = "drop", key = "x.csv")`+"\n", nil),
		`destination "drop" is not configured`)
}
