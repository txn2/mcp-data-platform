package promptlayer

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// multiOwnerStore is a slice-backed prompt store: unlike the name-keyed
// mockPromptStore it can hold two personal prompts with the same name owned by
// different users, which is exactly the #1042 condition. It overrides only the
// two personal lookups; every other method comes from the embedded mock (an
// empty map), so Get (shared) misses and resolution falls through as in
// production.
type multiOwnerStore struct {
	*mockPromptStore
	rows []prompt.Prompt
}

func (s *multiOwnerStore) GetPersonal(_ context.Context, owner, name string) (*prompt.Prompt, error) {
	for i := range s.rows {
		r := s.rows[i]
		if r.Scope == prompt.ScopePersonal && r.OwnerEmail == owner && r.Name == name {
			return &r, nil
		}
	}
	return nil, nil //nolint:nilnil // Store interface contract: nil, nil means not found
}

func (s *multiOwnerStore) ListPersonalByName(_ context.Context, name string) ([]prompt.Prompt, error) {
	var out []prompt.Prompt
	for _, r := range s.rows {
		if r.Scope == prompt.ScopePersonal && r.Name == name {
			out = append(out, r)
		}
	}
	return out, nil
}

// TestResolveManagedPrompt_AdminResolvesForeignPersonal is the core #1042
// regression: an admin resolves another user's personal prompt by name, matching
// the admin-wide visibility that list already shows. Before the fix the resolver
// tried only the caller's own personal prompt and the shared (non-personal) set,
// so it returned nil for a prompt the admin could see in list.
func TestResolveManagedPrompt_AdminResolvesForeignPersonal(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["example-prompt"] = &prompt.Prompt{
		ID: "p-a", Name: "example-prompt", Scope: prompt.ScopePersonal,
		OwnerEmail: "author@example.com", Content: "body", Enabled: true,
	}

	got, err := h.resolveManagedPrompt(adminCtx(), "example-prompt", "admin@example.com", "", "")
	require.NoError(t, err)
	require.NotNil(t, got, "admin resolves another user's personal prompt")
	assert.Equal(t, "p-a", got.ID)
}

// TestManagePrompt_AdminGetForeignPersonal drives the full get command: the admin
// gets a prompt they did not author and receives it, not "not found".
func TestManagePrompt_AdminGetForeignPersonal(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["example-prompt"] = &prompt.Prompt{
		ID: "p-a", Name: "example-prompt", Scope: prompt.ScopePersonal,
		OwnerEmail: "author@example.com", Content: "the body", Enabled: true,
	}

	r, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{
		Command: "get", Name: "example-prompt",
	})
	require.NoError(t, err)
	require.False(t, r.IsError, "admin get of a foreign personal prompt must succeed: %s", resultText(r))
	assert.Contains(t, resultText(r), "the body")
}

// TestResolveManagedPrompt_AdminOwnerDisambiguation: when two owners share a
// personal-prompt name, owner_email picks the intended one.
func TestResolveManagedPrompt_AdminOwnerDisambiguation(t *testing.T) {
	h, _ := newTestHandle()
	h.store = &multiOwnerStore{
		mockPromptStore: newMockPromptStore(),
		rows: []prompt.Prompt{
			{ID: "p-a", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "a@example.com", Content: "a"},
			{ID: "p-b", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "b@example.com", Content: "b"},
		},
	}

	got, err := h.resolveManagedPrompt(adminCtx(), "report", "admin@example.com", "", "b@example.com")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "p-b", got.ID, "owner_email targets that owner's personal prompt")
}

// TestManagePrompt_AdminAmbiguousForeignPersonal: without owner_email, an admin
// naming a personal prompt held by more than one owner gets an explicit
// ambiguity error that lists the owners, not a silent wrong pick.
func TestManagePrompt_AdminAmbiguousForeignPersonal(t *testing.T) {
	h, _ := newTestHandle()
	h.store = &multiOwnerStore{
		mockPromptStore: newMockPromptStore(),
		rows: []prompt.Prompt{
			{ID: "p-a", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "a@example.com", Content: "a"},
			{ID: "p-b", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "b@example.com", Content: "b"},
		},
	}

	r, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{Command: "get", Name: "report"})
	require.NoError(t, err)
	require.True(t, r.IsError)
	msg := resultText(r)
	assert.Contains(t, msg, "owner_email")
	assert.Contains(t, msg, "a@example.com")
	assert.Contains(t, msg, "b@example.com")
	assert.NotContains(t, msg, "not found", "an ambiguous match is not a not-found")
}

// TestManagePrompt_NonAdminForeignPersonalIsExplicit: a non-admin naming another
// owner's personal prompt is told the scope condition, not "not found", and the
// owner is not disclosed.
func TestManagePrompt_NonAdminForeignPersonalIsExplicit(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["example-prompt"] = &prompt.Prompt{
		ID: "p-a", Name: "example-prompt", Scope: prompt.ScopePersonal,
		OwnerEmail: "author@example.com", Content: "body", Enabled: true,
	}

	r, _, err := h.handleManagePrompt(userCtx("other@example.com", "analyst"), managePromptInput{
		Command: "get", Name: "example-prompt",
	})
	require.NoError(t, err)
	require.True(t, r.IsError)
	msg := resultText(r)
	assert.Contains(t, msg, "owned by another user")
	assert.NotContains(t, msg, "not found")
	assert.NotContains(t, msg, "author@example.com", "owner is not disclosed to a non-admin")
}

// TestResolveManagedPrompt_NonAdminGenuinelyMissing: a name that matches no
// prompt still resolves to nil (rendered as "not found"), for admin and
// non-admin alike.
func TestResolveManagedPrompt_NonAdminGenuinelyMissing(t *testing.T) {
	h, _ := newTestHandle()

	got, err := h.resolveManagedPrompt(userCtx("u@example.com", "analyst"), "nope", "u@example.com", "", "")
	require.NoError(t, err)
	assert.Nil(t, got)

	got, err = h.resolveManagedPrompt(adminCtx(), "nope", "admin@example.com", "", "")
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestForeignPersonalPromptError_Message documents the caller-safe wording.
func TestForeignPersonalPromptError_Message(t *testing.T) {
	err := error(&foreignPersonalPromptError{name: "x"})
	var target *foreignPersonalPromptError
	require.True(t, errors.As(err, &target))
	assert.Contains(t, err.Error(), "your own personal prompts")
}

// TestPersonalPromptOwners_Dedup: repeated owners collapse to distinct entries in
// store order, so an ambiguity message never repeats a name.
func TestPersonalPromptOwners_Dedup(t *testing.T) {
	owners := personalPromptOwners([]prompt.Prompt{
		{OwnerEmail: "a@example.com"},
		{OwnerEmail: "b@example.com"},
		{OwnerEmail: "a@example.com"},
	})
	assert.Equal(t, []string{"a@example.com", "b@example.com"}, owners)
}
