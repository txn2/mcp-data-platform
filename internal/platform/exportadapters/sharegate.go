package exportadapters

import (
	"context"
	"errors"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// errSharingNotAllowed is the refusal a gated store answers an export's
// share link with when the caller's persona withholds sharing.
var errSharingNotAllowed = errors.New(access.ErrSharingNotAllowed)

// gatedShares is a share store whose Insert refuses a caller whose persona
// withholds sharing (#2241). An export's create_public_link mints its share
// through it, so asking an export for a link is held to the same permission
// as the Share dialog and manage_asset.
type gatedShares struct {
	portal.ShareStore
	allowed func(persona string) bool
}

// GatedShares wraps shares so a share is created only for an administrator or
// a caller whose persona allows sharing. A nil check returns shares as it is.
func GatedShares(shares portal.ShareStore, allowed func(persona string) bool) portal.ShareStore {
	if shares == nil || allowed == nil {
		return shares
	}
	return gatedShares{ShareStore: shares, allowed: allowed}
}

// Insert refuses the share when the call's persona withholds sharing.
func (g gatedShares) Insert(ctx context.Context, share *portal.Share) error {
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil || (!pc.IsAdmin && !g.allowed(pc.PersonaName)) {
		return errSharingNotAllowed
	}
	return g.ShareStore.Insert(ctx, share) //nolint:wrapcheck // the store's own error is what the export logs
}
