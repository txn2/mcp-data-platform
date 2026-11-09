package sitesvc

import (
	"context"
	"fmt"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/site"
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

// Promote points an environment at another environment's release. Where the
// target requires approval, an administrator's promotion records its own
// approval; anyone else's files a request and is carried out when they promote
// again after it is approved. An approval names the release's manifest digest.
func (s *Service) Promote(ctx context.Context, actor Actor, req PromoteRequest) (*Promoted, error) {
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

func (s *Service) promotable(ctx context.Context, st *site.Site, req PromoteRequest) (*site.Release, error) {
	number := req.Release
	if number == 0 {
		from := st.Environment(req.From)
		if from == nil {
			return nil, noEnvironment(req.From)
		}
		if from.Release == 0 {
			return nil, refused("environment %q serves no release yet", req.From)
		}
		number = from.Release
	}
	return s.Store.Release(ctx, st.ID, number) //nolint:wrapcheck // read by identity
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
	target, err := rollbackTarget(env, history, to)
	if err != nil {
		return nil, err
	}
	if err := s.move(ctx, actor, repoint{st: st, env: env, to: target, action: site.ActionRollback}); err != nil {
		return nil, err
	}
	return &Promoted{Environment: env.Name, Release: target, URL: s.Address(ctx, st, env.Name), Status: site.ActionRollback}, nil
}

func rollbackTarget(env *site.Environment, history []site.Deployment, to int) (int, error) {
	served := make([]int, 0, len(history))
	for _, d := range history {
		if d.To != env.Release {
			served = append(served, d.To)
		}
	}
	if to != 0 {
		if !slices.Contains(served, to) {
			return 0, refused("environment %q has not served release %d; a release it never served is reached by promote", env.Name, to)
		}
		return to, nil
	}
	// The newest release it served that is older than the current one, so a
	// second rollback keeps going back rather than returning.
	before := 0
	for _, n := range served {
		if n < env.Release && n > before {
			before = n
		}
	}
	if before == 0 {
		return 0, refused("environment %q has served no earlier release", env.Name)
	}
	return before, nil
}
