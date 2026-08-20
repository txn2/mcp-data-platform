package script_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// personal returns a personal script owned by testAuthor with nothing approved,
// which is the state automatic approval acts on.
func personal() *script.Script {
	return &script.Script{
		ID: "script_1", Name: "daily", Scope: script.ScopePersonal,
		OwnerEmail: testAuthor.Email, Source: "x = 1", Status: script.StatusDraft,
	}
}

// TestAutoApprovable_IsTheOwnersOwnPersonalScript pins the whole authority
// argument: the script is personal, the author is its owner, and it has not been
// replaced. Anything else goes to review.
func TestAutoApprovable_IsTheOwnersOwnPersonalScript(t *testing.T) {
	assert.True(t, script.AutoApprovable(personal(), testAuthor))

	tests := []struct {
		name   string
		mutate func(*script.Script)
		author script.Author
	}{
		{"a shared script", func(s *script.Script) { s.Scope = script.ScopeGlobal }, testAuthor},
		{"a persona script", func(s *script.Script) { s.Scope = script.ScopePersona }, testAuthor},
		{"a superseded script", func(s *script.Script) { s.Status = script.StatusSuperseded }, testAuthor},
		{"an unidentified owner", func(s *script.Script) { s.OwnerEmail = "" }, script.Author{}},
		{
			"somebody else's edit", func(*script.Script) {},
			script.Author{Email: "admin@example.com", Roles: []string{"admin"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sc := personal()
			tt.mutate(sc)
			assert.False(t, script.AutoApprovable(sc, tt.author))
		})
	}

	assert.False(t, script.AutoApprovable(nil, testAuthor))
}

// TestOwnedPersonally is the shared question behind both rules: automatic
// approval, and a script being its owner's to delete outright.
func TestOwnedPersonally(t *testing.T) {
	sc := personal()
	assert.True(t, sc.OwnedPersonally(testAuthor.Email))
	assert.False(t, sc.OwnedPersonally("admin@example.com"))
	assert.False(t, sc.OwnedPersonally(""), "an unnamed caller owns nothing")

	shared := personal()
	shared.Scope = script.ScopeGlobal
	assert.False(t, shared.OwnedPersonally(testAuthor.Email))

	unowned := personal()
	unowned.OwnerEmail = ""
	assert.False(t, unowned.OwnedPersonally(""),
		"a script whose owner could not be established belongs to nobody")

	var nilScript *script.Script
	assert.False(t, nilScript.OwnedPersonally(testAuthor.Email))
}

// TestWithdrawsAutoApproval_OnlyOnWideningAnExecutablePersonalScript pins the
// one edit that takes a script out of the audience its approval was reasoned
// about.
func TestWithdrawsAutoApproval_OnlyOnWideningAnExecutablePersonalScript(t *testing.T) {
	before := personal()
	before.ApprovedVersionID = "sver_1"

	widened := *before
	widened.Scope = script.ScopeGlobal
	assert.True(t, script.WithdrawsAutoApproval(before, &widened))

	toPersona := *before
	toPersona.Scope = script.ScopePersona
	assert.True(t, script.WithdrawsAutoApproval(before, &toPersona))

	// Nothing approved: there is nothing to withdraw.
	unapproved := personal()
	unapprovedWide := *unapproved
	unapprovedWide.Scope = script.ScopeGlobal
	assert.False(t, script.WithdrawsAutoApproval(unapproved, &unapprovedWide))

	// Scope unchanged, and a shared script narrowing to personal, both keep it.
	same := *before
	assert.False(t, script.WithdrawsAutoApproval(before, &same))
	shared := personal()
	shared.Scope = script.ScopeGlobal
	shared.ApprovedVersionID = "sver_1"
	narrowed := *shared
	narrowed.Scope = script.ScopePersonal
	assert.False(t, script.WithdrawsAutoApproval(shared, &narrowed))

	assert.False(t, script.WithdrawsAutoApproval(nil, &widened))
	assert.False(t, script.WithdrawsAutoApproval(before, nil))
}

// TestDeriveGrants_MintsWhatTheCodeReaches is the grant an owner-authored
// version runs under: exactly what a static read found, with the author's own
// roles as the authority.
func TestDeriveGrants_MintsWhatTheCodeReaches(t *testing.T) {
	ref := script.Referenced{
		Capabilities: []string{script.CapabilityQuery, script.CapabilityExport},
		Connections:  []string{"warehouse"},
		Destinations: []string{script.DestinationPortal},
	}

	grants, err := script.DeriveGrants(ref, testAuthor.Roles, nil)
	require.NoError(t, err)
	assert.Equal(t, testAuthor.Roles, grants.Roles)
	assert.Equal(t, []string{"warehouse"}, grants.Connections)
	assert.Equal(t, ref.Capabilities, grants.Capabilities)
	assert.Equal(t, []script.Destination{script.PortalDestination()}, grants.Destinations)
}

// TestDeriveGrants_ResolvesABucketOnlyFromTheAddressAlreadyPinned is the whole
// destination rule: where a bucket destination points is a decision a person
// made, and nothing in the source states it.
func TestDeriveGrants_ResolvesABucketOnlyFromTheAddressAlreadyPinned(t *testing.T) {
	pinned := []script.Destination{{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}}
	ref := script.Referenced{
		Capabilities: []string{script.CapabilityExport},
		Destinations: []string{"acme-drop"},
	}

	grants, err := script.DeriveGrants(ref, testAuthor.Roles, pinned)
	require.NoError(t, err)
	require.Len(t, grants.Destinations, 1)
	assert.Equal(t, pinned[0], grants.Destinations[0],
		"the address comes from the approval that pinned it, never from the source")

	_, err = script.DeriveGrants(ref, testAuthor.Roles, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "acme-drop")
	assert.Contains(t, err.Error(), "ask an administrator")
}

// TestDeriveGrants_RefusesWhatCannotBeReadOrBound covers the three refusals: a
// source the reader cannot see through, an author with no authority, and a
// pinned destination that is not a place the platform can write.
func TestDeriveGrants_RefusesWhatCannotBeReadOrBound(t *testing.T) {
	incomplete := script.Referenced{
		Capabilities: []string{script.CapabilityQuery},
		Incomplete:   true,
	}
	_, err := script.DeriveGrants(incomplete, testAuthor.Roles, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "computes a connection or a destination")

	_, err = script.DeriveGrants(script.Referenced{}, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no roles")

	broken := []script.Destination{{Name: "acme-drop", Kind: script.DestinationKindS3}}
	_, err = script.DeriveGrants(
		script.Referenced{Destinations: []string{"acme-drop"}}, testAuthor.Roles, broken)
	require.Error(t, err, "a pinned destination is validated like any other grant")
}

// TestRefuseUnreachable_NamesAConnectionTheAuthorCannotCall is the answer given
// at save rather than by a failed run: an approved run presents the author's
// roles, so a grant reaching past them fails on its first query.
func TestRefuseUnreachable_NamesAConnectionTheAuthorCannotCall(t *testing.T) {
	grants := script.Grants{
		Roles:       testAuthor.Roles,
		Connections: []string{"warehouse", "finance"},
		Destinations: []script.Destination{{
			Name: "acme-drop", Kind: script.DestinationKindS3,
			Connection: "acme-s3", Bucket: "acme-exports",
		}},
	}

	query := func(names ...string) []script.ConnectionRef {
		refs := make([]script.ConnectionRef, 0, len(names))
		for _, n := range names {
			refs = append(refs, script.ConnectionRef{Kind: script.ConnectionParamKind, Name: n})
		}
		return refs
	}
	drop := script.ConnectionRef{Kind: script.DestinationKindS3, Name: "acme-s3"}

	assert.Empty(t, script.RefuseUnreachable(grants, append(query("warehouse", "finance"), drop)))

	reason := script.RefuseUnreachable(grants, append(query("warehouse"), drop))
	assert.Contains(t, reason, "finance")
	assert.NotContains(t, reason, "warehouse")

	assert.Contains(t,
		script.RefuseUnreachable(grants, query("warehouse", "finance")), "acme-s3",
		"the connection a destination is delivered over crosses the same boundary")

	assert.Empty(t, script.RefuseUnreachable(grants, nil),
		"a deployment that cannot enumerate its connections refuses nothing")
}

// TestRefuseUnreachable_MatchesTheKindTheRunWillReach proves a granted name is
// checked against the connection the run reaches under it, not against any
// connection that happens to share the name (#1384). A deployment may carry one
// name across kinds; matching by name alone admitted a grant the middleware
// would then refuse, which is exactly the answer this check exists to give
// early.
func TestRefuseUnreachable_MatchesTheKindTheRunWillReach(t *testing.T) {
	grants := script.Grants{
		Roles:       testAuthor.Roles,
		Connections: []string{"acme"},
		Destinations: []script.Destination{{
			Name: "acme-drop", Kind: script.DestinationKindS3,
			Connection: "acme", Bucket: "acme-exports",
		}},
	}

	// The author reaches only the s3 connection called "acme". The queried
	// connection is reached through the query binding, which is another kind,
	// so the query grant is refused and the destination is not.
	reason := script.RefuseUnreachable(grants,
		[]script.ConnectionRef{{Kind: script.DestinationKindS3, Name: "acme"}})
	assert.Contains(t, reason, "acme ("+script.ConnectionParamKind+")")
	assert.NotContains(t, reason, "acme ("+script.DestinationKindS3+")")

	// Reaching both is what the grant actually needs.
	assert.Empty(t, script.RefuseUnreachable(grants, []script.ConnectionRef{
		{Kind: script.ConnectionParamKind, Name: "acme"},
		{Kind: script.DestinationKindS3, Name: "acme"},
	}))

	// A connection a grant names more than once is one connection, so an
	// unreachable one is reported once rather than per mention.
	repeated := script.Grants{
		Connections: []string{"acme", "acme"},
		Destinations: []script.Destination{
			// The portal is reached through the platform, not over a connection,
			// so it crosses no connection boundary and is not checked here.
			script.PortalDestination(),
			{Name: "a", Kind: script.DestinationKindS3, Connection: "drop"},
			{Name: "b", Kind: script.DestinationKindS3, Connection: "drop"},
		},
	}
	reason = script.RefuseUnreachable(repeated,
		[]script.ConnectionRef{{Kind: "datahub", Name: "elsewhere"}})
	assert.Equal(t, 1, strings.Count(reason, "acme ("))
	assert.Equal(t, 1, strings.Count(reason, "drop ("))
}

// stubApprover records what the funnel asked it and answers with a fixed
// decision, so the funnel's own behavior is what is under test.
type stubApprover struct {
	considered *script.Script
	author     script.Author
	approved   *script.Script
	version    int
	decision   script.AutoDecision
}

func (s *stubApprover) Consider(
	_ context.Context, sc *script.Script, author script.Author,
) script.AutoDecision {
	s.considered, s.author = sc, author
	return s.decision
}

func (s *stubApprover) Approve(
	_ context.Context, sc *script.Script, version int, decision script.AutoDecision,
) script.AutoOutcome {
	if !decision.Approvable {
		return script.AutoOutcome{Reason: decision.Reason}
	}
	s.approved, s.version = sc, version
	return script.AutoOutcome{Approved: true}
}

// TestApplyEdit_AppliesAndApprovesTheOwnersOwnEdit is where automatic approval
// happens: once, in the one funnel every mutation surface crosses, and on the
// live row rather than in the review queue.
func TestApplyEdit_AppliesAndApprovesTheOwnersOwnEdit(t *testing.T) {
	auto := &stubApprover{decision: script.AutoDecision{Approvable: true}}
	store := &versioningStore{}
	before := approved()
	before.OwnerEmail = testAuthor.Email
	after := *before
	after.Source = "x = 2"

	outcome, err := script.ApplyEdit(context.Background(), store, script.Edit{
		Before: before, After: &after, Author: testAuthor, Auto: auto,
	})
	require.NoError(t, err)
	assert.True(t, outcome.Applied)
	assert.Zero(t, outcome.PendingVersion, "an approved edit is not also awaiting review")
	assert.True(t, outcome.Auto.Approved)
	assert.Same(t, &after, auto.considered)
	assert.Equal(t, testAuthor, auto.author)
	assert.Same(t, &after, store.updated, "the live row carries the edit")
	assert.Empty(t, store.draftFor, "nothing waits for a reviewer")
}

// TestApplyEdit_DefersAnEditAutomaticApprovalWouldRefuse is the other half of
// deciding first: a refusal leaves exactly the state the funnel had before
// automatic approval existed, which is a draft in the review queue.
func TestApplyEdit_DefersAnEditAutomaticApprovalWouldRefuse(t *testing.T) {
	auto := &stubApprover{decision: script.AutoDecision{Reason: "it writes to acme-drop"}}
	store := &versioningStore{}
	before := approved()
	after := approved()
	after.Source = "x = 2"

	outcome, err := script.ApplyEdit(context.Background(), store, script.Edit{
		Before: before, After: after, Author: testAuthor, Auto: auto,
	})
	require.NoError(t, err)
	assert.Equal(t, 7, outcome.PendingVersion)
	assert.False(t, outcome.Auto.Approved)
	assert.Equal(t, "it writes to acme-drop", outcome.Auto.Reason,
		"the owner is told why their edit is waiting on somebody")
	assert.Nil(t, store.updated, "the live row must not move under its approval")
}

// TestApplyEdit_ApprovesAnEditNoReviewGateApplied covers the script with
// nothing approved yet: the edit was never gated, and approving it here is what
// makes it start running.
func TestApplyEdit_ApprovesAnEditNoReviewGateApplied(t *testing.T) {
	auto := &stubApprover{decision: script.AutoDecision{Approvable: true}}
	before := personal()
	before.Version = 2
	after := *before
	after.Source = "x = 2"

	outcome, err := script.ApplyEdit(context.Background(), &versioningStore{}, script.Edit{
		Before: before, After: &after, Author: testAuthor, Auto: auto,
	})
	require.NoError(t, err)
	assert.True(t, outcome.Auto.Approved)
	assert.Equal(t, 3, auto.version,
		"the version this edit produced is the one approved, and it is the one the live row now carries")
}

// TestApplyEdit_ApprovesOnlyTheVersionTheEditProduced is what keeps automatic
// approval off a version somebody else wrote. An edit that moves only fields a
// snapshot cannot carry produces no version, and the one the live row already
// holds was written at some other time by some other person — approving it would
// bind THEIR roles.
func TestApplyEdit_ApprovesOnlyTheVersionTheEditProduced(t *testing.T) {
	auto := &stubApprover{decision: script.AutoDecision{Approvable: true}}
	before := personal()
	before.Version = 4
	after := *before
	after.Enabled = !before.Enabled

	outcome, err := script.ApplyEdit(context.Background(), &versioningStore{stored: before}, script.Edit{
		Before: before, After: &after, Author: testAuthor, Auto: auto,
	})
	require.NoError(t, err)
	assert.True(t, outcome.Applied)
	assert.False(t, outcome.Auto.Approved)
	assert.Nil(t, auto.approved, "no version was produced, so none was approved")
}

// TestApplyEdit_TellsTheStoreWhetherReviewWasWaived pins the other half of that
// decision reaching the store: the write is gated by the funnel's ACTUAL
// verdict, so an edit it would defer can never land on the live row through a
// race the store would otherwise have refused.
func TestApplyEdit_TellsTheStoreWhetherReviewWasWaived(t *testing.T) {
	store := &versioningStore{}
	before := personal()
	after := *before
	after.Source = "x = 2"

	_, err := script.ApplyEdit(context.Background(), store, script.Edit{
		Before: before, After: &after, Author: testAuthor,
		Auto: &stubApprover{decision: script.AutoDecision{Approvable: true}},
	})
	require.NoError(t, err)
	assert.True(t, store.ungated)

	store = &versioningStore{}
	_, err = script.ApplyEdit(context.Background(), store, script.Edit{
		Before: before, After: &after, Author: testAuthor,
	})
	require.NoError(t, err)
	assert.False(t, store.ungated, "an edit nothing would approve is written under the gate")
}

// TestApplyEdit_WithoutAnApproverNothingIsApproved covers the deployment where
// every version still waits for a reviewer.
func TestApplyEdit_WithoutAnApproverNothingIsApproved(t *testing.T) {
	before := personal()
	after := *before
	after.Source = "x = 2"

	outcome, err := script.ApplyEdit(context.Background(), &versioningStore{}, script.Edit{
		Before: before, After: &after, Author: testAuthor,
	})
	require.NoError(t, err)
	assert.True(t, outcome.Applied)
	assert.False(t, outcome.Auto.Approved)
	assert.Empty(t, outcome.Auto.Reason)
}
