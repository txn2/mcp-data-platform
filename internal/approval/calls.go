package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Bounds on a call request's allowance.
const (
	// MaxCalls is the most calls one allowance holds.
	MaxCalls = 10000
	// DefaultAllowance is how long an approved allowance lasts when the
	// approver sets no deadline.
	DefaultAllowance = 24 * time.Hour
	// MaxAllowance is the furthest deadline an approver may set.
	MaxAllowance = 90 * 24 * time.Hour
	// Administrators is how a policy names every administrator as its
	// approver.
	Administrators = "administrators"
)

// CallRequest is a held call to file (#2437): what the platform read of it and
// what its policies asked.
type CallRequest struct {
	Tool       string
	Connection string
	Operation  string
	// Arguments are the call's arguments as the caller sent them, before
	// any stored secret is filled.
	Arguments map[string]any
	Rendering string
	Handlers  []HandlerAsk
	Approvers []string
	Calls     int
	Reason    string
}

// HeldCall is a call a person was told is held, as manage_approval submit
// names it: who made it, under which persona, the tool and its arguments as
// the call was made, how many calls to ask for and why.
type HeldCall struct {
	User, Persona, Tool string
	Arguments           map[string]any
	Calls               int
	Reason              string
}

// Ruling is one decision on a request: approve or deny, a comment for the
// requester, and on a call request what an approval grants.
type Ruling struct {
	Decision, Comment string
	Allowance         Allowance
}

// Allowance is what an approver grants: a number of calls until a deadline.
// Zero values take what was asked and DefaultAllowance.
type Allowance struct {
	Calls int
	Until *time.Time
}

// AllowanceStore spends approved allowances (PostgresStore).
type AllowanceStore interface {
	// Spend uses one call of the oldest approved allowance requester holds
	// for operation on connection that has calls left and has not passed
	// its deadline, returning its id; ok is false when none does.
	Spend(ctx context.Context, requester, connection, operation string) (id string, ok bool, err error)
}

// SubmitCall files a held call for actor, who made it. A request is frozen
// when filed: nothing changes its arguments afterward.
func (s *Service) SubmitCall(ctx context.Context, actor Actor, in CallRequest) (*Request, error) {
	if actor.Email == "" {
		return nil, refused("a request belongs to a person: sign in with an account that has an email")
	}
	if in.Tool == "" || in.Connection == "" {
		return nil, refused("a call request names the tool and the connection it calls")
	}
	if in.Calls <= 0 {
		in.Calls = 1
	}
	if in.Calls > MaxCalls {
		return nil, refused("calls: at most %d", MaxCalls)
	}
	reason, err := limitReason(in.Reason)
	if err != nil {
		return nil, refused("reason: at most %d bytes", MaxReason)
	}
	r := &Request{
		ID: NewID(), SubjectKind: SubjectCall, Purpose: PurposeCallAllowance, RequesterEmail: actor.Email,
		RequesterPersona: actor.Persona, Reason: reason,
		CallTool: in.Tool, CallConnection: in.Connection, CallOperation: in.Operation, CallArguments: in.Arguments,
		Rendering: in.Rendering, Handlers: in.Handlers, Approvers: in.Approvers, CallsAsked: in.Calls,
	}
	if err := s.Store.Insert(ctx, r); err != nil {
		return nil, fmt.Errorf("filing the request: %w", err)
	}
	if s.Notifier != nil {
		s.Notifier.Requested(ctx, r, s.callApprovers(ctx, r))
	}
	return r, nil
}

// callApprovers are who a call request is told to: each person its policies
// named, once, and the administrators when they named no person.
func (s *Service) callApprovers(ctx context.Context, r *Request) []string {
	var out []string
	for _, a := range r.Approvers {
		if strings.Contains(a, "@") && !slices.Contains(out, strings.ToLower(a)) && !strings.EqualFold(a, r.RequesterEmail) {
			out = append(out, strings.ToLower(a))
		}
	}
	if len(out) == 0 && s.Approvers != nil {
		for _, a := range s.Approvers(ctx) {
			if !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	return out
}

// mayDecideCall refuses an actor who may not decide a call request: one who is
// neither an administrator nor named by its policies, or a requester deciding
// their own who is not an administrator.
func mayDecideCall(actor Actor, r *Request) error {
	if actor.Admin {
		return nil
	}
	if strings.EqualFold(actor.Email, r.RequesterEmail) {
		return fmt.Errorf("you asked for this call; someone its policies name, or an administrator, decides it: %w", ErrNotAllowed)
	}
	if !namedApprover(actor, r) {
		return fmt.Errorf("only %s or an administrator decide this request: %w", strings.Join(r.Approvers, ", "), ErrNotAllowed)
	}
	return nil
}

// namedApprover reports whether a call request's policies name actor, by
// address or persona.
func namedApprover(actor Actor, r *Request) bool {
	for _, a := range r.Approvers {
		if (actor.Email != "" && strings.EqualFold(a, actor.Email)) || (actor.Persona != "" && a == actor.Persona) {
			return true
		}
	}
	return false
}

// mayRead reports whether actor may read r: an administrator, its requester,
// or someone a call request's policies name.
func mayRead(actor Actor, r *Request) bool {
	return actor.Admin || strings.EqualFold(r.RequesterEmail, actor.Email) || (r.SubjectKind == SubjectCall && namedApprover(actor, r))
}

// decideCall records the decision on a call request: approved, an allowance
// of the calls the approver grants (at most what was asked) until a deadline.
func (s *Service) decideCall(ctx context.Context, actor Actor, cur *Request, r Ruling) (*Request, error) {
	if err := mayDecideCall(actor, cur); err != nil {
		return nil, err
	}
	d := Decision{
		Status: r.Decision, By: actor.Email, Comment: r.Comment, At: s.now(),
		SelfApproved: r.Decision == StatusApproved && strings.EqualFold(cur.RequesterEmail, actor.Email),
	}
	if r.Decision == StatusApproved {
		calls, until, err := s.allowance(cur, r.Allowance)
		if err != nil {
			return nil, err
		}
		d.CallsApproved, d.Until = calls, &until
	}
	return s.record(ctx, actor, cur, d)
}

// allowance is the count and deadline an approval grants.
func (s *Service) allowance(cur *Request, a Allowance) (int, time.Time, error) {
	calls := a.Calls
	if calls <= 0 {
		calls = cur.CallsAsked
	}
	if calls > cur.CallsAsked {
		return 0, time.Time{}, refused("%d calls were asked for; an approval grants at most that many", cur.CallsAsked)
	}
	until := s.now().Add(DefaultAllowance)
	if a.Until != nil {
		until = *a.Until
	}
	if !until.After(s.now()) {
		return 0, time.Time{}, refused("until is in the past: an allowance needs a deadline still to come")
	}
	if until.Sub(s.now()) > MaxAllowance {
		return 0, time.Time{}, refused("until: at most %d days from now", int(MaxAllowance.Hours()/24))
	}
	return calls, until, nil
}

// SpendAllowance uses one call of an approved allowance requester holds for
// operation on connection, returning its id; ok is false when none has a
// call left before its deadline, and then the policy decides again.
func (s *Service) SpendAllowance(ctx context.Context, requester, connection, operation string) (string, bool) {
	if s == nil || s.Allowances == nil || requester == "" {
		return "", false
	}
	id, ok, err := s.Allowances.Spend(ctx, requester, connection, operation)
	if err != nil {
		return "", false
	}
	return id, ok
}

// spendSQL takes one call from the oldest usable allowance, under a row lock
// skipped by a concurrent spender, so two calls never spend the last one.
const spendSQL = `UPDATE approval_requests SET calls_used = calls_used + 1
	WHERE id = (SELECT id FROM approval_requests
	             WHERE subject_kind = 'call' AND status = 'approved' AND lower(requester_email) = lower($1)
	               AND call_connection = $2 AND call_operation = $3
	               AND calls_used < calls_approved AND (allowance_until IS NULL OR allowance_until > NOW())
	             ORDER BY decided_at, id LIMIT 1 FOR UPDATE SKIP LOCKED)
	RETURNING id`

// Spend uses one call of an approved allowance.
func (p *PostgresStore) Spend(ctx context.Context, requester, connection, operation string) (id string, ok bool, err error) {
	err = p.db.QueryRowContext(ctx, spendSQL, requester, connection, operation).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("spending an allowance: %w", err)
	}
	return id, true, nil
}
