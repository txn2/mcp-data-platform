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
// evaluated for the caller identified by email. It returns nil when the prompt
// references none, and nil rather than an error when the link read fails: a
// store outage must not take down prompt serving.
//
// A script is one person's, so a reference resolves for its owner and for
// nobody else. A prompt served to a wider audience still serves — every other
// reader is told an automation was referenced and is out of their reach, which
// is what AudienceNote warns its author about at the moment they attach it.
func (r *ScriptResolver) Resolve(ctx context.Context, promptID, email string) []ResolvedScript {
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
		out = append(out, r.resolveOne(ctx, link.ScriptRef, email))
	}
	return out
}

// resolveOne evaluates a single reference for the caller.
func (r *ScriptResolver) resolveOne(ctx context.Context, ref, email string) ResolvedScript {
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
	case !c.OwnedBy(email):
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
	// note reports on.
	Prompt *prompt.Prompt
	// Ref is the script reference or bare id the caller supplied.
	Ref string
	// CallerEmail identifies the author, who must be able to see the script
	// they are referencing: a script is its owner's, so referencing one is
	// something its owner does.
	CallerEmail string
	// CallerIsAdmin lifts that requirement, as administrative authority lifts
	// every other script rule.
	CallerIsAdmin bool
}

// Attach references a script from a prompt.
//
// The one rule is that the caller can see what they are referencing. A wider
// prompt is not refused: a reference resolves for the script's owner only, and
// a prompt that also serves other people is a normal thing to write — the
// automation is simply not part of what those readers receive. AudienceNote
// states that where the caller can act on it, at the moment they attach, and it
// is returned so the surface that took the request can show it.
func (r *ScriptResolver) Attach(ctx context.Context, req ScriptAttachRequest) (string, error) {
	if r == nil {
		return "", errors.New("managed scripts are not available on this deployment")
	}
	if req.Prompt == nil || req.Prompt.ID == "" {
		return "", errors.New("a stored prompt is required to reference a script")
	}
	ref, id, err := normalizeScriptRef(req.Ref)
	if err != nil {
		return "", err
	}
	c, err := r.deps.Scripts.Contract(ctx, id)
	if err != nil {
		return "", fmt.Errorf("reading script %s: %w", id, err)
	}
	if c == nil {
		return "", fmt.Errorf("script %s does not exist", strconv.Quote(id))
	}
	if !req.CallerIsAdmin && !c.OwnedBy(req.CallerEmail) {
		// Wrapped in the shared attachment sentinel so the surfaces that pass a
		// refusal through verbatim keep passing this one through: it is a
		// complete sentence the author can act on.
		return "", fmt.Errorf("script %s cannot be attached: it belongs to somebody else: %w",
			strconv.Quote(c.Title()), prompt.ErrAttachmentScope)
	}
	if err := r.deps.Attachments.AttachScript(ctx, prompt.ScriptAttachment{
		PromptID:   req.Prompt.ID,
		ScriptRef:  ref,
		AttachedBy: req.CallerEmail,
	}); err != nil {
		return "", fmt.Errorf("attaching script to prompt: %w", err)
	}
	return AudienceNote(req.Prompt, c), nil
}

// AudienceNote states what a reference means for the people this prompt serves,
// or "" when every reader of the prompt is the script's owner.
//
// It exists because the mismatch is invisible from the authoring side: the
// author sees their own automation resolve perfectly, while every other reader
// of a shared prompt receives a note saying part of the procedure was
// unavailable. Saying so where the reference is made is the difference between
// a prompt whose author knows what it serves and one that quietly serves less
// than it reads.
func AudienceNote(p *prompt.Prompt, c *script.Contract) string {
	if p == nil || c == nil {
		return ""
	}
	if p.Scope == prompt.ScopePersonal && strings.EqualFold(p.OwnerEmail, c.OwnerEmail) {
		return ""
	}
	owner := c.OwnerEmail
	if owner == "" {
		owner = "nobody"
	}
	return fmt.Sprintf(
		"This reference resolves only for %s, who owns the script. Anyone else this prompt "+
			"serves is told an automation was referenced and is out of their reach.", owner)
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
