package producedview

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// AssetGetter resolves one asset id to its record.
type AssetGetter interface {
	Get(ctx context.Context, id string) (*portaldomain.Asset, error)
}

// Access decides which produced files a reader may open, by the rules the
// file's own surfaces apply: an asset to its owner, an administrator and the
// people it or a collection holding it is shared with; a collection to its
// owner, an administrator and its share recipients; a resource by the
// resource scopes (resource.CanAccessResource). The portal and fetch name a
// script's outputs through it, so the two cannot disagree about what one
// reader is shown.
type Access struct {
	assets      AssetGetter
	collections CollectionNames
	resources   ResourceNames
	// checker reads the share graph; nil when the deployment has no share
	// store, and then only owners and administrators open an asset.
	checker *access.Checker
}

// NewAccess builds the rules over the stores files resolve through. Any store
// may be nil; a file of a kind with no store opens for no one.
func NewAccess(assets AssetGetter, collections CollectionNames, shares portaldomain.ShareStore, resources ResourceNames) *Access {
	a := &Access{assets: assets, collections: collections, resources: resources}
	if shares != nil {
		a.checker = access.New(access.Config{Shares: shares})
	}
	return a
}

// Viewer is the reader a file is judged for.
type Viewer struct {
	UserID string
	Email  string
	// Admin is whether the reader administers the platform, which opens every
	// asset and collection.
	Admin bool
	// Claims are the reader's resource claims (resource.BuildClaims).
	Claims resource.Claims
	// Unattended is a managed-script run reading for the person it acts for:
	// it opens that person's own files and inherits neither the share graph
	// nor an administrator's reach.
	Unattended bool
}

// For returns the Opener for one reader.
func (a *Access) For(v Viewer) Opener {
	return viewerOpener{a: a, v: v, user: &access.User{UserID: v.UserID, Email: v.Email}}
}

type viewerOpener struct {
	a    *Access
	v    Viewer
	user *access.User
}

// CanOpen reports whether the reader may open the file an item names. A file
// that cannot be read, or no longer exists, opens for no one.
func (o viewerOpener) CanOpen(ctx context.Context, it Item) bool {
	switch it.TargetKind {
	case TargetAsset:
		return o.asset(ctx, it.TargetID)
	case TargetCollection:
		return o.collection(ctx, it.TargetID)
	case TargetResource:
		return o.resource(ctx, it.TargetID)
	}
	return false
}

func (o viewerOpener) asset(ctx context.Context, id string) bool {
	if o.a.assets == nil {
		return false
	}
	asset, err := o.a.assets.Get(ctx, id)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		return false
	}
	if access.OwnsAsset(asset, o.user) {
		return true
	}
	if o.v.Unattended {
		return false
	}
	return o.v.Admin || (o.a.checker != nil && o.a.checker.CanViewAsset(ctx, id, asset, o.user))
}

func (o viewerOpener) collection(ctx context.Context, id string) bool {
	if o.a.collections == nil {
		return false
	}
	coll, err := o.a.collections.Get(ctx, id)
	if err != nil || coll == nil || coll.DeletedAt != nil {
		return false
	}
	if o.user.UserID != "" && coll.OwnerID == o.user.UserID {
		return true
	}
	if o.v.Unattended {
		return false
	}
	return o.v.Admin || (o.a.checker != nil && o.a.checker.CanViewCollection(ctx, coll, o.user))
}

func (o viewerOpener) resource(ctx context.Context, id string) bool {
	if o.a.resources == nil {
		return false
	}
	res, err := o.a.resources.Get(ctx, id)
	if err != nil || res == nil {
		return false
	}
	return resource.CanAccessResource(o.v.Claims, res)
}

// SharedWith reports whether an asset is shared with a person, directly or
// through a collection holding it, as the portal opens it; nil when the
// deployment keeps no shares. Asset fetch reads it for a person (#2027).
func SharedWith(shares portaldomain.ShareStore) func(ctx context.Context, asset *portaldomain.Asset, userID, email string) bool {
	if shares == nil {
		return nil
	}
	checker := access.New(access.Config{Shares: shares})
	return func(ctx context.Context, asset *portaldomain.Asset, userID, email string) bool {
		return checker.CanViewAsset(ctx, asset.ID, asset, &access.User{UserID: userID, Email: email})
	}
}
