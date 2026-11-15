// Package siteenv carries out the operations on a site's environments that
// came after the core service (#2648): what an environment answers (online,
// offline, redirect, #2631, #2632), renaming and removing one (#2630), the
// check of a new site's name (#2636), reading one file for the portal's viewer
// (#2629), and the rule that an agent's move of an environment people use
// waits on a person (#2643). It works through internal/site/sitesvc's exported
// surface, so the core service stays the size it was.
package siteenv

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/site/sitesvc"
)

// Ops is the operations over one service.
type Ops struct{ *sitesvc.Service }

// New is the operations over svc.
func New(svc *sitesvc.Service) Ops { return Ops{Service: svc} }

// StatusDone and StatusAwaitingApproval are the statuses a change answers.
const (
	StatusDone             = sitesvc.StatusDone
	StatusAwaitingApproval = sitesvc.StatusAwaitingApproval
)

func refused(format string, args ...any) error {
	return &sitesvc.RefusedError{Msg: fmt.Sprintf(format, args...)}
}

func refusedBy(err error) error { return &sitesvc.RefusedError{Msg: err.Error()} }

func noEnvironment(name string) error {
	return fmt.Errorf("environment %q: %w", name, site.ErrNoEnvironment)
}

// invalidate has every replica re-read a site's routes.
func (o Ops) invalidate(name string) {
	if o.Invalidate != nil {
		o.Invalidate(name)
	}
}

// siteFor is a site and one of its environments, for someone who manages it.
func (o Ops) siteFor(ctx context.Context, actor sitesvc.Actor, ref, name string) (*site.Site, *site.Environment, error) {
	st, err := o.Find(ctx, ref)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // read by identity
	}
	if err := o.MayEdit(ctx, actor, st); err != nil {
		return nil, nil, err //nolint:wrapcheck // read by identity
	}
	name, _ = site.CanonicalEnvironment(name)
	env := st.Environment(name)
	if env == nil {
		return nil, nil, noEnvironment(name)
	}
	return st, env, nil
}
