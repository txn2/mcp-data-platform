//go:build integration

package appstore

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptstore"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/internal/portal/portalstore"
	"github.com/txn2/mcp-data-platform/internal/portal/portalversions"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const owner = "jane@example.com"

func newScript(ctx context.Context, t *testing.T, s *scriptstore.Store, name string) *script.Script {
	t.Helper()
	sc := &script.Script{Name: name, Source: "def main():\n    return 1\n", OwnerEmail: owner, Enabled: true}
	require.NoError(t, s.Create(ctx, sc, script.Author{Email: owner}))
	return sc
}

func saveVersion(ctx context.Context, t *testing.T, s *scriptstore.Store, sc *script.Script, src string) {
	t.Helper()
	sc.Source = src
	sc.Version++
	require.NoError(t, s.UpdateWithVersion(ctx, sc, script.Author{Email: owner}))
}

func newAsset(ctx context.Context, t *testing.T, assets portaldomain.AssetStore, versions portaldomain.VersionStore, n int) string {
	t.Helper()
	id := uuid.NewString()
	require.NoError(t, assets.Insert(ctx, portaldomain.Asset{
		ID: id, OwnerID: "u1", OwnerEmail: owner, Name: "storefront", ContentType: "text/html",
		S3Bucket: "portal", S3Key: "k0", Tags: []string{},
	}))
	for i := 1; i <= n; i++ {
		_, err := versions.CreateVersion(ctx, portaldomain.AssetVersion{
			ID: uuid.NewString(), AssetID: id, S3Key: "k" + string(rune('0'+i)), S3Bucket: "portal",
			ContentType: "text/html", SizeBytes: int64(i), CreatedBy: owner,
		})
		require.NoError(t, err)
	}
	return id
}

// A release holds each pinned part at its version, so a run executes and the
// portal serves that version while a newer one is saved; a part removed from
// the application, or an application deleted, lets go of it.
func TestRealDB_ReleaseHoldsPartsAtTheirPinnedVersions(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s, scripts := New(db), scriptstore.New(db)
	assets := portalstore.NewPostgresAssetStore(db, nil)
	versions := portalversions.NewPostgres(db, nil, nil, nil)

	app := &application.Application{Name: "shop", OwnerEmail: owner, CreatedBy: owner, Connection: "orders", Schema: "shop"}
	require.NoError(t, s.Create(ctx, app))
	require.ErrorIs(t, s.Create(ctx, &application.Application{Name: "shop", OwnerEmail: owner, CreatedBy: owner}), application.ErrNameTaken)
	require.ErrorIs(t, s.Create(ctx, &application.Application{
		Name: "other", OwnerEmail: owner, CreatedBy: owner,
		Connection: "orders", Schema: "shop",
	}), application.ErrSchemaHeld, "one application per schema")

	api := newScript(ctx, t, scripts, "shop-api")
	assetID := newAsset(ctx, t, assets, versions, 1)
	require.NoError(t, s.AddPart(ctx, app.ID, application.Part{Kind: application.KindScript, ID: api.ID, AddedBy: owner}))
	require.NoError(t, s.AddPart(ctx, app.ID, application.Part{Kind: application.KindAsset, ID: assetID, AddedBy: owner}))

	other := &application.Application{Name: "other", OwnerEmail: owner, CreatedBy: owner}
	require.NoError(t, s.Create(ctx, other))
	require.ErrorIs(t, s.AddPart(ctx, other.ID, application.Part{Kind: application.KindScript, ID: api.ID, AddedBy: owner}),
		application.ErrPartTaken, "a part belongs to one application")
	held, err := s.Owning(ctx, application.KindScript, api.ID)
	require.NoError(t, err)
	assert.Equal(t, "shop", held.Name)

	rel, err := s.Record(ctx, app.ID, application.Recorded{Release: application.Release{
		Action: application.ActionRelease, CreatedBy: "admin@example.com", MigrationVersion: 2,
		Pins: []application.Pin{
			{Kind: application.KindScript, PartID: api.ID, Version: 1},
			{Kind: application.KindAsset, PartID: assetID, Version: 1},
		},
	}})
	require.NoError(t, err)
	assert.Equal(t, 1, rel.Number)

	saveVersion(ctx, t, scripts, api, "def main():\n    return 2\n")
	got, err := scripts.GetByID(ctx, api.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Version, "the save made a newer version")
	assert.Equal(t, 1, got.RunVersion(), "a run executes the released version")
	_, err = versions.CreateVersion(ctx, portaldomain.AssetVersion{
		ID: uuid.NewString(), AssetID: assetID, S3Key: "k2", S3Bucket: "portal", ContentType: "text/html", CreatedBy: owner,
	})
	require.NoError(t, err)
	a, err := assets.Get(ctx, assetID)
	require.NoError(t, err)
	assert.Equal(t, 2, a.CurrentVersion)
	assert.Equal(t, 1, a.ServedVersion(), "the portal serves the released version")
	served, err := portaldomain.Served(ctx, versions, a)
	require.NoError(t, err)
	assert.Equal(t, "k1", served.S3Key)

	_, err = s.Record(ctx, app.ID, application.Recorded{After: 0, Release: application.Release{Action: application.ActionRelease, CreatedBy: "x"}})
	require.ErrorIs(t, err, application.ErrStale, "a release decided against a moved current release changes nothing")

	rel2, err := s.Record(ctx, app.ID, application.Recorded{After: 1, Release: application.Release{
		Action: application.ActionRelease, CreatedBy: "admin@example.com",
		Pins: []application.Pin{{Kind: application.KindScript, PartID: api.ID, Version: 2}},
	}})
	require.NoError(t, err)
	assert.Equal(t, 2, rel2.Number)
	got, _ = scripts.GetByID(ctx, api.ID)
	assert.Equal(t, 2, got.RunVersion())
	a, _ = assets.Get(ctx, assetID)
	assert.Equal(t, 0, a.ReleasedVersion, "a part the release does not pin is let go")

	list, err := s.Releases(ctx, app.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, 2, list[0].Number, "newest first")
	assert.Equal(t, 1, list[1].PinOf(application.KindAsset, assetID))
	one, err := s.Release(ctx, app.ID, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, one.MigrationVersion)
	_, err = s.Release(ctx, app.ID, 9)
	require.ErrorIs(t, err, application.ErrNoRelease)

	require.NoError(t, s.RemovePart(ctx, app.ID, application.KindScript, api.ID))
	got, _ = scripts.GetByID(ctx, api.ID)
	assert.Equal(t, 0, got.ReleasedVersion, "a removed part runs its newest version again")
	require.ErrorIs(t, s.RemovePart(ctx, app.ID, application.KindScript, api.ID), application.ErrNoPart)

	require.NoError(t, s.AddPart(ctx, app.ID, application.Part{Kind: application.KindScript, ID: api.ID, AddedBy: owner}))
	_, err = s.Record(ctx, app.ID, application.Recorded{After: 2, Release: application.Release{
		Action: application.ActionRelease, CreatedBy: "admin@example.com",
		Pins: []application.Pin{{Kind: application.KindScript, PartID: api.ID, Version: 1}},
	}})
	require.NoError(t, err)
	require.NoError(t, s.Delete(ctx, app.ID))
	got, _ = scripts.GetByID(ctx, api.ID)
	assert.Equal(t, 0, got.ReleasedVersion, "a deleted application lets go of its parts")
	_, err = s.Get(ctx, app.ID)
	require.ErrorIs(t, err, application.ErrNotFound)

	apps, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, apps, 1)
	assert.Equal(t, "other", apps[0].Name)
	assert.Equal(t, []application.Part{}, apps[0].Parts, "a listed application with no parts lists none")
	other.Description, other.OwnerEmail = "moved", "joe@example.com"
	require.NoError(t, s.Update(ctx, other))
	byName, err := s.GetByName(ctx, "other")
	require.NoError(t, err)
	assert.Equal(t, "joe@example.com", byName.OwnerEmail)
}

// Version retention never prunes a version a release pins: it is what the
// asset serves, and what a rollback restores.
func TestRealDB_RetentionKeepsPinnedAssetVersions(t *testing.T) {
	db := testdb.New(t)
	ctx := context.Background()
	s := New(db)
	assets := portalstore.NewPostgresAssetStore(db, nil)
	keep := 1
	versions := portalversions.NewPostgres(db, nil, &keep, nil)
	app := &application.Application{Name: "shop", OwnerEmail: owner, CreatedBy: owner}
	require.NoError(t, s.Create(ctx, app))
	assetID := newAsset(ctx, t, assets, versions, 1)
	require.NoError(t, s.AddPart(ctx, app.ID, application.Part{Kind: application.KindAsset, ID: assetID, AddedBy: owner}))
	_, err := s.Record(ctx, app.ID, application.Recorded{Release: application.Release{
		Action: application.ActionRelease, CreatedBy: "admin@example.com",
		Pins: []application.Pin{{Kind: application.KindAsset, PartID: assetID, Version: 1}},
	}})
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		_, err := versions.CreateVersion(ctx, portaldomain.AssetVersion{
			ID: uuid.NewString(), AssetID: assetID, S3Key: "n" + uuid.NewString(), S3Bucket: "portal", ContentType: "text/html", CreatedBy: owner,
		})
		require.NoError(t, err)
	}
	v1, err := versions.GetByVersion(ctx, assetID, 1)
	require.NoError(t, err)
	assert.Equal(t, "k1", v1.S3Key, "the pinned version outlived a retention of one")
	_, err = versions.GetByVersion(ctx, assetID, 2)
	require.Error(t, err, "an unpinned old version is pruned as before")
}
