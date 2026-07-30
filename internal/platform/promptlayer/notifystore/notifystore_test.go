package notifystore

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// fakeStore is a minimal in-memory prompt.Store for exercising the wrapper.
type fakeStore struct {
	prompts  map[string]*prompt.Prompt
	writeErr error
}

func newFakeStore() *fakeStore {
	return &fakeStore{prompts: make(map[string]*prompt.Prompt)}
}

func (f *fakeStore) Create(_ context.Context, p *prompt.Prompt) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.prompts[p.Name] = p
	return nil
}

func (f *fakeStore) Get(_ context.Context, name string) (*prompt.Prompt, error) {
	return f.prompts[name], nil
}

func (*fakeStore) GetPersonal(_ context.Context, _, _ string) (*prompt.Prompt, error) {
	return nil, nil //nolint:nilnil // store contract: nil, nil means not found
}

func (*fakeStore) ListPersonalByName(_ context.Context, _ string) ([]prompt.Prompt, error) {
	return nil, nil
}

func (*fakeStore) GetByID(_ context.Context, _ string) (*prompt.Prompt, error) {
	return nil, nil //nolint:nilnil // store contract: nil, nil means not found
}

func (f *fakeStore) Update(_ context.Context, p *prompt.Prompt) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	f.prompts[p.Name] = p
	return nil
}

func (f *fakeStore) Delete(_ context.Context, name string) error {
	if f.writeErr != nil {
		return f.writeErr
	}
	delete(f.prompts, name)
	return nil
}

func (f *fakeStore) DeleteByID(_ context.Context, _ string) error {
	return f.writeErr
}

func (*fakeStore) List(_ context.Context, _ prompt.ListFilter) ([]prompt.Prompt, error) {
	return nil, nil
}

func (f *fakeStore) Count(_ context.Context, _ prompt.ListFilter) (int, error) {
	return len(f.prompts), nil
}

var _ prompt.Store = (*fakeStore)(nil)

// fakeSearchStore adds the search capability.
type fakeSearchStore struct {
	*fakeStore
	result []prompt.ScoredPrompt
	err    error
}

func (f *fakeSearchStore) Search(_ context.Context, _ prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	return f.result, f.err
}

// fakeVersionStore adds the versioning capability.
type fakeVersionStore struct {
	*fakeStore
	versionErr error
	drafts     int
	approvals  int
}

func (f *fakeVersionStore) UpdateWithVersion(ctx context.Context, p *prompt.Prompt, _ string) error {
	if f.versionErr != nil {
		return f.versionErr
	}
	return f.Update(ctx, p)
}

func (f *fakeVersionStore) CreateDraftVersion(_ context.Context, _ string, _ *prompt.Prompt, _ string) (int, error) {
	if f.versionErr != nil {
		return 0, f.versionErr
	}
	f.drafts++
	return f.drafts + 1, nil
}

func (f *fakeVersionStore) ApproveVersion(_ context.Context, _ string, _ int, _ string) (*prompt.Prompt, error) {
	if f.versionErr != nil {
		return nil, f.versionErr
	}
	f.approvals++
	return &prompt.Prompt{}, nil
}

func (f *fakeVersionStore) RejectVersion(_ context.Context, _ string, _ int) error {
	return f.versionErr
}

func (*fakeVersionStore) ListVersions(_ context.Context, _ string) ([]prompt.Version, error) {
	return nil, nil
}

func (*fakeVersionStore) GetVersion(_ context.Context, _ string, _ int) (*prompt.Version, error) {
	return nil, nil //nolint:nilnil // store contract: nil, nil means not found
}

var _ prompt.VersionStore = (*fakeVersionStore)(nil)

func TestWrap_NotifiesOnSuccessfulWritesOnly(t *testing.T) {
	base := newFakeStore()
	notified := 0
	s := Wrap(base, func() { notified++ }, nil)
	ctx := context.Background()

	require.NoError(t, s.Create(ctx, &prompt.Prompt{Name: "p"}))
	require.NoError(t, s.Update(ctx, &prompt.Prompt{Name: "p"}))
	require.NoError(t, s.Delete(ctx, "p"))
	require.NoError(t, s.DeleteByID(ctx, "x"))
	assert.Equal(t, 4, notified)

	base.writeErr = errors.New("db down")
	assert.ErrorIs(t, s.Create(ctx, &prompt.Prompt{Name: "q"}), base.writeErr)
	assert.ErrorIs(t, s.Update(ctx, &prompt.Prompt{Name: "q"}), base.writeErr)
	assert.ErrorIs(t, s.Delete(ctx, "q"), base.writeErr)
	assert.ErrorIs(t, s.DeleteByID(ctx, "q"), base.writeErr)
	assert.Equal(t, 4, notified, "failed writes must not notify")
}

func TestWrap_GuardRefusesUpdateBeforePersisting(t *testing.T) {
	base := newFakeStore()
	base.prompts["p"] = &prompt.Prompt{Name: "p", Content: "old"}
	guardErr := errors.New("attachment out of scope")
	notified := 0
	s := Wrap(base, func() { notified++ }, func(_ context.Context, _ *prompt.Prompt) error { return guardErr })

	err := s.Update(context.Background(), &prompt.Prompt{Name: "p", Content: "new"})
	assert.ErrorIs(t, err, guardErr)
	assert.Equal(t, "old", base.prompts["p"].Content, "a refused write must not persist")
	assert.Zero(t, notified)
}

func TestWrap_SearchDelegationAndCapabilityMatrix(t *testing.T) {
	search := &fakeSearchStore{
		fakeStore: newFakeStore(),
		result:    []prompt.ScoredPrompt{{Prompt: prompt.Prompt{Name: "hit"}, Score: 1}},
	}
	wrapped := Wrap(search, func() {}, nil)
	searcher, ok := wrapped.(prompt.Searcher)
	require.True(t, ok, "search capability must survive wrapping")
	got, err := searcher.Search(context.Background(), prompt.SearchQuery{QueryText: "x"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "hit", got[0].Prompt.Name)
	_, hasVersions := wrapped.(prompt.VersionStore)
	assert.False(t, hasVersions, "a search-only base must not gain versioning")

	plain := Wrap(newFakeStore(), func() {}, nil)
	_, ok = plain.(prompt.Searcher)
	assert.False(t, ok, "a plain base must not gain search")
	_, ok = plain.(prompt.VersionStore)
	assert.False(t, ok, "a plain base must not gain versioning")
}

func TestWrap_VersionWritesNotifyAndGuard(t *testing.T) {
	base := &fakeVersionStore{fakeStore: newFakeStore()}
	notified := 0
	vs, ok := Wrap(base, func() { notified++ }, nil).(prompt.VersionStore)
	require.True(t, ok, "version capability must survive wrapping")
	ctx := context.Background()

	_, err := vs.CreateDraftVersion(ctx, "p1", &prompt.Prompt{Name: "p"}, "a@x")
	require.NoError(t, err)
	assert.Zero(t, notified, "a draft changes nothing served: no notification")

	require.NoError(t, vs.UpdateWithVersion(ctx, &prompt.Prompt{Name: "p"}, "a@x"))
	_, err = vs.ApproveVersion(ctx, "p1", 2, "admin@x")
	require.NoError(t, err)
	assert.Equal(t, 2, notified)

	guardErr := errors.New("attachment out of scope")
	guarded, _ := Wrap(base, func() { notified++ }, func(_ context.Context, _ *prompt.Prompt) error { return guardErr }).(prompt.VersionStore)
	assert.ErrorIs(t, guarded.UpdateWithVersion(ctx, &prompt.Prompt{Name: "p"}, "a@x"), guardErr)

	base.versionErr = errors.New("db down")
	assert.ErrorIs(t, vs.UpdateWithVersion(ctx, &prompt.Prompt{Name: "p"}, "a@x"), base.versionErr)
	_, err = vs.ApproveVersion(ctx, "p1", 2, "admin@x")
	assert.ErrorIs(t, err, base.versionErr)
	assert.Equal(t, 2, notified, "failed version writes must not notify")
}

func TestWrap_CapabilityAccessors(t *testing.T) {
	// A base without the collection/attachment capabilities resolves to nil
	// through the provider accessors rather than fabricating them.
	s := Wrap(newFakeStore(), func() {}, nil)
	assert.Nil(t, prompt.AsCollectionStore(s))
	assert.Nil(t, prompt.AsAttachmentStore(s))
}
