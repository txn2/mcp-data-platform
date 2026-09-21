package scriptexec

import (
	"context"
	"errors"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Draft is one draft run whose author asked for its writes (#1822): the
// script it drafts, the run it is, and the person running it.
//
// A draft with allow_writes promises to write for real, and the next step of a
// staging pipeline reads what platform.export wrote -- the file it registers,
// the table it queries. A draft that previewed its exports while writing
// everything else could not exercise that pipeline at all.
type Draft struct {
	// Script is the record the draft runs as. A draft of a script that is not
	// saved yet carries a record with a name and no id.
	Script *script.Script
	// RunID is the draft's own id, the session every call it makes records.
	RunID string
	// Email and Subject are the person running the draft, and Roles the roles
	// their session holds. A library output lands in that person's library,
	// which is where a platform run of the same script, acting for them as its
	// author, would land it.
	Email   string
	Subject string
	Roles   []string
	// Caller is the draft's own session, which a bucket delivery is made over.
	Caller scriptrun.Caller
}

// errUnsavedPortal refuses a portal output from a script that is not saved.
// A portal output is the version series of one asset per (script, output
// name), and a script with no id has no series to add a version to.
var errUnsavedPortal = errors.New("a portal output belongs to a saved script, and this draft is of a script " +
	"that is not saved yet; save it and draft it again, or write the output to \"resources\"")

// DraftExporter builds the writer a draft that was allowed to write persists
// its outputs through: the same writer a platform run uses, with nothing to
// record on, because a draft has no run row. A nil Handle -- a deployment with
// nowhere to keep runs -- returns nil, and the draft previews.
func (h *Handle) DraftExporter(d Draft) scriptrun.Exporter {
	if h == nil || d.Script == nil {
		return nil
	}
	w := &outputWriter{
		deps: h.export, run: &script.Run{ID: d.RunID}, script: d.Script, caller: d.Caller,
		written: map[string]bool{}, delivered: map[string]string{},
		claims: resource.BuildClaims(d.Script.Principal(), d.Script.OwnerEmail, "", d.Roles, false).
			ActingFor(d.Email, d.Subject),
	}
	return &draftWriter{outputWriter: w}
}

// draftWriter is an outputWriter serving a draft. It differs in one respect:
// a script that is not saved has no portal asset, so a portal output from one
// is refused rather than filed under an identity no later run would find.
type draftWriter struct {
	*outputWriter
}

// Export writes one output, refusing a portal one from an unsaved script.
func (d *draftWriter) Export(ctx context.Context, req scriptrun.ExportRequest) (*scriptrun.ExportResult, error) {
	if req.Destination.IsPortal() && d.script.ID == "" {
		return nil, errUnsavedPortal
	}
	return d.outputWriter.Export(ctx, req)
}

// PublishData refreshes a portal document, which an unsaved script has none of.
func (d *draftWriter) PublishData(ctx context.Context, req scriptrun.PublishRequest) (*scriptrun.ExportResult, error) {
	if d.script.ID == "" {
		return nil, errUnsavedPortal
	}
	return d.outputWriter.PublishData(ctx, req)
}
