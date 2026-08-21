package attachserve

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const (
	scriptID  = "11111111-1111-1111-1111-111111111111"
	scriptRef = "mcp:script:" + scriptID
)

// fakeScriptLinks is an in-memory ScriptAttachmentStore.
type fakeScriptLinks struct {
	byPrompt map[string][]prompt.ScriptAttachment
	listErr  error
	attached []prompt.ScriptAttachment
	attachEr error
	detached [][2]string
	detachEr error
}

func (f *fakeScriptLinks) AttachScript(_ context.Context, a prompt.ScriptAttachment) error {
	f.attached = append(f.attached, a)
	return f.attachEr
}

func (f *fakeScriptLinks) DetachScript(_ context.Context, promptID, ref string) error {
	f.detached = append(f.detached, [2]string{promptID, ref})
	return f.detachEr
}

func (f *fakeScriptLinks) ListScriptsByPrompt(_ context.Context, promptID string) ([]prompt.ScriptAttachment, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byPrompt[promptID], nil
}

// fakeContracts is an in-memory ScriptReader.
type fakeContracts struct {
	byID map[string]*script.Contract
	err  error
}

func (f *fakeContracts) Contract(_ context.Context, id string) (*script.Contract, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byID[id], nil
}

// ownedContract is Jane's runnable script: active, enabled, latest saved
// version 3, with no run refusal.
func ownedContract() *script.Contract {
	return &script.Contract{
		ID: scriptID, Name: "daily-sales", DisplayName: "Daily Sales",
		Description: "Yesterday's sales by region", OwnerEmail: "jane@example.com",
		Status: script.StatusActive, Enabled: true,
		Params:  []script.Param{{Name: "report_date", Required: true}},
		Version: 3,
	}
}

// resolverWith builds a resolver over one prompt's links.
func resolverWith(t *testing.T, links []prompt.ScriptAttachment, contracts map[string]*script.Contract) (*ScriptResolver, *fakeScriptLinks) {
	t.Helper()
	store := &fakeScriptLinks{byPrompt: map[string][]prompt.ScriptAttachment{"p1": links}}
	r := NewScripts(ScriptDeps{Attachments: store, Scripts: &fakeContracts{byID: contracts}})
	require.NotNil(t, r)
	return r, store
}

// TestNewScriptsRequiresBothHalves proves a deployment missing either the links
// or the contracts serves prompts without automations rather than half-serving
// them.
func TestNewScriptsRequiresBothHalves(t *testing.T) {
	assert.Nil(t, NewScripts(ScriptDeps{}))
	assert.Nil(t, NewScripts(ScriptDeps{Attachments: &fakeScriptLinks{}}))
	assert.Nil(t, NewScripts(ScriptDeps{Scripts: &fakeContracts{}}))
}

// TestResolveNilResolverIsEmpty proves the nil resolver is usable, which is what
// lets every serving site skip a nil check.
func TestResolveNilResolverIsEmpty(t *testing.T) {
	var r *ScriptResolver
	assert.Nil(t, r.Resolve(context.Background(), "p1", ""))
}

// TestResolveDeliversTheContract proves a visible reference resolves to the
// contract the serve payload renders.
func TestResolveDeliversTheContract(t *testing.T) {
	r, _ := resolverWith(t, []prompt.ScriptAttachment{{PromptID: "p1", ScriptRef: scriptRef}},
		map[string]*script.Contract{scriptID: ownedContract()})

	got := r.Resolve(context.Background(), "p1", "jane@example.com")

	require.Len(t, got, 1)
	assert.Equal(t, AvailableEmbedded, got[0].Availability)
	require.NotNil(t, got[0].Contract)
	assert.Equal(t, "daily-sales", got[0].Contract.Name)
}

// TestResolveWithholdsAScriptTheCallerCannotSee proves a reference is not a
// side channel: a caller who does not own the script learns only that something
// is referenced and out of reach, never its name or parameters.
func TestResolveWithholdsAScriptTheCallerCannotSee(t *testing.T) {
	r, _ := resolverWith(t, []prompt.ScriptAttachment{{PromptID: "p1", ScriptRef: scriptRef}},
		map[string]*script.Contract{scriptID: ownedContract()})

	got := r.Resolve(context.Background(), "p1", "bob@example.com")

	require.Len(t, got, 1)
	assert.Equal(t, UnavailableForbidden, got[0].Availability)
	assert.Nil(t, got[0].Contract)

	summary := ScriptSummary(got)
	require.Len(t, summary, 1)
	assert.NotContains(t, summary[0], "script_ref", "a withheld reference is not an existence probe")
}

// TestResolveReportsTheThreeFailures proves a deleted script, a malformed
// stored reference, and a read failure are distinguishable: only one of them
// means the automation is actually gone.
func TestResolveReportsTheThreeFailures(t *testing.T) {
	r, _ := resolverWith(t, []prompt.ScriptAttachment{
		{PromptID: "p1", ScriptRef: scriptRef},
		{PromptID: "p1", ScriptRef: "garbage"},
	}, map[string]*script.Contract{})

	got := r.Resolve(context.Background(), "p1", "jane@example.com")
	require.Len(t, got, 2)
	assert.Equal(t, UnavailableMissing, got[0].Availability, "a deleted script")
	assert.Equal(t, UnavailableMissing, got[1].Availability, "an unparseable stored reference")

	store := &fakeScriptLinks{byPrompt: map[string][]prompt.ScriptAttachment{
		"p1": {{PromptID: "p1", ScriptRef: scriptRef}},
	}}
	broken := NewScripts(ScriptDeps{Attachments: store, Scripts: &fakeContracts{err: errors.New("down")}})
	got = broken.Resolve(context.Background(), "p1", "jane@example.com")
	require.Len(t, got, 1)
	assert.Equal(t, UnavailableUnreadable, got[0].Availability,
		"a read failure must not be reported as a deleted script")
}

// TestResolveSurvivesALinkStoreOutage proves a store failure serves the prompt
// without its automations rather than failing the prompt: a procedure that has
// lost an automation is still a procedure.
func TestResolveSurvivesALinkStoreOutage(t *testing.T) {
	store := &fakeScriptLinks{listErr: errors.New("down")}
	r := NewScripts(ScriptDeps{Attachments: store, Scripts: &fakeContracts{}})

	assert.Nil(t, r.Resolve(context.Background(), "p1", "jane@example.com"))
}

// TestScriptContentFramesTheAutomations proves the served text tells the agent
// what to do with a referenced script — run it rather than re-derive its output
// — and carries the contract plus the reference that dereferences it.
func TestScriptContentFramesTheAutomations(t *testing.T) {
	items := []ResolvedScript{{Reference: scriptRef, Availability: AvailableEmbedded, Contract: ownedContract()}}

	content := ScriptContent(items)

	require.Len(t, content, 1)
	text, ok := content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "The following managed script is referenced by this prompt")
	assert.Contains(t, text.Text, "run_script")
	assert.Contains(t, text.Text, "Daily Sales")
	assert.Contains(t, text.Text, "Runs: version 3, the latest saved version")
	assert.Contains(t, text.Text, "Reference: "+scriptRef)
	assert.Nil(t, ScriptContent(nil))
}

// TestScriptContentNotesWhatWasNotDelivered proves an incomplete procedure says
// so, counting by reason without naming the material.
func TestScriptContentNotesWhatWasNotDelivered(t *testing.T) {
	content := ScriptContent([]ResolvedScript{
		{Reference: scriptRef, Availability: AvailableEmbedded, Contract: ownedContract()},
		{Reference: "mcp:script:a", Availability: UnavailableForbidden},
		{Reference: "mcp:script:b", Availability: UnavailableMissing},
		{Reference: "mcp:script:c", Availability: UnavailableUnreadable},
	})

	require.Len(t, content, 2)
	note, ok := content[1].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, note.Text, "3 referenced scripts were not delivered")
	assert.Contains(t, note.Text, "1 you are not permitted to see")
	assert.Contains(t, note.Text, "1 no longer exists")
	assert.Contains(t, note.Text, "1 could not be read")
	assert.NotContains(t, note.Text, "mcp:script:a")
}

// TestScriptContentWithNothingDeliverable proves the framing block is omitted
// when every reference was withheld: there is nothing to frame.
func TestScriptContentWithNothingDeliverable(t *testing.T) {
	content := ScriptContent([]ResolvedScript{{Reference: scriptRef, Availability: UnavailableMissing}})

	require.Len(t, content, 1)
	text, ok := content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, text.Text, "1 referenced script was not delivered")
}

// TestScriptSummaryCarriesTheContract proves the JSON provenance block lets an
// agent state exactly which automations it received.
func TestScriptSummaryCarriesTheContract(t *testing.T) {
	summary := ScriptSummary([]ResolvedScript{
		{Reference: scriptRef, Availability: AvailableEmbedded, Contract: ownedContract()},
	})

	require.Len(t, summary, 1)
	assert.Equal(t, scriptRef, summary[0]["script_ref"])
	assert.Equal(t, string(AvailableEmbedded), summary[0]["availability"])
	assert.NotNil(t, summary[0]["contract"])
	assert.Nil(t, ScriptSummary(nil))
}

// TestNormalizeScriptRef proves both input forms an agent can hold — the
// reference search returns and the bare id manage_script returns — normalize to
// the one stored form, and that nothing else does.
func TestNormalizeScriptRef(t *testing.T) {
	got, id, err := normalizeScriptRef(scriptRef)
	require.NoError(t, err)
	assert.Equal(t, scriptRef, got)
	assert.Equal(t, scriptID, id)

	got, id, err = normalizeScriptRef("  " + scriptID + "  ")
	require.NoError(t, err)
	assert.Equal(t, scriptRef, got, "a bare id normalizes to the canonical reference")
	assert.Equal(t, scriptID, id)

	for _, bad := range []string{"", "mcp:prompt:11111111-1111-1111-1111-111111111111", "urn:li:dataset:(a,b,PROD)"} {
		_, _, err = normalizeScriptRef(bad)
		require.Error(t, err, bad)
	}
}

// TestScriptIDFromRefRejectsOtherEntities proves the parser refuses to read
// some other entity's reference as a script id.
func TestScriptIDFromRefRejectsOtherEntities(t *testing.T) {
	_, err := scriptIDFromRef("mcp:asset:a1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a managed script")
}

// TestAttachStoresTheCanonicalReference proves the caller may reference the
// script they own, whatever the prompt's audience, and that what lands in the
// row is the canonical reference rather than the bare id a tool hands out.
func TestAttachStoresTheCanonicalReference(t *testing.T) {
	r, store := resolverWith(t, nil, map[string]*script.Contract{scriptID: ownedContract()})

	note, err := r.Attach(context.Background(), ScriptAttachRequest{
		Prompt:      &prompt.Prompt{ID: "p1", Name: "sop", Scope: prompt.ScopeGlobal},
		Ref:         scriptID,
		CallerEmail: "jane@example.com",
	})

	require.NoError(t, err)
	require.Len(t, store.attached, 1)
	assert.Equal(t, scriptRef, store.attached[0].ScriptRef, "the reference is stored, never a bare id")
	assert.Equal(t, "jane@example.com", store.attached[0].AttachedBy)
	assert.Contains(t, note, "jane@example.com",
		"a shared prompt tells its author who the reference resolves for")
}

// TestAttachRefusesSomebodyElsesScript proves a reference is not a way to read
// a script the caller cannot see, and that an administrator is not held to it.
func TestAttachRefusesSomebodyElsesScript(t *testing.T) {
	r, store := resolverWith(t, nil, map[string]*script.Contract{scriptID: ownedContract()})
	p := &prompt.Prompt{ID: "p1", Name: "sop", Scope: prompt.ScopeGlobal}

	_, err := r.Attach(context.Background(), ScriptAttachRequest{
		Prompt: p, Ref: scriptRef, CallerEmail: "bob@example.com",
	})

	require.ErrorIs(t, err, prompt.ErrAttachmentScope)
	assert.Contains(t, err.Error(), "belongs to somebody else")
	assert.Empty(t, store.attached)

	_, err = r.Attach(context.Background(), ScriptAttachRequest{
		Prompt: p, Ref: scriptRef, CallerEmail: "bob@example.com", CallerIsAdmin: true,
	})
	require.NoError(t, err)
	assert.Len(t, store.attached, 1)
}

// TestAudienceNoteSaysWhoAReferenceResolvesFor proves the author is told what
// their prompt's readers will actually receive: nothing where the prompt serves
// somebody other than the script's owner, and no note at all where it does not.
func TestAudienceNoteSaysWhoAReferenceResolvesFor(t *testing.T) {
	c := ownedContract()

	own := &prompt.Prompt{Scope: prompt.ScopePersonal, OwnerEmail: "jane@example.com"}
	assert.Empty(t, AudienceNote(own, c), "the author is the only reader, so there is nothing to warn about")

	shared := &prompt.Prompt{Scope: prompt.ScopeGlobal, OwnerEmail: "jane@example.com"}
	assert.Contains(t, AudienceNote(shared, c), "jane@example.com")

	someoneElses := &prompt.Prompt{Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com"}
	assert.Contains(t, AudienceNote(someoneElses, c), "jane@example.com",
		"a personal prompt somebody else owns is still a reader the script does not reach")

	orphan := ownedContract()
	orphan.OwnerEmail = ""
	assert.Contains(t, AudienceNote(shared, orphan), "nobody")

	assert.Empty(t, AudienceNote(nil, c))
	assert.Empty(t, AudienceNote(shared, nil))
}

// TestAttachRefusesAMissingScript proves a reference to nothing is refused at
// authoring time rather than becoming a broken link a reader discovers.
func TestAttachRefusesAMissingScript(t *testing.T) {
	r, store := resolverWith(t, nil, map[string]*script.Contract{})

	_, err := r.Attach(context.Background(), ScriptAttachRequest{
		Prompt: &prompt.Prompt{ID: "p1", Name: "sop", Scope: prompt.ScopeGlobal},
		Ref:    scriptRef,
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not exist")
	assert.Empty(t, store.attached)
}

// TestAttachRequiresAStoredPrompt proves a static or file prompt cannot carry a
// reference: there is no row to hang it on.
func TestAttachRequiresAStoredPrompt(t *testing.T) {
	r, _ := resolverWith(t, nil, map[string]*script.Contract{scriptID: ownedContract()})

	_, err := r.Attach(context.Background(), ScriptAttachRequest{Prompt: &prompt.Prompt{Name: "sop"}, Ref: scriptRef})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stored prompt")
}

// TestAttachAndDetachOnANilResolver proves the deployment-without-scripts case
// answers rather than panicking.
func TestAttachAndDetachOnANilResolver(t *testing.T) {
	var r *ScriptResolver
	_, attachErr := r.Attach(context.Background(), ScriptAttachRequest{})
	require.Error(t, attachErr)
	require.Error(t, r.Detach(context.Background(), "p1", scriptRef))
}

// TestDetachNormalizesTheReference proves detaching by bare id removes the row
// stored under the canonical reference, so an agent holding either form can
// repair a prompt.
func TestDetachNormalizesTheReference(t *testing.T) {
	r, store := resolverWith(t, nil, map[string]*script.Contract{scriptID: ownedContract()})

	require.NoError(t, r.Detach(context.Background(), "p1", scriptID))

	require.Len(t, store.detached, 1)
	assert.Equal(t, scriptRef, store.detached[0][1])

	require.Error(t, r.Detach(context.Background(), "p1", "mcp:asset:a1"),
		"another entity's reference names no script to detach")
}
