package middleware

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// CallReferenceScheme is the reference form a data call's identifier is handed
// back under. An agent may cite either the bare id or this reference when it
// names the calls an asset was built from.
const CallReferenceScheme = "mcp:call:"

// CallReferenceKey is the top-level key of the block this middleware appends.
const CallReferenceKey = "call_reference"

// CallReference is the receipt a data call hands back: the identifier of the
// audit event that recorded it (issue #1320). It is what makes provenance
// citable — an agent that ran three queries and one API call can name exactly
// which of them produced the asset it is about to save, instead of relying on
// the platform's default "everything since the last capture" window.
type CallReference struct {
	// CallID is the audit event id of this call.
	CallID string `json:"call_id"`
	// Reference is CallID in reference form (mcp:call:<id>).
	Reference string `json:"reference"`
}

// MCPCallReferenceMiddleware appends each data call's own reference to its
// result, so the agent that made the call can cite it as an asset's source.
//
// It fires only for calls the platform records as data access — the toolkit
// kinds in sourceKinds, which are the same kinds provenance capture draws its
// sources from. Bookkeeping calls (saving an asset, reading memory, searching)
// are never an asset's source, so stamping them would spend context on an id
// nothing can use.
//
// Failed calls are not stamped. They are still recorded and still appear in an
// asset's captured provenance through the default window (a failed query is
// part of how the answer was reached), but a result that carries an error
// message is not a place to hand back a citation token.
//
// The reference is appended as a JSON content block and mirrored into
// StructuredContent, matching how the enrichment middleware delivers the
// context it adds: clients that render only structured output see it too. A
// result whose handler set no structured output keeps the reference in content
// alone — see mirrorEnrichmentToStructured for why one is not synthesized.
func MCPCallReferenceMiddleware(sourceKinds []string) mcp.Middleware {
	kinds := make(map[string]struct{}, len(sourceKinds))
	for _, k := range sourceKinds {
		kinds[k] = struct{}{}
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != methodToolsCall {
				return next(ctx, method, req)
			}
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			if callResult, eventID := referenceableCall(ctx, result, kinds); callResult != nil {
				appendCallReference(callResult, eventID)
			}
			return result, nil
		}
	}
}

// referenceableCall returns the result to stamp and the id to stamp it with,
// or a nil result when this call gets no reference: it is not a data call, the
// platform recorded no id for it, or it failed.
func referenceableCall(ctx context.Context, result mcp.Result, kinds map[string]struct{}) (call *mcp.CallToolResult, eventID string) {
	pc := GetPlatformContext(ctx)
	if pc == nil || pc.EventID == "" {
		return nil, ""
	}
	if _, ok := kinds[pc.ToolkitKind]; !ok {
		return nil, ""
	}
	callResult, ok := result.(*mcp.CallToolResult)
	if !ok || callResult == nil || callResult.IsError {
		return nil, ""
	}
	return callResult, pc.EventID
}

// appendCallReference adds the call's reference block to result and mirrors it
// into the structured output the handler set, if any. Best-effort: a marshal
// failure leaves the result untouched rather than failing a call that already
// succeeded.
func appendCallReference(result *mcp.CallToolResult, eventID string) {
	block := map[string]CallReference{
		CallReferenceKey: {CallID: eventID, Reference: CallReferenceScheme + eventID},
	}
	payload, err := json.Marshal(block)
	if err != nil {
		return
	}
	before := len(result.Content)
	result.Content = append(result.Content, &mcp.TextContent{Text: string(payload)})
	mirrorEnrichmentToStructured(result, before)
}
