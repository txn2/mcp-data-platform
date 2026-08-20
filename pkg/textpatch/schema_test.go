package textpatch

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPropertiesJSONIsValidSchema(t *testing.T) {
	var props map[string]any
	require.NoError(t, json.Unmarshal([]byte(PropertiesJSON), &props))

	for _, name := range []string{
		"edits", "base_version", "dry_run",
		"find", "pattern", "section", "line_start", "line_end",
		"context_bytes", "from_version", "to_version",
	} {
		assert.Contains(t, props, name)
	}
}

func TestPropertiesMapMatchesTheJSONConstant(t *testing.T) {
	got := PropertiesMap()

	var want map[string]any
	require.NoError(t, json.Unmarshal([]byte(PropertiesJSON), &want))
	assert.Equal(t, want, got, "both tools must splice the identical grammar")
}

func TestEditsSchemaEnumeratesEveryOperation(t *testing.T) {
	props := PropertiesMap()
	edits, ok := props["edits"].(map[string]any)
	require.True(t, ok)
	items, ok := edits["items"].(map[string]any)
	require.True(t, ok)
	itemProps, ok := items["properties"].(map[string]any)
	require.True(t, ok)
	op, ok := itemProps["op"].(map[string]any)
	require.True(t, ok)

	enum, ok := op["enum"].([]any)
	require.True(t, ok)
	declared := make([]string, 0, len(enum))
	for _, v := range enum {
		name, isString := v.(string)
		require.True(t, isString, "enum entries must be strings")
		declared = append(declared, name)
	}
	assert.ElementsMatch(t, []string{
		OpReplace, OpInsertBefore, OpInsertAfter,
		OpReplaceSection, OpReplaceContent, OpMoveSection, OpAppend, OpPrepend,
	}, declared, "the advertised operations are exactly the ones applyOne dispatches")
}

func TestVerbsDescriptionNamesEveryVerb(t *testing.T) {
	for _, verb := range []string{"patch", "locate", "outline", "get_content", "stats", "diff"} {
		assert.True(t, strings.Contains(VerbsDescription, verb), "steering text must name %q", verb)
	}
}
