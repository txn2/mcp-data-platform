// Package notifywiring assembles the notify tool from the stores the platform
// already holds (#1723).
//
// It is a seam rather than a method on the facade for the reason every other
// composition seam here is one: the facade is at its size budget, and
// composition is not behavior it should own. What it adds over notifylayer
// itself is the two adapters that join the portal's storage to the two
// narrow contracts the tool asks for -- reading an asset, and deciding who
// may read it.
package notifywiring

import (
	"context"
	"database/sql"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/notification/notifychannel"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyprefs"
	"github.com/txn2/mcp-data-platform/internal/notification/notifyqueue"
	"github.com/txn2/mcp-data-platform/internal/platform/connscope"
	"github.com/txn2/mcp-data-platform/internal/platform/notifylayer"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/notification"
	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// Deps are what the facade hands over. Every field is a value it already
// holds; nothing here reads configuration or opens a connection of its own.
type Deps struct {
	// DB is the platform pool. nil registers no tool.
	DB *sql.DB
	// Server is the MCP server the tool is registered on. nil registers
	// nothing.
	Server *mcp.Server
	// Enabled is the deployment's notifications setting. False registers no
	// tool, so a deployment that turned notifications off does not advertise
	// a way to send one.
	Enabled bool
	// Personas resolves which connections a persona reaches, which is what
	// decides the channels a caller may send to.
	Personas *persona.Registry
	// Assets, Blobs, Shares and Collections are the portal stores publish
	// reads an asset and its entitlement through. All nil leaves publish
	// refusing and the other two actions unaffected.
	Assets      portal.AssetStore
	Blobs       portal.S3Client
	Shares      portal.ShareStore
	Collections portal.CollectionStore
	// PortalURL is the deployment's public address, for the link a published
	// document carries.
	PortalURL string
	// AdminPersona is the persona whose reach is unrestricted.
	AdminPersona string
	// DigestHourUTC is the hour a daily channel's bulletin is scheduled for.
	DigestHourUTC int
}

// Wire assembles the notify tool and registers it, or does nothing when this
// deployment cannot send at all.
//
// The channel store and the enqueuer are built here rather than taken from the
// HTTP composition root's delivery handle, because the tool is registered on
// the MCP server during platform construction and that handle does not exist
// yet. Both are stateless over the same pool, so there is nothing to share:
// what must agree is the table, and it does.
func Wire(deps Deps) {
	if deps.DB == nil || deps.Server == nil || !deps.Enabled {
		return
	}
	handle := notifylayer.New(notifylayer.Config{
		Channels: notifychannel.NewPostgresStore(deps.DB),
		Enqueuer: notification.NewEnqueuer(
			notifyprefs.NewPostgresStore(deps.DB),
			notifyqueue.NewPostgresStore(deps.DB),
			deps.DigestHourUTC),
		Scope:        connscope.New(connscope.Deps{Registry: deps.Personas}),
		Assets:       assetReaderOf(deps),
		Access:       assetAccessOf(deps),
		PortalURL:    deps.PortalURL,
		AdminPersona: deps.AdminPersona,
	})
	handle.RegisterTool(deps.Server)
}

// assetReader reads the asset a publish names and its stored content. It is
// the two portal stores the tool needs, joined here so notifylayer depends on
// reading an asset rather than on the portal's storage layout.
type assetReader struct {
	assets portal.AssetStore
	blobs  portal.S3Client
}

// Get returns one asset by id.
func (r assetReader) Get(ctx context.Context, id string) (*portaldomain.Asset, error) {
	return r.assets.Get(ctx, id) //nolint:wrapcheck // the caller reports in its own terms
}

// Content returns the asset's stored bytes.
func (r assetReader) Content(ctx context.Context, asset *portaldomain.Asset) ([]byte, error) {
	data, _, err := r.blobs.GetObject(ctx, asset.S3Bucket, asset.S3Key)
	return data, err //nolint:wrapcheck // the caller reports in its own terms
}

// assetReaderOf builds the reader, or nil when this deployment has no portal
// storage to read from -- which leaves publish refusing and send unaffected.
func assetReaderOf(deps Deps) notifylayer.AssetReader {
	if deps.Assets == nil || deps.Blobs == nil {
		return nil
	}
	return assetReader{assets: deps.Assets, blobs: deps.Blobs}
}

// assetAccess answers the one entitlement question publish asks, through the
// portal's own authorization core, so "may this person read this asset" cannot
// come to mean something different here than it does on the asset's page.
type assetAccess struct {
	checker *access.Checker
}

// CanRead reports whether the identified caller may read the asset.
//
// Roles are deliberately not carried: this check is the share graph, not the
// persona boundary, and the channel the document is going to has already been
// authorized against the caller's persona.
func (a assetAccess) CanRead(ctx context.Context, asset *portaldomain.Asset, userID, email string) bool {
	if asset == nil {
		return false
	}
	return a.checker.CanViewAsset(ctx, asset.ID, asset, &access.User{UserID: userID, Email: email})
}

// assetAccessOf builds the entitlement check, or nil when the stores it reads
// are absent.
func assetAccessOf(deps Deps) notifylayer.Entitlement {
	if deps.Assets == nil || deps.Shares == nil {
		return nil
	}
	return assetAccess{checker: access.New(access.Config{
		Assets:      deps.Assets,
		Collections: deps.Collections,
		Shares:      deps.Shares,
	})}
}
