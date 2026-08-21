package promptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
)

// Command names for the prompt-to-script reference commands (#1289).
const (
	cmdAttachScript = "attach_script"
	cmdDetachScript = "detach_script"
)

// fieldScript is the JSON result key naming the script a reference command
// acted on.
const fieldScript = "script"

// handlePromptAttachScript references a managed script from a prompt, so
// serving the prompt hands the agent that script's contract, its latest
// results, and the instruction to run it.
//
// Authorization is the prompt's, not the script's: referencing is an edit to
// the prompt, so it takes the same authority every other prompt mutation takes.
// The script's own visibility governs one thing more — a caller may only
// reference a script they can see — and the response carries the note saying
// who the reference will resolve for.
func (h *Handle) handlePromptAttachScript(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	pr, res := h.resolveForScriptEdit(ctx, input, "attach a script to")
	if res != nil {
		return res, nil, nil
	}
	note, err := h.attach.Scripts().Attach(ctx, attachserve.ScriptAttachRequest{
		Prompt:        pr,
		Ref:           input.Script,
		CallerEmail:   resolveEmail(ctx),
		CallerIsAdmin: h.isAdminPersona(ctx),
	})
	if err != nil {
		return h.scriptRefError(ctx, "failed to attach script", input.Script, err), nil, nil
	}
	out := map[string]any{
		fieldStatus: "attached",
		fieldName:   pr.Name,
		fieldScript: input.Script,
	}
	// The note is present exactly when this prompt serves somebody the script
	// does not, which is what its author needs to hear at the moment they made
	// the reference rather than from a reader who received less than the prompt
	// reads.
	if note != "" {
		out["audience_note"] = note
	}
	return promptJSONResult(out)
}

// handlePromptDetachScript removes one script reference from a prompt.
func (h *Handle) handlePromptDetachScript(ctx context.Context, input managePromptInput) (*mcp.CallToolResult, any, error) {
	pr, res := h.resolveForScriptEdit(ctx, input, "detach a script from")
	if res != nil {
		return res, nil, nil
	}
	if err := h.attach.Scripts().Detach(ctx, pr.ID, input.Script); err != nil {
		if errors.Is(err, prompt.ErrScriptAttachmentNotFound) {
			return promptErrorResult(fmt.Sprintf("prompt %q does not reference that script", pr.Name)), nil, nil
		}
		return h.scriptRefError(ctx, "failed to detach script", input.Script, err), nil, nil
	}
	return promptJSONResult(map[string]any{
		fieldStatus: "detached",
		fieldName:   pr.Name,
		fieldScript: input.Script,
	})
}

// resolveForScriptEdit resolves and authorizes the prompt a reference command
// targets. It returns either the prompt or the error result to send back, never
// both, so each command reads as one guarded call.
func (h *Handle) resolveForScriptEdit(ctx context.Context, input managePromptInput, action string) (*prompt.Prompt, *mcp.CallToolResult) {
	if h.attach.Scripts() == nil {
		return nil, promptErrorResult("managed scripts are not available on this deployment")
	}
	if input.Name == "" {
		return nil, promptErrorResult("name is required")
	}
	if input.Script == "" {
		return nil, promptErrorResult("script is required: the mcp:script:<id> reference search returns, or a script id")
	}
	email := resolveEmail(ctx)
	pr, err := h.resolveManagedPrompt(ctx, input.Name, email, input.Scope, input.OwnerEmail)
	if err != nil {
		slog.Error(promptErrGet, promptLogKey, input.Name, promptLogKeyErr, err)
		return nil, h.promptErrorDetail(ctx, promptErrGet, err)
	}
	if pr == nil {
		return nil, promptErrorResult(fmt.Sprintf("prompt %q not found", input.Name))
	}
	if pr.Source == prompt.SourceSystem {
		return nil, promptErrorResult("this prompt is defined in server configuration or built in and is read-only; " +
			"a script can only be referenced from a stored prompt")
	}
	if msg := authorizePromptMutation(pr, email, action, h.isAdminPersona(ctx)); msg != "" {
		return nil, promptErrorResult(msg)
	}
	return pr, nil
}

// scriptRefError renders a reference-command failure. A refusal the author can
// act on — the scope rule, or a reference that names nothing — is surfaced
// verbatim, because its message already says exactly what to fix. Anything else
// is a store failure, logged and reported through the shared detail path, which
// withholds internals from a non-admin caller.
func (h *Handle) scriptRefError(ctx context.Context, what, ref string, err error) *mcp.CallToolResult {
	if errors.Is(err, prompt.ErrAttachmentScope) {
		return promptErrorResult(err.Error())
	}
	slog.Error(what, "script", ref, promptLogKeyErr, err)
	return h.promptErrorDetail(ctx, what, err)
}
