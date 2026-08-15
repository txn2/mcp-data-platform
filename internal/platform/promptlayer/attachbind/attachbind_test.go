package attachbind

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	"github.com/txn2/mcp-data-platform/pkg/resource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const (
	scriptID  = "11111111-1111-1111-1111-111111111111"
	scriptRef = "mcp:script:" + scriptID
)

// stubLinks is a ScriptAttachmentStore returning one prompt's references.
type stubLinks struct {
	refs []prompt.ScriptAttachment
}

func (*stubLinks) AttachScript(context.Context, prompt.ScriptAttachment) error { return nil }
func (*stubLinks) DetachScript(context.Context, string, string) error          { return nil }
func (s *stubLinks) ListScriptsByPrompt(context.Context, string) ([]prompt.ScriptAttachment, error) {
	return s.refs, nil
}

// stubContracts records the identity a resolution was made for, which is what
// the binder is responsible for deriving.
type stubContracts struct {
	contract *script.Contract
}

func (s *stubContracts) Contract(context.Context, string) (*script.Contract, error) {
	return s.contract, nil
}

// personaScript is visible only to the analyst persona, so whether the binder
// passes the caller's membership decides whether it resolves at all.
func personaScript() *script.Contract {
	return &script.Contract{
		ID: scriptID, Name: "daily-sales", DisplayName: "Daily Sales",
		Scope: script.ScopePersona, Personas: []string{"analyst"},
	}
}

// stubResources is an AttachmentStore with no attachments, enough to build a
// resource resolver whose promotion check runs.
type stubResources struct{ err error }

func (*stubResources) Attach(context.Context, prompt.Attachment) error { return nil }
func (*stubResources) Detach(context.Context, string, string) error    { return nil }
func (*stubResources) Reorder(context.Context, string, []string) error { return nil }
func (*stubResources) ListByResource(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *stubResources) ListByPrompt(context.Context, string) ([]prompt.Attachment, error) {
	if s.err != nil {
		return nil, s.err
	}
	return nil, nil
}

// noResources satisfies resource.Store well enough to build a resolver.
type noResources struct{}

func (noResources) Insert(context.Context, resource.Resource) error { return nil }
func (noResources) Get(context.Context, string) (*resource.Resource, error) {
	return nil, nil //nolint:nilnil // test stub mirroring the store's not-found contract
}

func (noResources) GetByURI(context.Context, string) (*resource.Resource, error) {
	return nil, nil //nolint:nilnil // test stub
}

func (noResources) List(context.Context, resource.Filter) ([]resource.Resource, int, error) {
	return nil, 0, nil
}
func (noResources) Update(context.Context, string, resource.Update) error { return nil }
func (noResources) Delete(context.Context, string) error                  { return nil }

// binderWithScripts returns a binder whose script resolver is bound to one
// referenced script.
func binderWithScripts(t *testing.T, c *script.Contract) *Binder {
	t.Helper()
	b := New()
	r := attachserve.NewScripts(attachserve.ScriptDeps{
		Attachments: &stubLinks{refs: []prompt.ScriptAttachment{{PromptID: "p1", ScriptRef: scriptRef}}},
		Scripts:     &stubContracts{contract: c},
	})
	require.NotNil(t, r)
	b.SetScripts(r)
	return b
}

// ctxWith returns a context carrying a platform identity, the way the tool-call
// and prompt-visibility middleware set one.
func ctxWith(email, persona string) context.Context {
	return middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserEmail: email, PersonaName: persona,
	})
}

// TestUnboundBinderServesNothing proves a deployment with neither kind wired
// serves prompts unchanged rather than failing, and that a nil binder answers
// too — both are real states during composition.
func TestUnboundBinderServesNothing(t *testing.T) {
	var nilBinder *Binder
	assert.Nil(t, nilBinder.Scripts())
	require.NoError(t, nilBinder.GuardScope(context.Background(), &prompt.Prompt{ID: "p1"}))

	b := New()
	assert.Nil(t, b.Scripts(), "an unbound binder offers no script resolver")
	pr := &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal}
	assert.Nil(t, b.ResolveResources(context.Background(), pr, nil))
	assert.Nil(t, b.ResolveScripts(context.Background(), pr, nil))
	require.NoError(t, b.GuardScope(context.Background(), pr))
}

// TestScriptsReturnsTheBoundResolver proves the accessor the attach/detach
// commands act through hands back what was bound.
func TestScriptsReturnsTheBoundResolver(t *testing.T) {
	assert.NotNil(t, binderWithScripts(t, personaScript()).Scripts())
}

// TestResolveScriptsUsesTheRequestPersonaAsMembership proves the binder derives
// the caller's identity from the request context: without it a persona-scoped
// script resolves for nobody, and the serve payload would report every
// automation as out of reach.
func TestResolveScriptsUsesTheRequestPersonaAsMembership(t *testing.T) {
	b := binderWithScripts(t, personaScript())
	pr := &prompt.Prompt{ID: "p1", Scope: prompt.ScopePersona}

	got := b.ResolveScripts(ctxWith("jane@example.com", "analyst"), pr, nil)
	require.Len(t, got, 1)
	assert.Equal(t, attachserve.AvailableEmbedded, got[0].Availability)

	other := b.ResolveScripts(ctxWith("bob@example.com", "engineer"), pr, nil)
	require.Len(t, other, 1)
	assert.Equal(t, attachserve.UnavailableForbidden, other[0].Availability)
}

// TestResolveScriptsPrefersTheKnownMembershipSet proves the full persona set,
// where the serving path knows it, replaces the single acting persona — so a
// member of two personas is not refused their own second persona's automation.
func TestResolveScriptsPrefersTheKnownMembershipSet(t *testing.T) {
	b := binderWithScripts(t, personaScript())
	pr := &prompt.Prompt{ID: "p1", Scope: prompt.ScopePersona}

	got := b.ResolveScripts(ctxWith("jane@example.com", "engineer"), pr, []string{"engineer", "analyst"})

	require.Len(t, got, 1)
	assert.Equal(t, attachserve.AvailableEmbedded, got[0].Availability)
}

// TestResolveScriptsWithoutIdentityResolvesOnlyGlobal proves an anonymous
// request sees exactly what an unauthenticated reader already sees.
func TestResolveScriptsWithoutIdentityResolvesOnlyGlobal(t *testing.T) {
	pr := &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal}

	restricted := binderWithScripts(t, personaScript()).ResolveScripts(context.Background(), pr, nil)
	require.Len(t, restricted, 1)
	assert.Equal(t, attachserve.UnavailableForbidden, restricted[0].Availability)

	global := binderWithScripts(t, &script.Contract{ID: scriptID, Name: "n", Scope: script.ScopeGlobal}).
		ResolveScripts(context.Background(), pr, nil)
	require.Len(t, global, 1)
	assert.Equal(t, attachserve.AvailableEmbedded, global[0].Availability)
}

// TestResolveSkipsPromptsThatCannotCarryMaterial proves a static or file prompt
// (no stored id) costs no store read.
func TestResolveSkipsPromptsThatCannotCarryMaterial(t *testing.T) {
	b := binderWithScripts(t, personaScript())

	assert.Nil(t, b.ResolveScripts(ctxWith("jane@example.com", "analyst"), &prompt.Prompt{Name: "static"}, nil))
	assert.Nil(t, b.ResolveScripts(ctxWith("jane@example.com", "analyst"), nil, nil))
}

// TestAppendContentAddsOneMessagePerContentItem proves referenced automations
// reach a prompts/get result as their own user-role messages, which is what
// lets a client render or elide them independently of the procedure.
func TestAppendContentAddsOneMessagePerContentItem(t *testing.T) {
	b := binderWithScripts(t, &script.Contract{ID: scriptID, Name: "daily-sales", Scope: script.ScopeGlobal})
	res := &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "procedure"}}}}

	b.AppendContent(ctxWith("jane@example.com", ""), res, &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal}, nil)

	require.Len(t, res.Messages, 2)
	text, ok := res.Messages[1].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "daily-sales")
	assert.Equal(t, mcp.Role(promptRoleUser), res.Messages[1].Role)

	b.AppendContent(context.Background(), nil, &prompt.Prompt{ID: "p1"}, nil) // must not panic
}

// TestGuardScopeChecksBothKinds proves one guard covers both kinds of attached
// material: a referenced script that is narrower than the prompt blocks the
// write exactly as a narrow resource does.
func TestGuardScopeChecksBothKinds(t *testing.T) {
	b := binderWithScripts(t, personaScript())

	err := b.GuardScope(context.Background(), &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal})

	require.ErrorIs(t, err, prompt.ErrAttachmentScope)
	assert.Contains(t, err.Error(), "script")
}

// TestGuardScopeChecksThePendingPromotion proves the author is refused at
// request time, not at approval: they are the only person who can fix it.
func TestGuardScopeChecksThePendingPromotion(t *testing.T) {
	b := binderWithScripts(t, personaScript())

	err := b.GuardScope(context.Background(), &prompt.Prompt{
		ID: "p1", Scope: prompt.ScopePersonal, OwnerEmail: "jane@example.com",
		ReviewRequested: true, RequestedScope: prompt.ScopeGlobal,
	})

	require.ErrorIs(t, err, prompt.ErrAttachmentScope)
}

// TestGuardScopeAdmitsACompatibleAudience proves the guard is not a blanket
// refusal: a persona prompt whose personas the script serves is allowed.
func TestGuardScopeAdmitsACompatibleAudience(t *testing.T) {
	b := binderWithScripts(t, personaScript())

	require.NoError(t, b.GuardScope(context.Background(),
		&prompt.Prompt{ID: "p1", Scope: prompt.ScopePersona, Personas: []string{"analyst"}}))
}

// TestGuardScopeFailsClosedOnAResolverError proves a store outage blocks the
// write rather than publishing a shared procedure whose material nobody can
// read.
func TestGuardScopeFailsClosedOnAResolverError(t *testing.T) {
	b := New()
	b.SetResources(attachserve.New(attachserve.Deps{
		Attachments: &stubResources{err: errors.New("down")},
		Resources:   noResources{},
	}))

	require.Error(t, b.GuardScope(context.Background(), &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal}))
}

// TestResolveResourcesCarriesTheCallerScopes proves the resource arm derives
// claims from the request context and prefers the known persona set, matching
// the script arm's rule.
func TestResolveResourcesCarriesTheCallerScopes(t *testing.T) {
	b := New()
	b.SetResources(attachserve.New(attachserve.Deps{Attachments: &stubResources{}, Resources: noResources{}}))
	pr := &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal}

	assert.Nil(t, b.ResolveResources(ctxWith("jane@example.com", "analyst"), pr, []string{"analyst"}),
		"a prompt with no attachments resolves to nothing")
	assert.Nil(t, b.ResolveResources(context.Background(), pr, nil))
}
