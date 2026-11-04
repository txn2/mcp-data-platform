package scripttx

import (
	"fmt"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptfail"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptsession"
)

// CapabilityMaintain is platform.maintain (#2410).
const CapabilityMaintain = "platform.maintain"

// Maintain is platform.maintain(connection, command, table = None, on_error =
// ""): ANALYZE, VACUUM (with ANALYZE) or REINDEX TABLE CONCURRENTLY, run
// outside a transaction, for the administrator or a persona the connection's
// schema_apply_personas names. It answers what ran and how long it took.
// Creating or dropping an index is refused naming manage_schema.
func (h *Host) Maintain(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		connection, command, onError string
		table                        starlark.Value = starlark.None
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "connection", &connection, "command", &command,
		"table?", &table, argOnError, &onError); err != nil {
		return nil, fmt.Errorf(inBinding, b.Name(), err)
	}
	returnFailure, err := scriptfail.OnError(b.Name(), onError)
	if err != nil {
		return nil, err //nolint:wrapcheck // names the binding
	}
	call := map[string]any{argConnection: connection, "command": command}
	switch t := table.(type) {
	case starlark.NoneType:
	case starlark.String:
		call["table"] = string(t)
	default:
		return nil, fmt.Errorf("in %s: table is a table name or None, not %s", b.Name(), table.Type())
	}
	out, err := h.Invoke(b, scriptsession.AppDBMaintain, call)
	if err != nil {
		return h.Failed(b, err, returnFailure)
	}
	stmt, _ := out["statement"].(string)
	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"statement":   starlark.String(stmt),
		"duration_ms": starlark.MakeInt64(count(out["duration_ms"])),
	}), nil
}
