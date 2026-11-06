// Package threadreach decides who can open a feedback thread's target (#2512):
// the caller of a thread route, and a person a message names or a thread
// involves. Both questions are answered by the one decision here, made with
// the portal's authorization core (internal/portal/access), so the people a
// mention reaches and a reply notifies are exactly the people the target's
// read admits -- by ownership, an email share, a persona share, a collection
// share or administration -- and not a second, hand-kept list.
//
// A person named by address is judged as they would be if they signed in now:
// the roles the known-users directory last recorded for them decide their
// persona and whether they administer the platform. An address the directory
// does not hold is judged with no roles, so only a grant addressed to that
// address admits it; a target open to every signed-in person (a knowledge
// page, the general channel, a prompt that is not personal) admits only the
// people the directory holds, so a message can never mail an arbitrary
// address.
package threadreach

import (
	"context"
	"errors"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
	"github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"
	"github.com/txn2/mcp-data-platform/pkg/portal/mention"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/user"
)

// Outcome is what a decision found.
type Outcome int

// Outcomes of a decision.
const (
	// Open admits the person.
	Open Outcome = iota
	// Missing is a target that does not exist or was deleted.
	Missing
	// Denied is a target the person may not open.
	Denied
	// Failed is a check that could not complete.
	Failed
	// Unavailable is a target kind this deployment has no store for.
	Unavailable
	// BadTarget is a target type the decision does not know.
	BadTarget
)

// Directory is the known-users directory a named person's roles are read
// from.
type Directory interface {
	Get(ctx context.Context, email string) (*user.User, error)
}

// PromptReader reads one prompt.
type PromptReader interface {
	GetByID(ctx context.Context, id string) (*prompt.Prompt, error)
}

// Config is what a decision reads.
type Config struct {
	Access         *access.Checker
	Assets         portaldomain.AssetStore
	Collections    portaldomain.CollectionStore
	Prompts        PromptReader
	KnowledgePages knowledgepage.Store
	// Directory holds the roles a named person last signed in with. nil
	// judges every named person with no roles.
	Directory Directory
	// PersonaName resolves roles to the persona a share may be addressed
	// to (#2089). nil resolves none.
	PersonaName func(roles []string) string
}

// Reach answers who can open a thread target.
type Reach struct {
	cfg Config
}

// New returns a Reach over cfg.
func New(cfg Config) *Reach { return &Reach{cfg: cfg} }

// Target type discriminators, as the thread substrate stores them.
const (
	targetAsset         = "asset"
	targetCollection    = "collection"
	targetPrompt        = "prompt"
	targetKnowledgePage = "knowledge_page"
	targetStandalone    = "standalone"
)

// Decide reports whether u may open the target. The caller's persona is the
// one on ctx (portaldomain.WithCallerPersona), as on every portal request.
// known says whether the person is one the platform knows: a signed-in caller
// always is; a named address only when the directory holds it.
func (r *Reach) Decide(ctx context.Context, u *access.User, known bool, targetType, targetID string) Outcome {
	if r == nil || r.cfg.Access == nil || u == nil {
		return Failed
	}
	switch targetType {
	case targetStandalone:
		return openIf(known)
	case targetAsset:
		return r.asset(ctx, u, targetID)
	case targetCollection:
		return r.collection(ctx, u, targetID)
	case targetPrompt:
		return r.prompt(ctx, u, known, targetID)
	case targetKnowledgePage:
		return r.knowledgePage(ctx, known, targetID)
	default:
		return BadTarget
	}
}

func openIf(ok bool) Outcome {
	if ok {
		return Open
	}
	return Denied
}

func (r *Reach) asset(ctx context.Context, u *access.User, id string) Outcome {
	if r.cfg.Assets == nil {
		return Unavailable
	}
	asset, err := r.cfg.Assets.Get(ctx, id)
	if err != nil || asset == nil || asset.DeletedAt != nil {
		return Missing
	}
	if r.cfg.Access.IsAdmin(u) {
		return Open
	}
	granted, err := r.cfg.Access.AssetViewGrant(ctx, id, asset, u)
	if err != nil {
		return Failed
	}
	return openIf(granted)
}

func (r *Reach) collection(ctx context.Context, u *access.User, id string) Outcome {
	if r.cfg.Collections == nil {
		return Unavailable
	}
	coll, err := r.cfg.Collections.Get(ctx, id)
	if err != nil || coll == nil || coll.DeletedAt != nil {
		return Missing
	}
	owner := coll.OwnerID == u.UserID ||
		(u.Email != "" && strings.EqualFold(coll.OwnerEmail, u.Email))
	return openIf(owner || r.cfg.Access.IsAdmin(u) ||
		r.cfg.Access.CollectionSharePermission(ctx, id, u) != "")
}

func (r *Reach) prompt(ctx context.Context, u *access.User, known bool, id string) Outcome {
	if r.cfg.Prompts == nil {
		return Unavailable
	}
	pr, err := r.cfg.Prompts.GetByID(ctx, id)
	if err != nil || pr == nil {
		return Missing
	}
	// A prompt that is not personal is open to every signed-in person.
	if pr.Scope != prompt.ScopePersonal && !known {
		return Denied
	}
	return openIf(r.cfg.Access.CanViewPrompt(ctx, u, pr))
}

func (r *Reach) knowledgePage(ctx context.Context, known bool, id string) Outcome {
	if r.cfg.KnowledgePages == nil {
		return Unavailable
	}
	page, err := r.cfg.KnowledgePages.Get(ctx, id)
	switch {
	case errors.Is(err, knowledgepage.ErrNotFound), err == nil && (page == nil || page.DeletedAt != nil):
		return Missing
	case err != nil:
		return Failed
	}
	return openIf(known)
}

// person is a named address judged as the person it belongs to: the roles the
// directory last recorded for it, and the persona they resolve to.
type person struct {
	User    *access.User
	Persona string
	Known   bool
}

// personIDPrefix marks the user id a named person is judged under. A share
// addressed by user id names an identity provider's subject, which the
// directory does not record; the prefix keeps that id from matching an empty
// one, so only the address, the persona and the roles decide.
const personIDPrefix = "threadreach:"

// personOf builds the person an address belongs to.
func (r *Reach) personOf(ctx context.Context, email string) person {
	email = strings.ToLower(strings.TrimSpace(email))
	p := person{User: &access.User{UserID: personIDPrefix + email, Email: email}}
	if r.cfg.Directory == nil || email == "" {
		return p
	}
	u, err := r.cfg.Directory.Get(ctx, email)
	if err != nil || u == nil {
		return p
	}
	p.Known = true
	p.User.Roles = u.Roles
	if r.cfg.PersonaName != nil {
		p.Persona = r.cfg.PersonaName(u.Roles)
	}
	return p
}

// CanOpen reports whether the person at an address can open the target now.
func (r *Reach) CanOpen(ctx context.Context, email, targetType, targetID string) bool {
	if r == nil || strings.TrimSpace(email) == "" {
		return false
	}
	p := r.personOf(ctx, email)
	ctx = portaldomain.WithCallerPersona(ctx, p.Persona)
	return r.Decide(ctx, p.User, p.Known, targetType, targetID) == Open
}

// Filter returns the addresses that can open the target, in their order.
func (r *Reach) Filter(ctx context.Context, targetType, targetID string, emails []string) []string {
	out := make([]string, 0, len(emails))
	for _, e := range emails {
		if r.CanOpen(ctx, e, targetType, targetID) {
			out = append(out, e)
		}
	}
	return out
}

// Eligible implements mention.Gate: a named address may be mentioned when
// its person can open the target.
func (r *Reach) Eligible(ctx context.Context, t mention.Target, emails []string) ([]string, error) {
	return r.Filter(ctx, t.Type, t.ID, emails), nil
}

var _ mention.Gate = (*Reach)(nil)
