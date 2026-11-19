// Package assetwire assembles the asset organization seams (#2511) onto the
// portal: each feature's routes live in its own package under internal/portal
// and are registered on the portal's authenticated mux through portal.Deps'
// Mounts hook, with the portal's authorization core. It is built only by the
// HTTP composition root (internal/httpserver), which calls Wire beside the
// other portal wiring.
package assetwire

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/assetbulk"
	"github.com/txn2/mcp-data-platform/internal/portal/assetlinks"
	"github.com/txn2/mcp-data-platform/internal/portal/assetstatus"
	"github.com/txn2/mcp-data-platform/internal/portal/assettags"
	"github.com/txn2/mcp-data-platform/internal/portal/assetverify"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// Wire adds the asset organization routes to deps. A deployment with no
// database has no portal stores, and gets none of them.
func Wire(deps *portal.Deps, p *platform.Platform) {
	if p == nil {
		return
	}
	db := p.DB()
	if db == nil || deps.AssetStore == nil {
		return
	}
	deps.Mounts = append(deps.Mounts,
		assetstatus.Mount(assetstatus.New(db), deps.AssetStore),
		assetverify.Mount(assetverify.New(db), deps.AssetStore),
		assetlinks.Mount(assetlinks.New(db), deps.AssetStore),
		libraryMount(deps, p),                    // favorites, recents, trash, unused, duplicates (#2504, #2509)
		assetbulk.Mount(deps.AssetStore),         // bulk actions (#2507)
		assettags.MountPortal(assettags.New(db)), // the tags a person can see (#2508)
	)
}

// StartReminders starts the pass that reminds the person who verified an
// asset and its owner that the verification expires within a week (#2503),
// and returns its stop. It enqueues through the notification substrate, so it
// starts after it and stops before it.
func StartReminders(ctx context.Context, p *platform.Platform, notify *notifydelivery.Handle) func() {
	if p == nil || notify == nil {
		return func() {}
	}
	db := p.DB()
	assets := p.PortalAssetStore()
	enqueuer := notify.Enqueuer()
	if db == nil || assets == nil || enqueuer == nil {
		return func() {}
	}
	checker := access.New(access.Config{Assets: assets, Shares: p.PortalShareStore(), Collections: p.PortalCollectionStore()})
	reminder := &assetverify.Reminder{
		Store:    assetverify.New(db),
		Assets:   assets,
		Notifier: enqueuer,
		CanOpen: func(ctx context.Context, asset *portaldomain.Asset, email string) bool {
			return checker.CanViewAsset(ctx, asset.ID, asset, &access.User{Email: email})
		},
		BaseURL: p.Config().Portal.PublicBaseURL,
	}
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reminder.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}
