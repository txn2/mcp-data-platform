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
// roles because a run of the version presents exactly those roles.
var testAuthor = script.Author{Email: "jane@example.com", Roles: []string{"analyst"}}

type plainStore struct {
	updated *script.Script
	failErr error
}

func (*plainStore) Create(context.Context, *script.Script, script.Author) error { return nil }
func (*plainStore) GetByName(context.Context, string, string) (*script.Script, error) {
	return nil, nil //nolint:nilnil // Store contract: nil, nil means not found
}

func (*plainStore) Transfer(context.Context, string, string, script.Author) error { return nil }

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

// versioningStore adds the versioning capability, recording what the funnel
// handed it.
type versioningStore struct {
	plainStore
	appliedAuthor script.Author
	updateErr     error
	// stored is the pre-edit row, when a test needs the fake to answer the real
	// store's question of whether the snapshot moved. Nil means it did, which is
	// what a source edit — the ordinary case here — does.
	stored *script.Script
}

// UpdateWithVersion models the real store: an edit that moved a versioned field
// snapshots a new version and advances sc.Version to it.
func (s *versioningStore) UpdateWithVersion(
	_ context.Context, sc *script.Script, author script.Author,
) error {
	if s.updateErr != nil {
		return s.updateErr
	}
	s.appliedAuthor = author
	if s.stored == nil || script.SnapshotChanged(s.stored, sc) {
		sc.Version++
	}
	s.updated = sc
	return nil
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

// inService returns a script in ordinary service.
func inService() *script.Script {
	return &script.Script{
		ID: "script_1", Name: "daily",
		Source: "x = 1", Status: script.StatusActive,
		Params: []script.Param{{Name: "day", Type: script.ParamTypeDate}},
	}
}

func TestSnapshotChanged(t *testing.T) {
	base := inService()
	assert.False(t, script.SnapshotChanged(base, inService()))

	for _, mutate := range []func(*script.Script){
		func(s *script.Script) { s.Source = "x = 2" },
		func(s *script.Script) { s.DisplayName = "d" },
		func(s *script.Script) { s.Description = "d" },
		func(s *script.Script) { s.Params = nil },
		func(s *script.Script) { s.Tags = []string{"t"} },
	} {
		after := inService()
		mutate(after)
		assert.True(t, script.SnapshotChanged(base, after))
	}

	// A field a snapshot cannot carry is not a snapshot change.
	moved := inService()
	moved.OwnerEmail = "someone@example.com"
	assert.False(t, script.SnapshotChanged(base, moved))
}

// TestApplyEdit_EveryEditAppliesWithAVersion pins the funnel's one path: an
// edit lands on the live row, is captured as a version, and the version
// records who wrote it and the roles they held — which is what a run of it
// presents.
func TestApplyEdit_EveryEditAppliesWithAVersion(t *testing.T) {
	store := &versioningStore{}
	before := inService()
	after := *before
	after.Source = "x = 2"

	err := script.ApplyEdit(context.Background(), store, script.Edit{
		Before: before, After: &after, Author: testAuthor,
	})
	require.NoError(t, err)
	assert.Same(t, &after, store.updated, "the live row carries the edit")
	assert.Equal(t, testAuthor, store.appliedAuthor,
		"the author and the roles they held are recorded on the applied version")
	assert.Equal(t, before.Version+1, after.Version)
}

func TestApplyEdit_StoreWithoutVersioningDegradesToAPlainUpdate(t *testing.T) {
	store := &plainStore{}
	before := inService()
	after := inService()
	after.Source = "x = 2"

	err := script.ApplyEdit(context.Background(), store, script.Edit{
		Before: before, After: after, Author: testAuthor,
	})
	require.NoError(t, err)
	assert.Same(t, after, store.updated)
}

func TestApplyEdit_StoreErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")

	err := script.ApplyEdit(context.Background(), &plainStore{failErr: boom}, script.Edit{
		Before: inService(), After: inService(), Author: testAuthor,
	})
	require.ErrorIs(t, err, boom)

	after := inService()
	after.Source = "x = 2"
	err = script.ApplyEdit(context.Background(), &versioningStore{updateErr: boom}, script.Edit{
		Before: inService(), After: after, Author: testAuthor,
	})
	require.ErrorIs(t, err, boom)
}
