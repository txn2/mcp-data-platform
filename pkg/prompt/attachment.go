package prompt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Attachment links a managed resource to a prompt as reference material the
// prompt's procedure depends on: the template it fills, the checklist it
// follows, the brand header it embeds, the sample payload it matches (#1013).
//
// The link is stored by resource id, not by a copy of the resource, so editing
// the uploaded file updates every prompt that attaches it. The row deliberately
// carries no foreign key to the resource table: deleting a resource must leave
// the attachment row behind so the prompt still serves and both the served
// result and the portal can flag the link as broken.
type Attachment struct {
	PromptID   string `json:"prompt_id" example:"prompt_a1b2c3d4"`
	ResourceID string `json:"resource_id" example:"res_01HK7R9F"`

	// Position orders attachments within a prompt, starting at 0. The order is
	// authored, not incidental: an SOP that says "fill the template, then check
	// it against the rubric" needs the template first.
	Position int `json:"position" example:"0"`

	// AttachedBy is the email of the author who created the link, kept for the
	// same reason approval stamps are kept: an operator reading a shared SOP
	// needs to know who put the material there.
	AttachedBy string `json:"attached_by,omitempty" example:"analyst@example.com"`
}

// AttachKindResource is the kind of material this rule governs: the managed
// resources a prompt attaches (#1013). The kind appears only in the wording of
// a refusal, never in the rule. A prompt also references managed scripts
// (#1289), which this rule does not govern: a script is one person's, so a
// reference resolves for its owner at every prompt scope and cannot be
// narrower than the prompt carrying it (#1404).
const AttachKindResource = "resource"

// AttachmentScope is the subset of an attachment's identity the attach-time
// scope rule needs. It exists so the rule can live in pkg/prompt without
// importing pkg/resource or pkg/script: the caller reads the material and
// passes its visibility through.
//
// Scope uses the resource vocabulary ("global", "persona", "user"), and a kind
// whose own vocabulary differs is translated at the projection that builds this
// value. One vocabulary is what keeps one rule; two would be two rules wearing
// the same name.
type AttachmentScope struct {
	// Kind is what sort of material this is (AttachKindResource), used only to
	// word a refusal.
	Kind string
	// ID identifies the material in error messages.
	ID string
	// DisplayName names the material in error messages, so an author who is
	// blocked learns which attachment is at fault without a second lookup.
	DisplayName string
	// Scope is the material's visibility: "global", "persona", or "user".
	Scope string
	// ScopeIDs is the audience the scope names: every persona a persona-scoped
	// attachment is visible to, or the owning user's subject and/or email for a
	// user-scoped one. It is a set because a script may serve several personas
	// while a resource serves exactly one, and the rule below asks whether the
	// prompt's audience is contained in it — a question a single id cannot
	// answer.
	ScopeIDs []string
}

// Resource scope values mirrored from pkg/resource. Duplicated rather than
// imported to keep pkg/prompt free of a dependency on the resource package;
// TestAttachmentScopeConstantsMatchResource pins them to the originals.
const (
	resourceScopeGlobal  = "global"
	resourceScopePersona = "persona"
	resourceScopeUser    = "user"
)

// ErrAttachmentNotFound reports that no attachment links the prompt and
// resource named in the request.
var ErrAttachmentNotFound = errors.New("attachment not found")

// ErrAttachmentScope marks every rejection from the attachment scope rule, so
// handlers can map it to a conflict carrying the author-facing message rather
// than to a generic failure. Every error CheckAttachScope, CheckAttachOwnership,
// and CheckPromotionAttachments return wraps it.
var ErrAttachmentScope = errors.New("attachment scope violation")

// AttachmentStore persists the prompt-to-resource links.
type AttachmentStore interface {
	// Attach links a resource to a prompt at the end of the prompt's ordered
	// attachment list. Attaching an already-attached resource is a no-op that
	// preserves the existing position, so a double-click never reorders.
	Attach(ctx context.Context, a Attachment) error

	// Detach removes one link. Returns ErrAttachmentNotFound when no such link
	// exists. Remaining attachments keep their relative order.
	Detach(ctx context.Context, promptID, resourceID string) error

	// ListByPrompt returns one prompt's attachments in authored order.
	ListByPrompt(ctx context.Context, promptID string) ([]Attachment, error)

	// ListByResource returns the ids of prompts that attach a resource, so the
	// resource detail view can answer "what depends on this file?" before an
	// operator deletes it.
	ListByResource(ctx context.Context, resourceID string) ([]string, error)

	// Reorder rewrites one prompt's attachment order to exactly the given
	// resource ids. Ids not currently attached are rejected; omitting a
	// currently attached id detaches it.
	Reorder(ctx context.Context, promptID string, resourceIDs []string) error
}

// AttachmentProvider exposes the attachment capability through store decorators
// that would otherwise hide it from a type assertion, mirroring
// CollectionProvider. The promptlayer notifying wrapper implements it by
// delegating to the wrapped store; the composition root resolves the capability
// with AsAttachmentStore.
type AttachmentProvider interface {
	// Attachments returns the underlying attachment capability, or nil when the
	// backing store does not support attachments.
	Attachments() AttachmentStore
}

// AsAttachmentStore resolves the attachment capability from a prompt store,
// looking through any decorator that implements AttachmentProvider. Returns nil
// when the store has no attachment support (a file-only or in-memory store).
func AsAttachmentStore(store Store) AttachmentStore {
	if as, ok := store.(AttachmentStore); ok {
		return as
	}
	if ap, ok := store.(AttachmentProvider); ok {
		return ap.Attachments()
	}
	return nil
}

// CheckAttachScope reports whether a resource may be attached to a prompt of
// the given scope, and returns a caller-facing error naming the resource when
// it may not.
//
// The rule is that an attachment must be at least as widely visible as the
// prompt that carries it. A prompt is served to everyone its scope reaches, so
// a narrower attachment would produce a shared SOP whose materials are missing
// for most of its audience: the failure would surface at serve time, to a
// reader who cannot fix it, rather than at authoring time to the one person who
// can.
//
//	global             -> any prompt
//	persona P...       -> personal prompts, and persona prompts whose every
//	                      persona is among P... (a prompt serving a persona the
//	                      material does not would reach readers outside it)
//	user U             -> personal prompts only, and only the attaching
//	                      author's own (enforced by the caller, which knows the
//	                      subject; see CheckAttachOwnership)
func CheckAttachScope(promptScope string, promptPersonas []string, res AttachmentScope) error {
	switch res.Scope {
	case resourceScopeGlobal:
		return nil
	case resourceScopePersona:
		return checkPersonaAttach(promptScope, promptPersonas, res)
	case resourceScopeUser:
		if promptScope != ScopePersonal {
			return attachScopeErr(res, fmt.Sprintf("a private %s can only be attached to a personal prompt", kindWord(res)))
		}
		return nil
	default:
		return attachScopeErr(res, fmt.Sprintf("unknown %s scope %q", kindWord(res), res.Scope))
	}
}

// checkPersonaAttach applies the persona half of CheckAttachScope: every
// persona the prompt serves must be one the attached material is visible to.
func checkPersonaAttach(promptScope string, promptPersonas []string, res AttachmentScope) error {
	switch promptScope {
	case ScopePersonal:
		return nil
	case ScopePersona:
		for _, p := range promptPersonas {
			if !containsFold(res.ScopeIDs, p) {
				return attachScopeErr(res, fmt.Sprintf(
					"it is visible only to %s but the prompt also serves persona %q",
					personaList(res.ScopeIDs), p))
			}
		}
		if len(promptPersonas) == 0 {
			return attachScopeErr(res, fmt.Sprintf(
				"it is visible only to %s but the prompt names no persona", personaList(res.ScopeIDs)))
		}
		return nil
	default:
		return attachScopeErr(res, fmt.Sprintf(
			"it is visible only to %s but the prompt is %s", personaList(res.ScopeIDs), promptScope))
	}
}

// containsFold reports whether values holds v, case-insensitively.
func containsFold(values []string, v string) bool {
	for _, candidate := range values {
		if strings.EqualFold(candidate, v) {
			return true
		}
	}
	return false
}

// personaList renders an audience for a refusal message: "persona \"analyst\"" for
// one, "personas \"analyst\", \"engineer\"" for several, and "no persona" for an
// empty set, which is a material nobody reaches by persona at all.
func personaList(personas []string) string {
	switch len(personas) {
	case 0:
		return "no persona"
	case 1:
		return "persona " + strconv.Quote(personas[0])
	}
	quoted := make([]string, 0, len(personas))
	for _, p := range personas {
		quoted = append(quoted, strconv.Quote(p))
	}
	return "personas " + strings.Join(quoted, ", ")
}

// CheckAttachOwnership reports whether the caller may attach a user-scoped
// resource, which is allowed only when the resource lives in the caller's own
// user scope and the prompt is the caller's own personal prompt. It is separate
// from CheckAttachScope because it needs the caller's identity, which the scope
// rule alone does not.
//
// An admin is deliberately not exempt: attaching another user's private
// resource would build an SOP whose materials only that user can read.
//
// A user-scoped resource identifies its owner by subject or by email, because
// an admin can scope a resource to a user by email address before that user has
// ever signed in (see resource.VisibleScopes). Both forms are accepted here for
// the same reason they are accepted there: rejecting the email form would block
// an author from attaching a resource they demonstrably can read.
func CheckAttachOwnership(callerSub, callerEmail, promptOwnerEmail string, res AttachmentScope) error {
	if res.Scope != resourceScopeUser {
		return nil
	}
	if !ownsUserScope(callerSub, callerEmail, res.ScopeIDs) {
		return attachScopeErr(res, fmt.Sprintf("it is another user's private %s", kindWord(res)))
	}
	if !strings.EqualFold(callerEmail, promptOwnerEmail) {
		return attachScopeErr(res, fmt.Sprintf("a private %s can only be attached to your own prompt", kindWord(res)))
	}
	return nil
}

// ownsUserScope reports whether any of a user scope's ids names the caller, by
// subject or by email. An empty set names nobody and is never the caller.
func ownsUserScope(callerSub, callerEmail string, scopeIDs []string) bool {
	for _, scopeID := range scopeIDs {
		if scopeID == "" {
			continue
		}
		if callerSub != "" && scopeID == callerSub {
			return true
		}
		if callerEmail != "" && strings.EqualFold(scopeID, callerEmail) {
			return true
		}
	}
	return false
}

// CheckPromotionAttachments reports whether every attachment on a prompt would
// still satisfy the scope rule once the prompt moves to the target scope. It is
// the gate on the #1009 promotion flow: promoting a personal prompt that
// carries a private template must fail at request time, naming the offending
// resource, rather than succeed into a shared scope whose readers get nothing.
func CheckPromotionAttachments(targetScope string, targetPersonas []string, attached []AttachmentScope) error {
	for _, res := range attached {
		if err := CheckAttachScope(targetScope, targetPersonas, res); err != nil {
			return err
		}
	}
	return nil
}

// attachScopeErr formats a scope rejection so the message always names the kind
// of material and the one the author must fix.
func attachScopeErr(res AttachmentScope, reason string) error {
	name := res.DisplayName
	if name == "" {
		name = res.ID
	}
	//nolint:revive // string-format: the message is a complete author-facing sentence, matching the pre-existing refusal wording
	return fmt.Errorf("%s %s cannot be attached: %s: %w",
		kindWord(res), strconv.Quote(name), reason, ErrAttachmentScope)
}

// kindWord names the kind of material in a refusal, defaulting to "resource"
// for a value built before kinds existed.
func kindWord(res AttachmentScope) string {
	if res.Kind == "" {
		return AttachKindResource
	}
	return res.Kind
}
