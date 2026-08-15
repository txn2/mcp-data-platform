package promptschema

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaOf builds the schema for a command set and returns it with its
// properties map, which every assertion here reads.
func schemaOf(t *testing.T, commands ...string) (schema, props map[string]any) {
	t.Helper()
	schema, ok := ManagePrompt(commands).(map[string]any)
	require.True(t, ok, "the schema is a JSON object")
	props, ok = schema["properties"].(map[string]any)
	require.True(t, ok, "the schema declares properties")
	return schema, props
}

// TestManagePromptAdvertisesTheDispatchedCommands is the point of taking the
// command set as an argument: the enum is what the caller dispatches, so a
// command the layer handles can never be missing from the schema an agent
// reads. It is sorted, because a map-derived set would otherwise reorder the
// advertised tool between restarts.
func TestManagePromptAdvertisesTheDispatchedCommands(t *testing.T) {
	schema, props := schemaOf(t, "use", "create", "attach_script")

	command, ok := props["command"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"attach_script", "create", "use"}, command[KeyEnum])
	assert.Equal(t, []string{"command"}, schema["required"])
	assert.Equal(t, false, schema["additionalProperties"],
		"an open schema would accept a misspelled argument silently")
}

// TestManagePromptDoesNotAliasTheCallersSlice proves the schema copies the
// command set before sorting it: sorting in place would reorder the caller's
// dispatch-derived slice as a side effect of reading it.
func TestManagePromptDoesNotAliasTheCallersSlice(t *testing.T) {
	commands := []string{"use", "create"}

	ManagePrompt(commands)

	assert.Equal(t, []string{"use", "create"}, commands)
}

// TestManagePromptDocumentsTheScriptArgument proves the argument the
// attach_script and detach_script commands take is documented in both accepted
// forms: an agent holding either one must not have to guess.
func TestManagePromptDocumentsTheScriptArgument(t *testing.T) {
	_, props := schemaOf(t, "attach_script", "detach_script")

	script, ok := props[fieldScript].(map[string]any)
	require.True(t, ok, "the script argument is advertised")
	assert.Equal(t, ValString, script[KeyType])
	desc, ok := script[KeyDescription].(string)
	require.True(t, ok)
	assert.Contains(t, desc, "mcp:script:<id>")
	assert.Contains(t, desc, "script id")
}

// TestManagePromptSplicesThePatchGrammar proves the shared content-editing
// arguments arrive from pkg/textpatch rather than being restated here, which is
// what keeps manage_prompt and manage_asset advertising the identical grammar.
func TestManagePromptSplicesThePatchGrammar(t *testing.T) {
	_, props := schemaOf(t, "patch")

	assert.Contains(t, props, "edits")
	assert.Contains(t, props, "base_version")
	// A name the prompt schema defines itself keeps its own wording rather than
	// being overwritten by the shared grammar.
	name, ok := props[fieldName].(map[string]any)
	require.True(t, ok)
	assert.Contains(t, name[KeyDescription], "Prompt name")
}

// TestManagePromptCarriesTheCoreArguments pins the arguments every command set
// needs, so an extraction or an edit cannot quietly drop one.
func TestManagePromptCarriesTheCoreArguments(t *testing.T) {
	_, props := schemaOf(t, "create")

	for _, field := range []string{fieldName, fieldContent, "display_name", "scope", "tags", "args"} {
		assert.Contains(t, props, field, field)
	}
}

// TestPromotionRequestScopesExcludePersonal proves the promotion request offers
// only the shared scopes: requesting promotion to personal is what the prompt
// already is.
func TestPromotionRequestScopesExcludePersonal(t *testing.T) {
	_, props := schemaOf(t, "update")

	requested, ok := props["requested_scope"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, promotionRequestScopes, requested[KeyEnum])
	assert.NotContains(t, promotionRequestScopes, "personal")
}

// TestAddPatchPropertiesToleratesAMalformedSchema proves the splice declines a
// schema with no properties map rather than panicking on the type assertion.
func TestAddPatchPropertiesToleratesAMalformedSchema(t *testing.T) {
	schema := map[string]any{"properties": "not a map"}

	addPatchProperties(schema)

	assert.Equal(t, "not a map", schema["properties"])
}
