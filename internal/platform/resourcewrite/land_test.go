package resourcewrite_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// landing fixture: a writer over the in-memory store plus the lander in front of
// it, and the table follower it reports through.
type landFixture struct {
	*fixture
	lander   *resourcewrite.Lander
	followed []int
	tables   []string
}

func newLandFixture(t *testing.T) *landFixture {
	t.Helper()
	lf := &landFixture{fixture: newFixture(t)}
	lf.lander = resourcewrite.NewLander(resourcewrite.LanderDeps{
		Writer: lf.writer,
		Claims: func(context.Context) resource.Claims { return analyst() },
	})
	require.NotNil(t, lf.lander)
	lf.lander.SetTableFollower(func(_ context.Context, _ string, version int) []string {
		lf.followed = append(lf.followed, version)
		return lf.tables
	})
	return lf
}

// orders is the destination an export names: a folder, a filename, and the
// caller's own library by default.
func orders() toolkit.ResourceDestination {
	return toolkit.ResourceDestination{
		Path: "datasets", Filename: "orders.csv",
		DisplayName: "ACME orders", Description: "Nightly pull",
		Tags: []string{"orders"},
	}
}

func land(t *testing.T, lf *landFixture, body string) *toolkit.ResourceLanding {
	t.Helper()
	out, err := lf.lander.LandResource(context.Background(), orders(), strings.NewReader(body), "text/csv")
	require.NoError(t, err)
	return out
}

// A first landing creates the file and reports the two names the caller hands to
// the next call.
func TestLandCreatesTheFileAtThePath(t *testing.T) {
	lf := newLandFixture(t)

	out := land(t, lf, "id,total\n1,10\n")

	assert.True(t, out.Created)
	assert.Equal(t, 1, out.Version)
	assert.Equal(t, "mcp://user/"+authorSub+"/datasets/orders.csv", out.URI)
	assert.Equal(t, "mcp:resource:"+out.ResourceID, out.Reference)
	assert.Equal(t, "orders.csv", out.Filename)
	assert.Equal(t, "datasets", out.Path)
	assert.Equal(t, "text/csv", out.ContentType)
	assert.Equal(t, int64(len("id,total\n1,10\n")), out.SizeBytes)
	assert.Empty(t, lf.followed, "a file nothing was registered over needs no follow")

	stored, err := lf.store.Get(context.Background(), out.ResourceID)
	require.NoError(t, err)
	assert.Equal(t, "ACME orders", stored.DisplayName)
	assert.Equal(t, []string{"orders"}, stored.Tags)
}

// The second landing at the same address is a VERSION of the first file, not a
// second file. This is the whole contract of the destination.
func TestLandReplacesTheFileAlreadyAtThePath(t *testing.T) {
	lf := newLandFixture(t)
	lf.tables = []string{"acme.orders followed onto version 2."}

	first := land(t, lf, "id,total\n1,10\n")
	second := land(t, lf, "id,total\n1,10\n2,20\n")

	assert.Equal(t, first.ResourceID, second.ResourceID, "the same path is the same file")
	assert.Equal(t, first.URI, second.URI)
	assert.Equal(t, first.Reference, second.Reference)
	assert.False(t, second.Created)
	assert.Equal(t, 2, second.Version)
	assert.Equal(t, []int{2}, lf.followed, "the tables over the file follow the version written")
	assert.Equal(t, lf.tables, second.Tables)
	assert.Contains(t, second.Message, "acme.orders followed onto version 2.")

	versions, err := lf.store.ListVersions(context.Background(), second.ResourceID)
	require.NoError(t, err)
	assert.Len(t, versions, 2, "the trail holds both landings")

	body, _, err := lf.blobs.GetObject(context.Background(), testBucket, mustGet(t, lf, second.ResourceID).S3Key)
	require.NoError(t, err)
	assert.Equal(t, "id,total\n1,10\n2,20\n", string(body), "the file serves the newest bytes")
}

func mustGet(t *testing.T, lf *landFixture, id string) *resource.Resource {
	t.Helper()
	res, err := lf.store.Get(context.Background(), id)
	require.NoError(t, err)
	return res
}

// A file somebody refiled is still the file that path names: the landing follows
// the address it vacated and reports where it actually wrote, rather than
// creating a second file at the old address and leaving the moved one stale.
func TestLandFollowsAFileThatWasMoved(t *testing.T) {
	lf := newLandFixture(t)
	first := land(t, lf, "id,total\n1,10\n")

	moved := mustGet(t, lf, first.ResourceID)
	require.NoError(t, lf.store.Move(context.Background(), []resource.Move{{
		ID: moved.ID, FromURI: moved.URI, Scope: moved.Scope, ScopeID: moved.ScopeID,
		Path: "archive/2026",
		URI:  resource.BuildURI(testScheme, moved.Scope, moved.ScopeID, "archive/2026", moved.Filename),
	}}))

	second := land(t, lf, "id,total\n1,10\n2,20\n")

	assert.Equal(t, first.ResourceID, second.ResourceID)
	assert.Equal(t, 2, second.Version)
	assert.Equal(t, "mcp://user/"+authorSub+"/archive/2026/orders.csv", second.URI,
		"the result names the address the file is at now")
}

// Check is the refusal an export needs BEFORE it calls an upstream, and it
// writes nothing.
func TestCheckRefusesWithoutWriting(t *testing.T) {
	lf := newLandFixture(t)

	dest := orders()
	dest.Scope = string(resource.ScopeGlobal)
	err := lf.lander.CheckResourceDestination(context.Background(), dest)

	require.Error(t, err)
	assert.ErrorIs(t, err, resourcewrite.ErrRefused)
	assert.Contains(t, err.Error(), "global scope")
	_, _, listErr := lf.store.List(context.Background(), resource.Filter{})
	require.NoError(t, listErr)
	assert.Empty(t, lf.blobs.objects, "a refused destination stores nothing")
}

func TestCheckAcceptsADestinationLandWrites(t *testing.T) {
	lf := newLandFixture(t)
	require.NoError(t, lf.lander.CheckResourceDestination(context.Background(), orders()))
	land(t, lf, "id\n1\n")
	require.NoError(t, lf.lander.CheckResourceDestination(context.Background(), orders()),
		"an address with a replaceable file is still an address this caller may write")
}

// A destination whose parts the library would refuse is refused here, in the
// library's own words.
func TestLandRefusesAnAddressTheLibraryWouldNotAccept(t *testing.T) {
	cases := map[string]struct {
		mutate func(*toolkit.ResourceDestination)
		want   string
	}{
		"no folder":          {func(d *toolkit.ResourceDestination) { d.Path = "" }, "path is required"},
		"a relative segment": {func(d *toolkit.ResourceDestination) { d.Path = "../etc" }, "path"},
		"no filename":        {func(d *toolkit.ResourceDestination) { d.Filename = "" }, "plain file name"},
		"a refused type":     {func(d *toolkit.ResourceDestination) { d.Filename = "payload.exe" }, "not allowed"},
		"an unknown library": {func(d *toolkit.ResourceDestination) { d.Scope = "everyone" }, "unknown scope"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lf := newLandFixture(t)
			dest := orders()
			tc.mutate(&dest)
			_, err := lf.lander.LandResource(context.Background(), dest, strings.NewReader("x"), "text/csv")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, lf.blobs.objects)
		})
	}
}

// A file at the address that this caller may not replace is refused, and the
// file is left alone.
func TestLandRefusesAFileTheCallerMayNotReplace(t *testing.T) {
	lf := newLandFixture(t)
	dest := orders()
	dest.Scope, dest.ScopeID = string(resource.ScopeUser), "someone-else"
	// Filed by an administrator into another person's library, which the analyst
	// can neither write to nor replace within.
	_, err := lf.lander.Land(context.Background(), dest, strings.NewReader("id\n1\n"), "text/csv", admin())
	require.NoError(t, err)

	_, err = lf.lander.LandResource(context.Background(), dest, strings.NewReader("id\n2\n"), "text/csv")
	require.Error(t, err)
	assert.ErrorIs(t, err, resourcewrite.ErrRefused)
	assert.Contains(t, err.Error(), "already a file at")
}

// The producer's declaration decides the stored type when it says something
// specific, and the destination's filename decides when it does not. A CSV has
// no content signature, so an upstream that could only say "text" must not cost
// the file its family.
func TestLandSettlesTheTypeTheBytesAreStoredUnder(t *testing.T) {
	cases := map[string]struct {
		filename string
		declared string
		want     string
	}{
		"a specific declaration stands":     {"orders.csv", "text/csv", "text/csv"},
		"plain text yields to the filename": {"orders.csv", "text/plain", "text/csv"},
		"bytes yield to the filename":       {"orders.csv", "application/octet-stream", "text/csv"},
		"nothing declared reads the name":   {"report.md", "", "text/markdown"},
		"an unknown name keeps the type":    {"orders.dat", "application/json", "application/json"},
		"neither says anything":             {"orders.dat", "", "application/octet-stream"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lf := newLandFixture(t)
			dest := orders()
			dest.Filename = tc.filename
			out, err := lf.lander.LandResource(context.Background(), dest, strings.NewReader("a,b\n"), tc.declared)
			require.NoError(t, err)
			assert.Equal(t, tc.want, out.ContentType)
		})
	}
}

// The ceiling that applies is the managed-resource library's own, so a file this
// platform would refuse at its upload form is one it refuses here, at the same
// size.
func TestLandRefusesContentOverTheLibraryCeiling(t *testing.T) {
	lf := newLandFixture(t)
	small := resourcewrite.NewLander(resourcewrite.LanderDeps{
		Writer:         lf.writer,
		Claims:         func(context.Context) resource.Claims { return analyst() },
		MaxUploadBytes: 8,
	})

	_, err := small.LandResource(context.Background(), orders(), strings.NewReader(strings.Repeat("x", 64)), "text/csv")

	require.Error(t, err)
	assert.ErrorIs(t, err, resourcewrite.ErrTooLarge)
	assert.Contains(t, err.Error(), "nothing was written")
	_, getErr := lf.store.GetByURI(context.Background(), "mcp://user/"+authorSub+"/datasets/orders.csv")
	require.Error(t, getErr, "an over-ceiling landing leaves no record behind")
}

// An over-ceiling REPLACEMENT leaves the file serving what it served before.
func TestAnOverCeilingReplacementLeavesTheHeadWhereItWas(t *testing.T) {
	lf := newLandFixture(t)
	first := land(t, lf, "id\n1\n")
	small := resourcewrite.NewLander(resourcewrite.LanderDeps{
		Writer:         lf.writer,
		Claims:         func(context.Context) resource.Claims { return analyst() },
		MaxUploadBytes: 4,
	})

	_, err := small.LandResource(context.Background(), orders(), strings.NewReader(strings.Repeat("x", 64)), "text/csv")
	require.Error(t, err)
	assert.ErrorIs(t, err, resourcewrite.ErrTooLarge)

	head := mustGet(t, lf, first.ResourceID)
	body, _, readErr := lf.blobs.GetObject(context.Background(), testBucket, head.S3Key)
	require.NoError(t, readErr)
	assert.Equal(t, "id\n1\n", string(body))
	versions, err := lf.store.ListVersions(context.Background(), first.ResourceID)
	require.NoError(t, err)
	assert.Len(t, versions, 1)
}

// A deployment that keeps no version trail cannot offer this destination at all:
// the second landing would have nowhere to record the version it wrote.
func TestLandNeedsAVersionTrail(t *testing.T) {
	store := newMemStore()
	blobs := newMemBlobs()
	w := resourcewrite.New(resourcewrite.Deps{
		Store: metadataOnlyStore{inner: store}, Blobs: blobs, Bucket: testBucket, URIScheme: testScheme,
	})
	require.NotNil(t, w)
	lander := resourcewrite.NewLander(resourcewrite.LanderDeps{
		Writer: w, Claims: func(context.Context) resource.Claims { return analyst() },
	})

	_, err := lander.LandResource(context.Background(), orders(), strings.NewReader("id\n1\n"), "text/csv")

	require.Error(t, err)
	assert.ErrorIs(t, err, resourcewrite.ErrUnavailable)
	assert.Empty(t, blobs.objects)
}

// A run files its output in the library of the person it acts for, not under the
// principal it authenticates as (#1419).
func TestLandUnderARunFilesInTheAuthorsLibrary(t *testing.T) {
	lf := newLandFixture(t)

	out, err := lf.lander.Land(context.Background(), orders(),
		strings.NewReader("id\n1\n"), "text/csv", runFor(authorMail))

	require.NoError(t, err)
	assert.Equal(t, "mcp://user/"+authorMail+"/datasets/orders.csv", out.URI)
	assert.Equal(t, authorMail, mustGet(t, lf, out.ResourceID).UploaderEmail)
}

func TestNewLanderWithoutAWriter(t *testing.T) {
	assert.Nil(t, resourcewrite.NewLander(resourcewrite.LanderDeps{
		Claims: func(context.Context) resource.Claims { return analyst() },
	}))
}

// With no identity resolver a lander takes the one every request-driven surface
// uses, so a deployment cannot be assembled with a landing path that authorizes
// against nobody.
func TestALanderDefaultsToTheCallersOwnIdentity(t *testing.T) {
	f := newFixture(t)
	lander := resourcewrite.NewLander(resourcewrite.LanderDeps{Writer: f.writer})
	require.NotNil(t, lander)

	// A context with no platform identity is anonymous, and an anonymous caller
	// has no library of their own to file anything in.
	err := lander.CheckResourceDestination(context.Background(), orders())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scope_id is required")
}

// The holder every export tool is wired to answers for itself until the library
// exists, including when there is no holder at all.
func TestRefAnswersUntilItIsBound(t *testing.T) {
	var absent *resourcewrite.Ref
	unbound := &resourcewrite.Ref{}

	for name, ref := range map[string]*resourcewrite.Ref{"nil": absent, "unbound": unbound} {
		t.Run(name, func(t *testing.T) {
			err := ref.CheckResourceDestination(context.Background(), orders())
			assert.ErrorIs(t, err, resourcewrite.ErrNoLibrary)
			_, landErr := ref.LandResource(context.Background(), orders(), bytes.NewReader(nil), "text/csv")
			assert.ErrorIs(t, landErr, resourcewrite.ErrNoLibrary)
			_, runErr := ref.Land(context.Background(), orders(), bytes.NewReader(nil), "text/csv", analyst())
			assert.ErrorIs(t, runErr, resourcewrite.ErrNoLibrary)
		})
	}
	absent.Bind(nil) // a nil holder binds nothing rather than panicking
}

func TestRefLandsThroughWhatIsBound(t *testing.T) {
	lf := newLandFixture(t)
	ref := &resourcewrite.Ref{}
	ref.Bind(lf.lander)

	require.NoError(t, ref.CheckResourceDestination(context.Background(), orders()))
	out, err := ref.LandResource(context.Background(), orders(), strings.NewReader("id\n1\n"), "text/csv")
	require.NoError(t, err)
	assert.True(t, out.Created)

	runOut, err := ref.Land(context.Background(), orders(), strings.NewReader("id\n2\n"), "text/csv", analyst())
	require.NoError(t, err)
	assert.Equal(t, out.ResourceID, runOut.ResourceID)
	assert.Equal(t, 2, runOut.Version)
}

// A store that could not answer is not a store that answered "no": creating on a
// failed read would file a second file at an address that already has one.
func TestLandRefusesWhenTheAddressCannotBeRead(t *testing.T) {
	lf := newLandFixture(t)
	lf.store.getByURIErr = errors.New("connection reset")

	_, err := lf.lander.LandResource(context.Background(), orders(), strings.NewReader("id\n1\n"), "text/csv")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not read what is filed at")
	assert.Empty(t, lf.blobs.objects)
}
