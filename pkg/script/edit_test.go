package script_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// plainStore implements script.Store and nothing else, standing in for a
// deployment whose store has no versioning capability.
// testAuthor is the author every edit in this file is attributed to, carrying
// roles because a version's roles are the ceiling on what approving it grants.
var testAuthor = script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}

type plainStore struct {
	updated *script.Script
	failErr error
}

func (*plainStore) Create(context.Context, *script.Script, script.Author) error { return nil }
func (*plainStore) Get(context.Context, string) (*script.Script, error) {
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (*plainStore) GetPersonal(context.Context, string, string) (*script.Script, error) {
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (*plainStore) GetByID(context.Context, string) (*script.Script, error) {
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (s *plainStore) Update(_ context.Context, sc *script.Script) error {
	if s.failErr != nil {
		return s.failErr
	}
	s.updated = sc
	return nil
}
func (*plainStore) Delete(context.Context, string) error { return nil }
func (*plainStore) List(context.Context, script.ListFilter) ([]script.Script, error) {
	return nil, nil
}

// versioningStore adds the versioning capability, recording which branch of the
// funnel a call took.
type versioningStore struct {
	plainStore
	appliedAuthor script.Author
	draftAuthor   script.Author
	draftFor      string
	draftErr      error
	updateErr     error
}

func (s *versioningStore) UpdateWithVersion(_ context.Context, sc *script.Script, author script.Author) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.appliedAuthor = author
	s.updated = sc
	return nil
}

func (s *versioningStore) CreateDraftVersion(_ context.Context, scriptID string, _ *script.Script, author script.Author) (int, error) {
	if s.draftErr != nil {
		return 0, s.draftErr
	}
	s.draftAuthor, s.draftFor = author, scriptID
	return 7, nil
}

func (*versioningStore) ListVersions(context.Context, string) ([]script.Version, error) {
	return nil, nil
}

func (*versioningStore) GetVersion(context.Context, string, int) (*script.Version, error) {
	return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
}

func (*versioningStore) GetVersionByID(context.Context, string) (*script.Version, error) {
	return nil, nil //nolint:nilnil // VersionStore contract: nil, nil means not found
}

// approved returns a script whose approved version is live, which is the state
// the review gate keys on.
func approved() *script.Script {
	return &script.Script{
		ID: "script_1", Name: "daily", Scope: script.ScopePersonal,
		Source: "x = 1", ApprovedVersionID: "sver_1", Status: script.StatusActive,
		Params: []script.Param{{Name: "day", Type: script.ParamTypeDate}},
	}
}

// TestRequiresReview_KeysOnTheExecutionPointerNotOnScope pins the deliberate
// divergence from prompts: a PERSONAL script with an approved version is still
// gated, because the platform executes it.
func TestRequiresReview_KeysOnTheExecutionPointerNotOnScope(t *testing.T) {
	before := approved()
	after := approved()
	after.Source = "x = 2"
	assert.True(t, script.RequiresReview(before, after),
		"a personal script with an approved version must still be gated")

	unapproved := approved()
	unapproved.ApprovedVersionID = ""
	changed := *unapproved
	changed.Source = "x = 2"
	assert.False(t, script.RequiresReview(unapproved, &changed),
		"with nothing approved there is no approval to protect")

	// Non-substance edits are not gated even on an approved script.
	cosmetic := approved()
	cosmetic.DisplayName = "Daily"
	assert.False(t, script.RequiresReview(before, cosmetic))

	// The parameter contract is substance: it is what a caller and a schedule
	// bind against.
	params := approved()
	params.Params = []script.Param{{Name: "day", Type: script.ParamTypeString}}
	assert.True(t, script.RequiresReview(before, params))
}

func TestSnapshotChanged(t *testing.T) {
	base := approved()
	assert.False(t, script.SnapshotChanged(base, approved()))

	for _, mutate := range []func(*script.Script){
		func(s *script.Script) { s.Source = "x = 2" },
		func(s *script.Script) { s.DisplayName = "d" },
		func(s *script.Script) { s.Description = "d" },
		func(s *script.Script) { s.Params = nil },
		func(s *script.Script) { s.Tags = []string{"t"} },
	} {
		after := approved()
		mutate(after)
		assert.True(t, script.SnapshotChanged(base, after))
	}

	// A field a snapshot cannot carry is not a snapshot change.
	scoped := approved()
	scoped.Scope = script.ScopeGlobal
	assert.False(t, script.SnapshotChanged(base, scoped))
}

func TestApplyEdit_UngatedEditAppliesWithAVersion(t *testing.T) {
	store := &versioningStore{}
	before := approved()
	before.ApprovedVersionID = ""
	after := *before
	after.Source = "x = 2"

	outcome, err := script.ApplyEdit(context.Background(), store, before, &after, testAuthor)
	require.NoError(t, err)
	assert.True(t, outcome.Applied)
	assert.Zero(t, outcome.PendingVersion)
	assert.Equal(t, testAuthor, store.appliedAuthor,
		"the author and the roles they held are recorded on the applied version")
	assert.Empty(t, store.draftAuthor.Email)
}

func TestApplyEdit_GatedEditBecomesADraftAndLeavesTheLiveRow(t *testing.T) {
	store := &versioningStore{}
	before := approved()
	after := approved()
	after.Source = "x = 2"

	outcome, err := script.ApplyEdit(context.Background(), store, before, after, testAuthor)
	require.NoError(t, err)
	assert.False(t, outcome.Applied)
	assert.Equal(t, 7, outcome.PendingVersion)
	assert.Equal(t, "script_1", store.draftFor)
	assert.Nil(t, store.updated, "the live row must not be touched by a gated edit")
}

// TestApplyEdit_MixedEditRefused pins the funnel's refusal: a reviewer
// approving a code change must not also be silently approving a scope change.
func TestApplyEdit_MixedEditRefused(t *testing.T) {
	unversioned := []struct {
		name   string
		mutate func(*script.Script)
	}{
		{"scope", func(s *script.Script) { s.Scope = script.ScopeGlobal }},
		{"name", func(s *script.Script) { s.Name = "other" }},
		{"personas", func(s *script.Script) { s.Personas = []string{"analyst"} }},
		{"owner", func(s *script.Script) { s.OwnerEmail = "someone@example.com" }},
		{"enabled", func(s *script.Script) { s.Enabled = !s.Enabled }},
		{"status", func(s *script.Script) { s.Status = script.StatusDeprecated }},
		{"superseded_by", func(s *script.Script) { s.SupersededBy = "other" }},
		{"approved version", func(s *script.Script) { s.ApprovedVersionID = "sver_2" }},
	}
	for _, tc := range unversioned {
		t.Run(tc.name, func(t *testing.T) {
			store := &versioningStore{}
			before := approved()
			after := approved()
			after.Source = "x = 2"
			tc.mutate(after)

			_, err := script.ApplyEdit(context.Background(), store, before, after, testAuthor)
			require.ErrorIs(t, err, script.ErrReviewRequiredMixedEdit)
			assert.Empty(t, store.draftFor, "a refused edit must create nothing")
			assert.Nil(t, store.updated)
		})
	}
}

func TestApplyEdit_StoreWithoutVersioningDegradesToAPlainUpdate(t *testing.T) {
	store := &plainStore{}
	before := approved()
	after := approved()
	after.Source = "x = 2"

	outcome, err := script.ApplyEdit(context.Background(), store, before, after, testAuthor)
	require.NoError(t, err)
	assert.True(t, outcome.Applied)
	assert.Same(t, after, store.updated)
}

func TestApplyEdit_StoreErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")

	_, err := script.ApplyEdit(context.Background(), &plainStore{failErr: boom}, approved(), approved(), testAuthor)
	require.ErrorIs(t, err, boom)

	after := approved()
	after.Source = "x = 2"
	_, err = script.ApplyEdit(context.Background(), &versioningStore{draftErr: boom}, approved(), after, testAuthor)
	require.ErrorIs(t, err, boom)

	unapproved := approved()
	unapproved.ApprovedVersionID = ""
	_, err = script.ApplyEdit(context.Background(), &versioningStore{updateErr: boom}, unapproved, unapproved, testAuthor)
	require.ErrorIs(t, err, boom)
}
