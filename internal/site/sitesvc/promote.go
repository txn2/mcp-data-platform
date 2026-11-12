package sitesvc

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/siteconfig"
)

// PromoteRequest is one promotion: the release an environment serves, or one
// named by number, moved to another environment.
type PromoteRequest struct {
	Site    string
	From    string
	To      string
	Release int
	Reason  string
}

// Promoted is what a promotion answers: the move, or the request it waits on.
type Promoted struct {
	Environment string            `json:"environment"`
	Release     int               `json:"release"`
	URL         string            `json:"address,omitempty"`
	Status      string            `json:"status" example:"promoted"`
	Approval    *approval.Request `json:"approval,omitempty"`
}

// The statuses a promotion answers with.
const (
	StatusPromoted         = "promoted"
	StatusAwaitingApproval = "awaiting_approval"
)

// Promote points an environment at the release the environment immediately
// before it serves (#2617): a release moves one environment at a time, from
// dev to prod. Where the target requires approval, an administrator's promotion
// records its own approval; anyone else's files a request and is carried out
// when they promote again after it is approved. An approval names the release's
// manifest digest.
func (s *Service) Promote(ctx context.Context, actor Actor, req PromoteRequest) (*Promoted, error) {
	req.From, _ = site.CanonicalEnvironment(req.From)
	req.To, _ = site.CanonicalEnvironment(req.To)
	st, err := s.Find(ctx, req.Site)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	target := st.Environment(req.To)
	if target == nil {
		return nil, noEnvironment(req.To)
	}
	rel, err := s.promotable(ctx, st, req)
	if err != nil {
		return nil, err
	}
	out := &Promoted{Environment: target.Name, Release: rel.Number, Status: StatusPromoted}
	if target.RequiresApproval {
		waiting, err := s.approvedOrRequest(ctx, actor, approval.SubmitRelease{
			ReleaseID: rel.ID, Site: st.Name, Number: rel.Number, Target: target.Name, Digest: rel.ManifestSHA256, Reason: req.Reason,
		})
		if err != nil {
			return nil, err
		}
		if waiting != nil {
			out.Status, out.Approval = StatusAwaitingApproval, waiting
			return out, nil
		}
	}
	if err := s.move(ctx, actor, repoint{st: st, env: target, to: rel.Number, action: site.ActionPromote}); err != nil {
		return nil, err
	}
	out.URL = s.Address(ctx, st, target.Name)
	return out, nil
}

// promotable is the release a promotion serves: the one the environment
// before the target serves. A promotion naming another environment, or a
// release that environment does not serve, is refused naming the one it must
// come from, in the words the portal, the API and manage_site all answer.
func (s *Service) promotable(ctx context.Context, st *site.Site, req PromoteRequest) (*site.Release, error) {
	prev := st.Previous(req.To)
	if prev == nil {
		return nil, refused("%s takes every publish and edit directly, so nothing is promoted to it", req.To)
	}
	if req.From != "" && req.From != prev.Name {
		return nil, refused("%s takes releases only from %s, the environment before it: promote it from %s", req.To, prev.Name, prev.Name)
	}
	if prev.Release == 0 {
		return nil, noReleaseBefore(st, prev, req.To)
	}
	if req.Release != 0 && req.Release != prev.Release {
		return nil, refused("%s takes only the release %s serves, and %s serves release %d, not %d. Promote %s from %s to serve %d.",
			req.To, prev.Name, prev.Name, prev.Release, req.Release, req.To, prev.Name, prev.Release)
	}
	return s.liveRelease(ctx, st, prev.Release)
}

// noReleaseBefore is the refusal of a promotion whose source serves nothing
// yet: where its release comes from first.
func noReleaseBefore(st *site.Site, prev *site.Environment, to string) error {
	if before := st.Previous(prev.Name); before != nil {
		return refused("%s serves no release yet. Promote %s's release to %s first; %s can then take it from %s.",
			prev.Name, before.Name, prev.Name, to, prev.Name)
	}
	return refused("%s serves no release yet. Publish to %s first; %s can then take it from %s.", prev.Name, prev.Name, to, prev.Name)
}

// StepRelease is the release an application release pins a site environment
// part at (#2617): what the environment before it serves, so an application
// release moves each environment one step as a promotion does, or the site's
// newest release for dev, which has nothing before it. 0 is none to pin.
func (s *Service) StepRelease(ctx context.Context, ref, environment string) (int, error) {
	st, err := s.Find(ctx, ref)
	if err != nil {
		return 0, err
	}
	if st.Environment(environment) == nil {
		return 0, noEnvironment(environment)
	}
	prev := st.Previous(environment)
	if prev != nil {
		return prev.Release, nil
	}
	rels, err := s.Store.Releases(ctx, st.ID)
	if err != nil || len(rels) == 0 {
		return 0, err //nolint:wrapcheck // read by identity
	}
	return rels[0].Number, nil
}

// approvedOrRequest is nil when the promotion may go ahead, or the request it
// now waits on.
func (s *Service) approvedOrRequest(ctx context.Context, actor Actor, in approval.SubmitRelease) (*approval.Request, error) {
	if s.Approvals == nil {
		return nil, refused("this deployment has no approvals queue, so environment %q cannot be promoted to", in.Target)
	}
	if s.Approvals.ReleaseApproved(ctx, in.ReleaseID, in.Target, in.Digest) {
		return nil, nil //nolint:nilnil // nil request is "go ahead"
	}
	who := approval.Actor{Email: actor.Email, Persona: actor.Persona, Admin: actor.Admin}
	if actor.Admin {
		if err := s.Approvals.RecordReleaseSelfApproval(ctx, who, in); err != nil {
			return nil, fmt.Errorf("recording the approval: %w", err)
		}
		return nil, nil //nolint:nilnil // nil request is "go ahead"
	}
	r, err := s.Approvals.SubmitRelease(ctx, who, in)
	if err != nil {
		return nil, fmt.Errorf("requesting approval: %w", err)
	}
	return r, nil
}

// Rollback repoints an environment at an earlier release it served, the one
// before the current when to is 0. A release the environment served has been
// through whatever the environment requires, so it needs no new approval; a
// release it never served is reached by promote.
func (s *Service) Rollback(ctx context.Context, actor Actor, ref, environment string, to int) (*Promoted, error) {
	environment, _ = site.CanonicalEnvironment(environment)
	st, err := s.Find(ctx, ref)
	if err != nil {
		return nil, err
	}
	if err := s.mayEdit(ctx, actor, st); err != nil {
		return nil, err
	}
	env := st.Environment(environment)
	if env == nil {
		return nil, noEnvironment(environment)
	}
	history, err := s.Store.Deployments(ctx, st.ID, env.Name, 0)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	gone, err := s.prunedNumbers(ctx, st.ID)
	if err != nil {
		return nil, err
	}
	target, err := site.RollbackTarget(env, history, to, gone)
	if errors.Is(err, site.ErrPruned) {
		return nil, prunedRefusal(st, to)
	}
	if err != nil {
		return nil, refusedBy(err)
	}
	if err := s.move(ctx, actor, repoint{st: st, env: env, to: target, action: site.ActionRollback}); err != nil {
		return nil, err
	}
	return &Promoted{Environment: env.Name, Release: target, URL: s.Address(ctx, st, env.Name), Status: site.ActionRollback}, nil
}

// prunedNumbers is every release of the site a sweep pruned.
func (s *Service) prunedNumbers(ctx context.Context, siteID string) (map[int]bool, error) {
	rels, err := s.Store.Releases(ctx, siteID)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	out := map[int]bool{}
	for _, r := range rels {
		if r.Pruned {
			out[r.Number] = true
		}
	}
	return out, nil
}

// Release retention (#2602): each environment keeps its last N releases, and a
// sweep (internal/site/siteprune) prunes the rest. Lowering a limit deletes
// rollback history at the next sweep, so changing one is the owner's or an
// administrator's; a pruned release can be read as history and never served,
// compared, promoted or rolled back to.

// checkKeep refuses a limit out of range, and a change to it by anyone but the
// site's owner or an administrator.
func (s *Service) checkKeep(ctx context.Context, actor Actor, st *site.Site, c EnvironmentChange) error {
	if c.Keep == nil && !c.KeepDefault {
		return nil
	}
	if c.Keep != nil && (*c.Keep < 0 || *c.Keep > siteconfig.MaxKeepReleases) {
		return refused("keep_releases: 0 (keep every release) to %d, or \"default\"", siteconfig.MaxKeepReleases)
	}
	if err := s.mayOwn(ctx, actor, st); err != nil {
		return fmt.Errorf("only the owner of site %q or an administrator changes how many releases an environment keeps, since lowering it removes rollback history: %w",
			st.Name, site.ErrNotAllowed)
	}
	return nil
}

// liveRelease reads a release that has not been pruned, or refuses it naming
// the limits that removed it.
func (s *Service) liveRelease(ctx context.Context, st *site.Site, number int) (*site.Release, error) {
	rel, err := s.Store.Release(ctx, st.ID, number)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if rel.Pruned {
		return nil, prunedRefusal(st, number)
	}
	return rel, nil
}

// prunedRefusal says a release is gone and which limits removed it.
func prunedRefusal(st *site.Site, number int) error {
	limits := make([]string, 0, len(st.Environments))
	for _, e := range st.Environments {
		if e.Keep == 0 {
			limits = append(limits, e.Name+" keeps every release")
			continue
		}
		limits = append(limits, fmt.Sprintf("%s keeps the last %d", e.Name, e.Keep))
	}
	return &RefusedError{Err: site.ErrPruned, Msg: fmt.Sprintf(
		"release %d was removed by the release limit (%s, keep_releases): only a kept release can be served, compared, promoted or rolled back to",
		number, strings.Join(limits, ", "))}
}
