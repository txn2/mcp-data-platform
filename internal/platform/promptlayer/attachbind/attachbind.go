// Package attachbind binds a prompt's attached material into the serving
// surfaces: the reference material a prompt attaches (managed resources,
// #1013) and the automations it references (managed scripts, #1289).
//
// It exists as its own package for the reason the structural gate exists: the
// prompt layer is at its size ceiling, and material binding is the cohesive
// piece that comes out whole. What it owns is the boundary between a request
// and the two resolvers — deriving the caller's identity from the request
// context, appending each kind's content to a prompts/get result in a fixed
// order, and running the promotion guard across both kinds — while pkg/prompt/
// attachserve keeps the resolution rules themselves.
//
// Every path degrades rather than fails. A resolver that was never bound
// contributes nothing, which is exactly what a deployment without managed
// resources or without a database should serve.
package attachbind

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// promptRoleUser is the MCP message role attached material is served under: it
// is context for the model, not an assistant turn.
const promptRoleUser = "user"

// Binder holds the bound resolvers. Both are set after construction, because
// each is assembled later than the prompt layer — the managed-resource layer
// and the script store both come up after prompts do.
type Binder struct {
	resources *attachserve.Resolver
	scripts   *attachserve.ScriptResolver
}

// New returns an empty binder. A binder with nothing bound is valid and serves
// every prompt without material, so callers need no nil checks.
func New() *Binder { return &Binder{} }

// SetResources binds the resolver for attached reference material.
func (b *Binder) SetResources(r *attachserve.Resolver) { b.resources = r }

// SetScripts binds the resolver for referenced managed scripts.
func (b *Binder) SetScripts(r *attachserve.ScriptResolver) { b.scripts = r }

// Scripts returns the bound script resolver, nil when none is bound. The
// manage_prompt attach/detach commands act through it.
func (b *Binder) Scripts() *attachserve.ScriptResolver {
	if b == nil {
		return nil
	}
	return b.scripts
}

// ResolveResources evaluates a prompt's attached materials for the caller
// identified by the request context. It returns nil when no resolver is bound,
// when the prompt has no id (static and file prompts cannot carry attachments),
// or when the prompt has none.
//
// Identity comes from the PlatformContext: the tool-call middleware sets it on
// manage_prompt use, and the prompt-visibility middleware sets it on
// prompts/get. Without one the caller is anonymous, which resolves only global
// materials — the same set an unauthenticated reader already sees.
func (b *Binder) ResolveResources(ctx context.Context, pr *prompt.Prompt, personas []string) []attachserve.Resolved {
	if b == nil || b.resources == nil || pr == nil || pr.ID == "" {
		return nil
	}
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		// No identity: only global materials resolve, matching what an
		// unauthenticated reader already sees of the resource surface.
		return b.resources.Resolve(ctx, pr.ID, resource.Claims{})
	}
	claims := resource.BuildClaims(pc.UserID, pc.UserEmail, pc.PersonaName, pc.Roles, pc.IsAdmin)
	// A caller can hold several personas, and PlatformContext carries only the
	// first. Where the full set is known (the prompts/get path resolves it to
	// decide visibility) it replaces the single-persona default, so a member of
	// two personas is not refused material their own second persona owns.
	if len(personas) > 0 {
		claims.Personas = personas
	}
	return b.resources.Resolve(ctx, pr.ID, claims)
}

// ResolveScripts evaluates a prompt's referenced managed scripts for the same
// caller, on the same terms: nil without a resolver, without a stored prompt,
// or when the prompt references none.
//
// A script is its owner's, so the only identity this needs is the caller's
// address. A request carrying no PlatformContext resolves as nobody, which
// reaches no script at all.
func (b *Binder) ResolveScripts(ctx context.Context, pr *prompt.Prompt) []attachserve.ResolvedScript {
	if b == nil || b.scripts == nil || pr == nil || pr.ID == "" {
		return nil
	}
	pc := middleware.GetPlatformContext(ctx)
	if pc == nil {
		return b.scripts.Resolve(ctx, pr.ID, "")
	}
	return b.scripts.Resolve(ctx, pr.ID, pc.UserEmail)
}

// AppendContent adds a prompt's material to a prompts/get result as additional
// user-role messages: the attached reference material first, then the
// referenced automations.
//
// They are separate messages rather than extra content on the prompt's own
// message because a prompt message carries exactly one content item in the MCP
// schema, and because keeping the procedure and its materials in distinct
// messages is what lets a client render or elide the materials independently.
// The order is the authored one: what the procedure reads from comes before
// what it runs.
func (b *Binder) AppendContent(ctx context.Context, res *mcp.GetPromptResult, pr *prompt.Prompt, personas []string) {
	if res == nil {
		return
	}
	contents := attachserve.Content(b.ResolveResources(ctx, pr, personas))
	contents = append(contents, attachserve.ScriptContent(b.ResolveScripts(ctx, pr))...)
	for _, c := range contents {
		res.Messages = append(res.Messages, &mcp.PromptMessage{Role: promptRoleUser, Content: c})
	}
}

// GuardScope is the shared store's attachment guard: it refuses a write that
// would leave one of the prompt's attached materials unreachable for the
// audience the write gives it.
//
// Two targets are checked, because a prompt can reach a wider audience two
// ways. The prompt's own scope covers a direct edit and an admin's approval of
// a promotion (which lands the new scope on the row). A pending promotion
// request covers the moment an owner asks: the author is the only person who
// can re-scope or detach the material, so they must be told at request time
// rather than the reviewer discovering it at approval.
//
// Only attached reference material is checked. A referenced script resolves for
// its owner alone at every prompt scope, so promoting a prompt cannot widen it
// past a script's audience; the author is told what a reference serves when
// they attach it.
//
// It fails closed. A resolver error blocks the write, because the alternative
// is silently publishing a shared procedure whose material nobody but its
// author can read.
func (b *Binder) GuardScope(ctx context.Context, p *prompt.Prompt) error {
	if b == nil || p == nil || p.ID == "" {
		return nil
	}
	for _, check := range b.promotionChecks() {
		// The returned error is passed through unwrapped: it is a complete
		// author-facing sentence naming the material to fix, and every added
		// "checking X:" prefix pushes that sentence further from the start of
		// the message the author actually reads.
		if err := check(ctx, p.ID, p.Scope, p.Personas); err != nil {
			return err
		}
		if p.ReviewRequested && p.RequestedScope != "" {
			if err := check(ctx, p.ID, p.RequestedScope, p.RequestedPersonas); err != nil {
				return err
			}
		}
	}
	return nil
}

// promotionCheck is one kind of attached material's promotion gate.
type promotionCheck func(ctx context.Context, promptID, targetScope string, targetPersonas []string) error

// promotionChecks returns the gates bound on this deployment. A kind whose
// resolver was never wired contributes none.
func (b *Binder) promotionChecks() []promotionCheck {
	var checks []promotionCheck
	if b.resources != nil {
		checks = append(checks, b.resources.CheckPromotion)
	}
	return checks
}
