package storeresync

import (
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/connid"
	"github.com/txn2/mcp-data-platform/pkg/connreconcile"
)

// Removed drops a connection the reload bus says is gone from this replica's
// toolkits, unless the configuration file declares it. A peer that predates
// the delete refusal, or one racing a delete, can still broadcast the removal
// of such a connection; honoring it would take it out of service here until a
// restart put it back, and the file is unaffected by anything the store did
// (#1400). A delete is applied without a store read, so a transient store
// failure can never leave a deleted connection live. A failed removal is
// logged at WARN under msg, not ERROR: removing an already-absent connection
// is not state-corrupting.
func Removed(toolkits connreconcile.ToolkitSource, declared connid.Declarer, kind, name, msg string) {
	if declared != nil && declared.DeclaresConnection(kind, name) {
		slog.Info("reload-bus: keeping a connection the configuration file declares", logKeyKind, kind, logKeyName, name)
		return
	}
	for _, f := range connreconcile.New(toolkits).Remove(kind, name) {
		slog.Warn(msg, logKeyKind, kind, logKeyName, name, logKeyError, f.Err)
	}
}

// Read is what this replica's store read found for a connection a peer
// announced as saved: its config when Found, or the read's failure.
type Read struct {
	Config map[string]any
	Found  bool
	Err    error
}

// Upserted applies a peer's save once this replica has read the store. The
// read's outcome drives a three-way branch so a transient read failure never
// silently drops a live connection (#885):
//
//   - Err set (a failure, not a not-found): log at ERROR and leave the live
//     config in place. Removing here would drop a healthy connection over a
//     database blip; a later upsert event re-materializes it.
//   - not found (raced with a concurrent delete): remove it, as Removed does.
//   - found: adopt the stored config, so the changed config takes effect with
//     the state the saving replica stored (#1714). A toolkit that fails is
//     logged at ERROR, since it is out of sync with the store; the others are
//     still updated.
func Upserted(toolkits connreconcile.ToolkitSource, declared connid.Declarer, kind, name string, read Read) {
	switch {
	case read.Err != nil:
		slog.Error("reload-bus: failed to read connection from store; keeping live config",
			logKeyKind, kind, logKeyName, name, logKeyError, read.Err)
	case !read.Found:
		Removed(toolkits, declared, kind, name, "reload-bus: failed to remove connection from toolkit")
	default:
		for _, f := range connreconcile.New(toolkits).Adopt(kind, name, read.Config) {
			slog.Error("reload-bus: failed to reconcile connection onto toolkit",
				logKeyKind, kind, logKeyName, name, "phase", f.Phase.String(), logKeyError, f.Err)
		}
	}
}
