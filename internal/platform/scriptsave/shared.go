package scriptsave

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptlib"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptshare"
)

// sharedCode is each function of source a library or another live script
// already defines (#2273): what a save that goes through reports, never what
// refuses one. Sources it cannot read report none rather than fail the save.
func (g *Gate) sharedCode(ctx context.Context, req Request, source string) []scriptshare.Match {
	if g.Shared == nil {
		return []scriptshare.Match{}
	}
	others, err := g.Shared.CodeSources(ctx)
	if err != nil {
		slog.WarnContext(ctx, "script save: reading the sources a save is compared with failed",
			"error", logsan.SanitizeForLog(err.Error()))
		return []scriptshare.Match{}
	}
	self := ""
	if req.Existing != nil {
		self = req.Existing.ID
	}
	return scriptshare.Find(source, self, scriptlib.SourceIsLibrary(source), others)
}
