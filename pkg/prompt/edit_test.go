package prompt

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// approvedGlobal returns an approved global prompt as the pre-edit state.
func approvedGlobal() *Prompt {
	return &Prompt{
		ID: "p1", Name: "report", DisplayName: "Report", Description: "d",
		Content: "body", Arguments: []Argument{{Name: "topic", Required: true}},
		Tags: []string{"a"}, Category: "analysis",
		Scope: ScopeGlobal, Status: StatusApproved, Enabled: true,
	}
}

func TestRequiresReview(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(before, after *Prompt)
		want   bool
	}{
		{"content change on approved global", func(_, a *Prompt) { a.Content = "new" }, true},
		{"arguments change on approved global", func(_, a *Prompt) { a.Arguments = nil }, true},
		{"metadata-only change on approved global", func(_, a *Prompt) { a.Tags = []string{"b"}; a.Description = "x" }, false},
		{"content change on draft global", func(b, a *Prompt) { b.Status = StatusDraft; a.Status = StatusDraft; a.Content = "new" }, false},
		{"content change on approved personal", func(b, a *Prompt) { b.Scope = ScopePersonal; a.Scope = ScopePersonal; a.Content = "new" }, false},
		{"content change on approved persona prompt", func(b, a *Prompt) { b.Scope = ScopePersona; a.Scope = ScopePersona; a.Content = "new" }, true},
		{"content change on deprecated global", func(b, a *Prompt) { b.Status = StatusDeprecated; a.Status = StatusDeprecated; a.Content = "new" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, after := approvedGlobal(), approvedGlobal()
			tt.mutate(before, after)
			assert.Equal(t, tt.want, RequiresReview(before, after))
		})
	}
}

func TestSnapshotChanged(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Prompt)
		want   bool
	}{
		{"identical", func(*Prompt) {}, false},
		{"content", func(p *Prompt) { p.Content = "x" }, true},
		{"display name", func(p *Prompt) { p.DisplayName = "x" }, true},
		{"description", func(p *Prompt) { p.Description = "x" }, true},
		{"arguments", func(p *Prompt) { p.Arguments = append(p.Arguments, Argument{Name: "n"}) }, true},
		{"tags", func(p *Prompt) { p.Tags = []string{"b"} }, true},
		{"category is not snapshotted", func(p *Prompt) { p.Category = "x" }, false},
		{"status is not snapshotted", func(p *Prompt) { p.Status = StatusDeprecated }, false},
		{"nil versus empty slices are equal", func(p *Prompt) { p.Arguments = nil; p.Tags = nil }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, after := approvedGlobal(), approvedGlobal()
			if tt.name == "nil versus empty slices are equal" {
				before.Arguments = []Argument{}
				before.Tags = []string{}
			}
			tt.mutate(after)
			assert.Equal(t, tt.want, SnapshotChanged(before, after))
		})
	}
}

// --- ApplyEdit fakes ---

// plainStore implements Store only (no versioning capability).
type plainStore struct {
	updated *Prompt
	err     error
}

func (*plainStore) Create(context.Context, *Prompt) error { return nil }
func (*plainStore) Get(context.Context, string) (*Prompt, error) {
	return nil, nil //nolint:nilnil // interface contract
}

func (*plainStore) GetPersonal(context.Context, string, string) (*Prompt, error) {
	return nil, nil //nolint:nilnil // interface contract
}

func (*plainStore) GetByID(context.Context, string) (*Prompt, error) {
	return nil, nil //nolint:nilnil // interface contract
}

func (*plainStore) ListPersonalByName(context.Context, string) ([]Prompt, error) {
	return nil, nil
}

func (s *plainStore) Update(_ context.Context, p *Prompt) error {
	if s.err != nil {
		return s.err
	}
	s.updated = p
	return nil
}
func (*plainStore) Delete(context.Context, string) error               { return nil }
func (*plainStore) DeleteByID(context.Context, string) error           { return nil }
func (*plainStore) List(context.Context, ListFilter) ([]Prompt, error) { return nil, nil }
func (*plainStore) Count(context.Context, ListFilter) (int, error)     { return 0, nil }

// versionedStore adds a recording VersionStore capability to plainStore.
type versionedStore struct {
	plainStore
	versioned *Prompt
	draft     *Prompt
	draftN    int
	draftErr  error
	updateErr error
}

func (s *versionedStore) UpdateWithVersion(_ context.Context, p *Prompt, _ string) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.versioned = p
	return nil
}

func (s *versionedStore) CreateDraftVersion(_ context.Context, _ string, proposed *Prompt, _ string) (int, error) {
	if s.draftErr != nil {
		return 0, s.draftErr
	}
	s.draft = proposed
	s.draftN = 5
	return s.draftN, nil
}

func (*versionedStore) ListVersions(context.Context, string) ([]Version, error) { return nil, nil }
func (*versionedStore) GetVersion(context.Context, string, int) (*Version, error) {
	return nil, nil //nolint:nilnil // interface contract
}

func (*versionedStore) ApproveVersion(context.Context, string, int, string) (*Prompt, error) {
	return nil, nil //nolint:nilnil // unused in these tests
}
func (*versionedStore) RejectVersion(context.Context, string, int) error { return nil }

func TestApplyEdit_NoVersionCapabilityFallsBackToPlainUpdate(t *testing.T) {
	store := &plainStore{}
	before, after := approvedGlobal(), approvedGlobal()
	after.Content = "new"

	out, err := ApplyEdit(context.Background(), store, before, after, "jane@example.com")
	require.NoError(t, err)
	assert.True(t, out.Applied)
	assert.Same(t, after, store.updated, "plain update applied even for a gated edit: no versioning capability")
}

func TestApplyEdit_ReviewGatedEditBecomesDraft(t *testing.T) {
	store := &versionedStore{}
	before, after := approvedGlobal(), approvedGlobal()
	after.Content = "new body"

	out, err := ApplyEdit(context.Background(), store, before, after, "jane@example.com")
	require.NoError(t, err)
	assert.False(t, out.Applied)
	assert.Equal(t, 5, out.PendingVersion)
	assert.Same(t, after, store.draft, "the proposed state is snapshotted as the draft")
	assert.Nil(t, store.versioned, "the live row is not updated")
	assert.Nil(t, store.updated)
}

func TestApplyEdit_MixedGatedEditRejected(t *testing.T) {
	store := &versionedStore{}
	tests := []struct {
		name   string
		mutate func(*Prompt)
	}{
		{"scope change", func(p *Prompt) { p.Scope = ScopePersona }},
		{"status change", func(p *Prompt) { p.Status = StatusDeprecated }},
		{"category change", func(p *Prompt) { p.Category = "other" }},
		{"collection change", func(p *Prompt) { p.CollectionID = "col-1" }},
		{"rename", func(p *Prompt) { p.Name = "renamed" }},
		{"personas change", func(p *Prompt) { p.Personas = []string{"analyst"} }},
		{"enabled change", func(p *Prompt) { p.Enabled = false }},
		{"promotion request", func(p *Prompt) { p.ReviewRequested = true; p.RequestedScope = ScopeGlobal }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, after := approvedGlobal(), approvedGlobal()
			after.Content = "new body"
			tt.mutate(after)

			_, err := ApplyEdit(context.Background(), store, before, after, "jane@example.com")
			require.ErrorIs(t, err, ErrReviewRequiredMixedEdit)
			assert.Nil(t, store.draft, "no draft is created for a rejected mixed edit")
		})
	}
}

func TestApplyEdit_UngatedEditAppliesWithVersion(t *testing.T) {
	store := &versionedStore{}
	before, after := approvedGlobal(), approvedGlobal()
	after.Tags = []string{"b"}
	after.Status = StatusDeprecated

	out, err := ApplyEdit(context.Background(), store, before, after, "jane@example.com")
	require.NoError(t, err)
	assert.True(t, out.Applied)
	assert.Same(t, after, store.versioned, "metadata edits apply through the versioned update")
	assert.Nil(t, store.draft)
}

func TestApplyEdit_ErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")

	_, err := ApplyEdit(context.Background(), &plainStore{err: boom},
		approvedGlobal(), approvedGlobal(), "a@example.com")
	assert.ErrorIs(t, err, boom)

	gated := approvedGlobal()
	gated.Content = "new"
	_, err = ApplyEdit(context.Background(), &versionedStore{draftErr: boom},
		approvedGlobal(), gated, "a@example.com")
	assert.ErrorIs(t, err, boom)

	_, err = ApplyEdit(context.Background(), &versionedStore{updateErr: boom},
		approvedGlobal(), approvedGlobal(), "a@example.com")
	assert.ErrorIs(t, err, boom)
}
