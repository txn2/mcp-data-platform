package promptlayer

import (
	"context"
	"errors"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer/promptschema"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer/attachbind"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

const (
	cmdScriptID  = "11111111-1111-1111-1111-111111111111"
	cmdScriptRef = "mcp:script:" + cmdScriptID
)

// scriptLinkStore records the attach and detach calls the commands make.
type scriptLinkStore struct {
	attached []prompt.ScriptAttachment
	detached [][2]string
	detachEr error
}

func (s *scriptLinkStore) AttachScript(_ context.Context, a prompt.ScriptAttachment) error {
	s.attached = append(s.attached, a)
	return nil
}

func (s *scriptLinkStore) DetachScript(_ context.Context, promptID, ref string) error {
	s.detached = append(s.detached, [2]string{promptID, ref})
	return s.detachEr
}

func (*scriptLinkStore) ListScriptsByPrompt(context.Context, string) ([]prompt.ScriptAttachment, error) {
	return nil, nil
}

// oneContract serves a single script contract by id.
type oneContract struct{ c *script.Contract }

func (o oneContract) Contract(_ context.Context, id string) (*script.Contract, error) {
	if o.c != nil && o.c.ID == id {
		return o.c, nil
	}
	return nil, nil //nolint:nilnil // the reader's not-found contract
}

// handleWithScripts builds a prompt handle whose script resolver is bound, plus
// the link store the commands write through.
func handleWithScripts(t *testing.T, c *script.Contract) (*Handle, *mockPromptStore, *scriptLinkStore) {
	t.Helper()
	h, store := newTestHandle()
	links := &scriptLinkStore{}
	h.attach = attachbind.New()
	r := attachserve.NewScripts(attachserve.ScriptDeps{Attachments: links, Scripts: oneContract{c: c}})
	require.NotNil(t, r)
	h.attach.SetScripts(r)
	return h, store, links
}

// seedOwnedPrompt stores a personal prompt owned by jane.
func seedOwnedPrompt(t *testing.T, store *mockPromptStore) {
	t.Helper()
	require.NoError(t, store.Create(context.Background(), &prompt.Prompt{
		Name: "sop", Content: "do the thing", Scope: prompt.ScopePersonal,
		OwnerEmail: "jane@example.com", Enabled: true,
	}))
}

// globalScript is a script every caller can see.
func globalScript() *script.Contract {
	return &script.Contract{
		ID: cmdScriptID, Name: "daily-sales", DisplayName: "Daily Sales",
		Scope: script.ScopeGlobal,
	}
}

// TestAttachScriptCommandStoresTheReference proves the command records the
// canonical reference against the prompt, and reports the action it took.
func TestAttachScriptCommandStoresTheReference(t *testing.T) {
	h, store, links := handleWithScripts(t, globalScript())
	seedOwnedPrompt(t, store)

	res, _, err := h.handleManagePrompt(userCtx("jane@example.com", "analyst"), managePromptInput{
		Command: cmdAttachScript, Name: "sop", Script: cmdScriptID,
	})

	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
	assert.Contains(t, resultText(res), "attached")
	require.Len(t, links.attached, 1)
	assert.Equal(t, cmdScriptRef, links.attached[0].ScriptRef,
		"a bare id normalizes to the reference the platform stores")
	assert.Equal(t, "jane@example.com", links.attached[0].AttachedBy)
}

// TestDetachScriptCommandRemovesTheReference proves the reverse command, and
// that "not referenced" is reported as such rather than as a failure.
func TestDetachScriptCommandRemovesTheReference(t *testing.T) {
	h, store, links := handleWithScripts(t, globalScript())
	seedOwnedPrompt(t, store)
	ctx := userCtx("jane@example.com", "analyst")

	res, _, err := h.handleManagePrompt(ctx, managePromptInput{
		Command: cmdDetachScript, Name: "sop", Script: cmdScriptRef,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
	require.Len(t, links.detached, 1)
	assert.Equal(t, cmdScriptRef, links.detached[0][1])

	links.detachEr = prompt.ErrScriptAttachmentNotFound
	res, _, err = h.handleManagePrompt(ctx, managePromptInput{
		Command: cmdDetachScript, Name: "sop", Script: cmdScriptRef,
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "does not reference that script")
}

// TestDetachScriptReportsAStoreFailure proves a store outage is reported as a
// failure rather than as "that was not attached", which would tell an author
// the repair already happened.
func TestDetachScriptReportsAStoreFailure(t *testing.T) {
	h, store, links := handleWithScripts(t, globalScript())
	seedOwnedPrompt(t, store)
	links.detachEr = errors.New("db down")

	res, _, err := h.handleManagePrompt(userCtx("jane@example.com", "analyst"), managePromptInput{
		Command: cmdDetachScript, Name: "sop", Script: cmdScriptRef,
	})

	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.NotContains(t, resultText(res), "does not reference that script")
}

// TestAttachScriptRefusesAnotherOwnersPrompt proves referencing is an edit to
// the prompt and takes the same authority every other prompt mutation takes.
func TestAttachScriptRefusesAnotherOwnersPrompt(t *testing.T) {
	h, store, links := handleWithScripts(t, globalScript())
	seedOwnedPrompt(t, store)

	res, _, err := h.handleManagePrompt(userCtx("bob@example.com", "analyst"), managePromptInput{
		Command: cmdAttachScript, Name: "sop", Script: cmdScriptRef,
	})

	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Empty(t, links.attached)
}

// TestAttachScriptSurfacesTheScopeRefusalVerbatim proves an author who is
// blocked reads the sentence that says what to fix, rather than a generic
// failure they cannot act on.
func TestAttachScriptSurfacesTheScopeRefusalVerbatim(t *testing.T) {
	narrow := globalScript()
	narrow.Scope = script.ScopePersona
	narrow.Personas = []string{"analyst"}
	h, store, links := handleWithScripts(t, narrow)
	require.NoError(t, store.Create(context.Background(), &prompt.Prompt{
		Name: "shared-sop", Content: "x", Scope: prompt.ScopeGlobal, Enabled: true,
	}))

	res, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{
		Command: cmdAttachScript, Name: "shared-sop", Script: cmdScriptRef,
	})

	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "cannot be attached")
	assert.Contains(t, resultText(res), "the prompt is global")
	assert.Empty(t, links.attached)
}

// TestScriptCommandsValidateTheirInputs proves each missing argument is named,
// so an agent can correct the call without guessing.
func TestScriptCommandsValidateTheirInputs(t *testing.T) {
	h, store, _ := handleWithScripts(t, globalScript())
	seedOwnedPrompt(t, store)
	ctx := userCtx("jane@example.com", "analyst")

	res, _, err := h.handleManagePrompt(ctx, managePromptInput{Command: cmdAttachScript, Script: cmdScriptRef})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "name is required")

	res, _, err = h.handleManagePrompt(ctx, managePromptInput{Command: cmdAttachScript, Name: "sop"})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "script is required")

	res, _, err = h.handleManagePrompt(ctx, managePromptInput{
		Command: cmdAttachScript, Name: "missing", Script: cmdScriptRef,
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found")
}

// TestScriptCommandsOnADeploymentWithoutScripts proves the commands answer
// plainly where managed scripts are unavailable rather than failing obscurely.
func TestScriptCommandsOnADeploymentWithoutScripts(t *testing.T) {
	h, store := newTestHandle()
	h.attach = attachbind.New()
	seedOwnedPrompt(t, store)

	res, _, err := h.handleManagePrompt(userCtx("jane@example.com", "analyst"), managePromptInput{
		Command: cmdAttachScript, Name: "sop", Script: cmdScriptRef,
	})

	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not available on this deployment")
}

// TestAttachScriptRefusesAConfigDefinedPrompt proves a prompt that lives in
// server configuration has no row to hang a reference on, and says so.
func TestAttachScriptRefusesAConfigDefinedPrompt(t *testing.T) {
	h, store, _ := handleWithScripts(t, globalScript())
	require.NoError(t, store.Create(context.Background(), &prompt.Prompt{
		Name: "builtin", Content: "x", Scope: prompt.ScopeGlobal, Enabled: true,
		Source: prompt.SourceSystem,
	}))

	res, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{
		Command: cmdAttachScript, Name: "builtin", Script: cmdScriptRef,
	})

	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "read-only")
}

// TestAttachScriptReportsAMissingScript proves a reference to nothing is
// refused at authoring time rather than stored as a broken link.
func TestAttachScriptReportsAMissingScript(t *testing.T) {
	h, store, links := handleWithScripts(t, globalScript())
	seedOwnedPrompt(t, store)

	res, _, err := h.handleManagePrompt(userCtx("jane@example.com", "analyst"), managePromptInput{
		Command: cmdAttachScript, Name: "sop", Script: "mcp:script:22222222-2222-2222-2222-222222222222",
	})

	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Empty(t, links.attached)
}

// TestManagePromptAdvertisesTheScriptCommands proves the tool schema offers the
// commands and the argument they take: a command an agent cannot discover from
// the schema does not exist as far as the agent is concerned.
func TestManagePromptAdvertisesTheScriptCommands(t *testing.T) {
	schema, ok := promptschema.ManagePrompt(testCommandNames()).(map[string]any)
	require.True(t, ok)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)

	command, ok := props["command"].(map[string]any)
	require.True(t, ok)
	enum, ok := command[promptschema.KeyEnum].([]string)
	require.True(t, ok)
	assert.Contains(t, enum, cmdAttachScript)
	assert.Contains(t, enum, cmdDetachScript)

	scriptProp, ok := props[fieldScript].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, scriptProp[promptschema.KeyDescription], "mcp:script:<id>")
}
