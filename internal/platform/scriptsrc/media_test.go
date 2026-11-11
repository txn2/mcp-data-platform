package scriptsrc

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// listing is the library with a List that answers by folder, as a site mount
// checks (#2591).
type listing struct {
	*library
	listErr error
}

func (l listing) List(_ context.Context, f resource.Filter) ([]resource.Resource, int, error) {
	if l.listErr != nil {
		return nil, 0, l.listErr
	}
	var out []resource.Resource
	for _, r := range l.files {
		if r.Path == f.Path || len(r.Path) > len(f.Path) && r.Path[:len(f.Path)+1] == f.Path+"/" {
			out = append(out, *r)
		}
	}
	return out, len(out), nil
}

func TestCheckFolder_TheCallersLibraryAndAFolderItHolds(t *testing.T) {
	ctx := context.Background()
	_, lib := fixture()
	lib.files["bin"].Path = "x"
	r := New(listing{library: lib}, blobs{}, "bucket", "")
	mine := resource.ScopeFilter{Scope: resource.ScopeUser, ScopeID: "sub-jane"}

	require.NoError(t, r.CheckFolder(ctx, resource.ScopeFilter{Scope: resource.ScopeGlobal}, "/x/", jane), "a global folder holding a file")
	require.ErrorIs(t, r.CheckFolder(ctx, resource.ScopeFilter{Scope: resource.ScopeGlobal}, "empty", jane), ErrNotFound, "a folder holding nothing")
	require.ErrorIs(t, r.CheckFolder(ctx, resource.ScopeFilter{Scope: resource.ScopeUser, ScopeID: "someone-else"}, "flows", jane), ErrNotFound,
		"a library the caller cannot see reads as absent")
	require.ErrorIs(t, r.CheckFolder(ctx, mine, "nothing-here", jane), ErrNotFound)

	broken := New(listing{library: lib, listErr: errors.New("down")}, blobs{}, "bucket", "")
	require.ErrorContains(t, broken.CheckFolder(ctx, resource.ScopeFilter{Scope: resource.ScopeGlobal}, "x", jane), "down")
}

// folders is a library that knows its folders apart from the files in them,
// so an empty folder still exists. Of the folder contract a mount check reads
// only FolderExists; the rest is the embedded nil interface's.
type folders struct {
	listing
	resource.FolderStore
	exists map[string]bool
	err    error
}

func (f folders) FolderExists(_ context.Context, _ resource.ScopeFilter, folder string) (bool, error) {
	return f.exists[folder], f.err
}

func TestCheckFolder_AnEmptyFolderTheLibraryKnows(t *testing.T) {
	ctx := context.Background()
	_, lib := fixture()
	global := resource.ScopeFilter{Scope: resource.ScopeGlobal}
	r := New(folders{library: lib, exists: map[string]bool{"empty": true}}, blobs{}, "bucket", "")
	require.NoError(t, r.CheckFolder(ctx, global, "empty", jane), "a folder with no file yet, which the library holds")
	require.ErrorIs(t, r.CheckFolder(ctx, global, "never", jane), ErrNotFound, "falls back to the files, and finds none")

	broken := New(folders{library: lib, err: errors.New("down")}, blobs{}, "bucket", "")
	require.ErrorContains(t, broken.CheckFolder(ctx, global, "empty", jane), "down")
}

func TestAtPath_IsTheFileAMountedPathNames(t *testing.T) {
	ctx := context.Background()
	r, _ := fixture()
	got, err := r.AtPath(ctx, resource.ScopeFilter{Scope: resource.ScopeGlobal}, "x/logo.png")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "bin", got.ID)

	none, err := r.AtPath(ctx, resource.ScopeFilter{Scope: resource.ScopeGlobal}, "x/missing.png")
	require.NoError(t, err)
	assert.Nil(t, none, "no file is nil, not an error")

	lib := &library{files: map[string]*resource.Resource{}}
	failing := New(uriFailure{lib}, blobs{}, "bucket", "")
	_, err = failing.AtPath(ctx, resource.ScopeFilter{Scope: resource.ScopeGlobal}, "x")
	require.ErrorContains(t, err, "reading the managed resource")

	store, bucket := r.Blobs()
	assert.NotNil(t, store)
	assert.Equal(t, "bucket", bucket)
}

// aliasing answers a URI a file was moved from with the file, as the store's
// resource_uri_aliases fallback does.
type aliasing struct {
	*library
	aliases map[string]string // old URI -> resource id
}

func (a aliasing) GetByURI(ctx context.Context, uri string) (*resource.Resource, error) {
	if id, ok := a.aliases[uri]; ok {
		return a.files[id], nil
	}
	return a.library.GetByURI(ctx, uri)
}

func TestAtPath_ServesOnlyWhatTheFolderHoldsNow(t *testing.T) {
	ctx := context.Background()
	_, lib := fixture()
	// bin was filed at x/logo.png in Global and moved into jane's library;
	// moved was renamed within the folder.
	lib.files["bin"].Scope, lib.files["bin"].ScopeID = resource.ScopeUser, "sub-jane"
	lib.files["bin"].URI = "mcp://user/sub-jane/private/logo.png"
	lib.files["moved"] = &resource.Resource{ID: "moved", Scope: resource.ScopeGlobal, URI: "mcp://global/x/new.png"}
	// odd names a Global path but is filed in a persona library: never served.
	lib.files["odd"] = &resource.Resource{ID: "odd", Scope: resource.ScopePersona, ScopeID: "analyst", URI: "mcp://global/x/odd.png"}
	r := New(aliasing{library: lib, aliases: map[string]string{
		"mcp://global/x/logo.png": "bin", "mcp://global/x/old.png": "moved",
	}}, blobs{}, "bucket", "")
	global := resource.ScopeFilter{Scope: resource.ScopeGlobal}

	for _, tail := range []string{"x/logo.png", "x/old.png", "x/odd.png"} {
		got, err := r.AtPath(ctx, global, tail)
		require.NoError(t, err, tail)
		assert.Nil(t, got, "%s is not filed at that path now", tail)
	}
	got, err := r.AtPath(ctx, global, "x/new.png")
	require.NoError(t, err)
	require.NotNil(t, got, "a file moved within the folder is served at its new path")
	assert.Equal(t, "moved", got.ID)
}

func TestAtPath_ATailNoFileCanHaveIsNotLookedUp(t *testing.T) {
	ctx := context.Background()
	// The store fails every read, so a tail that reached it would be an error.
	r := New(uriFailure{&library{files: map[string]*resource.Resource{}}}, blobs{}, "bucket", "")
	for _, tail := range []string{"x/\xff.png", "x/a\x00b.png", "x/a\nb.png", "x/\u0085.png", "x/\x7f"} {
		got, err := r.AtPath(ctx, resource.ScopeFilter{Scope: resource.ScopeGlobal}, tail)
		require.NoError(t, err, "%q", tail)
		assert.Nil(t, got, "%q", tail)
	}
	assert.True(t, fileableTail("x/résumé café.png"), "any UTF-8 text a name holds")
}

type uriFailure struct{ *library }

func (uriFailure) GetByURI(context.Context, string) (*resource.Resource, error) {
	return nil, errors.New("down")
}
