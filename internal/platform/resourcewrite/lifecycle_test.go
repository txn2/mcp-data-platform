package resourcewrite_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// weatherAddress is the address newResource() files its file at.
func weatherAddress() toolkit.ResourceAddress {
	return toolkit.ResourceAddress{
		Scope: string(resource.ScopeUser), ScopeID: authorSub, Path: "datasets", Filename: "weather.csv",
	}
}

func TestLocateFindsTheFileFiledAtAnAddress(t *testing.T) {
	f := newFixture(t)
	created, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)

	found, uri, err := f.writer.Locate(t.Context(), weatherAddress(), analyst())
	require.NoError(t, err)
	require.NotNil(t, found)
	assert.Equal(t, created.ID, found.ID, "the address resolves to the file the create made")
	assert.Equal(t, created.URI, uri)
}

func TestLocateReportsAnEmptyAddressAsAnAnswer(t *testing.T) {
	f := newFixture(t)

	found, uri, err := f.writer.Locate(t.Context(), weatherAddress(), analyst())

	require.NoError(t, err, "nothing being filed there is an answer, not a failure")
	assert.Nil(t, found)
	assert.Equal(t, testScheme+"://user/"+authorSub+"/datasets/weather.csv", uri,
		"the address is named even when it holds nothing, so the caller can say what it asked about")
}

func TestLocateFollowsTheAliasAMoveLeftBehind(t *testing.T) {
	f := newFixture(t)
	created, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)
	moved := toolkit.ResourceAddress{
		Scope: string(resource.ScopeUser), ScopeID: authorSub, Path: "archive", Filename: "weather.csv",
	}
	require.NoError(t, f.store.Move(t.Context(), []resource.Move{{
		ID: created.ID, Scope: resource.ScopeUser, ScopeID: authorSub, Path: "archive",
		URI: resource.BuildURI(testScheme, resource.ScopeUser, authorSub, "archive", "weather.csv"),
	}}))

	fromOldAddress, _, err := f.writer.Locate(t.Context(), weatherAddress(), analyst())
	require.NoError(t, err)
	require.NotNil(t, fromOldAddress, "the address a citation names keeps resolving after a move")
	assert.Equal(t, created.ID, fromOldAddress.ID)

	fromNewAddress, _, err := f.writer.Locate(t.Context(), moved, analyst())
	require.NoError(t, err)
	require.NotNil(t, fromNewAddress)
	assert.Equal(t, created.ID, fromNewAddress.ID)
}

func TestLocateHidesAFileTheCallerCannotSee(t *testing.T) {
	f := newFixture(t)
	_, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)

	found, _, err := f.writer.Locate(t.Context(), weatherAddress(),
		resource.Claims{Sub: "someone-else", Email: "other@example.com"})

	require.NoError(t, err)
	assert.Nil(t, found, "a file the caller may not see reads as nothing being there")
}

func TestLocateRefusesAnAddressThatIsNotOne(t *testing.T) {
	f := newFixture(t)

	_, _, err := f.writer.Locate(t.Context(), toolkit.ResourceAddress{
		Scope: string(resource.ScopeUser), ScopeID: authorSub, Path: "", Filename: "weather.csv",
	}, analyst())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "path is required")
}

func TestLocateReportsAFailedReadRatherThanAnEmptyAddress(t *testing.T) {
	f := newFixture(t)
	f.store.getByURIErr = errors.New("connection reset")

	_, _, err := f.writer.Locate(t.Context(), weatherAddress(), analyst())

	require.Error(t, err, "a store that could not answer is not a store that answered no")
	assert.Contains(t, err.Error(), "could not read what is filed at")
}

// createAt files one resource at a path, for the listing tests.
func createAt(t *testing.T, f *fixture, path, filename string) *resource.Resource {
	t.Helper()
	in := newResource()
	in.Path, in.Filename = path, filename
	in.Content = bytes.NewReader([]byte("day,high\nmon,71\n"))
	res, err := f.writer.Create(t.Context(), in, analyst())
	require.NoError(t, err)
	return res
}

func TestListIsRootedAtTheFolderAndIncludesWhatIsBeneathIt(t *testing.T) {
	f := newFixture(t)
	createAt(t, f, "datasets", "a.csv")
	createAt(t, f, "datasets/weather", "b.csv")
	createAt(t, f, "runbooks", "c.csv")

	found, total, err := f.writer.List(t.Context(), toolkit.ResourceQuery{Path: "datasets"}, analyst())

	require.NoError(t, err)
	assert.Equal(t, 2, total, "the folder and everything beneath it, and nothing beside it")
	require.Len(t, found, 2)
	for _, r := range found {
		assert.True(t, resource.PathUnder(r.Path, "datasets"), "listed %s", r.Path)
	}
}

func TestListWithNoPathIsTheWholeLibrary(t *testing.T) {
	f := newFixture(t)
	createAt(t, f, "datasets", "a.csv")
	createAt(t, f, "runbooks", "c.csv")

	_, total, err := f.writer.List(t.Context(), toolkit.ResourceQuery{}, analyst())

	require.NoError(t, err)
	assert.Equal(t, 2, total)
}

func TestListShowsOnlyTheLibrariesTheCallerCanSee(t *testing.T) {
	f := newFixture(t)
	createAt(t, f, "datasets", "a.csv")

	_, total, err := f.writer.List(t.Context(), toolkit.ResourceQuery{},
		resource.Claims{Sub: "someone-else", Email: "other@example.com"})

	require.NoError(t, err)
	assert.Zero(t, total, "another person's library is not listed")
}

func TestListPagesTheAnswer(t *testing.T) {
	f := newFixture(t)
	createAt(t, f, "datasets", "a.csv")
	createAt(t, f, "datasets", "b.csv")
	createAt(t, f, "datasets", "c.csv")

	first, total, err := f.writer.List(t.Context(), toolkit.ResourceQuery{Path: "datasets", Limit: 2}, analyst())
	require.NoError(t, err)
	assert.Equal(t, 3, total, "the total counts everything under the folder, not the page")
	require.Len(t, first, 2)

	second, _, err := f.writer.List(t.Context(),
		toolkit.ResourceQuery{Path: "datasets", Limit: 2, Offset: 2}, analyst())
	require.NoError(t, err)
	assert.Len(t, second, 1)
}

func TestListRefusesAPathThatIsNotOne(t *testing.T) {
	f := newFixture(t)

	_, _, err := f.writer.List(t.Context(), toolkit.ResourceQuery{Path: "/leading-slash"}, analyst())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must not start or end with")
}

func TestListCapsAnUnboundedRequest(t *testing.T) {
	f := newFixture(t)
	createAt(t, f, "datasets", "a.csv")

	_, _, err := f.writer.List(t.Context(),
		toolkit.ResourceQuery{Limit: resourcewrite.DefaultListLimit * 10, Offset: -3}, analyst())

	require.NoError(t, err, "an out-of-range page is clamped rather than refused")
}

func TestDeleteRemovesTheRecordTheContentAndTheVersionBlobs(t *testing.T) {
	f := newFixture(t)
	created, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)
	revised, _, err := f.writer.Replace(t.Context(), created.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("day,high\ntue,80\n")), MIMEType: "text/csv",
		ChangeSummary: "second pull",
	}, analyst())
	require.NoError(t, err)
	require.NotEqual(t, created.S3Key, revised.S3Key, "a revision keys the new blob on its own directory")

	var unregistered []string
	f.writer = resourcewrite.New(resourcewrite.Deps{
		Store: f.store, Blobs: f.blobs, Bucket: testBucket, URIScheme: testScheme,
		Unregistered: func(uri string) { unregistered = append(unregistered, uri) },
	})

	deleted, err := f.writer.Delete(t.Context(), created.ID, analyst())
	require.NoError(t, err)
	assert.Equal(t, created.ID, deleted.ID)

	_, err = f.writer.Get(t.Context(), created.ID, analyst())
	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)
	assert.Empty(t, f.blobs.objects, "neither the head object nor the superseded one is left behind")
	assert.Equal(t, []string{created.URI}, unregistered,
		"a client that has already listed must stop being offered a file that is gone")
}

func TestDeleteRefusesACallerWhoMayNotChangeTheFile(t *testing.T) {
	f := newFixture(t)
	in := newResource()
	in.Scope, in.ScopeID = resource.ScopeGlobal, ""
	created, err := f.writer.Create(t.Context(), in, admin())
	require.NoError(t, err)

	_, err = f.writer.Delete(t.Context(), created.ID, analyst())

	require.ErrorIs(t, err, resourcewrite.ErrRefused)
	assert.Contains(t, err.Error(), "global scope")
	_, err = f.writer.Get(t.Context(), created.ID, analyst())
	require.NoError(t, err, "the refused delete left the file where it was")
}

func TestDeleteOfSomethingThatIsNotThere(t *testing.T) {
	f := newFixture(t)

	_, err := f.writer.Delete(t.Context(), "res-nope", analyst())

	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)
}

func TestDeleteReportsAContentFailureWithoutRemovingTheRecord(t *testing.T) {
	f := newFixture(t)
	created, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)
	f.blobs.deleteErr = errors.New("bucket unreachable")

	_, err = f.writer.Delete(t.Context(), created.ID, analyst())

	require.Error(t, err)
	require.ErrorIs(t, err, resource.ErrDeleteContent)
	_, err = f.writer.Get(t.Context(), created.ID, analyst())
	require.NoError(t, err, "the record still points at content that is still there")
}

func TestListReportsAFailedRead(t *testing.T) {
	f := newFixture(t)
	f.store.listErr = errors.New("connection reset")

	_, _, err := f.writer.List(t.Context(), toolkit.ResourceQuery{}, analyst())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "could not list the managed resources")
}

func TestDeleteSurvivesAnUnreadableVersionTrail(t *testing.T) {
	f := newFixture(t)
	created, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)
	f.store.listVersionsErr = errors.New("db down")

	_, err = f.writer.Delete(t.Context(), created.ID, analyst())

	require.NoError(t, err, "an unreadable trail must not make a resource undeletable")
	_, err = f.writer.Get(t.Context(), created.ID, analyst())
	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)
}
