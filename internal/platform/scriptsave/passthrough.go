package scriptsave

import (
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// RulePassthroughConnection names the warning a source gets for each
// connection it names that a saved script's run cannot call (#2086).
const RulePassthroughConnection = "passthrough-connection"

// scriptRunServer is the part of a toolkit that knows which of its
// connections send each call with the caller's own token to an upstream that
// checks it, which a saved script's run, having no token, cannot call.
type scriptRunServer interface {
	ScriptRunCannotServe(connection string) bool
}

// PassthroughConnections answers, over a registry's toolkits at the moment it
// is asked, whether a saved script's run cannot call a connection.
type PassthroughConnections struct {
	Kits interface{ All() []registry.Toolkit }
}

// CannotServe reports whether any toolkit serves connection by forwarding the
// caller's token to an upstream that checks it.
func (p PassthroughConnections) CannotServe(connection string) bool {
	if p.Kits == nil {
		return false
	}
	for _, tk := range p.Kits.All() {
		if s, ok := tk.(scriptRunServer); ok && s.ScriptRunCannotServe(connection) {
			return true
		}
	}
	return false
}

// passthroughFindings warns of each connection source names that a saved
// script's run cannot call. A draft of the same source calls it successfully,
// because a draft runs in the caller's session and carries their token, so
// this is the one place the difference is told before the first scheduled
// run fails. A warning: the save goes through.
func passthroughFindings(source string, cannotServe func(string) bool) []scriptrun.Finding {
	if cannotServe == nil {
		return nil
	}
	var found []scriptrun.Finding
	for _, name := range scriptrun.Validate(source).Connections {
		if !cannotServe(name) {
			continue
		}
		found = append(found, scriptrun.Finding{
			Rule:     RulePassthroughConnection,
			Severity: scriptrun.SeverityWarning,
			Message: fmt.Sprintf("connection %q sends each call with the caller's own sign-in token, and a saved script's run has none, "+
				"so its calls on it fail when it runs on a schedule or with run_script; a draft run succeeds because it runs in your session", name),
			Hint: "Call this upstream through a connection that holds its own credentials.",
		})
	}
	return found
}
