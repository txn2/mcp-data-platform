package script

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Automatic approval of a personal script (#1367).
//
// A script whose entire audience is its author still could not run: nothing
// executes a version nobody approved, so every personal automation needed a
// second person before it did anything on its own. Approval is not what caps a
// script's access — the roles a run presents are copied from the version's
// author and are refused from the approval request (see Author) — so for a
// script only its owner can invoke, the reviewer was narrowing a script against
// the person it belongs to.
//
// What approval also does is bind the other three axes of the grant, and those
// do not follow from who the author is. Automatic approval therefore MINTS a
// grant rather than skipping one: it reads what the code plainly reaches and
// grants exactly that, with a destination carrying the same resolved address a
// reviewed one carries. A grant it cannot read off the source is not guessed —
// the version goes to review, which is where it was before.

// Referenced is what a static read of a script's source says the code plainly
// reaches: the host bindings it calls, the connections it names, and the
// destinations it writes to. It is the domain's view of a report the engine
// produces, so the rules here can be stated without the domain knowing what
// Starlark is.
type Referenced struct {
	Capabilities []string
	Connections  []string
	Destinations []string

	// Incomplete is true when a call computes its connection or its destination
	// instead of naming one, so the lists above are known to be short. A grant
	// derived from a short list is a grant the run then refuses itself on, so it
	// is a refusal to derive rather than a partial answer.
	Incomplete bool
}

// AutoOutcome is what automatic approval did with a version.
type AutoOutcome struct {
	// Approved reports that the version is now the script's approved version,
	// bound to a grant the platform minted.
	Approved bool

	// Reason states why it was not, in the owner's terms, so the answer to
	// "why is nothing running this" is on the response that saved it. It is
	// empty when the version was approved, and equally empty when the script was
	// never a candidate — a shared script and an edit somebody else wrote are
	// not refusals, they are the ordinary review path.
	Reason string
}

// AutoDecision is what automatic approval WOULD do, decided without writing
// anything.
//
// The decision is separate from the act because the edit funnel has to know the
// answer before it chooses where to put the edit. An edit that will be approved
// is applied to the live row; one that will not becomes a draft awaiting review.
// Deciding afterwards would leave a declined edit applied to a script whose
// execution pointer still names the older version — running code nobody can
// find in the review queue.
type AutoDecision struct {
	// Approvable reports that the grant below can be bound with no reviewer.
	Approvable bool
	// Reason states why it cannot, in the owner's terms; empty when Approvable
	// and empty when the script was never a candidate.
	Reason string
	// Grants is what the version would be approved to reach.
	Grants Grants
	// Approved is the version the script executes today, nil when it has none.
	// It is carried so binding can tell an approval that would change nothing
	// from one that would, without reading the row again.
	Approved *Version
}

// AutoApprover mints and binds the grant an owner-authored personal version
// executes under. A nil one is a deployment with no automatic approval, where
// every version waits for a reviewer exactly as before.
//
// Neither method reports an error. A version that cannot be approved
// automatically is a version awaiting review, which is a state the surface has
// to describe either way, and failing the SAVE over the approval that followed
// it would tell an owner their work was lost when it was stored. Every refusal,
// including one caused by a store failure, comes back as a Reason.
type AutoApprover interface {
	// Consider decides whether this script's next version can be approved with
	// no reviewer, and what it would be approved to reach. It writes nothing.
	Consider(ctx context.Context, sc *Script, author Author) AutoDecision

	// Approve binds a decision Consider admitted to the named version, and
	// advances sc to what the write left behind. A decision that was not
	// approvable is answered with its own reason and nothing is written.
	Approve(ctx context.Context, sc *Script, version int, decision AutoDecision) AutoOutcome
}

// AutoApprovable reports whether a version of this script, written by this
// author, is a candidate for automatic approval at all.
//
// The three conditions are the whole of the authority argument. The script is
// PERSONAL, so its only caller is the person it belongs to; the author IS that
// person, so the roles the version captured are their own and approving binds
// nothing they did not already hold; and the script has not been replaced, which
// is the one lifecycle state that must never become executable again.
//
// An edit written by anybody else — an administrator fixing somebody's script
// is the author of what they wrote, and their roles are what the version would
// capture — is not a candidate and goes to review.
func AutoApprovable(sc *Script, author Author) bool {
	return sc != nil && sc.Status != StatusSuperseded && sc.OwnedPersonally(author.Email)
}

// WithdrawsAutoApproval reports whether an edit takes a script out of the scope
// its automatic approval was granted under.
//
// Widening scope off personal is the one edit that changes who the approval was
// reasoned about. The version was approved because its only caller was its
// author; a persona-scoped or global script has an audience that never agreed to
// anything, so the approval nobody made must not follow the script into it. The
// gate is cleared and the version returns to the review queue.
//
// It says nothing about whether the approval WAS automatic — that is a fact
// about the version, which the store reads under the same lock — because an
// approval a person made survives the scope change: they decided, and widening
// the audience does not un-decide it.
func WithdrawsAutoApproval(before, after *Script) bool {
	return before != nil && after != nil &&
		before.Scope == ScopePersonal && after.Scope != ScopePersonal &&
		before.Executable()
}

// DeriveGrants mints the capability grant a version runs under from what a
// static read says its code reaches, or reports why it cannot be minted.
//
// pinned is the destination set the script's currently-approved version already
// carries, and it is the ONLY source of an address for a destination outside the
// platform. A destination names a connection, a bucket and a prefix that a
// person decided on, so there is nothing in the source to resolve one from: the
// first delivery to a bucket is reviewed, a reviewer pins the address, and the
// owner's later edits are approved against the address that was pinned. The
// portal needs no pinning, because the platform owns where its own assets live.
//
// The error text is the owner's answer, not a diagnostic: it is put in front of
// the person who just pressed save.
func DeriveGrants(ref Referenced, roles []string, pinned []Destination) (Grants, error) {
	if ref.Incomplete {
		return Grants{}, errors.New(
			"this script computes a connection or a destination instead of naming one, " +
				"so what it reaches cannot be read from its source; a reviewer decides what it may reach")
	}
	destinations, err := resolveDestinations(ref.Destinations, pinned)
	if err != nil {
		return Grants{}, err
	}
	grants := Grants{
		Roles:        slices.Clone(roles),
		Connections:  slices.Clone(ref.Connections),
		Capabilities: slices.Clone(ref.Capabilities),
		Destinations: destinations,
	}
	if err := grants.Validate(); err != nil {
		return Grants{}, err
	}
	return grants, nil
}

// resolveDestinations turns the destination NAMES a source writes into the
// addressed destinations a grant records.
func resolveDestinations(names []string, pinned []Destination) ([]Destination, error) {
	out := make([]Destination, 0, len(names))
	var unresolved []string
	for _, name := range names {
		if name == DestinationPortal {
			out = append(out, PortalDestination())
			continue
		}
		if d, ok := destinationNamed(pinned, name); ok {
			out = append(out, d.Normalized())
			continue
		}
		unresolved = append(unresolved, name)
	}
	if len(unresolved) > 0 {
		return nil, fmt.Errorf(
			"this script writes to %s, and where that is — the connection, the bucket and the prefix — "+
				"is a decision nothing in the source states; ask an administrator to approve this version once "+
				"and later edits are approved against the address they pin",
			strings.Join(unresolved, ", "))
	}
	return out, nil
}

// destinationNamed finds one destination by the name a script writes.
func destinationNamed(destinations []Destination, name string) (Destination, bool) {
	for _, d := range destinations {
		if d.Name == name {
			return d, true
		}
	}
	return Destination{}, false
}

// RefuseUnreachable reports the granted connections the author's own persona
// cannot reach, or an empty string when every one of them is reachable.
//
// An approved run presents the author's roles, so a script granted a connection
// its author cannot reach is a script that fails on its first query — the
// middleware refuses the call whatever the grant says. Answering it here means
// the owner is told while they are still looking at the script, rather than by a
// failed run at three in the morning.
//
// An empty reachable set is not read as "reaches nothing": a deployment that
// cannot enumerate its connections would otherwise refuse every script, and this
// check is a courtesy in front of a boundary the middleware enforces regardless.
func RefuseUnreachable(g Grants, reachable []string) string {
	if len(reachable) == 0 {
		return ""
	}
	var missing []string
	for _, name := range grantedConnectionNames(g) {
		if !slices.Contains(reachable, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return fmt.Sprintf(
		"this script reaches %s, which your persona cannot; a run of it would be refused, "+
			"so it was not approved", strings.Join(missing, ", "))
}

// grantedConnectionNames is every connection a grant lets a run reach: the ones
// it queries, and the one each external destination is delivered over. Both
// cross the same persona boundary at run time, so both are checked here.
func grantedConnectionNames(g Grants) []string {
	names := slices.Clone(g.Connections)
	for _, d := range g.Destinations {
		if d.Connection != "" && !slices.Contains(names, d.Connection) {
			names = append(names, d.Connection)
		}
	}
	return names
}
