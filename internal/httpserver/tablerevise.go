package httpserver

import (
	"context"
	"fmt"
	"path"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/pkg/contenttype"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// A registration that cannot read a file the way it is stored saves a
// corrected version of it and registers that (#1441). The correction goes
// through the version mechanism the kind already has rather than into a
// derived copy beside the original: the uploaded bytes stay the version they
// are, the correction is the version on top of them, and it is revertible from
// the same panel every other version is.
//
// One reviser per kind, for the same reason there is one object reader per
// kind: a managed resource and a portal asset keep their trails in different
// tables and their blobs behind different clients.

// tableRevisers builds the per-kind revisers the registrar corrects through,
// from what the platform has.
func tableRevisers(p *platform.Platform) map[string]tableregister.Reviser {
	managed, portalCfg := p.Config().Resources.Managed, p.Config().Portal
	return revisersFor(
		resourceReviserFor(p.ResourceStore(), p.ResourceS3Client(), managed.S3Bucket, managed.MaxVersions,
			p.RegisterManagedResource),
		assetReviserFor(p.PortalAssetStore(), p.PortalVersionStore(), p.PortalS3Client(),
			portalCfg.S3Bucket, portalCfg.S3Prefix),
	)
}

// revisersFor keys the revisers a deployment has by the kind each corrects. A
// nil one is left out, which leaves a defective file of that kind refused
// rather than silently corrected somewhere the version panel cannot show it.
func revisersFor(resources *resourceReviser, assets *assetReviser) map[string]tableregister.Reviser {
	revisers := make(map[string]tableregister.Reviser, 2)
	if resources != nil {
		revisers[tableregister.KindResource] = resources
	}
	if assets != nil {
		revisers[tableregister.KindAsset] = assets
	}
	return revisers
}

// resourceReviser records a corrected managed resource as a content revision,
// through the same call the replace-content route makes, so a revision written
// on somebody's behalf is indistinguishable from one they uploaded: same trail,
// same retention, same per-revision directory.
type resourceReviser struct {
	deps resource.Deps
	// revised is what the replace-content route calls after a revision: it
	// re-registers the resource under its unchanged URI with the new type and
	// size, and fires resources/list_changed. Without it a client that has
	// already read the resource keeps serving the bytes the correction
	// replaced, with no signal that they moved.
	revised func(*resource.Resource)
}

// resourceReviserFor builds the managed-resource reviser, or nil when the
// deployment keeps no resource version trail or has nowhere to put a blob. The
// trail is a capability of the store rather than a requirement of it, the same
// way the replace-content route treats it.
func resourceReviserFor(
	store resource.Store, blobs resource.S3Client, bucket string, maxVersions int, revised func(*resource.Resource),
) *resourceReviser {
	if store == nil || blobs == nil {
		return nil
	}
	versions, ok := store.(resource.VersionStore)
	if !ok {
		return nil
	}
	return &resourceReviser{
		deps: resource.Deps{
			Store:       store,
			Versions:    versions,
			S3Client:    blobs,
			S3Bucket:    bucket,
			MaxVersions: maxVersions,
		},
		revised: revised,
	}
}

// Revise writes the corrected bytes as the resource's next revision. The
// summary is what the version panel shows beside it, so a reader of the history
// sees why the file changed without having to find the registration that did
// it -- the same thing the corrected asset's version carries.
func (r *resourceReviser) Revise(
	ctx context.Context, src tableregister.Source, caller tableregister.Caller, content []byte, summary string,
) (tableregister.Revised, error) {
	res, err := r.deps.Store.Get(ctx, src.ID)
	if err != nil {
		return tableregister.Revised{}, fmt.Errorf("reading the file to correct: %w", err)
	}
	if res == nil {
		return tableregister.Revised{}, fmt.Errorf("correcting %s: %w", src.ID, tableregister.ErrNoSuchFile)
	}
	claims := resource.BuildClaims(caller.UserID, caller.Email, caller.Persona, caller.Roles, caller.IsAdmin)

	updated, version, err := resource.ReviseContent(ctx, r.deps, res, &claims,
		resource.RevisionUpload{Data: content, MIMEType: contenttype.CSV, ChangeSummary: summary})
	if err != nil {
		return tableregister.Revised{}, fmt.Errorf("recording the corrected revision: %w", err)
	}
	// The URI is unchanged, so this re-registers the same resource with its new
	// type and size and tells connected clients the list changed -- exactly
	// what a revision uploaded through the portal does.
	if r.revised != nil {
		r.revised(updated)
	}
	return tableregister.Revised{
		Bucket:  r.deps.S3Bucket,
		Key:     updated.S3Key,
		Version: version.Version,
	}, nil
}

// The three slices of the portal an asset correction touches. Each is named
// here rather than taken whole so the reviser depends on what it uses -- read
// the asset, write one object, record one version -- and can be exercised
// without standing up an asset store or an object endpoint.
type (
	// assetReader reads the asset a correction is for: its id and owner are
	// what the new object's directory is keyed on.
	assetReader interface {
		Get(ctx context.Context, id string) (*portal.Asset, error)
	}
	// assetVersionWriter records the version that moves the asset's head onto
	// the corrected object.
	assetVersionWriter interface {
		CreateVersion(ctx context.Context, version portal.AssetVersion) (int, error)
	}
	// assetBlobs stores the corrected object, and removes it again when no
	// version row ends up pointing at it.
	assetBlobs interface {
		PutObject(ctx context.Context, bucket, key string, data []byte, contentType string) error
		DeleteObject(ctx context.Context, bucket, key string) error
	}
)

// assetReviser records a corrected portal asset as its next version: the
// object under a fresh per-version directory, then the version row that moves
// the asset's head onto it.
type assetReviser struct {
	assets   assetReader
	versions assetVersionWriter
	objects  assetBlobs
	bucket   string
	prefix   string
}

// assetReviserFor builds the portal-asset reviser, or nil when the deployment
// keeps no asset version history or has no object client to write through.
func assetReviserFor(
	assets assetReader, versions assetVersionWriter, blobs assetBlobs, bucket, prefix string,
) *assetReviser {
	if assets == nil || versions == nil || blobs == nil {
		return nil
	}
	return &assetReviser{assets: assets, versions: versions, objects: blobs, bucket: bucket, prefix: prefix}
}

// Revise writes the corrected bytes as the asset's next version. The summary
// is what the version panel shows beside it, so a reader of the history sees
// why the file changed without having to find the registration that did it.
func (a *assetReviser) Revise(
	ctx context.Context, src tableregister.Source, caller tableregister.Caller, content []byte, summary string,
) (tableregister.Revised, error) {
	asset, err := a.assets.Get(ctx, src.ID)
	if err != nil {
		return tableregister.Revised{}, fmt.Errorf("reading the asset to correct: %w", err)
	}
	if asset == nil {
		return tableregister.Revised{}, fmt.Errorf("correcting %s: %w", src.ID, tableregister.ErrNoSuchFile)
	}

	versionID := uuid.New().String()
	key := path.Join(a.prefix, asset.OwnerID, asset.ID, versionID, "content"+portal.ExtensionForContentType(contenttype.CSV))
	if err := a.objects.PutObject(ctx, a.bucket, key, content, contenttype.CSV); err != nil {
		return tableregister.Revised{}, fmt.Errorf("storing the corrected content: %w", err)
	}

	version, err := a.versions.CreateVersion(ctx, portal.AssetVersion{
		ID:            versionID,
		AssetID:       asset.ID,
		S3Key:         key,
		S3Bucket:      a.bucket,
		ContentType:   contenttype.CSV,
		SizeBytes:     int64(len(content)),
		CreatedBy:     caller.Email,
		ChangeSummary: summary,
	})
	if err != nil {
		// The version row is what makes the object the asset's content. Without
		// it the object is unreachable, so it goes rather than sitting in the
		// bucket forever.
		_ = a.objects.DeleteObject(ctx, a.bucket, key)
		return tableregister.Revised{}, fmt.Errorf("recording the corrected version: %w", err)
	}
	return tableregister.Revised{Bucket: a.bucket, Key: key, Version: version}, nil
}
