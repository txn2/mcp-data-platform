package scriptauto_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/connreach"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptauto"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// owner is the person every script in this file belongs to and every version is
// written by, which is the only shape automatic approval acts on.
var owner = script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}

// approvalStore records what was bound and answers with the version the write
// would have produced.
type approvalStore struct {
	calls   int
	scriptI string
	version int
	by      string
	grants  script.Grants
	err     error
}

func (s *approvalStore) AutoApproveVersion(
	_ context.Context, scriptID string, version int, ownerEmail string, grants script.Grants,
) (*script.Version, error) {
	s.calls++
	s.scriptI, s.version, s.by, s.grants = scriptID, version, ownerEmail, grants
	if s.err != nil {
		return nil, s.err
	}
	return &script.Version{ID: "sver_9", Version: version, AutoApproved: true, Grants: grants}, nil
}

// versionStore answers the read of a script's currently approved version.
type versionStore struct {
	approved *script.Version
	err      error
}

func (*versionStore) UpdateWithVersion(context.Context, *script.Script, script.Author, bool) error {
	return nil
}

func (*versionStore) CreateDraftVersion(context.Context, string, *script.Script, script.Author) (int, error) {
	return 0, nil
}

func (*versionStore) ListVersions(context.Context, string) ([]script.Version, error) {
	return nil, nil
}

func (*versionStore) GetVersion(context.Context, string, int) (*script.Version, error) {
	return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
}

func (v *versionStore) GetVersionByID(context.Context, string) (*script.Version, error) {
	return v.approved, v.err
}

// personal returns an owner-authored personal script with nothing approved.
func personal(source string) *script.Script {
	return &script.Script{
		ID: "script_1", Name: "daily", Scope: script.ScopePersonal,
		OwnerEmail: owner.Email, Source: source, Status: script.StatusDraft, Version: 3,
	}
}

// querySource reaches one capability and one connection, and writes nowhere.
const querySource = `rows = platform.query(connection="warehouse", sql="SELECT 1")`

// TestAutoApprove_ApprovesTheOwnersOwnScriptWithWhatItsCodeReaches is the whole
// feature: a personal script becomes executable on save, under a grant nobody
// had to be asked for and that covers exactly what the source calls.
func TestAutoApprove_ApprovesTheOwnersOwnScriptWithWhatItsCodeReaches(t *testing.T) {
	approvals := &approvalStore{}
	a := scriptauto.New(scriptauto.Deps{Approvals: approvals, Versions: &versionStore{}})
	sc := personal(querySource)

	out := a.AutoApprove(context.Background(), sc, sc.Version, owner)

	require.True(t, out.Approved, out.Reason)
	assert.Equal(t, "script_1", approvals.scriptI)
	assert.Equal(t, 3, approvals.version)
	assert.Equal(t, owner.Email, approvals.by)
	assert.Equal(t, []string{script.CapabilityQuery}, approvals.grants.Capabilities)
	assert.Equal(t, []string{"warehouse"}, approvals.grants.Connections)
	assert.Empty(t, approvals.grants.Destinations, "a script that writes nowhere is granted nowhere")
	assert.Equal(t, "sver_9", sc.ApprovedVersionID,
		"the caller's script is advanced to what the write left behind")
	assert.Equal(t, script.StatusActive, sc.Status)
}

// TestAutoApprove_GrantsThePortalToAnExportThatNamesNoDestination pins the
// default a run actually applies: an export naming no destination writes to the
// portal, so the derived grant has to carry it.
func TestAutoApprove_GrantsThePortalToAnExportThatNamesNoDestination(t *testing.T) {
	approvals := &approvalStore{}
	a := scriptauto.New(scriptauto.Deps{Approvals: approvals, Versions: &versionStore{}})

	out := a.AutoApprove(context.Background(), personal(
		`platform.export("sales", [], format="csv")`), 3, owner)

	require.True(t, out.Approved, out.Reason)
	assert.Equal(t, []script.Destination{script.PortalDestination()}, approvals.grants.Destinations)
}

// TestAutoApprove_LeavesASharedOrSomebodyElsesEditToReview keeps the review
// path exactly where it was for everything this feature does not cover.
func TestAutoApprove_LeavesASharedOrSomebodyElsesEditToReview(t *testing.T) {
	approvals := &approvalStore{}
	a := scriptauto.New(scriptauto.Deps{Approvals: approvals, Versions: &versionStore{}})

	shared := personal(querySource)
	shared.Scope = script.ScopeGlobal
	out := a.AutoApprove(context.Background(), shared, shared.Version, owner)
	assert.False(t, out.Approved)
	assert.Empty(t, out.Reason, "the ordinary review path is not a refusal to explain")

	admin := script.Author{Email: "admin@example.com", Roles: []string{"admin"}}
	out = a.AutoApprove(context.Background(), personal(querySource), 3, admin)
	assert.False(t, out.Approved)
	assert.Zero(t, approvals.calls)
}

// TestAutoApprove_SendsToReviewWhatItCannotReadOffTheSource covers the three
// refusals an owner is shown, each naming what to do about it.
func TestAutoApprove_SendsToReviewWhatItCannotReadOffTheSource(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			"a computed connection",
			"name = \"ware\" + \"house\"\nrows = platform.query(connection=name, sql=\"SELECT 1\")",
			"computes a connection or a destination",
		},
		{
			"a bucket nobody pinned",
			`platform.export("sales", [], format="csv", destination="acme-drop")`,
			"acme-drop",
		},
		{
			"source that does not parse",
			"import os",
			"does not parse",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			approvals := &approvalStore{}
			a := scriptauto.New(scriptauto.Deps{Approvals: approvals, Versions: &versionStore{}})

			out := a.AutoApprove(context.Background(), personal(tt.source), 3, owner)

			assert.False(t, out.Approved)
			assert.Contains(t, out.Reason, tt.want)
			assert.Zero(t, approvals.calls, "nothing is approved on a grant it could not derive")
		})
	}
}

// TestAutoApprove_ReusesTheAddressAPersonPinned is how a personal script keeps
// delivering to a bucket: the first delivery is reviewed, and the owner's later
// edits are approved against the address that review pinned.
func TestAutoApprove_ReusesTheAddressAPersonPinned(t *testing.T) {
	pinned := script.Destination{
		Name: "acme-drop", Kind: script.DestinationKindS3,
		Connection: "acme-s3", Bucket: "acme-exports", Prefix: "weekly",
	}
	approvals := &approvalStore{}
	a := scriptauto.New(scriptauto.Deps{
		Approvals: approvals,
		Versions: &versionStore{approved: &script.Version{
			ID: "sver_1", Version: 2,
			Grants: script.Grants{Destinations: []script.Destination{pinned}},
		}},
	})
	sc := personal(`platform.export("sales", [], format="csv", destination="acme-drop")`)
	sc.ApprovedVersionID = "sver_1"

	out := a.AutoApprove(context.Background(), sc, sc.Version, owner)

	require.True(t, out.Approved, out.Reason)
	assert.Equal(t, []script.Destination{pinned}, approvals.grants.Destinations)
}

// TestAutoApprove_RefusesAConnectionTheAuthorCannotReach answers at save what
// the middleware would otherwise answer at the first fire, with nobody watching.
func TestAutoApprove_RefusesAConnectionTheAuthorCannotReach(t *testing.T) {
	approvals := &approvalStore{}
	a := scriptauto.New(scriptauto.Deps{
		Approvals: approvals, Versions: &versionStore{},
		Reach: func(context.Context, []string) []connreach.Connection {
			return []connreach.Connection{{Kind: script.ConnectionParamKind, Name: "finance"}}
		},
	})

	out := a.AutoApprove(context.Background(), personal(querySource), 3, owner)

	assert.False(t, out.Approved)
	assert.Contains(t, out.Reason, "warehouse")
	assert.Zero(t, approvals.calls)
}

// TestAutoApprove_RefusesAVersionSomebodyElseWrote is the invariant the whole
// grant model rests on, enforced where the roles are read rather than only where
// the request is made: a version carries ITS author's roles, so approving one
// its owner did not write would put a personal script on authority its owner
// never held.
func TestAutoApprove_RefusesAVersionSomebodyElseWrote(t *testing.T) {
	approvals := &approvalStore{err: errors.New("written by somebody else")}
	a := scriptauto.New(scriptauto.Deps{Approvals: approvals, Versions: &versionStore{}})

	out := a.AutoApprove(context.Background(), personal(querySource), 3, owner)

	assert.False(t, out.Approved)
	assert.Equal(t, 1, approvals.calls,
		"the store is asked, and the store is what refuses")
}

// TestAutoApprove_ReportsAFailedWriteAsUnapprovedRatherThanAsAFailedSave keeps
// the edit that already landed from being reported as lost.
func TestAutoApprove_ReportsAFailedWriteAsUnapprovedRatherThanAsAFailedSave(t *testing.T) {
	a := scriptauto.New(scriptauto.Deps{
		Approvals: &approvalStore{err: errors.New("boom")}, Versions: &versionStore{},
	})
	sc := personal(querySource)

	out := a.AutoApprove(context.Background(), sc, sc.Version, owner)

	assert.False(t, out.Approved)
	assert.Contains(t, out.Reason, "ask an administrator")
	assert.Empty(t, sc.ApprovedVersionID)
}

// TestAutoApprove_AnUnreadableApprovedVersionLeavesABucketUnresolvable is the
// safe direction: the alternative is minting an address nothing verified.
func TestAutoApprove_AnUnreadableApprovedVersionLeavesABucketUnresolvable(t *testing.T) {
	approvals := &approvalStore{}
	a := scriptauto.New(scriptauto.Deps{
		Approvals: approvals, Versions: &versionStore{err: errors.New("boom")},
	})
	sc := personal(`platform.export("sales", [], format="csv", destination="acme-drop")`)
	sc.ApprovedVersionID = "sver_1"

	out := a.AutoApprove(context.Background(), sc, sc.Version, owner)

	assert.False(t, out.Approved)
	assert.Contains(t, out.Reason, "acme-drop")
}

// TestAutoApprove_IsInertWithoutAStore covers the deployment that cannot approve
// and the nil approver the edit funnel tolerates.
func TestAutoApprove_IsInertWithoutAStore(t *testing.T) {
	out := scriptauto.New(scriptauto.Deps{}).AutoApprove(context.Background(), personal(querySource), 3, owner)
	assert.False(t, out.Approved)
	assert.Empty(t, out.Reason)

	var nilApprover *scriptauto.Approver
	assert.False(t, nilApprover.AutoApprove(context.Background(), personal(querySource), 3, owner).Approved)
}
