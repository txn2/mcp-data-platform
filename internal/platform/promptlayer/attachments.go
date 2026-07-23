package promptlayer

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// resolveAttachments evaluates a prompt's attached materials for the caller
// identified by the request context. It returns nil when no resolver is bound,
// when the prompt has no id (static and file prompts cannot carry attachments),
// or when the prompt has no attachments.
//
// Identity comes from the PlatformContext: the tool-call middleware sets it on
// manage_prompt use, and the prompt-visibility middleware sets it on
// prompts/get. Without one the caller is anonymous, which resolves only global
// materials — the same set an unauthenticated reader already sees.
func (h *Handle) resolveAttachments(ctx context.Context, pr *prompt.Prompt, personas []string) []attachserve.Resolved {
	if h == nil || h.attachments == nil || pr == nil || pr.ID == "" {
		return nil
	}
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		// No identity: only global materials resolve, matching what an
		// unauthenticated reader already sees of the resource surface.
		return h.attachments.Resolve(ctx, pr.ID, resource.Claims{})
	}
	claims := resource.BuildClaims(pc.UserID, pc.UserEmail, pc.PersonaName, pc.Roles, pc.IsAdmin)
	// A caller can hold several personas, and PlatformContext carries only the
	// first. Where the full set is known (the prompts/get path resolves it to
	// decide visibility) it replaces the single-persona default, so a member of
	// two personas is not refused material their own second persona owns.
	if len(personas) > 0 {
		claims.Personas = personas
	}
	return h.attachments.Resolve(ctx, pr.ID, claims)
}

// appendAttachments adds the prompt's resolved materials to a prompts/get
// result as additional user-role messages: the framing text, then each embedded
// resource or resource link, then any withheld note.
//
// They are separate messages rather than extra content on the prompt's own
// message because a prompt message carries exactly one content item in the MCP
// schema, and because keeping the procedure and its materials in distinct
// messages is what lets a client render or elide the materials independently.
func (h *Handle) appendAttachments(ctx context.Context, res *mcp.GetPromptResult, pr *prompt.Prompt, personas []string) {
	if res == nil {
		return
	}
	for _, c := range attachserve.Content(h.resolveAttachments(ctx, pr, personas)) {
		res.Messages = append(res.Messages, &mcp.PromptMessage{Role: promptRoleUser, Content: c})
	}
}

// guardAttachmentScope is the shared store's attachment guard: it refuses a
// write that would leave one of the prompt's attached materials unreachable for
// the audience the write gives it.
//
// Two targets are checked, because a prompt can reach a wider audience two
// ways. The prompt's own scope covers a direct edit and an admin's approval of
// a promotion (which lands the new scope on the row). A pending promotion
// request covers the moment an owner asks: the author is the only person who
// can re-scope or detach the material, so they must be told at request time
// rather than the reviewer discovering it at approval.
//
// It fails closed. A resolver error blocks the write, because the alternative
// is silently publishing a shared SOP whose template nobody but its author can
// read.
func (h *Handle) guardAttachmentScope(ctx context.Context, p *prompt.Prompt) error {
	if h.attachments == nil || p == nil || p.ID == "" {
		return nil
	}
	// The returned error is passed through unwrapped: it is a complete
	// author-facing sentence naming the resource to fix, and every added
	// "checking X:" prefix pushes that sentence further from the start of the
	// message the author actually reads.
	if err := h.attachments.CheckPromotion(ctx, p.ID, p.Scope, p.Personas); err != nil {
		return err //nolint:wrapcheck // caller-facing message, deliberately verbatim
	}
	if p.ReviewRequested && p.RequestedScope != "" {
		return h.attachments.CheckPromotion(ctx, p.ID, p.RequestedScope, p.RequestedPersonas) //nolint:wrapcheck // caller-facing message, deliberately verbatim
	}
	return nil
}
