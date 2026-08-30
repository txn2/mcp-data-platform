// Package tablesource is the registrar's view of the two kinds of stored file
// a table is built over: how a managed resource and a portal asset are
// resolved into a tableregister.Source, and under which rule.
//
// Three resolutions serve three questions, and they are kept together because
// they read the same two stores and build the same Source from the same
// record. Subjects decides whether THIS caller may act on the file, which is
// what the register and unregister routes and the manage_table tool ask.
// RefLookup resolves a page of records at once for the cross-source listing,
// where authority is a field on the answer rather than the answer itself.
// Locator resolves a record with no caller at all, for the follow a content
// write triggers (#1536): the write was authorized by the surface that made
// it, and the follow acts for the registrant.
//
// It is its own package rather than part of the HTTP composition root because
// nothing in it composes anything: it is the rule for who may register a file
// and the shape of a file as the registrar sees it, applied identically by
// every door.
package tablesource

import (
	"cmp"
	"context"
	"log/slog"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/tableregister"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// Subjects is the one place a stored file is resolved and the caller's
// authority over it is decided, per kind. Both surfaces take their resolvers
// from here: the REST routes convert their authenticated portal user into a
// Caller, and the tool reads one from the platform context.
//
// A kind whose store is absent has no entry, which leaves that kind
// unregisterable on both surfaces rather than half-served on one.
func Subjects(
	resources resource.Store, resourceBucket string, assets portal.AssetStore, adminRoles []string,
) map[string]tableregister.Subject {
	subjects := make(map[string]tableregister.Subject, 2)
	if resources != nil {
		subjects[tableregister.KindResource] = ResourceSubject(resources, resourceBucket)
	}
	if assets != nil {
		subjects[tableregister.KindAsset] = AssetSubject(assets, adminRoles)
	}
	return subjects
}

// ResourceSubject resolves a managed resource for the table surfaces.
//
// The rule is authority to CHANGE the resource, not authority to read it:
// resource.CanModifyResource, which is the uploader, a platform administrator,
// or an administrator of the scope the resource lives in -- the same rule that
// governs updating and deleting it. Registering publishes the file's contents
// into a schema everyone granted the connection can read, and resource scopes
// are not carried into Trino (docs/security/threat-model.md), so a read rule
// here would let anyone who can see a persona-scoped file widen its audience.
// This matches the asset rule below, so one sentence describes both kinds.
func ResourceSubject(store resource.Store, bucket string) tableregister.Subject {
	return func(ctx context.Context, id string, caller tableregister.Caller) (tableregister.Source, bool) {
		res, ok := readResource(ctx, store, id)
		if !ok {
			return tableregister.Source{}, false
		}
		claims := resource.BuildClaims(caller.UserID, caller.Email, caller.Persona, caller.Roles, caller.IsAdmin).
			ActingFor(caller.OnBehalfOf)
		if !resource.CanModifyResource(claims, res) {
			return tableregister.Source{}, false
		}
		return resourceSource(res, bucket), true
	}
}

// readResource reads one managed resource, answering a store that is absent,
// a read that failed and a record that is not there the same way: nothing to
// resolve.
func readResource(ctx context.Context, store resource.Store, id string) (*resource.Resource, bool) {
	if store == nil {
		return nil, false
	}
	res, err := store.Get(ctx, id)
	if err != nil || res == nil {
		return nil, false
	}
	return res, true
}

// resourceSource is the registrar's view of a managed resource.
func resourceSource(res *resource.Resource, bucket string) tableregister.Source {
	return tableregister.SourceFromResource(tableregister.Record{
		ID: res.ID, Name: res.DisplayName, Bucket: bucket,
		Key: res.S3Key, ContentType: res.MIMEType,
	})
}

// AssetSubject resolves a portal asset for the table surfaces.
func AssetSubject(store portal.AssetStore, adminRoles []string) tableregister.Subject {
	return func(ctx context.Context, id string, caller tableregister.Caller) (tableregister.Source, bool) {
		asset, ok := readAsset(ctx, store, id)
		if !ok || !AssetVisibleTo(*asset, caller, adminRoles) {
			return tableregister.Source{}, false
		}
		return assetSource(asset), true
	}
}

// readAsset reads one portal asset. A soft-deleted asset is gone, the way
// every table surface treats it.
func readAsset(ctx context.Context, store portal.AssetStore, id string) (*portal.Asset, bool) {
	if store == nil {
		return nil, false
	}
	asset, err := store.Get(ctx, id)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		return nil, false
	}
	return asset, true
}

// assetSource is the registrar's view of a portal asset.
func assetSource(asset *portal.Asset) tableregister.Source {
	return tableregister.SourceFromAssetRecord(tableregister.Record{
		ID: asset.ID, Name: asset.Name, Bucket: asset.S3Bucket,
		Key: asset.S3Key, ContentType: asset.ContentType,
	})
}

// AssetVisibleTo reports whether this caller may act on an asset through the
// table surfaces.
//
// An asset belongs to one person, so the owner and an administrator reach it
// and nobody else does; an editor share does not carry it, because registering
// publishes the file's contents into a schema everyone with the connection can
// read, which is owner authority the way sharing is.
//
// Ownership is the portal's own judgment (assetOwnerOf), so a table over a
// managed script's output is registered, listed and dropped by the same person
// the portal calls its owner, and by nobody else.
//
// The administrator arm is checked twice on purpose: a caller assembled by a
// surface that does not resolve IsAdmin still carries the roles it was
// authenticated with, and an administrator is unrestricted whichever door they
// came through.
func AssetVisibleTo(asset portal.Asset, caller tableregister.Caller, adminRoles []string) bool {
	if caller.IsAdmin || HasAnyRole(caller.Roles, adminRoles) {
		return true
	}
	return assetOwnerOf(caller).OwnsAsset(&asset)
}

// assetOwnerOf is the ownership identity a table-surface caller is judged by.
//
// The address is the one the caller is acting as: their own, or for a managed
// script run the address of the person whose authority the run presents. Using
// the run's own address instead would pair one person's authority with
// another's ownership after a transfer, which is the pairing the run's identity
// binding refuses to make.
func assetOwnerOf(caller tableregister.Caller) portaldomain.AssetOwner {
	return portaldomain.NewAssetOwner(caller.UserID, cmp.Or(caller.OnBehalfOf, caller.Email))
}

// Locator resolves a source by kind and id with no authority check. It
// is what a follow reads the source's new head through (#1536): the write that
// moved the head was authorized by the surface that made it, and the follow
// acts for the registrant, not for the caller of the write. A kind whose store
// is absent, a record that is gone, and a deleted asset all answer ok=false.
func Locator(resources resource.Store, resourceBucket string, assets portal.AssetStore) tableregister.Locator {
	return func(ctx context.Context, kind, id string) (tableregister.Source, bool) {
		switch kind {
		case tableregister.KindResource:
			return locateResource(ctx, resources, resourceBucket, id)
		case tableregister.KindAsset:
			return locateAsset(ctx, assets, id)
		default:
			return tableregister.Source{}, false
		}
	}
}

// locateResource reads a managed resource as a source.
func locateResource(ctx context.Context, store resource.Store, bucket, id string) (tableregister.Source, bool) {
	res, ok := readResource(ctx, store, id)
	if !ok {
		return tableregister.Source{}, false
	}
	return resourceSource(res, bucket), true
}

// locateAsset reads a portal asset as a source.
func locateAsset(ctx context.Context, store portal.AssetStore, id string) (tableregister.Source, bool) {
	asset, ok := readAsset(ctx, store, id)
	if !ok {
		return tableregister.Source{}, false
	}
	return assetSource(asset), true
}

// RefLookup dispatches a listing's source resolution to the store that holds
// that kind, the way Subjects does for the per-source routes.
func RefLookup(
	resources resource.Store, bucket string, assets portal.AssetStore, adminRoles []string,
) tableregister.Sources {
	return func(
		ctx context.Context, kind string, ids []string, caller tableregister.Caller,
	) map[string]tableregister.SourceRef {
		switch kind {
		case tableregister.KindResource:
			return resourceSourceRefs(ctx, resources, bucket, ids, caller)
		case tableregister.KindAsset:
			return assetSourceRefs(ctx, assets, adminRoles, ids, caller)
		default:
			return nil
		}
	}
}

// resourceSourceRefs reads a page of managed resources in one query.
//
// A read that fails answers with nothing rather than with an error: the
// listing is about the registrations, and a resource store that stopped
// answering leaves each row without a source name and without the unregister
// action, which is a degraded listing rather than no listing at all.
func resourceSourceRefs(
	ctx context.Context, store resource.Store, bucket string, ids []string, caller tableregister.Caller,
) map[string]tableregister.SourceRef {
	if store == nil {
		return nil
	}
	found, err := store.GetByIDs(ctx, ids)
	if err != nil {
		slog.Warn("registered tables: reading the resources a listing names failed",
			"error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	claims := resource.BuildClaims(caller.UserID, caller.Email, caller.Persona, caller.Roles, caller.IsAdmin).
		ActingFor(caller.OnBehalfOf)
	out := make(map[string]tableregister.SourceRef, len(found))
	for id, res := range found {
		if res == nil {
			continue
		}
		out[id] = tableregister.SourceRef{
			Name:      res.DisplayName,
			Bucket:    bucket,
			HeadKey:   res.S3Key,
			CanModify: resource.CanModifyResource(claims, res),
		}
	}
	return out
}

// assetSourceRefs reads a page of portal assets in one query. A soft-deleted
// asset is left out, which is what the Subject resolver does with one.
func assetSourceRefs(
	ctx context.Context, store portal.AssetStore, adminRoles []string,
	ids []string, caller tableregister.Caller,
) map[string]tableregister.SourceRef {
	if store == nil {
		return nil
	}
	found, err := store.GetByIDs(ctx, ids)
	if err != nil {
		slog.Warn("registered tables: reading the assets a listing names failed",
			"error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	out := make(map[string]tableregister.SourceRef, len(found))
	for id, asset := range found {
		if asset == nil || asset.DeletedAt != nil {
			continue
		}
		out[id] = tableregister.SourceRef{
			Name:      asset.Name,
			Bucket:    asset.S3Bucket,
			HeadKey:   asset.S3Key,
			CanModify: AssetVisibleTo(*asset, caller, adminRoles),
		}
	}
	return out
}

// HasAnyRole reports whether held contains any of want.
func HasAnyRole(held, want []string) bool {
	for _, h := range held {
		if slices.Contains(want, h) {
			return true
		}
	}
	return false
}
