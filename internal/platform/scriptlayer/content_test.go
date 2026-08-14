package scriptlayer

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

func TestContentVerbs(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)

	stats := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdStats, Name: "daily"}))
	assert.Equal(t, "daily", stats["name"])
	assert.EqualValues(t, 1, stats["version"])
	assert.NotEmpty(t, stats[textpatch.FieldSizeBytes])
	assert.NotEmpty(t, stats[textpatch.FieldHash])

	content := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdGetContent, Name: "daily"}))
	assert.Contains(t, content["content"], "hello")

	outline := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdOutline, Name: "daily"}))
	assert.NotNil(t, outline)

	locate := resultFields(t, call(t, h, authorCtx(), manageScriptInput{
		Command: cmdLocate, Name: "daily", Find: "hello",
	}))
	assert.NotEmpty(t, locate["matches"])
}

func TestContentVerbs_ErrorsSurface(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)

	// A section is meaningless on source with no region grammar, and the verb
	// says so rather than silently returning the whole body.
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdGetContent, Name: "daily", Section: "Intro",
	})
	assert.True(t, res.IsError, resultText(res))
}

func TestPatch_AppliesAndValidates(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	edits := []textpatch.Edit{{Op: "replace", Find: "hello", Replace: "goodbye"}}
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdPatch, Name: "daily", Edits: edits})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, "updated", resultFields(t, res)["status"])
	for _, sc := range store.scripts {
		assert.Contains(t, sc.Source, "goodbye")
	}
}

func TestPatch_DryRunChangesNothing(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "daily", DryRun: true,
		Edits: []textpatch.Edit{{Op: "replace", Find: "hello", Replace: "goodbye"}},
	})
	require.False(t, res.IsError, resultText(res))
	fields := resultFields(t, res)
	assert.Equal(t, true, fields["dry_run"])
	for _, sc := range store.scripts {
		assert.Contains(t, sc.Source, "hello")
	}
}

// TestPatch_RefusesAPatchThatBreaksTheSource pins the same rule as create: an
// edit that leaves the script unparseable is caught now, not at the next run.
func TestPatch_RefusesAPatchThatBreaksTheSource(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "daily",
		Edits: []textpatch.Edit{{Op: "replace", Find: "print(\"hello\")", Replace: "print(\"hello\""}},
	})
	assert.Equal(t, "invalid", resultFields(t, res)["status"])
	for _, sc := range store.scripts {
		assert.Equal(t, "print(\"hello\")\n", sc.Source)
	}
}

func TestPatch_StaleBaseVersionRefused(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "daily", BaseVersion: 99,
		Edits: []textpatch.Edit{{Op: "replace", Find: "hello", Replace: "x"}},
	})
	assert.True(t, res.IsError, resultText(res))
}

func TestPatch_BadAnchorRefused(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "daily",
		Edits: []textpatch.Edit{{Op: "replace", Find: "not present", Replace: "x"}},
	})
	assert.True(t, res.IsError, resultText(res))
}

func TestDiff_ComparesVersions(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)
	call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Source: "print(\"changed\")\n"})

	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdDiff, Name: "daily"}))
	assert.EqualValues(t, 1, fields["from_version"])
	assert.EqualValues(t, 2, fields["to_version"])
	diff, ok := fields[textpatch.FieldDiff].(string)
	require.True(t, ok)
	assert.Contains(t, diff, "changed")
}

// TestDiff_PrefersThePendingDraft is the reviewer's question by default: what
// is waiting, against what is live.
func TestDiff_PrefersThePendingDraft(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)
	for _, sc := range store.scripts {
		sc.ApprovedVersionID = "sver_1"
		sc.Status = script.StatusActive
	}
	call(t, h, authorCtx(), manageScriptInput{Command: cmdUpdate, Name: "daily", Source: "print(\"proposed\")\n"})

	fields := resultFields(t, call(t, h, authorCtx(), manageScriptInput{Command: cmdDiff, Name: "daily"}))
	assert.EqualValues(t, 1, fields["from_version"])
	assert.EqualValues(t, 2, fields["to_version"])
	diff, _ := fields[textpatch.FieldDiff].(string)
	assert.Contains(t, diff, "proposed")
}

func TestDiff_NoEarlierVersion(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)
	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdDiff, Name: "daily"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "no earlier version")
}

func TestDiff_UnknownVersion(t *testing.T) {
	h, _ := newHandle()
	createDaily(t, h)
	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdDiff, Name: "daily", FromVersion: 1, ToVersion: 9,
	})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found")
}

// TestManageScriptSchema_ClosedAndInSyncWithTheInputStruct holds manage_script
// to the #1057 contract: a schema that publishes an argument the struct does
// not decode is input silently ignored, and the reverse is an argument no model
// will ever learn to send.
func TestManageScriptSchema_ClosedAndInSyncWithTheInputStruct(t *testing.T) {
	raw, err := json.Marshal(manageScriptSchema())
	require.NoError(t, err)
	var obj struct {
		AdditionalProperties *bool          `json:"additionalProperties"`
		Properties           map[string]any `json:"properties"`
		Required             []string       `json:"required"`
	}
	require.NoError(t, json.Unmarshal(raw, &obj))

	require.NotNil(t, obj.AdditionalProperties)
	assert.False(t, *obj.AdditionalProperties, `the schema must declare "additionalProperties": false`)
	assert.Equal(t, []string{"command"}, obj.Required)

	fields := map[string]bool{}
	for _, f := range reflect.VisibleFields(reflect.TypeFor[manageScriptInput]()) {
		tag, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if tag != "" && tag != "-" {
			fields[tag] = true
		}
	}
	for name := range obj.Properties {
		assert.True(t, fields[name], "the schema publishes %q but the input struct does not decode it", name)
	}
	for name := range fields {
		assert.True(t, obj.Properties[name] != nil, "the input struct decodes %q but the closed schema does not publish it", name)
	}
}

// TestManageScriptSchema_CommandEnumMatchesTheDispatchTable keeps the advertised
// commands and the implemented ones from drifting apart in either direction.
func TestManageScriptSchema_CommandEnumMatchesTheDispatchTable(t *testing.T) {
	raw, err := json.Marshal(manageScriptSchema())
	require.NoError(t, err)
	var obj struct {
		Properties struct {
			Command struct {
				Enum []string `json:"enum"`
			} `json:"command"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &obj))

	h, _ := newHandle()
	implemented := h.commands()
	assert.Len(t, obj.Properties.Command.Enum, len(implemented))
	for _, name := range obj.Properties.Command.Enum {
		assert.Contains(t, implemented, name)
	}
}

// TestPatch_RefusesAPatchThatEmptiesTheSource closes the one way past the
// record check create and update both run: an edit that deletes the whole body
// parses fine, and a script with no source is not a script.
func TestPatch_RefusesAPatchThatEmptiesTheSource(t *testing.T) {
	h, store := newHandle()
	createDaily(t, h)

	res := call(t, h, authorCtx(), manageScriptInput{
		Command: cmdPatch, Name: "daily",
		Edits: []textpatch.Edit{{Op: "replace", Find: "print(\"hello\")\n", Replace: ""}},
	})
	assert.True(t, res.IsError, resultText(res))
	assert.Contains(t, resultText(res), "source is required")
	for _, sc := range store.scripts {
		assert.Equal(t, "print(\"hello\")\n", sc.Source)
	}
}

// TestDiff_StoreFailureIsNotReportedAsAMissingVersion keeps a transient store
// failure from telling an author their version does not exist.
func TestDiff_StoreFailureIsNotReportedAsAMissingVersion(t *testing.T) {
	h, store := newFailingHandle()
	createDaily(t, h)
	store.listVersionsErr = errors.New("pq: connection reset")

	res := call(t, h, authorCtx(), manageScriptInput{Command: cmdDiff, Name: "daily"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "version history")
	assert.NotContains(t, resultText(res), "not found")
}
