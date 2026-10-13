package scriptsrc

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// library is a managed-resource store and version trail over a map, enough of
// both contracts for a reader: the methods a reader does not call are the
// embedded nil interfaces', and would panic if it did.
type library struct {
	resource.Store
	files    map[string]*resource.Resource
	versions map[string][]resource.Version
	getErr   error
}

func (l *library) Get(_ context.Context, id string) (*resource.Resource, error) {
	if l.getErr != nil {
		return nil, l.getErr
	}
	if r, ok := l.files[id]; ok {
		return r, nil
	}
	return nil, fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
}

func (l *library) GetByURI(_ context.Context, uri string) (*resource.Resource, error) {
	for _, r := range l.files {
		if r.URI == uri {
			return r, nil
		}
	}
	return nil, fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
}

func (*library) AddRevision(context.Context, resource.Revision) (*resource.Version, error) {
	return nil, errors.New("not used")
}

func (l *library) ListVersions(_ context.Context, id string) ([]resource.Version, error) {
	return l.versions[id], nil
}

func (l *library) GetVersion(_ context.Context, id string, version int) (*resource.Version, error) {
	for _, v := range l.versions[id] {
		if v.Version == version {
			return &v, nil
		}
	}
	return nil, fmt.Errorf("reading version: %w", sql.ErrNoRows)
}

func (*library) PruneVersions(context.Context, string, int) ([]resource.Version, error) {
	return nil, nil
}

type blobs struct {
	resource.S3Client
	objects map[string][]byte
}

func (b blobs) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	body, ok := b.objects[key]
	if !ok {
		return nil, "", errors.New("no such key")
	}
	return body, "text/plain", nil
}

const (
	pageJS = "res1"
	owner  = "jane@example.com"
)

func fixture() (*Reader, *library) {
	lib := &library{
		files: map[string]*resource.Resource{
			pageJS: {
				ID: pageJS, Scope: resource.ScopeUser, ScopeID: owner, Path: "flows/sync", Filename: "page.js",
				URI: "mcp://user/" + owner + "/flows/sync/page.js", S3Key: "k/v2", SizeBytes: 9,
				UploaderEmail: owner,
			},
			"bin": {
				ID: "bin", Scope: resource.ScopeGlobal, URI: "mcp://global/x/logo.png", S3Key: "k/bin", SizeBytes: 3,
			},
		},
		versions: map[string][]resource.Version{
			pageJS: {
				{ResourceID: pageJS, Version: 2, S3Key: "k/v2", SizeBytes: 9},
				{ResourceID: pageJS, Version: 1, S3Key: "k/v1", SizeBytes: 9},
			},
		},
	}
	b := blobs{objects: map[string][]byte{"k/v2": []byte("waitFor()"), "k/v1": []byte("oldPage()"), "k/bin": {0xff, 0xfe, 0x00}}}
	return New(lib, b, "bucket", ""), lib
}

var jane = resource.BuildClaims("sub-jane", owner, "analyst", []string{"analyst"}, false)

func TestResolveUnderTheCallersAccess(t *testing.T) {
	r, _ := fixture()
	res, err := r.Resolve(context.Background(), "mcp:resource:"+pageJS, jane)
	require.NoError(t, err)
	assert.Equal(t, pageJS, res.ID)

	res, err = r.Resolve(context.Background(), "mcp://user/"+owner+"/flows/sync/page.js", jane)
	require.NoError(t, err)
	assert.Equal(t, pageJS, res.ID)

	bob := resource.BuildClaims("sub-bob", "bob@example.com", "analyst", []string{"analyst"}, false)
	_, err = r.Resolve(context.Background(), "mcp:resource:"+pageJS, bob)
	assert.ErrorIs(t, err, ErrNotFound, "another person's file is not there")
	_, err = r.Resolve(context.Background(), "mcp:resource:gone", jane)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = r.Resolve(context.Background(), "page.js", jane)
	assert.ErrorContains(t, err, "not a managed-resource reference")
}

func TestResolveSurfacesAStoreFailure(t *testing.T) {
	r, lib := fixture()
	lib.getErr = errors.New("db down")
	_, err := r.Resolve(context.Background(), "mcp:resource:"+pageJS, jane)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}

func TestTextReadsAVersion(t *testing.T) {
	r, lib := fixture()
	res := lib.files[pageJS]
	head, err := r.Head(context.Background(), res)
	require.NoError(t, err)
	assert.Equal(t, 2, head)

	text, err := r.Text(context.Background(), res, 0, 1024)
	require.NoError(t, err)
	assert.Equal(t, "waitFor()", text)
	text, err = r.Text(context.Background(), res, 1, 1024)
	require.NoError(t, err)
	assert.Equal(t, "oldPage()", text, "a pinned version reads its own bytes")

	_, err = r.Text(context.Background(), res, 7, 1024)
	assert.ErrorIs(t, err, ErrNotFound, "a version no longer kept")
	_, err = r.Text(context.Background(), res, 0, 4)
	assert.ErrorContains(t, err, "more than the 4")
	_, err = r.Text(context.Background(), lib.files["bin"], 0, 1024)
	assert.ErrorContains(t, err, "not UTF-8 text")

	empty, err := r.Head(context.Background(), lib.files["bin"])
	require.NoError(t, err)
	assert.Zero(t, empty)
}

func TestSourceReadsTheHead(t *testing.T) {
	r, _ := fixture()
	text, err := r.Source(context.Background(), "mcp:resource:"+pageJS, jane, 1024)
	require.NoError(t, err)
	assert.Equal(t, "waitFor()", text)
	_, err = r.Source(context.Background(), "mcp:resource:gone", jane, 1024)
	assert.ErrorIs(t, err, ErrNotFound)

	var none *Reader
	_, err = none.Source(context.Background(), "mcp:resource:"+pageJS, jane, 1024)
	assert.ErrorContains(t, err, "no managed-resource library")
}

func TestNewWithoutALibrary(t *testing.T) {
	assert.Nil(t, New(nil, blobs{}, "b", ""))
	assert.Nil(t, New(&library{}, nil, "b", ""))
}

func TestByIDReadsAPinnedFileWhoeverAsks(t *testing.T) {
	r, lib := fixture()
	res, err := r.ByID(context.Background(), pageJS)
	require.NoError(t, err)
	assert.Equal(t, pageJS, res.ID)
	assert.Equal(t, "mcp", r.Scheme())

	_, err = r.ByID(context.Background(), "gone")
	assert.ErrorIs(t, err, ErrNotFound, "a pinned file deleted since reads as gone")
	lib.getErr = errors.New("db down")
	_, err = r.ByID(context.Background(), pageJS)
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrNotFound)
}
