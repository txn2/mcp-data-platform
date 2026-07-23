package prompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// TestAttachmentScopeConstantsMatchResource pins the scope strings this package
// mirrors to the originals in pkg/resource. The duplication buys pkg/prompt
// independence from pkg/resource; this test is what keeps it honest, because a
// silent drift would make every scope comparison fall through to the unknown
// branch and reject every attachment.
func TestAttachmentScopeConstantsMatchResource(t *testing.T) {
	assert.Equal(t, string(resource.ScopeGlobal), resourceScopeGlobal)
	assert.Equal(t, string(resource.ScopePersona), resourceScopePersona)
	assert.Equal(t, string(resource.ScopeUser), resourceScopeUser)
}

func TestCheckAttachScope(t *testing.T) {
	global := AttachmentScope{ResourceID: "r1", DisplayName: "Brand Header", Scope: "global"}
	analyst := AttachmentScope{ResourceID: "r2", DisplayName: "Analyst Rubric", Scope: "persona", ScopeID: "analyst"}
	private := AttachmentScope{ResourceID: "r3", DisplayName: "My Draft", Scope: "user", ScopeID: "sub-1"}

	tests := []struct {
		name     string
		scope    string
		personas []string
		res      AttachmentScope
		wantErr  bool
	}{
		{"global resource on global prompt", ScopeGlobal, nil, global, false},
		{"global resource on persona prompt", ScopePersona, []string{"analyst"}, global, false},
		{"global resource on personal prompt", ScopePersonal, nil, global, false},

		{"persona resource on its own persona prompt", ScopePersona, []string{"analyst"}, analyst, false},
		{"persona resource is case-insensitive on persona name", ScopePersona, []string{"Analyst"}, analyst, false},
		{"persona resource on personal prompt", ScopePersonal, nil, analyst, false},
		{"persona resource on global prompt", ScopeGlobal, nil, analyst, true},
		{"persona resource on a multi-persona prompt", ScopePersona, []string{"analyst", "engineer"}, analyst, true},
		{"persona resource on a persona prompt naming no persona", ScopePersona, nil, analyst, true},
		{"persona resource on another persona's prompt", ScopePersona, []string{"engineer"}, analyst, true},

		{"private resource on personal prompt", ScopePersonal, nil, private, false},
		{"private resource on persona prompt", ScopePersona, []string{"analyst"}, private, true},
		{"private resource on global prompt", ScopeGlobal, nil, private, true},

		{"unknown resource scope is refused", ScopePersonal, nil, AttachmentScope{ResourceID: "r4", Scope: "team"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckAttachScope(tt.scope, tt.personas, tt.res)
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrAttachmentScope,
				"every rejection must carry the sentinel so handlers map it to a conflict")
		})
	}
}

// TestCheckAttachScopeErrorNamesResource proves the rejection tells the author
// which attachment to fix. A message that only says "scope violation" would
// leave an author with several attachments guessing.
func TestCheckAttachScopeErrorNamesResource(t *testing.T) {
	err := CheckAttachScope(ScopeGlobal, nil, AttachmentScope{
		ResourceID: "r9", DisplayName: "Q4 Template", Scope: "persona", ScopeID: "analyst",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Q4 Template")
	assert.Contains(t, err.Error(), "analyst")
}

// TestCheckAttachScopeErrorFallsBackToID covers a resource saved without a
// display name: the message must still identify it.
func TestCheckAttachScopeErrorFallsBackToID(t *testing.T) {
	err := CheckAttachScope(ScopeGlobal, nil, AttachmentScope{ResourceID: "res_abc", Scope: "user", ScopeID: "sub-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "res_abc")
}

func TestCheckAttachOwnership(t *testing.T) {
	private := AttachmentScope{ResourceID: "r1", DisplayName: "My Draft", Scope: "user", ScopeID: "sub-1"}

	t.Run("owner attaching their own private resource to their own prompt", func(t *testing.T) {
		assert.NoError(t, CheckAttachOwnership("sub-1", "me@example.com", "me@example.com", private))
	})

	t.Run("email match is case-insensitive", func(t *testing.T) {
		assert.NoError(t, CheckAttachOwnership("sub-1", "Me@Example.com", "me@example.com", private))
	})

	t.Run("another user's private resource is refused", func(t *testing.T) {
		err := CheckAttachOwnership("sub-2", "other@example.com", "other@example.com", private)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrAttachmentScope)
	})

	t.Run("an admin may not attach a user's private resource to that user's prompt", func(t *testing.T) {
		// The admin can read it, but the SOP's materials would be readable only
		// by the resource's owner, so the check is deliberately not admin-exempt.
		err := CheckAttachOwnership("admin-sub", "admin@example.com", "me@example.com", private)
		require.Error(t, err)
	})

	t.Run("own resource on someone else's prompt is refused", func(t *testing.T) {
		err := CheckAttachOwnership("sub-1", "me@example.com", "other@example.com", private)
		require.Error(t, err)
	})

	t.Run("an anonymous caller is refused", func(t *testing.T) {
		require.Error(t, CheckAttachOwnership("", "", "", private))
	})

	t.Run("a resource scoped to the caller by email is theirs", func(t *testing.T) {
		// An admin can scope a resource to a user by email before that user has
		// ever signed in; resource.VisibleScopes makes it readable by them, so
		// this check must agree or the author could read it but not attach it.
		byEmail := AttachmentScope{ResourceID: "r2", Scope: "user", ScopeID: "Me@Example.com"}
		assert.NoError(t, CheckAttachOwnership("sub-1", "me@example.com", "me@example.com", byEmail))
	})

	t.Run("an empty scope id belongs to nobody", func(t *testing.T) {
		orphan := AttachmentScope{ResourceID: "r3", Scope: "user"}
		require.Error(t, CheckAttachOwnership("sub-1", "me@example.com", "me@example.com", orphan))
	})

	t.Run("non-user scopes are not this check's business", func(t *testing.T) {
		global := AttachmentScope{ResourceID: "r2", Scope: "global"}
		assert.NoError(t, CheckAttachOwnership("", "", "", global))
	})
}

// TestCheckPromotionAttachments is the gate on the promotion flow: the first
// attachment that would be unreachable at the target scope blocks the move and
// names itself.
func TestCheckPromotionAttachments(t *testing.T) {
	attached := []AttachmentScope{
		{ResourceID: "r1", DisplayName: "Brand Header", Scope: "global"},
		{ResourceID: "r2", DisplayName: "Private Draft", Scope: "user", ScopeID: "sub-1"},
	}

	t.Run("staying personal is fine", func(t *testing.T) {
		assert.NoError(t, CheckPromotionAttachments(ScopePersonal, nil, attached))
	})

	t.Run("promoting to persona is blocked by the private attachment", func(t *testing.T) {
		err := CheckPromotionAttachments(ScopePersona, []string{"analyst"}, attached)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrAttachmentScope)
		assert.Contains(t, err.Error(), "Private Draft")
	})

	t.Run("promoting succeeds once the offending attachment is gone", func(t *testing.T) {
		assert.NoError(t, CheckPromotionAttachments(ScopePersona, []string{"analyst"}, attached[:1]))
	})

	t.Run("a prompt with no attachments promotes freely", func(t *testing.T) {
		assert.NoError(t, CheckPromotionAttachments(ScopeGlobal, nil, nil))
	})
}

// fakeAttachmentStore is a Store that also carries the attachment capability,
// used to prove AsAttachmentStore resolves it directly.
type fakeAttachmentStore struct {
	Store
	AttachmentStore
}

// attachmentProviderOnly is a decorator that hides the capability behind
// AttachmentProvider, the shape the promptlayer's notifying wrapper has.
type attachmentProviderOnly struct {
	Store
	inner AttachmentStore
}

func (a attachmentProviderOnly) Attachments() AttachmentStore { return a.inner }

func TestAsAttachmentStore(t *testing.T) {
	inner := struct{ AttachmentStore }{}

	t.Run("direct implementation", func(t *testing.T) {
		got := AsAttachmentStore(fakeAttachmentStore{AttachmentStore: inner})
		assert.NotNil(t, got)
	})

	t.Run("through a provider decorator", func(t *testing.T) {
		got := AsAttachmentStore(attachmentProviderOnly{inner: inner})
		assert.NotNil(t, got, "a decorator must not hide the capability from the composition root")
	})

	t.Run("a store without attachments resolves to nil", func(t *testing.T) {
		assert.Nil(t, AsAttachmentStore(struct{ Store }{}))
	})
}
