package scriptexec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// fakeLander stands in for the platform's managed-resource writer, recording the
// destination, the bytes and the identity each landing was made under.
type fakeLander struct {
	landed   []toolkit.ResourceDestination
	bodies   []string
	claims   []resource.Claims
	versions map[string]int
	err      error
	tables   []string
}

func newFakeLander() *fakeLander { return &fakeLander{versions: map[string]int{}} }

func (f *fakeLander) Land(
	_ context.Context, dest toolkit.ResourceDestination,
	content io.Reader, contentType string, claims resource.Claims,
) (*toolkit.ResourceLanding, error) {
	body, err := io.ReadAll(content)
	if err != nil {
		return nil, fmt.Errorf("reading the content the caller streamed: %w", err)
	}
	if f.err != nil {
		return nil, f.err
	}
	f.landed = append(f.landed, dest)
	f.bodies = append(f.bodies, string(body))
	f.claims = append(f.claims, claims)
	address := dest.Path + "/" + dest.Filename
	f.versions[address]++
	version := f.versions[address]
	return &toolkit.ResourceLanding{
		ResourceID: "res-" + address, Reference: "mcp:resource:res-" + address,
		URI: "mcp://user/jane@example.com/" + address, Filename: dest.Filename, Path: dest.Path,
		ContentType: contentType, SizeBytes: int64(len(body)), Version: version,
		Created: version == 1, Tables: f.tables, Message: "Landed.",
	}, nil
}

// libraryRequest is one output addressed to the built-in managed-resource
// destination, in the shape the engine hands over.
func libraryRequest(name, key string) scriptrun.ExportRequest {
	req := csvRequest(name)
	req.Destination = script.ResourcesDestination()
	req.Key = key
	return req
}

// landingHarness is the writer harness with a library behind it.
func landingHarness(t *testing.T) (writerHarness, *fakeLander) {
	t.Helper()
	h := newWriterHarness(t)
	lander := newFakeLander()
	h.writer.deps.Lander = lander
	return h, lander
}

// The output lands as the file at the key, labeled from the output name, under
// the run's own identity.
func TestExportToTheLibraryWritesTheFileAtTheKey(t *testing.T) {
	h, lander := landingHarness(t)

	out, err := h.writer.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))

	require.NoError(t, err)
	assert.Equal(t, "res-datasets/orders.csv", out.ResourceID)
	assert.Equal(t, "mcp:resource:res-datasets/orders.csv", out.ResourceRef)
	assert.Equal(t, "mcp://user/jane@example.com/datasets/orders.csv", out.ResourceURI)
	assert.Equal(t, 1, out.ResourceVersion)
	assert.Empty(t, out.AssetID, "a library output writes no asset")
	assert.Empty(t, out.Bucket)

	require.Len(t, lander.landed, 1)
	assert.Equal(t, "datasets", lander.landed[0].Path)
	assert.Equal(t, "orders.csv", lander.landed[0].Filename)
	assert.Equal(t, "orders", lander.landed[0].DisplayName)
	assert.Contains(t, lander.landed[0].ChangeSummary, "daily v3, run dpx_1")
	assert.Contains(t, lander.bodies[0], "region,total")

	// The run acts for the version's AUTHOR and authenticates as the script, so
	// the file is filed in the author's library and the principal is recorded as
	// the writer (#1419).
	assert.Equal(t, "script:daily", lander.claims[0].Sub)
	assert.Equal(t, "jane@example.com", lander.claims[0].OnBehalfOf)
	assert.Equal(t, []string{"analyst"}, lander.claims[0].Roles)
	assert.False(t, lander.claims[0].IsAdmin, "a run asserts no admin authority of its own")
}

// The output is recorded on the run by what it is: a file, its version, and what
// the version did to the tables over it.
func TestExportToTheLibraryIsRecordedOnTheRun(t *testing.T) {
	h, lander := landingHarness(t)
	lander.tables = []string{"scratch.orders followed onto version 1."}

	_, err := h.writer.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.NoError(t, err)

	require.Len(t, h.run.Outputs, 1)
	out := h.run.Outputs[0]
	assert.Equal(t, script.DestinationResources, out.Destination)
	assert.Equal(t, "datasets/orders.csv", out.Key)
	assert.Equal(t, "res-datasets/orders.csv", out.ResourceID)
	assert.Equal(t, "mcp://user/jane@example.com/datasets/orders.csv", out.ResourceURI)
	assert.Equal(t, 1, out.ResourceVersion)
	assert.Equal(t, lander.tables, out.Tables)
}

// Two outputs at one key would make the second a version of the first one's
// file while the run recorded both as written, so the second is refused.
func TestTwoOutputsAtOneLibraryPathAreRefused(t *testing.T) {
	h, _ := landingHarness(t)
	_, err := h.writer.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.NoError(t, err)

	_, err = h.writer.Export(context.Background(), libraryRequest("orders-copy", "datasets/orders.csv"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already wrote")
	assert.Len(t, h.run.Outputs, 1)
}

// One output may go to the library and to the portal in one run: the same result
// as the file other things read and as the asset people open.
func TestOneOutputReachesBothTheLibraryAndThePortal(t *testing.T) {
	h, lander := landingHarness(t)

	_, err := h.writer.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.NoError(t, err)
	portal, err := h.writer.Export(context.Background(), csvRequest("orders"))
	require.NoError(t, err)

	assert.Len(t, lander.landed, 1)
	assert.NotEmpty(t, portal.AssetID)
	assert.Len(t, h.run.Outputs, 2)
}

// A key that is not an address in the library is refused with the address it
// should have been, and nothing is written.
func TestALibraryOutputNeedsAFolderAndAFilename(t *testing.T) {
	h, lander := landingHarness(t)

	_, err := h.writer.Export(context.Background(), libraryRequest("orders", "orders.csv"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "names a file but no folder")
	assert.Empty(t, lander.landed)
}

// A deployment with no library says so, naming the missing piece.
func TestALibraryOutputOnADeploymentWithoutOne(t *testing.T) {
	h := newWriterHarness(t)

	_, err := h.writer.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "no managed-resource library")
}

// A landing failure fails the output, in the words the library wrote it in.
func TestALandingFailureFailsTheOutput(t *testing.T) {
	h, lander := landingHarness(t)
	lander.err = errors.New("the response is larger than 100 MB, the managed-resource upload ceiling")

	_, err := h.writer.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "upload ceiling")
	assert.Empty(t, h.run.Outputs)
}

// A run reclaimed after its worker died must not write its library output twice:
// the run row is the record of what the earlier attempt already landed.
func TestAReclaimedRunDoesNotLandALibraryOutputTwice(t *testing.T) {
	h, lander := landingHarness(t)
	_, err := h.writer.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.NoError(t, err)

	reclaimed := newOutputWriter(h.writer.deps, h.runs, claimedRun{run: h.run, script: h.writer.script, version: testVersion()}, h.caller)
	out, err := reclaimed.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))

	require.NoError(t, err)
	assert.Len(t, lander.landed, 1, "the second attempt landed the file again")
	assert.Equal(t, "res-datasets/orders.csv", out.ResourceID)
	assert.Equal(t, 1, out.ResourceVersion, "the record of the earlier attempt is what is reported")
}

// A reclaimed attempt claims the address the earlier attempt wrote, so a second
// output aimed at that path is refused exactly as the first attempt refused it
// rather than landing on top of the file.
func TestAReclaimedAttemptStillRefusesASecondOutputAtOnePath(t *testing.T) {
	h, lander := landingHarness(t)
	_, err := h.writer.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.NoError(t, err)

	reclaimed := newOutputWriter(h.writer.deps, h.runs, claimedRun{run: h.run, script: h.writer.script, version: testVersion()}, h.caller)
	_, err = reclaimed.Export(context.Background(), libraryRequest("orders", "datasets/orders.csv"))
	require.NoError(t, err, "the output the earlier attempt wrote is answered from the run record")

	_, err = reclaimed.Export(context.Background(), libraryRequest("orders-copy", "datasets/orders.csv"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already wrote")
	assert.Len(t, lander.landed, 1)
}

// The claims a run writes under name the version's author, whoever owns the
// script now: a transfer writes a new version authored by the administrator who
// moved it, and a run of that version acts for them.
func TestRunClaimsNameTheVersionAuthor(t *testing.T) {
	sc := &script.Script{ID: "script_1", Name: "daily", OwnerEmail: "newowner@example.com"}
	v := &script.Version{Author: "admin@example.com", AuthorRoles: []string{"admin"}}

	claims := runClaims(sc, v)

	assert.Equal(t, "script:daily", claims.Sub)
	assert.Equal(t, "newowner@example.com", claims.Email)
	assert.Equal(t, "admin@example.com", claims.OnBehalfOf)
	assert.Equal(t, "admin@example.com", resource.PersonAddress(claims),
		"the person a run files work under is the author it acts for")
}

func TestLibraryAddressIsThePath(t *testing.T) {
	assert.NotEqual(t, libraryAddress("a/b.csv"), libraryAddress("a/c.csv"))
	assert.True(t, strings.HasPrefix(libraryAddress("a/b.csv"), "library"))
}
