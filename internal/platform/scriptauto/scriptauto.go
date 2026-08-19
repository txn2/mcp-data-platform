// Package scriptauto approves a personal script for its own owner (#1367).
//
// A managed script runs nothing until a version is approved, and approving binds
// the capability grant that run is confined to. For a script whose entire
// audience is its author that asked an administrator to authorize a person
// against themselves: the roles an approved run presents are copied from the
// version's AUTHOR and are refused from the approval request, so a reviewer of a
// personal script was narrowing a script only its owner could invoke.
//
// This package is the other three axes of the grant, which do not follow from
// who the author is and therefore cannot simply be skipped. It reads what the
// code plainly reaches — the same static read the review route checks a
// reviewer's grant against — and mints exactly that:
//
//   - capabilities and connections come from the source;
//   - a portal destination resolves to the canonical portal address;
//   - any other destination resolves to the address the script's currently
//     approved version already pins, because where a bucket destination points
//     is a decision a person made and nothing in the source states it.
//
// Anything it cannot read off the source is not guessed. The version stays
// unapproved and goes to the review queue, which is exactly where it was before
// this package existed.
//
// It lives here rather than in pkg/script because minting the grant needs the
// Starlark reader in scriptrun and the deployment's connection enumeration, and
// the domain knows about neither.
package scriptauto

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// ConnectionReach lists the connections one author's authority reaches, in the
// deployment's terms. It is the composition root's, because resolving it means
// walking the live toolkit registry through the persona boundary.
//
// Nil skips the check. The middleware enforces the same boundary at run time
// regardless, so what is lost is the early answer, not the boundary.
type ConnectionReach func(ctx context.Context, roles []string) []string

// Deps are the collaborators an approver needs.
type Deps struct {
	// Approvals binds the grant and moves the execution gate. Nil disables
	// automatic approval entirely.
	Approvals script.AutoApprovalStore
	// Versions reads the currently approved version, which is where the address
	// of an already-pinned destination comes from. Nil leaves every non-portal
	// destination unresolvable, so those versions go to review.
	Versions script.VersionStore
	// Reach reports what the author's own authority can call.
	Reach ConnectionReach
}

// Approver mints and binds the grant an owner-authored personal version runs
// under. It implements script.AutoApprover.
type Approver struct {
	deps Deps
}

// New builds an approver. A nil result is never returned; a deployment that
// cannot approve is expressed by a nil Approvals, which declines every version
// with no reason, because "this deployment does not do that" is not something to
// put in front of the person who pressed save.
func New(deps Deps) *Approver { return &Approver{deps: deps} }

// Consider decides whether this script's next version can be approved with no
// reviewer, and what it would be approved to reach. It writes nothing.
//
// The nil receiver is the deployment with no automatic approval at all, and it
// is answered the same way a shared script is: an empty decision, which reads as
// "nobody approved this", because nobody did.
func (a *Approver) Consider(ctx context.Context, sc *script.Script, author script.Author) script.AutoDecision {
	if a == nil || a.deps.Approvals == nil || !script.AutoApprovable(sc, author) {
		return script.AutoDecision{}
	}
	report := scriptrun.Validate(sc.Source)
	if !report.OK {
		return declined("this script does not parse, so what it reaches cannot be read from it")
	}
	approved := a.approvedVersion(ctx, sc)
	grants, err := script.DeriveGrants(referenced(report), author.Roles, pinnedDestinations(approved))
	if err != nil {
		return declined(err.Error())
	}
	if a.deps.Reach != nil {
		if reason := script.RefuseUnreachable(grants, a.deps.Reach(ctx, author.Roles)); reason != "" {
			return declined(reason)
		}
	}
	return script.AutoDecision{Approvable: true, Grants: grants, Approved: approved}
}

// Approve binds a decision Consider admitted to the named version, and advances
// sc to what the write left behind, so the surface that saved the edit reports
// the state that now exists rather than the one it sent.
func (a *Approver) Approve(
	ctx context.Context, sc *script.Script, version int, decision script.AutoDecision,
) script.AutoOutcome {
	if a == nil || !decision.Approvable {
		return script.AutoOutcome{Reason: decision.Reason}
	}
	v, err := a.deps.Approvals.AutoApproveVersion(ctx, sc.ID, version, sc.OwnerEmail, decision.Grants)
	if err != nil {
		// The edit itself was stored; only the approval that would have followed
		// it failed. Reporting that as a failed SAVE would tell an owner their
		// work was lost when it was not, so the failure is logged and answered as
		// the state the script is actually in: unapproved, awaiting a reviewer.
		slog.Error("failed to approve a personal script automatically",
			"script", sc.Name, "version", version, "error", err)
		return script.AutoOutcome{
			Reason: "this version could not be approved automatically; ask an administrator to approve it",
		}
	}
	sc.ApprovedVersionID = v.ID
	sc.Version = v.Version
	if sc.Status == script.StatusDraft {
		sc.Status = script.StatusActive
	}
	return script.AutoOutcome{Approved: true}
}

// AutoApprove is Consider followed by Approve, for a version with no edit funnel
// in front of it: the first version of a script being created.
func (a *Approver) AutoApprove(
	ctx context.Context, sc *script.Script, version int, author script.Author,
) script.AutoOutcome {
	return a.Approve(ctx, sc, version, a.Consider(ctx, sc, author))
}

// approvedVersion reads the version the execution gate points at, or nil when
// there is none and when it cannot be read. A read that fails yields nil, which
// leaves a bucket destination unresolvable and sends the version to review —
// the safe direction: the alternative is minting an address nothing verified.
func (a *Approver) approvedVersion(ctx context.Context, sc *script.Script) *script.Version {
	if a.deps.Versions == nil || !sc.Executable() {
		return nil
	}
	v, err := a.deps.Versions.GetVersionByID(ctx, sc.ApprovedVersionID)
	if err != nil {
		slog.Error("failed to read a script's approved version while approving automatically",
			"script", sc.Name, "error", err)
		return nil
	}
	return v
}

// referenced projects the engine's static read into the domain's view of it.
//
// A computed connection or destination makes the lists short, and a grant
// derived from a short list is one the run refuses itself on, so the two flags
// collapse into the single fact the domain acts on.
func referenced(report scriptrun.Report) script.Referenced {
	return script.Referenced{
		Capabilities: report.Capabilities,
		Connections:  report.Connections,
		Destinations: report.Destinations,
		Incomplete:   report.DynamicConnections || report.DynamicDestinations,
	}
}

// pinnedDestinations is the addressed destination set the currently approved
// version carries, which is the only place the address of a destination outside
// the platform comes from.
func pinnedDestinations(approved *script.Version) []script.Destination {
	if approved == nil {
		return nil
	}
	return approved.Grants.Destinations
}

// declined is a refusal carrying the reason the owner is shown.
func declined(reason string) script.AutoDecision {
	return script.AutoDecision{Reason: reason}
}
