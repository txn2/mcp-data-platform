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

// janesScript is Jane's, so whether the binder derives the caller's address
// from the request context decides whether it resolves at all.
func janesScript() *script.Contract {
	return &script.Contract{
		ID: scriptID, Name: "daily-sales", DisplayName: "Daily Sales",
		OwnerEmail: "jane@example.com",
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

func (noResources) GetByIDs(context.Context, []string) (map[string]*resource.Resource, error) {
	return map[string]*resource.Resource{}, nil
}

func (noResources) GetByURI(context.Context, string) (*resource.Resource, error) {
	return nil, nil //nolint:nilnil // test stub
}

func (noResources) List(context.Context, resource.Filter) ([]resource.Resource, int, error) {
	return nil, 0, nil
}
func (noResources) Update(context.Context, string, resource.Update) error { return nil }
func (noResources) Move(context.Context, []resource.Move) error {
	return errors.New("noResources does not move resources")
}
func (noResources) Delete(context.Context, string) error { return nil }

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
	assert.Nil(t, b.ResolveScripts(context.Background(), pr))
	require.NoError(t, b.GuardScope(context.Background(), pr))
}

// TestScriptsReturnsTheBoundResolver proves the accessor the attach/detach
// commands act through hands back what was bound.
func TestScriptsReturnsTheBoundResolver(t *testing.T) {
	assert.NotNil(t, binderWithScripts(t, janesScript()).Scripts())
}

// TestResolveScriptsUsesTheRequestIdentity proves the binder derives the
// caller's address from the request context: without it every reference would
// resolve for nobody and the serve payload would report every automation as out
// of reach.
func TestResolveScriptsUsesTheRequestIdentity(t *testing.T) {
	b := binderWithScripts(t, janesScript())
	pr := &prompt.Prompt{ID: "p1", Scope: prompt.ScopePersona}

	got := b.ResolveScripts(ctxWith("jane@example.com", "analyst"), pr)
	require.Len(t, got, 1)
	assert.Equal(t, attachserve.AvailableEmbedded, got[0].Availability)

	other := b.ResolveScripts(ctxWith("bob@example.com", "engineer"), pr)
	require.Len(t, other, 1)
	assert.Equal(t, attachserve.UnavailableForbidden, other[0].Availability)
}

// TestResolveScriptsWithoutIdentityResolvesNothing proves an anonymous request
// reaches no script: a script is its owner's, and a request the platform cannot
// name owns none.
func TestResolveScriptsWithoutIdentityResolvesNothing(t *testing.T) {
	pr := &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal}

	got := binderWithScripts(t, janesScript()).ResolveScripts(context.Background(), pr)

	require.Len(t, got, 1)
	assert.Equal(t, attachserve.UnavailableForbidden, got[0].Availability)
}

// TestResolveSkipsPromptsThatCannotCarryMaterial proves a static or file prompt
// (no stored id) costs no store read.
func TestResolveSkipsPromptsThatCannotCarryMaterial(t *testing.T) {
	b := binderWithScripts(t, janesScript())

	assert.Nil(t, b.ResolveScripts(ctxWith("jane@example.com", "analyst"), &prompt.Prompt{Name: "static"}))
	assert.Nil(t, b.ResolveScripts(ctxWith("jane@example.com", "analyst"), nil))
}

// TestAppendContentAddsOneMessagePerContentItem proves referenced automations
// reach a prompts/get result as their own user-role messages, which is what
// lets a client render or elide them independently of the procedure.
func TestAppendContentAddsOneMessagePerContentItem(t *testing.T) {
	b := binderWithScripts(t, janesScript())
	res := &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: "procedure"}}}}

	b.AppendContent(ctxWith("jane@example.com", ""), res, &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal}, nil)

	require.Len(t, res.Messages, 2)
	text, ok := res.Messages[1].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "Daily Sales")
	assert.Equal(t, mcp.Role(promptRoleUser), res.Messages[1].Role)

	b.AppendContent(context.Background(), nil, &prompt.Prompt{ID: "p1"}, nil) // must not panic
}

// TestGuardScopeIgnoresReferencedScripts proves promoting a prompt is not
// blocked by the automations it references: a script resolves for its owner at
// every prompt scope, so widening the prompt cannot outrun it. The author was
// told what the reference serves when they made it.
func TestGuardScopeIgnoresReferencedScripts(t *testing.T) {
	b := binderWithScripts(t, janesScript())

	require.NoError(t, b.GuardScope(context.Background(), &prompt.Prompt{ID: "p1", Scope: prompt.ScopeGlobal}))
	require.NoError(t, b.GuardScope(context.Background(), &prompt.Prompt{
		ID: "p1", Scope: prompt.ScopePersonal, OwnerEmail: "jane@example.com",
		ReviewRequested: true, RequestedScope: prompt.ScopeGlobal,
	}))
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

// Folders is not exercised here: this fake stands in for the read paths a
// noResources uses, and none of them lists a library's folders.
func (noResources) Folders(_ context.Context, _ resource.Filter) ([]resource.Folder, error) {
	return nil, nil
}

// Tags is not exercised here: this fake stands in for the read paths a
// noResources uses, and none of them lists a library's tags.
func (noResources) Tags(_ context.Context, _ resource.Filter) ([]string, error) {
	return nil, nil
}

// The capture routes are not exercised here: this fake stands in for the read
// paths a noResources uses, and none of them captures or lists a thumbnail.
func (noResources) SetThumbnail(_ context.Context, _ string, _ resource.ThumbnailCapture) error {
	return nil
}

func (noResources) ClearThumbnail(_ context.Context, _, _ string) error { return nil }

func (noResources) PendingThumbnails(_ context.Context, _ resource.Filter, _ int) ([]resource.Resource, error) {
	return nil, nil
}
