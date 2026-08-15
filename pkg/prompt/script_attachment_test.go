package prompt

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptScope is a persona-scoped script's projection onto the shared rule,
// serving two personas — the shape a resource can never have and the reason the
// rule tests a SET rather than one id.
func scriptScope(personas ...string) AttachmentScope {
	return AttachmentScope{
		Kind: AttachKindScript, ID: "script_1", DisplayName: "Daily Sales",
		Scope: "persona", ScopeIDs: personas,
	}
}

// TestCheckAttachScope_PersonaSetContainsThePromptAudience proves the
// generalized rule: every persona the prompt serves must be one the material
// reaches. A script serving analysts and engineers may go on a prompt serving
// either or both, and must be refused on a prompt that also serves a third.
func TestCheckAttachScope_PersonaSetContainsThePromptAudience(t *testing.T) {
	material := scriptScope("analyst", "engineer")

	require.NoError(t, CheckAttachScope(ScopePersona, []string{"analyst"}, material))
	require.NoError(t, CheckAttachScope(ScopePersona, []string{"analyst", "engineer"}, material))
	require.NoError(t, CheckAttachScope(ScopePersonal, nil, material),
		"a personal prompt reaches one person, who is the only reader to disappoint")

	err := CheckAttachScope(ScopePersona, []string{"analyst", "auditor"}, material)
	require.ErrorIs(t, err, ErrAttachmentScope)
	assert.Contains(t, err.Error(), `personas "analyst", "engineer"`)
	assert.Contains(t, err.Error(), `also serves persona "auditor"`)

	err = CheckAttachScope(ScopeGlobal, nil, material)
	require.ErrorIs(t, err, ErrAttachmentScope)
	assert.Contains(t, err.Error(), "the prompt is global")
}

// TestCheckAttachScope_NamesTheKind proves a refusal says what sort of material
// is at fault. An author reading "script ... cannot be attached" knows which
// surface to go fix; "resource" would send them to the wrong one.
func TestCheckAttachScope_NamesTheKind(t *testing.T) {
	err := CheckAttachScope(ScopeGlobal, nil, scriptScope("analyst"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), `script "Daily Sales" cannot be attached`)

	res := AttachmentScope{
		Kind: AttachKindResource, ID: "r1", DisplayName: "Rubric",
		Scope: "persona", ScopeIDs: []string{"analyst"},
	}
	err = CheckAttachScope(ScopeGlobal, nil, res)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `resource "Rubric" cannot be attached`)

	unkinded := AttachmentScope{ID: "x", Scope: "team"}
	err = CheckAttachScope(ScopePersonal, nil, unkinded)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "resource", "an unset kind reads as a resource")
	assert.Contains(t, err.Error(), "unknown resource scope")
}

// TestCheckAttachScope_PersonaMaterialWithNoAudience proves a persona-scoped
// material naming no persona is refused rather than quietly admitted: it
// reaches nobody, so it can satisfy no prompt's audience.
func TestCheckAttachScope_PersonaMaterialWithNoAudience(t *testing.T) {
	err := CheckAttachScope(ScopePersona, []string{"analyst"}, scriptScope())
	require.ErrorIs(t, err, ErrAttachmentScope)
	assert.Contains(t, err.Error(), "no persona")

	err = CheckAttachScope(ScopePersona, nil, scriptScope("analyst"))
	require.ErrorIs(t, err, ErrAttachmentScope)
	assert.Contains(t, err.Error(), "the prompt names no persona")
}

// TestCheckAttachOwnership_PersonalScript proves a personal script may only be
// referenced by its owner, from their own prompt — the script counterpart of the
// private-resource rule, answered by the same function.
func TestCheckAttachOwnership_PersonalScript(t *testing.T) {
	mine := AttachmentScope{
		Kind: AttachKindScript, ID: "script_1", DisplayName: "My Draft",
		Scope: "user", ScopeIDs: []string{"jane@example.com"},
	}

	require.NoError(t, CheckAttachOwnership("sub-1", "jane@example.com", "jane@example.com", mine))

	err := CheckAttachOwnership("sub-2", "bob@example.com", "bob@example.com", mine)
	require.ErrorIs(t, err, ErrAttachmentScope)
	assert.Contains(t, err.Error(), "another user's private script")

	err = CheckAttachOwnership("sub-1", "jane@example.com", "bob@example.com", mine)
	require.ErrorIs(t, err, ErrAttachmentScope)
	assert.Contains(t, err.Error(), "your own prompt")
}

// TestCheckAttachOwnership_MatchesAnyIdentityForm proves the owner may be named
// by subject or by email, and that an empty entry in the set names nobody.
func TestCheckAttachOwnership_MatchesAnyIdentityForm(t *testing.T) {
	bySubject := AttachmentScope{Scope: "user", ScopeIDs: []string{"sub-1"}}
	require.NoError(t, CheckAttachOwnership("sub-1", "jane@example.com", "jane@example.com", bySubject))

	byEmail := AttachmentScope{Scope: "user", ScopeIDs: []string{"Jane@Example.com"}}
	require.NoError(t, CheckAttachOwnership("sub-1", "jane@example.com", "jane@example.com", byEmail))

	orphan := AttachmentScope{Scope: "user", ScopeIDs: []string{""}}
	require.Error(t, CheckAttachOwnership("sub-1", "jane@example.com", "jane@example.com", orphan))

	none := AttachmentScope{Scope: "user"}
	require.Error(t, CheckAttachOwnership("sub-1", "jane@example.com", "jane@example.com", none))
}

// scriptAttachStore is a Store that also persists script links.
type scriptAttachStore struct {
	Store
	links []ScriptAttachment
}

func (s *scriptAttachStore) AttachScript(_ context.Context, a ScriptAttachment) error {
	s.links = append(s.links, a)
	return nil
}
func (*scriptAttachStore) DetachScript(context.Context, string, string) error { return nil }
func (s *scriptAttachStore) ListScriptsByPrompt(context.Context, string) ([]ScriptAttachment, error) {
	return s.links, nil
}

// scriptAttachDecorator hides the capability behind a wrapper, the way the
// notifying store wraps the real one.
type scriptAttachDecorator struct {
	Store
	inner ScriptAttachmentStore
}

func (d scriptAttachDecorator) ScriptAttachments() ScriptAttachmentStore { return d.inner }

// TestAsScriptAttachmentStore proves the capability is resolvable both
// directly and through a decorator, so wrapping the store for notifications
// cannot silently turn prompt-to-script references off.
func TestAsScriptAttachmentStore(t *testing.T) {
	direct := &scriptAttachStore{}
	assert.NotNil(t, AsScriptAttachmentStore(direct))
	assert.NotNil(t, AsScriptAttachmentStore(scriptAttachDecorator{inner: direct}))
	assert.Nil(t, AsScriptAttachmentStore(scriptAttachDecorator{}), "a decorator over a store without the capability")
	assert.Nil(t, AsScriptAttachmentStore(nil))
}
