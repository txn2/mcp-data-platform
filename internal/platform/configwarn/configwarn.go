// Package configwarn is where the platform reports a configuration warning
// (#1898). A warning is a log line and a count: config_validation_warnings_total
// under a bounded code, so a fleet sees a misconfiguration as a series and not
// only as a line somebody has to read. It also holds the checks the platform
// runs over its personas, connections and agent instructions at boot, and
// again for a persona written through the admin API.
//
// Some warnings are raised while the configuration file is read, before the
// observability layer exists. Those are counted here and handed to the
// recorder when Flush runs, so a boot warning is never lost to ordering.
package configwarn

import (
	"context"
	"log/slog"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/opsobs"
)

// The warning codes config_validation_warnings_total records. Each names one
// condition; the log line beside it carries the specifics.
const (
	// CodePersonaToolUnregistered: a persona's tools.allow names, without a
	// wildcard, a tool this deployment does not register.
	CodePersonaToolUnregistered = "persona_tool_unregistered"
	// CodePersonaIncoherent: a persona grants a capability it cannot complete.
	CodePersonaIncoherent = "persona_incoherent"
	// CodeAgentInstructionsUnknownTool: the agent instructions name a tool
	// this deployment does not register.
	CodeAgentInstructionsUnknownTool = "agent_instructions_unknown_tool"
	// CodeUnusedConnection: no persona's connection rules admit a connection.
	CodeUnusedConnection = "unused_connection"
	// CodeUnrecognizedKeys: the configuration file carries keys the platform
	// ignores.
	CodeUnrecognizedKeys = "unrecognized_keys"
	// CodeDeprecatedAPIVersion: the configuration names a deprecated apiVersion.
	CodeDeprecatedAPIVersion = "deprecated_api_version"
	// CodeDeprecatedKey: the configuration uses a renamed key.
	CodeDeprecatedKey = "deprecated_key"
	// CodePlaceholderUnexpanded: a ${...} placeholder named an unset variable.
	CodePlaceholderUnexpanded = "placeholder_unexpanded"
	// CodeEphemeralOAuthKey: the OAuth server signs with a per-process key.
	CodeEphemeralOAuthKey = "ephemeral_oauth_signing_key"
	// CodePortalNoObjectStorage: the portal has no object storage.
	CodePortalNoObjectStorage = "portal_no_object_storage"
	// CodeResourcesNoObjectStorage: managed resources have no object storage.
	CodeResourcesNoObjectStorage = "resources_no_object_storage"
	// CodeMemoryNoEmbedding: memory runs without an embedding provider.
	CodeMemoryNoEmbedding = "memory_no_embedding"
	// CodeExcludePersonaUnknown: calls.exclude_personas names no persona.
	CodeExcludePersonaUnknown = "exclude_persona_unknown"
	// CodeDeploymentIDUnset: an OTLP exporter is on with no deployment id.
	CodeDeploymentIDUnset = "deployment_id_unset"
)

// logKeyCode is the attribute every warning's log line carries its code under,
// so a log search and the metric name the condition the same way.
const logKeyCode = "code"

// pending holds the warnings raised before a recorder was installed.
//
//nolint:gochecknoglobals // the boot-time buffer Flush drains; one per process, as the recorder is
var pending = struct {
	sync.Mutex
	counts map[string]int64
}{counts: map[string]int64{}}

// Warn logs msg as a warning carrying code, and counts it. args are slog
// key/value pairs; values a caller supplies must already be sanitized.
func Warn(ctx context.Context, code, msg string, args ...any) {
	slog.WarnContext(ctx, msg, append(args, logKeyCode, code)...)
	pending.Lock()
	defer pending.Unlock()
	if m := opsobs.Metrics(); m != nil {
		m.RecordConfigWarning(ctx, code, 1)
		return
	}
	pending.counts[code]++
}

// Flush hands the warnings raised before the recorder existed to it. A no-op
// until opsobs has a recorder, and after the first Flush that finds one.
func Flush(ctx context.Context) {
	pending.Lock()
	defer pending.Unlock()
	m := opsobs.Metrics()
	if m == nil {
		return
	}
	for code, n := range pending.counts {
		m.RecordConfigWarning(ctx, code, n)
	}
	clear(pending.counts)
}
