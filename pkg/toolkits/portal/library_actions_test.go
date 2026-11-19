package portal

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/library"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	portalpkg "github.com/txn2/mcp-data-platform/pkg/portal"
)

type fakeLibrary struct {
	err        error
	restoreErr error
	userKey    string
	reach      *portaldomain.Reach
	scope      library.Scope
	restored   []string
}

func (f *fakeLibrary) Recents(_ context.Context, user string, r *portaldomain.Reach, _ int) ([]library.Item, error) {
	f.userKey, f.reach = user, r
	return []library.Item{{Kind: library.KindAsset, ID: "a1", Name: "Report"}}, f.err
}

func (f *fakeLibrary) Favorites(_ context.Context, user string, r *portaldomain.Reach, _ int) ([]library.Item, error) {
	f.userKey, f.reach = user, r
	return []library.Item{}, f.err
}

func (f *fakeLibrary) Trash(_ context.Context, scope library.Scope, _ time.Duration, _, _ int) (library.Page[library.TrashItem], error) {
	f.scope = scope
	return library.Page[library.TrashItem]{Items: []library.TrashItem{{Kind: library.KindAsset, ID: "gone"}}, Total: 1}, f.err
}

func (f *fakeLibrary) Restore(_ context.Context, kind library.Kind, id string) error {
	f.restored = append(f.restored, string(kind)+":"+id)
	return f.restoreErr
}

func libraryToolkit(lib Library) (*Toolkit, *inMemoryAssetStore, *inMemoryCollectionStore) {
	assets, colls := newInMemoryAssetStore(), newInMemoryCollectionStore()
	tk := New(Config{Name: "test", AssetStore: assets, CollectionStore: colls})
	tk.SetLibrary(lib, 30*24*time.Hour)
	return tk, assets, colls
}

func libCtx(admin bool) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: "u1", UserEmail: "owner@example.com", PersonaName: "analyst", IsAdmin: admin,
	})
}

func TestSetLibraryNilLeavesActionsUnknown(t *testing.T) {
	tk := New(Config{Name: "test"})
	tk.SetLibrary(nil, time.Hour)
	res, _, err := tk.handleManageAsset(libCtx(false), nil, manageAssetInput{Action: actionListRecents})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestListRecentsAndFavorites(t *testing.T) {
	lib := &fakeLibrary{}
	tk, _, _ := libraryToolkit(lib)

	res, _, err := tk.handleManageAsset(libCtx(false), nil, manageAssetInput{Action: actionListRecents, Limit: 5})
	require.NoError(t, err)
	require.False(t, res.IsError)
	out := extractJSON(t, res)
	assert.InDelta(t, 1, out["total"], 0)
	assert.Equal(t, "u1", lib.userKey)
	require.NotNil(t, lib.reach)
	assert.Equal(t, portaldomain.ScopeAll, lib.reach.Scope, "recents read everything the caller can open")
	assert.Equal(t, "analyst", lib.reach.Persona)

	res, _, err = tk.handleManageAsset(libCtx(false), nil, manageAssetInput{Action: actionListFavorites})
	require.NoError(t, err)
	assert.False(t, res.IsError)

	lib.err = errors.New("down")
	res, _, err = tk.handleManageAsset(libCtx(false), nil, manageAssetInput{Action: actionListFavorites})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

// A call carrying no person reads no library.
func TestLibraryActionsNeedAPerson(t *testing.T) {
	tk, _, _ := libraryToolkit(&fakeLibrary{})
	for _, action := range []string{actionListRecents, actionListTrash, actionRestore} {
		res, _, err := tk.handleManageAsset(context.Background(), nil, manageAssetInput{Action: action, AssetID: "a1"})
		require.NoError(t, err)
		assert.True(t, res.IsError, action)
	}
}

func TestListTrashIsTheCallersOwn(t *testing.T) {
	lib := &fakeLibrary{}
	tk, _, _ := libraryToolkit(lib)
	res, _, err := tk.handleManageAsset(libCtx(true), nil, manageAssetInput{Action: actionListTrash})
	require.NoError(t, err)
	require.False(t, res.IsError)
	assert.False(t, lib.scope.Everyone, "an administrator's agent lists its own trash")
	assert.Equal(t, "u1", lib.scope.Owner.UserID)
	out := extractJSON(t, res)
	assert.InDelta(t, 30, out["retention_days"], 0)

	lib.err = errors.New("down")
	res, _, err = tk.handleManageAsset(libCtx(false), nil, manageAssetInput{Action: actionListTrash})
	require.NoError(t, err)
	assert.True(t, res.IsError)
}

func TestRestore(t *testing.T) {
	lib := &fakeLibrary{}
	tk, assets, colls := libraryToolkit(lib)
	deleted := time.Now()
	assets.assets["gone"] = portalpkg.Asset{ID: "gone", OwnerID: "u1", OwnerEmail: "owner@example.com", DeletedAt: &deleted}
	assets.assets["theirs"] = portalpkg.Asset{ID: "theirs", OwnerID: "u9", OwnerEmail: "x@example.com", DeletedAt: &deleted}
	assets.assets["live"] = portalpkg.Asset{ID: "live", OwnerID: "u1"}
	colls.collections["c-gone"] = portalpkg.Collection{ID: "c-gone", OwnerID: "u1", DeletedAt: &deleted}
	colls.collections["c-live"] = portalpkg.Collection{ID: "c-live", OwnerID: "u1"}

	ok := func(ctx context.Context, in manageAssetInput) {
		t.Helper()
		res, _, err := tk.handleManageAsset(ctx, nil, in)
		require.NoError(t, err)
		assert.False(t, res.IsError, in)
	}
	refused := func(ctx context.Context, in manageAssetInput) {
		t.Helper()
		res, _, err := tk.handleManageAsset(ctx, nil, in)
		require.NoError(t, err)
		assert.True(t, res.IsError, in)
	}
	ok(libCtx(false), manageAssetInput{Action: actionRestore, AssetID: "gone"})
	ok(libCtx(false), manageAssetInput{Action: actionRestore, CollectionID: "c-gone"})
	ok(libCtx(true), manageAssetInput{Action: actionRestore, AssetID: "theirs"})
	assert.Equal(t, []string{"asset:gone", "collection:c-gone", "asset:theirs"}, lib.restored)

	refused(libCtx(false), manageAssetInput{Action: actionRestore, AssetID: "theirs"})
	refused(libCtx(false), manageAssetInput{Action: actionRestore, AssetID: "live"})
	refused(libCtx(false), manageAssetInput{Action: actionRestore, CollectionID: "c-live"})
	refused(libCtx(false), manageAssetInput{Action: actionRestore, AssetID: "missing"})
	refused(libCtx(false), manageAssetInput{Action: actionRestore})
	assert.Len(t, lib.restored, 3)

	lib.restoreErr = library.ErrNotDeleted
	refused(libCtx(false), manageAssetInput{Action: actionRestore, AssetID: "gone"})
	lib.restoreErr = errors.New("down")
	refused(libCtx(false), manageAssetInput{Action: actionRestore, AssetID: "gone"})
}
