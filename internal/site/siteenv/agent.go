package siteenv

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// Agent moves (#2643). The site model is "agents work in dev, people use what
// was promoted", and the review between the two belongs to the person. So a
// move of an environment other than dev that an agent asks for (promote,
// rollback, and making one of its domains public) is carried out only on a
// person's decision: their answer to the question their MCP client asks them
// (elicitation), an approval decided in the portal, or the environment's own
// setting that lets agents move it (agents_may_promote, an administrator's).
// The portal's own controls are the person and are unchanged.

// The kinds of move an agent's request names.
const (
	MovePromote  = "promote"
	MoveRollback = "rollback"
	MovePublic   = "public_domain"
)

// AgentMove is one move an agent asked for, as the person is asked about it.
type AgentMove struct {
	Kind      string `json:"kind"`
	SiteID    string `json:"-"`
	Site      string `json:"site"`
	Target    string `json:"environment"`
	Release   int    `json:"release,omitempty"`
	ReleaseID string `json:"-"`
	Digest    string `json:"-"`
	Hostname  string `json:"hostname,omitempty"`
	// Stamp is a public domain's access stamp, which its approval names.
	Stamp string `json:"-"`
	// Current is the release the environment serves now.
	Current int `json:"current_release,omitempty"`
	// DevAddress is where the person looks at the release first.
	DevAddress string `json:"dev_address,omitempty"`
	// Changed is what the move changes for the environment's visitors.
	Changed *sitesvc.Comparison `json:"changed,omitempty"`
	Reason  string              `json:"-"`
}

// Key names the move exactly: an answer or approval for one release, target
// or hostname never carries out another. A retry whose state moved since the
// question is asked again.
func (m AgentMove) Key() string {
	return strings.Join([]string{"site_move", m.Kind, m.SiteID, m.Target, strconv.Itoa(m.Release), m.Digest, m.Hostname, m.Stamp}, ":")
}

// Question is what the person is asked.
func (m AgentMove) Question() string {
	var b strings.Builder
	switch m.Kind {
	case MovePromote:
		fmt.Fprintf(&b, "An agent asks to promote release %d of site %s to %s, where it replaces release %d.", m.Release, m.Site, m.Target, m.Current)
	case MoveRollback:
		fmt.Fprintf(&b, "An agent asks to roll %s of site %s back to release %d, from release %d.", m.Target, m.Site, m.Release, m.Current)
	default:
		fmt.Fprintf(&b, "An agent asks to make %s, a domain of %s on site %s, public: anyone could open it without signing in.", m.Hostname, m.Target, m.Site)
	}
	if m.DevAddress != "" {
		fmt.Fprintf(&b, " Look at it first: %s.", m.DevAddress)
	}
	if c := m.Changed; c != nil {
		fmt.Fprintf(&b, " It changes %d file(s), adds %d and removes %d.", len(c.Changed), len(c.Added), len(c.Removed))
		if names := firstNames(c, maxNamed); names != "" {
			fmt.Fprintf(&b, " %s", names)
		}
	}
	fmt.Fprintf(&b, " %s", "Accept to carry it out now.")
	return b.String()
}

// maxNamed is how many changed paths the question lists.
const maxNamed = 10

func firstNames(c *sitesvc.Comparison, n int) string {
	all := append(append(append([]string{}, c.Changed...), c.Added...), c.Removed...)
	if len(all) == 0 {
		return ""
	}
	more := ""
	if len(all) > n {
		more = fmt.Sprintf(" and %d more", len(all)-n)
		all = all[:n]
	}
	return "Files: " + strings.Join(all, ", ") + more + "."
}

// PersonDecisionError is a move an agent asked for that waits on the person.
type PersonDecisionError struct{ Move AgentMove }

func (e *PersonDecisionError) Error() string {
	return "APPROVAL_REQUIRED: " + e.Move.Question()
}

// decision is whose decision carries out a move: "" for a person's own move,
// one of site.DecidedBy* for an agent's, or a PersonDecisionError when there is
// none yet.
func (o Ops) decision(ctx context.Context, actor sitesvc.Actor, env *site.Environment, m AgentMove) (string, error) {
	if !actor.Agent || env.Name == site.EnvDev {
		return "", nil
	}
	if env.AgentsMayPromote {
		return site.DecidedByEnvironment, nil
	}
	if actor.Decided == m.Key() {
		return site.DecidedByElicitation, nil
	}
	if o.moveApproved(ctx, m) {
		return site.DecidedByApproval, nil
	}
	return "", &PersonDecisionError{Move: m}
}

// moveApproved reports whether a person approved the move in the portal.
func (o Ops) moveApproved(ctx context.Context, m AgentMove) bool {
	if o.Approvals == nil {
		return false
	}
	if m.Kind == MovePublic {
		return o.Approvals.EnvironmentApproved(ctx, m.SiteID, m.Hostname, m.Stamp)
	}
	return o.Approvals.ReleaseApproved(ctx, m.ReleaseID, m.Target, m.Digest)
}

// RequestMove files the request a person decides an agent's move by, in the
// portal's Approvals, when the agent's client cannot ask them. The agent can
// never decide it (approval.Actor.Agent).
func (o Ops) RequestMove(ctx context.Context, actor sitesvc.Actor, m AgentMove) (*approval.Request, error) {
	if o.Approvals == nil {
		return nil, refused("this deployment has no approvals queue: ask the person to carry this out in the portal")
	}
	who := approval.Actor{Email: actor.Email, Persona: actor.Persona, Admin: actor.Admin, Agent: true}
	reason := strings.TrimSpace("From an agent. " + m.Reason)
	var (
		r   *approval.Request
		err error
	)
	if m.Kind == MovePublic {
		r, err = o.Approvals.SubmitEnvironment(ctx, who, approval.SubmitEnvironment{
			SiteID: m.SiteID, Site: m.Site, Target: m.Hostname, Stamp: m.Stamp, Reason: reason,
		})
	} else {
		r, err = o.Approvals.SubmitRelease(ctx, who, approval.SubmitRelease{
			ReleaseID: m.ReleaseID, Site: m.Site, Number: m.Release, Target: m.Target, Digest: m.Digest, Reason: reason,
		})
	}
	if err != nil {
		return nil, fmt.Errorf("requesting approval: %w", err)
	}
	return r, nil
}

// moving is one move of an environment to a release, for the question.
type moving struct {
	st     *site.Site
	env    *site.Environment
	kind   string
	rel    *site.Release
	reason string
}

// moveOf describes a move for the person.
func (o Ops) moveOf(ctx context.Context, mv moving) AgentMove {
	m := AgentMove{
		Kind: mv.kind, SiteID: mv.st.ID, Site: mv.st.Name, Target: mv.env.Name, Release: mv.rel.Number, ReleaseID: mv.rel.ID,
		Digest: mv.rel.ManifestSHA256, Current: mv.env.Release, Reason: mv.reason,
	}
	look := site.EnvDev
	if mv.kind == MoveRollback {
		look = mv.env.Name
	}
	m.DevAddress = o.Addressing().PagesAddress(mv.st, look)
	if c, err := o.Compare(ctx, mv.st.ID, mv.env.Release, mv.rel.Number); err == nil {
		m.Changed = c
	}
	return m
}

// managed is the site, for someone who manages it; nil when the service's own
// refusal should answer, so nothing about the site reaches someone who cannot
// change it.
func (o Ops) managed(ctx context.Context, actor sitesvc.Actor, ref string) *site.Site {
	st, err := o.Find(ctx, ref)
	if err != nil || o.MayEdit(ctx, actor, st) != nil {
		return nil
	}
	return st
}

// Promote is the service's promote, an agent's waiting on a person (#2643).
// Whatever the service would refuse (the wrong environment, nothing to move,
// no permission) it refuses in its own words, before anyone is asked.
func (o Ops) Promote(ctx context.Context, actor sitesvc.Actor, req sitesvc.PromoteRequest) (*sitesvc.Promoted, error) {
	if actor.Agent {
		decided, err := o.promoteDecision(ctx, actor, req)
		if err != nil {
			return nil, err
		}
		actor.DecidedBy = decided
	}
	return o.Service.Promote(ctx, actor, req) //nolint:wrapcheck // the service's answer
}

func (o Ops) promoteDecision(ctx context.Context, actor sitesvc.Actor, req sitesvc.PromoteRequest) (string, error) {
	st := o.managed(ctx, actor, req.Site)
	if st == nil {
		return "", nil
	}
	target, prev := promotion(st, req)
	if target == nil {
		return "", nil
	}
	rel, err := o.Store.Release(ctx, st.ID, prev.Release)
	if err != nil || rel.Pruned {
		return "", nil //nolint:nilerr // the service reports it
	}
	return o.decision(ctx, actor, target, o.moveOf(ctx, moving{st: st, env: target, kind: MovePromote, rel: rel, reason: req.Reason}))
}

// promotion is the target and source environments of a promote the service
// would carry out, nil when it would refuse it in its own words.
func promotion(st *site.Site, req sitesvc.PromoteRequest) (target, prev *site.Environment) {
	to, _ := site.CanonicalEnvironment(req.To)
	from, _ := site.CanonicalEnvironment(req.From)
	target, prev = st.Environment(to), st.Previous(to)
	switch {
	case target == nil || prev == nil || prev.Release == 0:
		return nil, nil
	case from != "" && from != prev.Name, req.Release != 0 && req.Release != prev.Release:
		return nil, nil
	}
	return target, prev
}

// Rollback is the service's rollback, an agent's waiting on a person.
func (o Ops) Rollback(ctx context.Context, actor sitesvc.Actor, ref, environment string, to int) (*sitesvc.Promoted, error) {
	if actor.Agent {
		decided, err := o.rollbackDecision(ctx, actor, ref, environment, to)
		if err != nil {
			return nil, err
		}
		actor.DecidedBy = decided
	}
	return o.Service.Rollback(ctx, actor, ref, environment, to) //nolint:wrapcheck // the service's answer
}

func (o Ops) rollbackDecision(ctx context.Context, actor sitesvc.Actor, ref, environment string, to int) (string, error) {
	st := o.managed(ctx, actor, ref)
	if st == nil {
		return "", nil
	}
	name, _ := site.CanonicalEnvironment(environment)
	env := st.Environment(name)
	if env == nil {
		return "", nil
	}
	history, err := o.Store.Deployments(ctx, st.ID, env.Name, 0)
	if err != nil {
		return "", nil //nolint:nilerr // the service reports it
	}
	target, err := site.RollbackTarget(env, history, to, nil)
	if err != nil {
		return "", nil //nolint:nilerr // the service reports it
	}
	rel, err := o.Store.Release(ctx, st.ID, target)
	if err != nil || rel.Pruned {
		return "", nil //nolint:nilerr // the service reports it
	}
	return o.decision(ctx, actor, env, o.moveOf(ctx, moving{st: st, env: env, kind: MoveRollback, rel: rel}))
}

// DomainResult is a domain change with whose decision made an agent's domain
// public (#2643).
type DomainResult struct {
	*sitesvc.DomainResult
	DecidedBy string `json:"decided_by,omitempty"`
}

// AddDomain is the service's, a public domain an agent adds waiting on a
// person: it is added private, and making it public is the move the person
// decides. The agent asking again once the person accepted finds the domain it
// added and goes on to make it public.
func (o Ops) AddDomain(ctx context.Context, actor sitesvc.Actor, req sitesvc.DomainRequest) (*DomainResult, error) {
	if !actor.Agent || !req.Public || req.Application != "" {
		res, err := o.Service.AddDomain(ctx, actor, req)
		return wrapDomain(res, ""), err
	}
	host := site.NormalizeHost(req.Hostname)
	if d, err := o.Store.Domain(ctx, host); err == nil && actor.Decided != "" {
		if st := o.managed(ctx, actor, req.Site); st != nil && d.SiteID == st.ID {
			return o.SetDomainAccess(ctx, actor, host, true, req.Reason)
		}
	}
	private := req
	private.Public = false
	if _, err := o.Service.AddDomain(ctx, actor, private); err != nil {
		return nil, err //nolint:wrapcheck // the service's answer
	}
	return o.SetDomainAccess(ctx, actor, host, true, req.Reason)
}

// SetDomainAccess is the service's, making a domain public for an agent
// waiting on a person.
func (o Ops) SetDomainAccess(ctx context.Context, actor sitesvc.Actor, hostname string, public bool, reason string) (*DomainResult, error) {
	decided := ""
	if actor.Agent && public {
		var err error
		if decided, err = o.publicDecision(ctx, actor, hostname, reason); err != nil {
			return nil, err
		}
	}
	res, err := o.Service.SetDomainAccess(ctx, actor, hostname, public, reason)
	return wrapDomain(res, decided), err
}

func (o Ops) publicDecision(ctx context.Context, actor sitesvc.Actor, hostname, reason string) (string, error) {
	d, err := o.Store.Domain(ctx, site.NormalizeHost(hostname))
	if err != nil || d.SiteID == "" || d.Public {
		return "", nil //nolint:nilerr // the service reports it
	}
	st := o.managed(ctx, actor, d.SiteID)
	if st == nil {
		return "", nil
	}
	env := st.Environment(d.Environment)
	if env == nil {
		return "", nil
	}
	m := AgentMove{Kind: MovePublic, SiteID: st.ID, Site: st.Name, Target: env.Name, Hostname: d.Hostname, Stamp: d.PublicStamp(), Reason: reason}
	m.DevAddress = o.Addressing().PagesAddress(st, env.Name)
	return o.decision(ctx, actor, env, m)
}

func wrapDomain(res *sitesvc.DomainResult, decided string) *DomainResult {
	if res == nil {
		return nil
	}
	return &DomainResult{DomainResult: res, DecidedBy: decided}
}

// SetEnvironment is the service's, with agents_may_promote (#2643) set only by
// an administrator in the portal: a person's decision about the environment,
// never an agent's. dev has no such setting, since an agent's moves into dev
// never wait on anyone.
func (o Ops) SetEnvironment(ctx context.Context, actor sitesvc.Actor, ref, name string, c sitesvc.EnvironmentChange) (*sitesvc.EnvironmentResult, error) {
	if set := c.AgentsMayPromote; set != nil {
		canonical, _ := site.CanonicalEnvironment(name)
		switch {
		case actor.Agent:
			return nil, refused("agents_may_promote is set by an administrator in the portal, never from an agent's session")
		case !actor.Admin:
			return nil, fmt.Errorf("an administrator decides whether agents may promote into an environment: %w", site.ErrNotAllowed)
		case canonical == site.EnvDev && *set:
			return nil, refused("an agent's changes always land in dev directly, so dev has no agents_may_promote setting")
		}
	}
	return o.Service.SetEnvironment(ctx, actor, ref, name, c) //nolint:wrapcheck // the service's answer
}

// IsPersonDecision reports whether err is a move waiting on a person.
func IsPersonDecision(err error) (*PersonDecisionError, bool) {
	return errors.AsType[*PersonDecisionError](err)
}
