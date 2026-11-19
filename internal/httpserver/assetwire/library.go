package assetwire

import (
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/internal/httpserver/notifywire"
	"github.com/txn2/mcp-data-platform/internal/platform/retention"
	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/assetsimilar"
	"github.com/txn2/mcp-data-platform/internal/portal/library"
	"github.com/txn2/mcp-data-platform/internal/portal/libraryapi"
	"github.com/txn2/mcp-data-platform/internal/portal/portalpurge"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	portalkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// libraryMount assembles a person's library (#2504, #2509): recents and
// favorites, and the trash, unused and possible-duplicates lists, with the
// purge Delete permanently runs and the retention the trash reports.
func libraryMount(deps *portal.Deps, p *platform.Platform) func(*http.ServeMux, *access.Checker) {
	db := p.DB()
	purger := portalpurge.NewPurger(db, nil, p.Config().Portal.S3Bucket)
	if client := p.PortalS3Client(); client != nil {
		purger = portalpurge.NewPurger(db, client, p.Config().Portal.S3Bucket)
	}
	days := retention.Days(p.Config().Portal.DeletedRetentionDays, retention.DefaultDeletedRetentionDays)
	store, keep := library.New(db, assetsimilar.New(db)), time.Duration(days)*day
	bindLibraryTool(p, store, keep)
	return libraryapi.Mount(libraryapi.Config{
		Library:     store,
		Assets:      deps.AssetStore,
		Collections: deps.CollectionStore,
		Purger:      purger,
		Retention:   keep,
	})
}

// librarySink is satisfied by the asset toolkit, whose manage_asset lists a
// person's recents, favorites and trash and restores from it.
type librarySink interface {
	SetLibrary(portalkit.Library, time.Duration)
}

// bindLibraryTool hands manage_asset the library the portal routes read, so
// an agent lists the recents the portal recorded. Toolkits are built before
// the HTTP layer, so this runs here, the way the feedback notifier is bound.
func bindLibraryTool(p *platform.Platform, store *library.Store, keep time.Duration) {
	registry := p.ToolkitRegistry()
	if registry == nil {
		return
	}
	for _, tk := range registry.GetByKind(notifywire.PortalToolkitKind) {
		if sink, ok := tk.(librarySink); ok {
			sink.SetLibrary(store, keep)
		}
	}
}

// day is the unit portal.deleted_retention_days counts in.
const day = 24 * time.Hour
