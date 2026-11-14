package middleware

import (
	"encoding/json"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/removedvalue"
)

// A tool that removes a value one of its arguments took (manage_script's
// endpoint_set and schedule_set, #2441) refuses a call naming it with what
// replaces it, rather than with the input schema's list of the values it
// takes. The schema no longer lists the removed value, so the SDK refuses the
// call before the tool's handler could say so; the error contract, which sees
// both the refusal and the request, answers in its place from the registry the
// tool fills (internal/removedvalue).

// codeRemovedValue is the agent-facing error code for a removed value.
const codeRemovedValue = "removed_value"

// removedValueResult is the refusal for a call the SDK's schema check refused
// because it named a removed value, nil for any other result.
func removedValueResult(req mcp.Request, result mcp.Result) mcp.Result {
	ctr, ok := result.(*mcp.CallToolResult)
	if !ok || ctr == nil || !ctr.IsError || !strings.HasPrefix(extractMCPErrorMessage(ctr), sdkArgumentValidationPrefix) {
		return nil
	}
	return removedValueOf(toolCallParams(req))
}

// removedValueOf is the refusal for the first string argument of params that
// names a value its tool removed, nil when none does.
func removedValueOf(params *mcp.CallToolParamsRaw) mcp.Result {
	if params == nil || len(params.Arguments) == 0 {
		return nil
	}
	var args map[string]any
	if json.Unmarshal(params.Arguments, &args) != nil {
		return nil
	}
	for arg, raw := range args {
		value, isString := raw.(string)
		if !isString {
			continue
		}
		if replacement, found := removedvalue.Lookup(params.Name, arg, value); found {
			return BuildErrorResult(ClientInputError(codeRemovedValue,
				value+" was removed: use "+replacement,
				"Call the replacement it names; the removed value is not an alias."))
		}
	}
	return nil
}
