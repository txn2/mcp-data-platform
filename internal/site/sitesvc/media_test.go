package sitesvc

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/site"
)

type fakeFolders struct {
	readable map[string]bool
	checked  []string
}

func (f *fakeFolders) CheckFolder(_ context.Context, m site.Mount) error {
	f.checked = append(f.checked, m.Folder)
	if !f.readable[m.Folder] {
		return errors.New("no such folder")
	}
	return nil
}

type fakeMedia struct {
	set   map[string][]site.Mount
	fail  error
	store *memStore
}

func (f *fakeMedia) SetMedia(_ context.Context, siteID string, mounts []site.Mount, _ string) error {
	if f.fail != nil {
		return f.fail
	}
	f.set[siteID] = mounts
	f.store.sites[siteID].Media = mounts
	return nil
}

// mediaParts is a service able to mount, and the fakes behind it.
type mediaParts struct {
	svc     *Service
	store   *memStore
	folders *fakeFolders
	media   *fakeMedia
}

func mediaFixture(t *testing.T) mediaParts {
	t.Helper()
	svc, store, _, _ := fixture(t)
	folders := &fakeFolders{readable: map[string]bool{"sites/m/media": true, "sites/m/video": true}}
	media := &fakeMedia{set: map[string][]site.Mount{}, store: store}
	svc.MediaFolders, svc.MediaStore = folders, media
	return mediaParts{svc: svc, store: store, folders: folders, media: media}
}

func TestUpdate_MountsAFolderTheCallerCanRead(t *testing.T) {
	ctx := context.Background()
	f := mediaFixture(t)
	svc, store, folders, media := f.svc, f.store, f.folders, f.media
	grant(store, manager.Email, site.LevelManage)
	mounts := []site.Mount{{Prefix: "media", Scope: "global", Folder: "sites/m/media"}}
	got, err := svc.Update(ctx, manager, "marketing", UpdateRequest{Media: &mounts})
	require.NoError(t, err, "anyone who manages the site mounts a folder they can read")
	require.Len(t, media.set[siteID], 1)
	assert.Equal(t, "/media/", media.set[siteID][0].Prefix)
	assert.Equal(t, []string{"sites/m/media"}, folders.checked)
	assert.Len(t, got.Media, 1)

	// Adding a second leaves the first unchecked: it is already in place.
	mounts = append(slices.Clone(store.sites[siteID].Media), site.Mount{Prefix: "/video/", Scope: "global", Folder: "sites/m/video"})
	_, err = svc.Update(ctx, manager, "marketing", UpdateRequest{Media: &mounts})
	require.NoError(t, err)
	assert.Equal(t, []string{"sites/m/media", "sites/m/video"}, folders.checked)

	none := []site.Mount{}
	_, err = svc.Update(ctx, manager, "marketing", UpdateRequest{Media: &none})
	require.NoError(t, err)
	assert.Empty(t, media.set[siteID], "[] removes every mount")
}

func TestUpdate_MountRefusals(t *testing.T) {
	ctx := context.Background()
	t.Run("a folder the caller cannot read", func(t *testing.T) {
		f := mediaFixture(t)
		svc, media := f.svc, f.media
		mounts := []site.Mount{{Prefix: "/m/", Scope: "global", Folder: "hr/salaries"}}
		_, err := svc.Update(ctx, owner, "marketing", UpdateRequest{Media: &mounts})
		require.ErrorIs(t, err, ErrNoFolder)
		assert.Contains(t, err.Error(), "that you can read")
		assert.Empty(t, media.set, "nothing was mounted")
	})
	t.Run("someone who does not manage the site", func(t *testing.T) {
		f := mediaFixture(t)
		svc, store := f.svc, f.store
		grant(store, viewer.Email, site.LevelView)
		mounts := []site.Mount{{Prefix: "/m/", Scope: "global", Folder: "sites/m/media"}}
		_, err := svc.Update(ctx, viewer, "marketing", UpdateRequest{Media: &mounts})
		require.ErrorIs(t, err, site.ErrNotAllowed)
	})
	t.Run("a prefix that is not one", func(t *testing.T) {
		svc := mediaFixture(t).svc
		mounts := []site.Mount{{Prefix: "/", Scope: "global", Folder: "sites/m/media"}}
		_, err := svc.Update(ctx, owner, "marketing", UpdateRequest{Media: &mounts})
		require.ErrorContains(t, err, "never its root")
	})
	t.Run("a deployment with no library", func(t *testing.T) {
		svc, _, _, _ := fixture(t)
		mounts := []site.Mount{{Prefix: "/m/", Scope: "global", Folder: "a"}}
		_, err := svc.Update(ctx, owner, "marketing", UpdateRequest{Media: &mounts})
		require.ErrorContains(t, err, "cannot mount media")
	})
	t.Run("the store refuses", func(t *testing.T) {
		f := mediaFixture(t)
		svc, media := f.svc, f.media
		media.fail = errors.New("down")
		mounts := []site.Mount{{Prefix: "/m/", Scope: "global", Folder: "sites/m/media"}}
		_, err := svc.Update(ctx, owner, "marketing", UpdateRequest{Media: &mounts})
		require.ErrorContains(t, err, "down")
	})
}
