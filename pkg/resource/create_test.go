package resource

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newResourceInput is a valid create, which each test varies one field of.
func newResourceInput() NewResource {
	return NewResource{
		Scope: ScopeGlobal, Path: "samples", Filename: "weather.csv",
		DisplayName: "Daily Weather", Description: "Highs and lows",
		Tags: []string{}, Content: bytes.NewReader([]byte("day,high\nmon,71\n")), MIMEType: "text/csv",
	}
}

func createClaims() *Claims {
	return &Claims{Sub: "user-1", Email: "analyst@example.com"}
}

// TestCreateResourceOffHTTP covers the create path as a caller that is not the
// upload route reaches it: no request, no multipart form, just the record and
// its bytes. It is the entry point manage_resource and any other non-browser
// writer goes through, so it is exercised here rather than only through the
// handler.
func TestCreateResourceOffHTTP(t *testing.T) {
	store, s3 := newMockStore(), newMockS3()
	deps := Deps{Store: store, S3Client: s3, S3Bucket: "b", URIScheme: "mcp"}

	res, err := CreateResource(t.Context(), deps, createClaims(), newResourceInput())

	require.NoError(t, err)
	assert.Equal(t, "mcp://global/samples/weather.csv", res.URI)
	assert.Equal(t, "user-1", res.UploaderSub)
	assert.Equal(t, int64(16), res.SizeBytes)
	assert.Equal(t, "day,high\nmon,71\n", string(s3.objects[res.S3Key]))
}

func TestCreateResourceFallsBackToTheDefaultURIScheme(t *testing.T) {
	deps := Deps{Store: newMockStore(), S3Client: newMockS3(), S3Bucket: "b"}

	res, err := CreateResource(t.Context(), deps, createClaims(), newResourceInput())

	require.NoError(t, err)
	assert.Equal(t, DefaultURIScheme+"://global/samples/weather.csv", res.URI,
		"a caller that names no scheme gets the platform's, not an empty one")
}

func TestCreateResourceHonoursAConfiguredURIScheme(t *testing.T) {
	deps := Deps{Store: newMockStore(), S3Client: newMockS3(), S3Bucket: "b", URIScheme: "custom"}

	res, err := CreateResource(t.Context(), deps, createClaims(), newResourceInput())

	require.NoError(t, err)
	assert.Equal(t, "custom://global/samples/weather.csv", res.URI)
}

func TestCreateResourceWithoutBlobStorage(t *testing.T) {
	store := newMockStore()

	res, err := CreateResource(t.Context(), Deps{Store: store, URIScheme: "mcp"}, createClaims(), newResourceInput())

	require.NoError(t, err, "a deployment with no blob client still records the row, as the upload route does")
	assert.NotEmpty(t, res.S3Key)
}

// duplicateInsertStore answers the way Postgres does when the scope/category/
// filename unique index rejects a row.
type duplicateInsertStore struct{ mockStore }

func (*duplicateInsertStore) Insert(context.Context, Resource) error {
	return fmt.Errorf("pq: duplicate key value violates unique constraint %q", "resources_uri_key")
}

func TestCreateResourceReportsADuplicateAsAConflict(t *testing.T) {
	s3 := newMockS3()
	deps := Deps{
		Store: &duplicateInsertStore{mockStore: *newMockStore()}, S3Client: s3, S3Bucket: "b", URIScheme: "mcp",
	}

	_, err := CreateResource(t.Context(), deps, createClaims(), newResourceInput())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Empty(t, s3.objects, "a create that leaves no row must leave no object either")
}

// recordingVersions captures the revision a create records as version 1.
type recordingVersions struct {
	mockStore
	recorded []Revision
	err      error
}

func (r *recordingVersions) AddRevision(_ context.Context, rev Revision) (*Version, error) {
	r.recorded = append(r.recorded, rev)
	if r.err != nil {
		return nil, r.err
	}
	return &Version{ResourceID: rev.ResourceID, Version: len(r.recorded), S3Key: rev.S3Key}, nil
}

func (*recordingVersions) ListVersions(context.Context, string) ([]Version, error) { return nil, nil }

func (*recordingVersions) GetVersion(context.Context, string, int) (*Version, error) {
	return nil, errors.New("not found")
}

func (*recordingVersions) PruneVersions(context.Context, string, int) ([]Version, error) {
	return nil, nil
}

func TestCreateResourceStartsTheVersionTrailAtTheUpload(t *testing.T) {
	versions := &recordingVersions{mockStore: *newMockStore()}
	deps := Deps{
		Store: versions, Versions: versions, S3Client: newMockS3(), S3Bucket: "b", URIScheme: "mcp",
	}

	res, err := CreateResource(t.Context(), deps, createClaims(), newResourceInput())

	require.NoError(t, err)
	require.Len(t, versions.recorded, 1)
	assert.Equal(t, res.ID, versions.recorded[0].ResourceID)
	assert.Equal(t, res.S3Key, versions.recorded[0].S3Key)
	assert.Equal(t, "analyst@example.com", versions.recorded[0].UploaderEmail)
}

func TestCreateResourceSurvivesAnUnrecordableFirstVersion(t *testing.T) {
	versions := &recordingVersions{mockStore: *newMockStore(), err: errors.New("connection refused")}
	deps := Deps{
		Store: versions, Versions: versions, S3Client: newMockS3(), S3Bucket: "b", URIScheme: "mcp",
	}

	res, err := CreateResource(t.Context(), deps, createClaims(), newResourceInput())

	require.NoError(t, err, "the resource exists and is usable; a missing v1 row is a repairable gap, not a failure")
	assert.NotEmpty(t, res.ID)
}
