package prompt

import (
	"context"
	"errors"
)

// ScriptAttachment links a managed script to a prompt as part of the procedure
// the prompt describes (#1289): the report the analysis reads, the export it
// compares against. Serving the prompt hands the agent the script's contract
// and its latest results, plus the instruction to call run_script for fresh
// ones; serving never executes anything.
//
// The link is stored as the script's canonical reference (mcp:script:<id>,
// #1302) rather than a bare id, because that reference is the platform's one way
// to name a script from outside its own package — the same string search emits,
// fetch dereferences, and an agent can carry between the two. It resolves by id,
// so renaming a script leaves every attachment intact.
//
// Like a resource attachment, the row deliberately carries no foreign key to the
// script: deleting a script must leave the link behind so the prompt still
// serves and both the served payload and the portal can report the reference as
// broken.
type ScriptAttachment struct {
	PromptID string `json:"prompt_id" example:"prompt_a1b2c3d4"`
	// ScriptRef is the canonical mcp:script:<id> reference.
	ScriptRef string `json:"script_ref" example:"mcp:script:a1b2c3d4-0000-0000-0000-000000000000"`

	// Position orders referenced scripts within a prompt, starting at 0. The
	// order is authored: a procedure that says "refresh the sales report, then
	// compare it against the forecast" needs the sales report first.
	Position int `json:"position" example:"0"`

	// AttachedBy is the email of the author who created the link, kept for the
	// same reason approval stamps are: a reader of a shared procedure needs to
	// know who put the automation in it.
	AttachedBy string `json:"attached_by,omitempty" example:"analyst@example.com"`
}

// ErrScriptAttachmentNotFound reports that no attachment links the prompt and
// script named in the request.
var ErrScriptAttachmentNotFound = errors.New("script attachment not found")

// ScriptAttachmentStore persists the prompt-to-script links. It is a sibling of
// AttachmentStore rather than an extension of it because the two write
// different tables; the rule that governs both — an attachment must be at least
// as widely visible as the prompt carrying it — is CheckAttachScope, and is
// shared.
type ScriptAttachmentStore interface {
	// AttachScript links a script to a prompt at the end of the prompt's
	// ordered list. Attaching an already-attached script is a no-op that
	// preserves the existing position, so a repeated call never reorders.
	AttachScript(ctx context.Context, a ScriptAttachment) error

	// DetachScript removes one link, returning ErrScriptAttachmentNotFound when
	// no such link exists. Remaining attachments keep their relative order.
	DetachScript(ctx context.Context, promptID, scriptRef string) error

	// ListScriptsByPrompt returns one prompt's script references in authored
	// order.
	ListScriptsByPrompt(ctx context.Context, promptID string) ([]ScriptAttachment, error)
}

// ScriptAttachmentProvider exposes the script-attachment capability through
// store decorators that would otherwise hide it from a type assertion,
// mirroring AttachmentProvider.
type ScriptAttachmentProvider interface {
	// ScriptAttachments returns the underlying capability, or nil when the
	// backing store does not support script attachments.
	ScriptAttachments() ScriptAttachmentStore
}

// AsScriptAttachmentStore resolves the script-attachment capability from a
// prompt store, looking through any decorator that implements
// ScriptAttachmentProvider. Returns nil when the store has no support (a
// file-only or in-memory store).
func AsScriptAttachmentStore(store Store) ScriptAttachmentStore {
	if as, ok := store.(ScriptAttachmentStore); ok {
		return as
	}
	if ap, ok := store.(ScriptAttachmentProvider); ok {
		return ap.ScriptAttachments()
	}
	return nil
}
