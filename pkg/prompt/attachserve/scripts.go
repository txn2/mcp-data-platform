package attachserve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// fieldScriptRef is the JSON key naming a referenced script in the use-response
// summary.
const fieldScriptRef = "script_ref"

// ScriptReader resolves one script's contract by id. It is the half of
// script.Searcher this package needs: serving a prompt reads contracts and
// never ranks. The concrete PostgreSQL script store satisfies it.
type ScriptReader interface {
	Contract(ctx context.Context, id string) (*script.Contract, error)
}

// ScriptDeps carries the collaborators the script resolver needs: the links and
// the contracts they resolve to. Both are required; a deployment missing either
// serves prompts with no referenced scripts rather than failing.
type ScriptDeps struct {
	Attachments prompt.ScriptAttachmentStore
	Scripts     ScriptReader
}

// ScriptResolver resolves a prompt's referenced scripts (#1289) into the form
// each serving surface needs: MCP content appended to a prompts/get result, and
// a JSON summary carried by manage_prompt use.
//
// Like the resource resolver, it never fails a prompt. A reference whose script
// was deleted, or that this caller cannot see, is reported as unavailable and
// the prompt still serves: a procedure that has lost one of its automations is
// still a procedure, and saying so is strictly better than refusing to answer.
type ScriptResolver struct {
	deps ScriptDeps
}

// NewScripts builds a script resolver. A nil result is a valid
// zero-attachment resolver, so callers need no nil checks at every serving site.
func NewScripts(deps ScriptDeps) *ScriptResolver {
	if deps.Attachments == nil || deps.Scripts == nil {
		return nil
	}
	return &ScriptResolver{deps: deps}
}

// ResolvedScript is one referenced script after lookup and the visibility
// check.
type ResolvedScript struct {
	// Reference is always set, even when the script is gone, because it is what
	// an author needs in order to repair a broken link.
	Reference string
	// Availability records the outcome, reusing the resource vocabulary:
	// AvailableEmbedded means the contract is inline below, and the unavailable
	// values mean the caller received nothing but the reason.
	Availability Availability
	// Contract is the resolved contract, set only when Availability is
	// AvailableEmbedded. A caller who may not see the script gets nothing here:
	// a reference must not become a channel for reading a script's name,
	// parameters, or schedule.
	Contract *script.Contract
}

// Resolve returns a prompt's referenced scripts in authored order, each
// evaluated for the caller identified by email and the personas they belong to.
// It returns nil when the prompt references none, and nil rather than an error
// when the link read fails: a store outage must not take down prompt serving.
func (r *ScriptResolver) Resolve(ctx context.Context, promptID, email string, personas []string) []ResolvedScript {
	if r == nil || promptID == "" {
		return nil
	}
	links, err := r.deps.Attachments.ListScriptsByPrompt(ctx, promptID)
	if err != nil {
		slog.Warn("prompt script references: list failed; serving prompt without them",
			"prompt_id", promptID, "error", err)
		return nil
	}
	if len(links) == 0 {
		return nil
	}
	out := make([]ResolvedScript, 0, len(links))
	for _, link := range links {
		out = append(out, r.resolveOne(ctx, link.ScriptRef, email, personas))
	}
	return out
}

// resolveOne evaluates a single reference for the caller.
func (r *ScriptResolver) resolveOne(ctx context.Context, ref, email string, personas []string) ResolvedScript {
	id, err := scriptIDFromRef(ref)
	if err != nil {
		// A stored reference the parser rejects can only have come from an
		// earlier scheme or a hand-edited row; it names nothing resolvable.
		return ResolvedScript{Reference: ref, Availability: UnavailableMissing}
	}
	c, err := r.deps.Scripts.Contract(ctx, id)
	switch {
	case err != nil:
		// A failed read is not a deleted script. Reporting it as missing would
		// tell the agent the automation is gone when it may be fine.
		slog.Warn("prompt script references: contract read failed",
			"script_id", id, "error", err) //nolint:gosec // structured slog of a store error
		return ResolvedScript{Reference: ref, Availability: UnavailableUnreadable}
	case c == nil:
		// A deleted script is the expected case here, not an anomaly: the
		// attachment row deliberately outlives the script so the broken
		// reference stays visible.
		return ResolvedScript{Reference: ref, Availability: UnavailableMissing}
	case !c.VisibleToAny(email, personas):
		// Report only that something is referenced and out of reach. Returning
		// the name or description would make a reference a channel for reading
		// metadata the caller has no access to.
		return ResolvedScript{Reference: ref, Availability: UnavailableForbidden}
	}
	return ResolvedScript{Reference: ref, Availability: AvailableEmbedded, Contract: c}
}

// scriptIDFromRef extracts a script id from its canonical mcp:script:<id>
// reference, so every surface that stores or reads one parses it the same way.
func scriptIDFromRef(ref string) (string, error) {
	parsed, err := knowledgepage.ParseEntityRef(strings.TrimSpace(ref))
	if err != nil {
		return "", fmt.Errorf("parsing script reference: %w", err)
	}
	if parsed.TargetType != knowledgepage.RefTargetScript {
		return "", fmt.Errorf("reference %q names a %s, not a managed script", ref, parsed.TargetType)
	}
	return parsed.ScriptID, nil
}

// normalizeScriptRef canonicalizes what a caller supplied into the stored form,
// the mcp:script:<id> reference, and returns the id it resolves to.
//
// It accepts that reference (what search and fetch hand an agent) and a bare
// script id (what manage_script hands one), because both name the same script
// and refusing either would make an agent translate between two surfaces of one
// platform. Only the reference is ever stored, so there is one form in the
// database and one thing to resolve.
func normalizeScriptRef(s string) (ref, id string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", fmt.Errorf("a script reference is required (%s<id>)", scriptRefPrefix)
	}
	if strings.Contains(s, ":") {
		parsed, parseErr := scriptIDFromRef(s)
		if parseErr != nil {
			return "", "", parseErr
		}
		s = parsed
	}
	ref = knowledgepage.ScriptRef(s)
	if ref == "" {
		return "", "", fmt.Errorf("reference %q names no managed script", s)
	}
	return ref, s, nil
}

// scriptRefPrefix is the canonical reference form, named for error messages.
const scriptRefPrefix = "mcp:script:"

// ScriptAttachRequest is one request to reference a script from a prompt.
type ScriptAttachRequest struct {
	// Prompt is the prompt gaining the reference, read for the audience the
	// scope rule tests against.
	Prompt *prompt.Prompt
	// Ref is the script reference or bare id the caller supplied.
	Ref string
	// CallerSub and CallerEmail identify the author, for the ownership half of
	// the rule (a personal script may only be referenced by its owner, from
	// their own prompt).
	CallerSub, CallerEmail string
}

// Attach references a script from a prompt, after the scope rule admits it.
//
// Two audiences are checked, not one. The prompt's current scope covers what it
// serves today; a pending promotion request covers what its author has already
// asked for, because the author is the only person who can re-scope the script
// or drop the reference, and discovering the conflict at approval time would
// put it in front of a reviewer who cannot fix it.
func (r *ScriptResolver) Attach(ctx context.Context, req ScriptAttachRequest) error {
	if r == nil {
		return errors.New("managed scripts are not available on this deployment")
	}
	if req.Prompt == nil || req.Prompt.ID == "" {
		return errors.New("a stored prompt is required to reference a script")
	}
	ref, id, err := normalizeScriptRef(req.Ref)
	if err != nil {
		return err
	}
	c, err := r.deps.Scripts.Contract(ctx, id)
	if err != nil {
		return fmt.Errorf("reading script %s: %w", id, err)
	}
	if c == nil {
		return fmt.Errorf("script %s does not exist", strconv.Quote(id))
	}
	if err := checkAttachAudience(req, scopeOfScript(c)); err != nil {
		return err
	}
	if err := r.deps.Attachments.AttachScript(ctx, prompt.ScriptAttachment{
		PromptID:   req.Prompt.ID,
		ScriptRef:  ref,
		AttachedBy: req.CallerEmail,
	}); err != nil {
		return fmt.Errorf("attaching script to prompt: %w", err)
	}
	return nil
}

// checkAttachAudience applies the shared scope rule to the prompt's current and
// requested audiences. Errors are returned unwrapped: each is a complete
// author-facing sentence naming the script to fix.
func checkAttachAudience(req ScriptAttachRequest, scope prompt.AttachmentScope) error {
	p := req.Prompt
	if err := prompt.CheckAttachScope(p.Scope, p.Personas, scope); err != nil {
		return err //nolint:wrapcheck // caller-facing message, deliberately verbatim
	}
	if err := prompt.CheckAttachOwnership(req.CallerSub, req.CallerEmail, p.OwnerEmail, scope); err != nil {
		return err //nolint:wrapcheck // caller-facing message, deliberately verbatim
	}
	if p.ReviewRequested && p.RequestedScope != "" {
		return prompt.CheckAttachScope(p.RequestedScope, p.RequestedPersonas, scope) //nolint:wrapcheck // caller-facing message, deliberately verbatim
	}
	return nil
}

// Detach removes one script reference from a prompt, returning
// prompt.ErrScriptAttachmentNotFound when the prompt does not reference it. It
// applies no scope rule: dropping a reference can only narrow what a prompt
// carries.
func (r *ScriptResolver) Detach(ctx context.Context, promptID, ref string) error {
	if r == nil {
		return errors.New("managed scripts are not available on this deployment")
	}
	canonical, _, err := normalizeScriptRef(ref)
	if err != nil {
		return err
	}
	if err := r.deps.Attachments.DetachScript(ctx, promptID, canonical); err != nil {
		return fmt.Errorf("detaching script from prompt: %w", err)
	}
	return nil
}

// Scopes returns the visibility of every script a prompt references, for the
// authoring-time rule in prompt.CheckAttachScope. It is caller-independent: the
// rule asks how widely a script is visible, not whether one reader can see it.
//
// A script that no longer exists is skipped rather than reported: a broken
// reference cannot violate a scope rule, and blocking a promotion on it would
// freeze the prompt against every edit until someone detached a reference the
// portal already flags. Any other read failure does block, because an unknown
// scope is not a safe scope.
func (r *ScriptResolver) Scopes(ctx context.Context, promptID string) ([]prompt.AttachmentScope, error) {
	if r == nil || promptID == "" {
		return nil, nil
	}
	links, err := r.deps.Attachments.ListScriptsByPrompt(ctx, promptID)
	if err != nil {
		return nil, fmt.Errorf("reading prompt script references: %w", err)
	}
	out := make([]prompt.AttachmentScope, 0, len(links))
	for _, link := range links {
		id, refErr := scriptIDFromRef(link.ScriptRef)
		if refErr != nil {
			continue
		}
		c, getErr := r.deps.Scripts.Contract(ctx, id)
		if getErr != nil {
			return nil, fmt.Errorf("reading referenced script %s: %w", id, getErr)
		}
		if c == nil {
			continue
		}
		out = append(out, scopeOfScript(c))
	}
	return out, nil
}

// scopeOfScript projects a script contract onto the fields the shared scope rule
// needs, translating the script vocabulary into the rule's: a personal script is
// a "user"-scoped attachment owned by its author, and a persona-scoped script
// names every persona it serves, which is the set the rule tests the prompt's
// audience against.
func scopeOfScript(c *script.Contract) prompt.AttachmentScope {
	out := prompt.AttachmentScope{
		Kind:        prompt.AttachKindScript,
		ID:          c.ID,
		DisplayName: c.Title(),
		Scope:       scriptScopeWord(c.Scope),
	}
	switch c.Scope {
	case script.ScopePersona:
		out.ScopeIDs = c.Personas
	case script.ScopePersonal:
		if c.OwnerEmail != "" {
			out.ScopeIDs = []string{c.OwnerEmail}
		}
	}
	return out
}

// scriptScopeWord maps a script scope onto the shared rule's vocabulary. Only
// the personal/user pair differs; an unrecognized scope is passed through so the
// rule refuses it as unknown rather than silently reading it as global.
func scriptScopeWord(scope string) string {
	if scope == script.ScopePersonal {
		return userScopeWord
	}
	return scope
}

// userScopeWord is the shared rule's name for a scope owned by one person. It
// is stated here rather than imported because pkg/prompt keeps the constant
// unexported; TestScriptScopeWordsMatchRule pins the two together.
const userScopeWord = "user"

// CheckPromotion reports whether a prompt's referenced scripts would still
// satisfy the scope rule at the target scope. It fails closed: a store error
// blocks the promotion rather than letting it through unchecked.
func (r *ScriptResolver) CheckPromotion(ctx context.Context, promptID, targetScope string, targetPersonas []string) error {
	scopes, err := r.Scopes(ctx, promptID)
	if err != nil {
		return err
	}
	// Unwrapped by design: the rule's error is written for the author and is
	// surfaced verbatim by every caller.
	return prompt.CheckPromotionAttachments(targetScope, targetPersonas, scopes) //nolint:wrapcheck // caller-facing message, deliberately verbatim
}

// ScriptContent renders resolved references as MCP prompt-message content: one
// text block framing the automations, each resolved contract, and a trailing
// note counting anything the caller did not receive.
//
// It is text rather than embedded resources because a script is not a file: what
// the agent needs is the contract and the instruction to run it. The framing is
// deliberately directive — an agent holding a procedure that names an automation
// must run that automation rather than re-derive its output through a
// conversation, which is the entire point of the feature.
func ScriptContent(items []ResolvedScript) []mcp.Content {
	if len(items) == 0 {
		return nil
	}
	var blocks []string
	withheld := 0
	for _, it := range items {
		if it.Availability != AvailableEmbedded || it.Contract == nil {
			withheld++
			continue
		}
		blocks = append(blocks, it.Contract.Text()+"\nReference: "+it.Reference)
	}
	var out []mcp.Content
	if len(blocks) > 0 {
		out = append(out, &mcp.TextContent{
			Text: scriptFramingText(len(blocks)) + "\n\n" + strings.Join(blocks, "\n\n"),
		})
	}
	if note := withheldScriptNote(items, withheld); note != "" {
		out = append(out, &mcp.TextContent{Text: note})
	}
	return out
}

// scriptFramingText introduces the referenced automations and states what to do
// with them. It agrees in number, and it counts what is actually below it
// rather than what the prompt references: when some references were withheld,
// the trailing note reports that, and a heading claiming the full count would
// contradict it.
func scriptFramingText(n int) string {
	subject := fmt.Sprintf("The following %d managed scripts are referenced by this prompt", n)
	if n == 1 {
		subject = "The following managed script is referenced by this prompt"
	}
	return subject + ": governed automations the platform executes on request. " +
		"Call run_script with a script's name and parameters to produce fresh output, and use what it " +
		"returns rather than re-deriving the same result yourself. Each script's last successful output " +
		"is named below; run it again when you need current data."
}

// withheldScriptNote summarizes references the caller did not receive, counting
// them by reason without naming them. The count is what the agent needs: it can
// say its procedure was incomplete without the note becoming a metadata side
// channel.
func withheldScriptNote(items []ResolvedScript, withheld int) string {
	if withheld == 0 {
		return ""
	}
	counts := map[Availability]int{}
	for _, it := range items {
		if it.Availability != AvailableEmbedded {
			counts[it.Availability]++
		}
	}
	var parts []string
	if n := counts[UnavailableForbidden]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d you are not permitted to see", n))
	}
	if n := counts[UnavailableMissing]; n > 0 {
		verb := "exist"
		if n == 1 {
			verb = "exists"
		}
		parts = append(parts, fmt.Sprintf("%d no longer %s", n, verb))
	}
	if n := counts[UnavailableUnreadable]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d could not be read", n))
	}
	subject := fmt.Sprintf("%d referenced scripts were not delivered", withheld)
	if withheld == 1 {
		subject = "1 referenced script was not delivered"
	}
	return fmt.Sprintf("Note: %s (%s). "+
		"Proceed, and state that part of this procedure's automation was unavailable.",
		subject, joinAnd(parts))
}

// ScriptSummary renders resolved references for the manage_prompt use
// provenance block, so an agent can state exactly which automations it received.
// Unavailable entries carry their reason and nothing else.
func ScriptSummary(items []ResolvedScript) []map[string]any {
	if len(items) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		entry := map[string]any{
			fieldScriptRef: it.Reference,
			"availability": string(it.Availability),
		}
		if it.Availability == UnavailableForbidden {
			// A caller who may not see the script learns only that something is
			// referenced and out of reach. Even the reference is withheld: they
			// have no repair action, and it would be a probe for existence.
			delete(entry, fieldScriptRef)
		}
		if it.Contract != nil {
			entry["contract"] = it.Contract
		}
		out = append(out, entry)
	}
	return out
}
