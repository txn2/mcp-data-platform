package httpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// A CSV a query engine cannot read the way it is stored is corrected into a new
// version of the file itself (#1441). What is asserted here is the half that
// makes that true rather than a claim: the corrected bytes land in the trail the
// kind already has, under a directory of their own, with the head moved onto
// them -- which is what lets the table point at a directory holding exactly one
// file, and what makes the correction revertible from the version panel.

// --- fakes ---

// reviseObjects records what was written and answers reads from it.
type reviseObjects struct {
	put     map[string][]byte
	deleted []string
	putErr  error
}

func newReviseObjects() *reviseObjects { return &reviseObjects{put: map[string][]byte{}} }

func (o *reviseObjects) PutObject(_ context.Context, bucket, key string, data []byte, _ string) error {
	if o.putErr != nil {
		return o.putErr
	}
	o.put[bucket+"/"+key] = data
	return nil
}

func (o *reviseObjects) GetObject(
	_ context.Context, bucket, key string,
) (body []byte, contentType string, err error) {
	body, ok := o.put[bucket+"/"+key]
	if !ok {
		return nil, "", errors.New("no such object")
	}
	return body, "text/csv", nil
}

func (o *reviseObjects) DeleteObject(_ context.Context, bucket, key string) error {
	o.deleted = append(o.deleted, bucket+"/"+key)
	return nil
}

// reviseAssets is the slice of the asset store a reviser reads.
type reviseAssets struct {
	asset *portal.Asset
	err   error
}

func (a *reviseAssets) Get(_ context.Context, _ string) (*portal.Asset, error) {
	return a.asset, a.err
}

// reviseVersions records the version rows an asset correction writes.
type reviseVersions struct {
	created []portal.AssetVersion
	err     error
}

func (v *reviseVersions) CreateVersion(_ context.Context, av portal.AssetVersion) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	v.created = append(v.created, av)
	return len(v.created) + 1, nil
}

// --- the asset reviser ---

// assetHarness is one asset reviser and the three fakes behind it.
type assetHarness struct {
	reviser  *assetReviser
	assets   *reviseAssets
	versions *reviseVersions
	objects  *reviseObjects
}

func newAssetReviserHarness() assetHarness {
	h := assetHarness{
		assets: &reviseAssets{asset: &portal.Asset{
			ID: "asset_1", OwnerID: "u1", OwnerEmail: "alice@example.com",
			ContentType: "text/csv", S3Bucket: "portal-assets", S3Key: "artifacts/u1/asset_1/content.csv",
		}},
		versions: &reviseVersions{},
		objects:  newReviseObjects(),
	}
	h.reviser = &assetReviser{
		assets: h.assets, versions: h.versions, objects: h.objects,
		bucket: "portal-assets", prefix: "artifacts",
	}
	return h
}

func TestAssetReviser_WritesTheCorrectionAsTheAssetsNextVersion(t *testing.T) {
	h := newAssetReviserHarness()
	versions, objects := h.versions, h.objects

	revised, err := h.reviser.Revise(context.Background(),
		tableregister.Source{Kind: tableregister.KindAsset, ID: "asset_1", Bucket: "portal-assets"},
		tableregister.Caller{Email: "alice@example.com"},
		[]byte("store_id,address\n101,12 Mill Rd Suite 4\n"),
		"put 1 row back onto one line")
	require.NoError(t, err)

	// The object sits under a directory of its own, which is what lets a table
	// point at that directory and read exactly one file.
	assert.True(t, strings.HasPrefix(revised.Key, "artifacts/u1/asset_1/"), revised.Key)
	assert.True(t, strings.HasSuffix(revised.Key, "/content.csv"), revised.Key)
	assert.Equal(t, "portal-assets", revised.Bucket)
	assert.Equal(t, 2, revised.Version)
	assert.Equal(t, tableregister.DirectoryOf(revised.Key)+"content.csv", revised.Key)

	body, _, err := objects.GetObject(context.Background(), revised.Bucket, revised.Key)
	require.NoError(t, err)
	assert.Contains(t, string(body), "12 Mill Rd Suite 4")

	require.Len(t, versions.created, 1)
	got := versions.created[0]
	assert.Equal(t, "asset_1", got.AssetID)
	assert.Equal(t, revised.Key, got.S3Key)
	assert.Equal(t, "text/csv", got.ContentType, "the corrected file is stored as what it is")
	assert.Equal(t, "alice@example.com", got.CreatedBy)
	assert.Equal(t, "put 1 row back onto one line", got.ChangeSummary,
		"the version panel says why the file changed")
	assert.Equal(t, int64(len(body)), got.SizeBytes)
}

// TestAssetReviser_UnrecordedObjectIsRemoved: the version row is what makes an
// object the asset's content, so an object no row points at is unreachable and
// goes rather than sitting in the bucket forever.
func TestAssetReviser_UnrecordedObjectIsRemoved(t *testing.T) {
	h := newAssetReviserHarness()
	objects := h.objects
	h.versions.err = errors.New("version store unreachable")

	_, err := h.reviser.Revise(context.Background(),
		tableregister.Source{ID: "asset_1"}, tableregister.Caller{}, []byte("a,b\n1,2\n"), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "version store unreachable")
	require.Len(t, objects.deleted, 1)
	assert.Contains(t, objects.deleted[0], "artifacts/u1/asset_1/")
}

func TestAssetReviser_RefusesWhatItCannotRead(t *testing.T) {
	h := newAssetReviserHarness()
	objects := h.objects
	h.assets.asset, h.assets.err = nil, errors.New("no such asset")

	_, err := h.reviser.Revise(context.Background(),
		tableregister.Source{ID: "asset_1"}, tableregister.Caller{}, []byte("a,b\n1,2\n"), "")
	require.Error(t, err)
	assert.Empty(t, objects.put, "nothing is written for an asset that could not be read")
}

func TestAssetReviser_ReportsAFailedWrite(t *testing.T) {
	h := newAssetReviserHarness()
	versions, objects := h.versions, h.objects
	objects.putErr = errors.New("bucket unreachable")

	_, err := h.reviser.Revise(context.Background(),
		tableregister.Source{ID: "asset_1"}, tableregister.Caller{}, []byte("a,b\n1,2\n"), "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bucket unreachable")
	assert.Empty(t, versions.created, "no row records bytes that were never stored")
}

// --- the resource reviser ---

// reviseResourceStore serves one resource and records the head the revision
// moved it to, which is the half the registrar reads back.
type reviseResourceStore struct {
	resource.Store
	*reviseResourceVersions
	res    *resource.Resource
	getErr error
}

func (s *reviseResourceStore) Get(_ context.Context, _ string) (*resource.Resource, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.res, nil
}

// reviseResourceVersions is the resource half of the same trail.
type reviseResourceVersions struct {
	// res is the resource row the head move lands on.
	res   *resource.Resource
	added []resource.Revision
	err   error
}

func (v *reviseResourceVersions) AddRevision(_ context.Context, rev resource.Revision) (*resource.Version, error) {
	if v.err != nil {
		return nil, v.err
	}
	v.added = append(v.added, rev)
	// The head moves with the revision, in one transaction, which is what the
	// registrar depends on: it reads the head back to find the directory.
	v.res.S3Key, v.res.MIMEType, v.res.SizeBytes = rev.S3Key, rev.MIMEType, rev.SizeBytes
	return &resource.Version{ResourceID: rev.ResourceID, Version: len(v.added) + 1, S3Key: rev.S3Key}, nil
}

func (*reviseResourceVersions) ListVersions(context.Context, string) ([]resource.Version, error) {
	return nil, nil
}

func (*reviseResourceVersions) GetVersion(context.Context, string, int) (*resource.Version, error) {
	return nil, errors.New("not recorded")
}

func (*reviseResourceVersions) PruneVersions(context.Context, string, int) ([]resource.Version, error) {
	return nil, nil
}

func TestResourceReviser_WritesTheCorrectionAsTheResourcesNextRevision(t *testing.T) {
	res := &resource.Resource{
		ID: "res_1", Scope: resource.ScopeGlobal, Filename: "store-list.csv",
		MIMEType: "text/csv", S3Key: "resources/global/global/res_1/store-list.csv",
	}
	store := &reviseResourceStore{res: res}
	versions := &reviseResourceVersions{res: res}
	objects := newReviseObjects()

	var announced []*resource.Resource
	reviser := &resourceReviser{
		deps: resource.Deps{
			Store: store, Versions: versions, S3Client: objects, S3Bucket: "managed-resources",
		},
		revised: func(r *resource.Resource) { announced = append(announced, r) },
	}

	revised, err := reviser.Revise(context.Background(),
		tableregister.Source{Kind: tableregister.KindResource, ID: "res_1"},
		tableregister.Caller{UserID: "u1", Email: "alice@example.com"},
		[]byte("store_id,address\n101,12 Mill Rd Suite 4\n"),
		"put 1 row back onto one line")
	require.NoError(t, err)

	// BuildRevisionS3Key puts each revision under v/<revisionID>/, so the
	// directory the table is pointed at holds that one file.
	assert.Contains(t, revised.Key, "/res_1/v/")
	assert.True(t, strings.HasSuffix(revised.Key, "/store-list.csv"), revised.Key)
	assert.Equal(t, "managed-resources", revised.Bucket)
	assert.Equal(t, 2, revised.Version)
	assert.Equal(t, res.S3Key, revised.Key, "the head moved onto the correction")

	require.Len(t, versions.added, 1)
	assert.Equal(t, "alice@example.com", versions.added[0].UploaderEmail)
	assert.Equal(t, "text/csv", versions.added[0].MIMEType)
	assert.Equal(t, "put 1 row back onto one line", versions.added[0].ChangeSummary,
		"the version panel says why the file changed")

	body, _, err := objects.GetObject(context.Background(), revised.Bucket, revised.Key)
	require.NoError(t, err)
	assert.Contains(t, string(body), "12 Mill Rd Suite 4")

	// A correction is a revision like any other, so connected clients are told
	// the resource changed. Without this a client that already read it keeps
	// serving the bytes the correction replaced.
	require.Len(t, announced, 1)
	assert.Equal(t, "res_1", announced[0].ID)
	assert.Equal(t, revised.Key, announced[0].S3Key)
}

func TestResourceReviser_RefusesWhatItCannotRead(t *testing.T) {
	objects := newReviseObjects()
	reviser := &resourceReviser{deps: resource.Deps{
		Store:    &reviseResourceStore{getErr: errors.New("no such resource")},
		Versions: &reviseResourceVersions{},
		S3Client: objects, S3Bucket: "managed-resources",
	}}

	_, err := reviser.Revise(context.Background(),
		tableregister.Source{ID: "res_1"}, tableregister.Caller{}, []byte("a,b\n1,2\n"), "")
	require.Error(t, err)
	assert.Empty(t, objects.put)
}

// TestRevisers_BothKindsRecordWhyTheFileChanged. The registrar hands every
// reviser the same sentence describing the repair, and until #1450 only one
// kind kept it: a corrected managed resource sat in its version history
// indistinguishable from a file its owner uploaded, while a corrected asset
// carried the reason. Both trails are read by a person asking the same
// question, so both record the same answer.
func TestRevisers_BothKindsRecordWhyTheFileChanged(t *testing.T) {
	const summary = "rewrote the CRLF line endings as newlines and put 2 rows back onto one line"
	content := []byte("store_id,address\n101,12 Mill Rd Suite 4\n")

	res := &resource.Resource{
		ID: "res_1", Scope: resource.ScopeGlobal, Filename: "store-list.csv",
		MIMEType: "text/csv", S3Key: "resources/global/global/res_1/store-list.csv",
	}
	resourceVersions := &reviseResourceVersions{res: res}
	resources := &resourceReviser{deps: resource.Deps{
		Store:    &reviseResourceStore{res: res},
		Versions: resourceVersions,
		S3Client: newReviseObjects(), S3Bucket: "managed-resources",
	}}
	_, err := resources.Revise(context.Background(),
		tableregister.Source{Kind: tableregister.KindResource, ID: "res_1"},
		tableregister.Caller{Email: "alice@example.com"}, content, summary)
	require.NoError(t, err)

	assets := newAssetReviserHarness()
	_, err = assets.reviser.Revise(context.Background(),
		tableregister.Source{Kind: tableregister.KindAsset, ID: "asset_1", Bucket: "portal-assets"},
		tableregister.Caller{Email: "alice@example.com"}, content, summary)
	require.NoError(t, err)

	require.Len(t, resourceVersions.added, 1)
	require.Len(t, assets.versions.created, 1)
	assert.Equal(t, summary, resourceVersions.added[0].ChangeSummary)
	assert.Equal(t, assets.versions.created[0].ChangeSummary, resourceVersions.added[0].ChangeSummary,
		"a reader of either kind's version history is told the same thing")
}

// --- wiring ---

// TestTableRevisers_UnwiredDeploymentCorrectsNothing. A kind with no version
// trail has nowhere to put a correction, and the registrar refuses such a file
// rather than writing a copy the version panel could never show.
func TestTableRevisers_UnwiredDeploymentCorrectsNothing(t *testing.T) {
	assert.Empty(t, tableRevisers(newTestPlatform(t, &platform.Config{})))
}

// TestRevisersFor keys each reviser a deployment has by the kind it corrects,
// and leaves out the one it does not have.
func TestRevisersFor(t *testing.T) {
	assert.Empty(t, revisersFor(nil, nil))

	both := revisersFor(&resourceReviser{}, &assetReviser{})
	assert.Len(t, both, 2)
	assert.NotNil(t, both[tableregister.KindResource])
	assert.NotNil(t, both[tableregister.KindAsset])

	assetsOnly := revisersFor(nil, &assetReviser{})
	assert.Len(t, assetsOnly, 1)
	assert.NotNil(t, assetsOnly[tableregister.KindAsset])
}

// TestResourceReviserFor: the version trail is a capability of the store, not a
// requirement of it, so a store without one leaves the kind uncorrectable
// rather than half-wired.
func TestResourceReviserFor(t *testing.T) {
	objects := newReviseObjects()
	withTrail := &reviseResourceStore{}
	assert.Nil(t, resourceReviserFor(nil, objects, "b", 10, nil, nil), "no store")
	assert.Nil(t, resourceReviserFor(withTrail, nil, "b", 10, nil, nil), "nowhere to put the blob")
	assert.Nil(t, resourceReviserFor(&storeWithoutTrail{}, objects, "b", 10, nil, nil), "no version trail")

	built := resourceReviserFor(withTrail, objects, "managed-resources", 10, nil, nil)
	require.NotNil(t, built)
	assert.Equal(t, "managed-resources", built.deps.S3Bucket)
	assert.Equal(t, 10, built.deps.MaxVersions)
}

func TestAssetReviserFor(t *testing.T) {
	assets, versions, objects := &reviseAssets{}, &reviseVersions{}, newReviseObjects()
	assert.Nil(t, assetReviserFor(nil, versions, objects, "b", "p"))
	assert.Nil(t, assetReviserFor(assets, nil, objects, "b", "p"))
	assert.Nil(t, assetReviserFor(assets, versions, nil, "b", "p"))

	built := assetReviserFor(assets, versions, objects, "portal-assets", "artifacts")
	require.NotNil(t, built)
	assert.Equal(t, "portal-assets", built.bucket)
	assert.Equal(t, "artifacts", built.prefix)
}

// storeWithoutTrail is a resource store that records no content revisions,
// which is the shape a deployment with no database-backed store has.
type storeWithoutTrail struct{ resource.Store }
