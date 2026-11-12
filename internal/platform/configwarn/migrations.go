package configwarn

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/database/migrate"
)

// devRenameMessage is the warning for a dev environment migration 000239 is
// about to rename.
const devRenameMessage = "migration 000239 is about to rename this site's environment named dev, which has a domain: the name dev goes to " +
	"the site's working copy (named default until now), and this environment keeps serving its domains under its new name. " +
	"An environment cannot be renamed back."

// CheckPendingMigrations runs before the migrations do and warns of what a
// pending one is about to change that its operator cannot undo: each site
// whose environment named dev has a domain and is renamed by 000239, naming
// the site, its domains and the name it is given (#2608). A failed read is
// logged, never startup's failure.
//
// It also warns of each environment public until 000245 that has no domain,
// which from then on answers only to someone signed in (#2619).
func CheckPendingMigrations(ctx context.Context, db *sql.DB) {
	checkPendingRenames(ctx, func() ([]migrate.DevRename, error) { return migrate.PendingDevRenames(ctx, db) })
	checkPendingPublic(ctx, func() ([]migrate.PublicLoss, error) { return migrate.PendingPublicLosses(ctx, db) })
}

// publicLossMessage is the warning for a public environment 000245 makes
// answer only to someone signed in.
const publicLossMessage = "migration 000245 is about to make this public site environment answer only to someone signed in: public is now " +
	"a setting of a domain, and the environment has none. Add a domain to it and make that domain public to open it to anyone again."

func checkPendingPublic(ctx context.Context, read func() ([]migrate.PublicLoss, error)) {
	losses, err := read()
	if err != nil {
		slog.WarnContext(ctx, "reading what migration 000245 makes private failed", "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	for _, l := range losses {
		Warn(ctx, CodeSitePublicWithoutDomain, publicLossMessage,
			"site", logsan.SanitizeForLog(l.Site), "environment", logsan.SanitizeForLog(l.Environment))
	}
}

func checkPendingRenames(ctx context.Context, read func() ([]migrate.DevRename, error)) {
	renames, err := read()
	if err != nil {
		slog.WarnContext(ctx, "reading what migration 000239 renames failed", "error", logsan.SanitizeForLog(err.Error()))
		return
	}
	for _, r := range renames {
		Warn(ctx, CodeSiteEnvironmentRenamed, devRenameMessage,
			"site", logsan.SanitizeForLog(r.Site), "domains", logsan.SanitizeForLog(r.Domains), "renamed_to", r.NewName)
	}
}
