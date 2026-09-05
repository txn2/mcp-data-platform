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
)

const (
	testBucket = "managed-resources"
	testScheme = "mcp"
	authorSub  = "user-1"
	authorMail = "analyst@example.com"
)

// analyst is an ordinary signed-in caller: no admin, no persona admin.
func analyst() resource.Claims {
	return resource.Claims{Sub: authorSub, Email: authorMail, Personas: []string{"analyst"}}
}

func admin() resource.Claims {
	return resource.Claims{Sub: "admin-1", Email: "admin@example.com", IsAdmin: true}
}

type fixture struct {
	writer     *resourcewrite.Writer
	store      *memStore
	blobs      *memBlobs
	registered []*resource.Resource
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{store: newMemStore(), blobs: newMemBlobs()}
	f.writer = resourcewrite.New(resourcewrite.Deps{
		Store: f.store, Blobs: f.blobs, Bucket: testBucket, URIScheme: testScheme,
		Registered: func(r *resource.Resource) { f.registered = append(f.registered, r) },
	})
	require.NotNil(t, f.writer)
	return f
}

// newResource is a valid create, which each test then varies one field of.
func newResource() resource.NewResource {
	return resource.NewResource{
		Scope: resource.ScopeUser, ScopeID: authorSub,
		Path: "datasets", Filename: "weather.csv",
		DisplayName: "Daily Weather", Description: "Highs and lows",
		Tags: []string{}, Content: bytes.NewReader([]byte("day,high\nmon,71\n")), MIMEType: "text/csv",
	}
}

func TestNewWithoutALayerToWriteInto(t *testing.T) {
	t.Run("no store", func(t *testing.T) {
		assert.Nil(t, resourcewrite.New(resourcewrite.Deps{Blobs: newMemBlobs()}))
	})
	t.Run("no blob client", func(t *testing.T) {
		assert.Nil(t, resourcewrite.New(resourcewrite.Deps{Store: newMemStore()}))
	})
	t.Run("a store with no revision trail still writes", func(t *testing.T) {
		w := resourcewrite.New(resourcewrite.Deps{
			Store: metadataOnlyStore{inner: newMemStore()}, Blobs: newMemBlobs(), Bucket: testBucket,
		})
		require.NotNil(t, w, "creating does not need a version trail; only replacing does")
	})
}

func TestCreateFilesTheResource(t *testing.T) {
	f := newFixture(t)

	res, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)

	assert.Equal(t, "mcp://user/"+authorSub+"/datasets/weather.csv", res.URI)
	assert.Equal(t, authorSub, res.UploaderSub)
	assert.Equal(t, authorMail, res.UploaderEmail)
	assert.Equal(t, int64(16), res.SizeBytes)

	stored, _, err := f.blobs.GetObject(t.Context(), testBucket, res.S3Key)
	require.NoError(t, err)
	assert.Equal(t, "day,high\nmon,71\n", string(stored))

	versions, err := f.store.ListVersions(t.Context(), res.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1, "the trail starts at the upload, not at the first revision")
	assert.Equal(t, authorMail, versions[0].UploaderEmail)

	require.Len(t, f.registered, 1, "a created resource is registered so clients see it without reconnecting")
	assert.Equal(t, res.ID, f.registered[0].ID)
}

func TestCreateRefusesAScopeTheCallerMayNotWrite(t *testing.T) {
	tests := []struct {
		name    string
		scope   resource.Scope
		scopeID string
		claims  resource.Claims
		names   string
	}{
		{"global is administrators only", resource.ScopeGlobal, "", analyst(), "global scope"},
		{"a persona needs its administrator", resource.ScopePersona, "finance", analyst(), `"finance" persona scope`},
		{"another user's scope", resource.ScopeUser, "someone-else", analyst(), "another user's scope"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			in := newResource()
			in.Scope, in.ScopeID = tc.scope, tc.scopeID

			_, err := f.writer.Create(t.Context(), in, tc.claims)

			require.ErrorIs(t, err, resourcewrite.ErrRefused)
			assert.Contains(t, err.Error(), tc.names,
				"the refusal names the scope, which is what the caller has to change")
			assert.True(t, strings.HasPrefix(err.Error(), "you cannot"),
				"the reader gets a sentence first, not the sentinel's category label")
			assert.NotContains(t, err.Error(), in.Filename,
				"the refusal is about where it was filed, not about the file")
			assert.Empty(t, f.blobs.objects, "a refused create writes nothing")
			assert.Empty(t, f.registered)
		})
	}
}

func TestCreateAdminReachesEveryScope(t *testing.T) {
	f := newFixture(t)
	in := newResource()
	in.Scope, in.ScopeID = resource.ScopeGlobal, ""

	res, err := f.writer.Create(t.Context(), in, admin())

	require.NoError(t, err)
	assert.Equal(t, "mcp://global/datasets/weather.csv", res.URI)
}

func TestCreateReportsAStorageRefusalRatherThanAWrite(t *testing.T) {
	f := newFixture(t)
	f.blobs.putErr = errors.New("bucket is read-only")

	_, err := f.writer.Create(t.Context(), newResource(), analyst())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "Nothing was saved")
	assert.Empty(t, f.registered, "nothing is announced when nothing was stored")
}

func TestCreateCleansUpTheBlobWhenTheRowDoesNotLand(t *testing.T) {
	f := newFixture(t)
	f.store.insertErr = errors.New("connection refused")

	_, err := f.writer.Create(t.Context(), newResource(), analyst())

	require.Error(t, err)
	assert.NotEmpty(t, f.blobs.deleted, "a create that leaves no row must leave no object either")
	assert.Empty(t, f.blobs.objects)
}

func TestCreateRefusesADuplicateURI(t *testing.T) {
	f := newFixture(t)
	_, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)

	_, err = f.writer.Create(t.Context(), newResource(), analyst())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

// mustCreate files one resource and returns it.
func mustCreate(t *testing.T, f *fixture) *resource.Resource {
	t.Helper()
	res, err := f.writer.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)
	return res
}

func TestReplaceKeepsTheFilesIdentity(t *testing.T) {
	f := newFixture(t)
	original := mustCreate(t, f)
	f.registered = nil

	updated, version, err := f.writer.Replace(t.Context(), original.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("day,high\nmon,88\ntue,90\n")), MIMEType: "text/csv",
		ChangeSummary: "refreshed from the hourly run",
	}, analyst())

	require.NoError(t, err)
	assert.Equal(t, 2, version)
	assert.Equal(t, original.ID, updated.ID, "the id is what every reference is keyed on")
	assert.Equal(t, original.URI, updated.URI, "the URI is what an asset's reference resolves through")
	assert.Equal(t, original.Filename, updated.Filename)
	assert.NotEqual(t, original.S3Key, updated.S3Key, "a revision gets its own object so the prior one stays readable")

	stored, _, err := f.blobs.GetObject(t.Context(), testBucket, updated.S3Key)
	require.NoError(t, err)
	assert.Equal(t, "day,high\nmon,88\ntue,90\n", string(stored))

	prior, _, err := f.blobs.GetObject(t.Context(), testBucket, original.S3Key)
	require.NoError(t, err)
	assert.Equal(t, "day,high\nmon,71\n", string(prior), "the version before it is still restorable")

	versions, err := f.store.ListVersions(t.Context(), original.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, "refreshed from the hourly run", versions[0].ChangeSummary)
	assert.Equal(t, authorMail, versions[0].UploaderEmail, "the author of the replacement is recorded")

	require.Len(t, f.registered, 1, "a replacement re-registers, or a client keeps serving the bytes it moved off")
}

func TestReplaceRefusesWhatTheCallerMayNotSeeAsAbsent(t *testing.T) {
	f := newFixture(t)
	in := newResource()
	in.Scope, in.ScopeID = resource.ScopeUser, "someone-else"
	res, err := f.writer.Create(t.Context(), in, admin())
	require.NoError(t, err)

	_, _, err = f.writer.Replace(t.Context(), res.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, analyst())

	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource,
		"a caller who may not see the file must not learn it exists")
}

func TestReplaceRefusesAMissingResource(t *testing.T) {
	f := newFixture(t)

	_, _, err := f.writer.Replace(t.Context(), "nope", resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, analyst())

	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)
}

func TestReplaceRefusesAFileTheCallerMaySeeButNotChange(t *testing.T) {
	f := newFixture(t)
	in := newResource()
	in.Scope, in.ScopeID = resource.ScopePersona, "analyst"
	res, err := f.writer.Create(t.Context(), in, admin())
	require.NoError(t, err)

	// The analyst is a member of the persona, so the file is readable, and is
	// not its administrator, so it is not theirs to change.
	_, _, err = f.writer.Replace(t.Context(), res.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, analyst())

	require.ErrorIs(t, err, resourcewrite.ErrRefused)
	assert.Contains(t, err.Error(), `"analyst" persona scope`)
}

func TestReplaceReportsADeploymentWithNoVersionTrail(t *testing.T) {
	store := newMemStore()
	w := resourcewrite.New(resourcewrite.Deps{
		Store: metadataOnlyStore{inner: store}, Blobs: newMemBlobs(), Bucket: testBucket, URIScheme: testScheme,
	})
	require.NotNil(t, w)
	res, err := w.Create(t.Context(), newResource(), analyst())
	require.NoError(t, err)

	_, _, err = w.Replace(t.Context(), res.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, analyst())

	require.ErrorIs(t, err, resourcewrite.ErrUnavailable)
	assert.Contains(t, err.Error(), "no version history")
}

func TestReplaceReportsARevisionThatDidNotLand(t *testing.T) {
	f := newFixture(t)
	res := mustCreate(t, f)
	f.store.addRevisionErr = errors.New("connection refused")
	f.registered = nil

	_, _, err := f.writer.Replace(t.Context(), res.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, analyst())

	require.Error(t, err)
	assert.NotErrorIs(t, err, resourcewrite.ErrRefused)
	assert.Empty(t, f.registered, "a failed revision announces nothing")
}

func TestGetReadsWhatTheCallerMaySee(t *testing.T) {
	f := newFixture(t)
	res := mustCreate(t, f)

	got, err := f.writer.Get(t.Context(), res.ID, analyst())
	require.NoError(t, err)
	assert.Equal(t, res.Filename, got.Filename)

	_, err = f.writer.Get(t.Context(), res.ID, resource.Claims{Sub: "stranger", Email: "s@example.com"})
	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)

	_, err = f.writer.Get(t.Context(), "missing", analyst())
	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)
}

// A store that could not answer is not a store that answered "no". A scheduled
// run told its file is gone stops refreshing it and says so in the run log;
// told the lookup failed, it retries on its next fire.
func TestAFailedReadIsNotAnAbsentResource(t *testing.T) {
	f := newFixture(t)
	f.store.getErr = errors.New("connection refused")

	_, err := f.writer.Get(t.Context(), "anything", analyst())
	require.Error(t, err)
	assert.NotErrorIs(t, err, resourcewrite.ErrNoSuchResource)
	assert.Contains(t, err.Error(), "connection refused")

	_, _, replaceErr := f.writer.Replace(t.Context(), "anything", resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, analyst())
	assert.NotErrorIs(t, replaceErr, resourcewrite.ErrNoSuchResource,
		"a replacement blocked by an unreachable store must not read as a deleted file either")
}

func TestScopePhraseNamesEveryScope(t *testing.T) {
	assert.Contains(t, resourcewrite.ScopePhrase(resource.ScopeGlobal, ""), "administrators only")
	assert.Contains(t, resourcewrite.ScopePhrase(resource.ScopePersona, "finance"), `"finance"`)
	assert.Equal(t, "another user's scope", resourcewrite.ScopePhrase(resource.ScopeUser, "x"))
	assert.Contains(t, resourcewrite.ScopePhrase(resource.Scope("odd"), ""), `"odd"`)
}

// A writer with no registration callback must still write: a stdio deployment
// has no list-changed notifier to install.
func TestWriteWithoutARegistrationCallback(t *testing.T) {
	store, blobs := newMemStore(), newMemBlobs()
	w := resourcewrite.New(resourcewrite.Deps{Store: store, Blobs: blobs, Bucket: testBucket})
	require.NotNil(t, w)

	res, err := w.Create(context.Background(), newResource(), analyst())
	require.NoError(t, err)
	_, version, err := w.Replace(context.Background(), res.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, analyst())
	require.NoError(t, err)
	assert.Equal(t, 2, version)
}

// --- A managed-script run (#1419, #1487) ---

// runFor is the claims a scheduled run of a script authored by the given person
// carries: a principal that owns nothing, and the author's address as the
// person it acts for.
func runFor(author string) resource.Claims {
	return resource.BuildClaims("script:weekly-refresh", author, "analyst", []string{"analyst"}, false).
		ActingFor(author)
}

// TestARunRefreshesTheFileItsAuthorUploaded is the script half of the
// acceptance, over the real writer and the real permission rules: a person
// uploads through the portal, which files the resource under their SUB, and
// their own scheduled script replaces its content without them.
func TestARunRefreshesTheFileItsAuthorUploaded(t *testing.T) {
	const author = "author@example.com"
	f := newFixture(t)

	// The portal upload: scoped to the person's subject, uploaded by them.
	uploaded := newResource()
	uploaded.Scope, uploaded.ScopeID = resource.ScopeUser, "sub-of-author"
	human := resource.Claims{Sub: "sub-of-author", Email: author}
	original, err := f.writer.Create(t.Context(), uploaded, human)
	require.NoError(t, err)

	// The scheduled run replaces its content.
	updated, version, err := f.writer.Replace(t.Context(), original.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("day,high\nmon,88\n")), MIMEType: "text/csv", ChangeSummary: "hourly refresh",
	}, runFor(author))

	require.NoError(t, err, "a run refused its author's own file cannot be said to act for them")
	assert.Equal(t, 2, version)
	assert.Equal(t, original.URI, updated.URI)

	versions, err := f.store.ListVersions(t.Context(), original.ID)
	require.NoError(t, err)
	assert.Equal(t, author, versions[1].UploaderEmail,
		"the revision is recorded against the person the run acted for")
}

func TestAnotherPersonsRunReachesNothingOfTheirs(t *testing.T) {
	f := newFixture(t)
	uploaded := newResource()
	uploaded.Scope, uploaded.ScopeID = resource.ScopeUser, "sub-of-author"
	original, err := f.writer.Create(t.Context(),
		uploaded, resource.Claims{Sub: "sub-of-author", Email: "author@example.com"})
	require.NoError(t, err)

	_, _, err = f.writer.Replace(t.Context(), original.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, runFor("someone-else@example.com"))

	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)
}

// TestARunRecordsThePersonItActedFor is what makes a scheduled refresh readable
// in the version history. A run carries its script OWNER's address for
// accountability and its version AUTHOR's roles, and after a transfer those are
// different people; the trail has to name the one whose authority ran, or the
// file lands in the author's library attributed to somebody who cannot see it.
func TestARunRecordsThePersonItActedFor(t *testing.T) {
	const author = "author@example.com"
	f := newFixture(t)
	transferred := resource.BuildClaims(
		"script:weekly-refresh", "owner@example.com", "analyst", []string{"analyst"}, false).ActingFor(author)

	in := newResource()
	in.Scope, in.ScopeID = resource.ScopeUser, author
	res, err := f.writer.Create(t.Context(), in, transferred)
	require.NoError(t, err)
	assert.Equal(t, author, res.UploaderEmail, "the create names the person whose authority ran")
	assert.Equal(t, "script:weekly-refresh", res.UploaderSub, "the principal is still what made the call")

	_, _, err = f.writer.Replace(t.Context(), res.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/plain",
	}, transferred)
	require.NoError(t, err)

	versions, err := f.store.ListVersions(t.Context(), res.ID)
	require.NoError(t, err)
	require.Len(t, versions, 2)
	assert.Equal(t, author, versions[0].UploaderEmail)
	assert.Equal(t, author, versions[1].UploaderEmail)
}

func TestARunWritesIntoItsAuthorsLibrary(t *testing.T) {
	const author = "author@example.com"
	f := newFixture(t)
	in := newResource()
	in.Scope, in.ScopeID = resource.ScopeUser, author

	res, err := f.writer.Create(t.Context(), in, runFor(author))

	require.NoError(t, err)
	assert.Equal(t, "mcp://user/"+author+"/datasets/weather.csv", res.URI)
	// The person, opening their own Resources page, sees it.
	assert.True(t, resource.CanReadResource(resource.Claims{Sub: "sub-of-author", Email: author}, res),
		"a file a run wrote must land where the person who scheduled it will look")
}

// --- A move does not revoke the script that maintains the file (#1576) ---

// uploadedThenMoved is the sequence the ticket describes, over the real writer,
// the real move and the real permission rules: a person uploads a CSV into
// their own library, and it is later refiled into another one by the caller
// given. It returns the resource as it stands after the move.
func uploadedThenMoved(
	t *testing.T, f *fixture, author resource.Claims, mover resource.Claims, to resource.Destination,
) *resource.Resource {
	t.Helper()
	in := newResource()
	in.Scope, in.ScopeID = resource.ScopeUser, author.Sub
	original, err := f.writer.Create(t.Context(), in, author)
	require.NoError(t, err)

	deps := resource.Deps{Store: f.store, S3Client: f.blobs, S3Bucket: testBucket, URIScheme: testScheme}
	uri, err := resource.MoveResource(t.Context(), deps, &mover, original, to)
	require.NoError(t, err, "the premise: the move itself is one the platform permits")
	require.NotEmpty(t, uri)

	moved, err := f.store.Get(t.Context(), original.ID)
	require.NoError(t, err)
	require.Equal(t, to.Scope, moved.Scope)
	return moved
}

// TestARunRefreshesAFileMovedIntoAPersonaTheAuthorBelongsTo is criterion 1. The
// person moves the CSV their own scheduled script refreshes into a persona
// library they are a member of -- a move CanMoveToLibrary deliberately permits
// without persona-admin authority -- and the next run has to go on refreshing
// it.
func TestARunRefreshesAFileMovedIntoAPersonaTheAuthorBelongsTo(t *testing.T) {
	f := newFixture(t)
	moved := uploadedThenMoved(t, f, analyst(), analyst(),
		resource.Destination{Scope: resource.ScopePersona, ScopeID: "analyst", Path: "datasets"})

	_, version, err := f.writer.Replace(t.Context(), moved.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("day,high\nmon,88\n")), MIMEType: "text/csv", ChangeSummary: "hourly refresh",
	}, runFor(authorMail))

	require.NoError(t, err, "the move left the person able to replace the content; their script must be too")
	assert.Equal(t, 2, version)
}

// TestARunRefreshesAFileAnAdministratorPublishedForItsAuthor is criterion 2:
// the same move made to the global library by an administrator on behalf of a
// non-administrator author, whose script must go on writing it.
func TestARunRefreshesAFileAnAdministratorPublishedForItsAuthor(t *testing.T) {
	f := newFixture(t)
	moved := uploadedThenMoved(t, f, analyst(), admin(),
		resource.Destination{Scope: resource.ScopeGlobal, Path: "datasets"})

	_, version, err := f.writer.Replace(t.Context(), moved.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("day,high\nmon,91\n")), MIMEType: "text/csv",
	}, runFor(authorMail))

	require.NoError(t, err, "who made the move must not decide whether the author's automation survives it")
	assert.Equal(t, 2, version)
}

// TestAMovedFileIsRefusedToAScriptWithNoClaimOnIt is criterion 3's other half:
// a run whose author never uploaded the file and holds no authority over the
// library it now sits in is refused, and the refusal names the library.
func TestAMovedFileIsRefusedToAScriptWithNoClaimOnIt(t *testing.T) {
	f := newFixture(t)
	moved := uploadedThenMoved(t, f, analyst(), admin(),
		resource.Destination{Scope: resource.ScopeGlobal, Path: "datasets"})

	_, _, err := f.writer.Replace(t.Context(), moved.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/csv",
	}, runFor("stranger@example.com"))

	require.ErrorIs(t, err, resourcewrite.ErrRefused)
	assert.Contains(t, err.Error(), "the global scope",
		"a refusal a scheduled run logs has to name what it was refused")
}

// TestAMoveWidensNothingARunCanSee is criterion 4 over the real writer: the
// modify predicate is not the visibility gate, and Replace reads the file
// through CanAccessResource before it asks whether it may be changed. A persona
// library the author does not belong to therefore stays absent to their script,
// uploader arm or not.
//
// The person is refused the same read, which is the pairing the uploader arm is
// held to: a surface that reads before it writes refuses both of them here, and
// one that authorizes on modify alone admits both.
func TestAMoveWidensNothingARunCanSee(t *testing.T) {
	f := newFixture(t)
	moved := uploadedThenMoved(t, f, analyst(), admin(),
		resource.Destination{Scope: resource.ScopePersona, ScopeID: "finance", Path: "datasets"})

	_, _, err := f.writer.Replace(t.Context(), moved.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/csv",
	}, runFor(authorMail))

	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource)

	_, _, err = f.writer.Replace(t.Context(), moved.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/csv",
	}, analyst())
	require.ErrorIs(t, err, resourcewrite.ErrNoSuchResource,
		"the person is answered the same way, which is what makes the pair one grant")
}

// TestARunRefreshesAFileItsAuthorsOtherScriptFiled is the row the two holders
// record differently, over the real writer: a run filed it, so its subject is
// the principal and its address is the author. The author replaces its content
// after a move, and so does a DIFFERENT script of theirs -- neither more nor
// less than the other.
func TestARunRefreshesAFileItsAuthorsOtherScriptFiled(t *testing.T) {
	f := newFixture(t)
	in := newResource()
	in.Scope, in.ScopeID = resource.ScopeUser, authorMail
	filed, err := f.writer.Create(t.Context(), in, runFor(authorMail))
	require.NoError(t, err)
	require.Equal(t, "script:weekly-refresh", filed.UploaderSub, "the premise: the principal filed it")
	require.Equal(t, authorMail, filed.UploaderEmail)

	deps := resource.Deps{Store: f.store, S3Client: f.blobs, S3Bucket: testBucket, URIScheme: testScheme}
	mover := admin()
	_, err = resource.MoveResource(t.Context(), deps, &mover, filed,
		resource.Destination{Scope: resource.ScopeGlobal, Path: "datasets"})
	require.NoError(t, err)

	_, version, err := f.writer.Replace(t.Context(), filed.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("day,high\nmon,70\n")), MIMEType: "text/csv",
	}, analyst())
	require.NoError(t, err, "the person whose authority filed it may still replace it")
	assert.Equal(t, 2, version)

	sibling := resource.BuildClaims("script:monthly-rollup", authorMail, "analyst",
		[]string{"analyst"}, false).ActingFor(authorMail)
	_, version, err = f.writer.Replace(t.Context(), filed.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("day,high\nmon,71\n")), MIMEType: "text/csv",
	}, sibling)
	require.NoError(t, err, "and their other scripts reach exactly what they reach")
	assert.Equal(t, 3, version)

	stranger := resource.BuildClaims("script:weekly-refresh", "stranger@example.com", "analyst",
		[]string{"analyst"}, false).ActingFor("stranger@example.com")
	_, _, err = f.writer.Replace(t.Context(), filed.ID, resource.RevisionUpload{
		Content: bytes.NewReader([]byte("x")), MIMEType: "text/csv",
	}, stranger)
	require.ErrorIs(t, err, resourcewrite.ErrRefused,
		"another person's script of the same name is another person's script")
}
