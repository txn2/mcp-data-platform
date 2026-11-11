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
func CheckPendingMigrations(ctx context.Context, db *sql.DB) {
	checkPendingRenames(ctx, func() ([]migrate.DevRename, error) { return migrate.PendingDevRenames(ctx, db) })
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
