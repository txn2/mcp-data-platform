package promptlayer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/txn2/mcp-data-platform/internal/platform/promptlayer/promptschema"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// procedure is a multi-step operating procedure the patch tests edit.
const procedure = `# Daily Sales Report

## Step 1: Pull the data

Query the warehouse for yesterday's orders.

## Step 2: Summarize

Group by region and compute totals.

## Step 3: Publish

Save the result as an asset.
`

// callPrompt runs a manage_prompt command and decodes a successful JSON result.
func callPrompt(ctx context.Context, t *testing.T, h *Handle, input managePromptInput) (map[string]any, *mcp.CallToolResult) {
	t.Helper()
	res, _, err := h.handleManagePrompt(ctx, input)
	require.NoError(t, err)
	require.NotNil(t, res)

	var decoded map[string]any
	if !res.IsError {
		require.NoError(t, json.Unmarshal([]byte(resultText(res)), &decoded), resultText(res))
	}
	return decoded, res
}

// seedPersonalPrompt stores an owned, unreviewed prompt the caller may patch.
func seedPersonalPrompt(store *mockVersionStore, content string) {
	store.prompts["daily-sales"] = &prompt.Prompt{
		ID: "p1", Name: "daily-sales", Scope: prompt.ScopePersonal,
		OwnerEmail: "user@example.com", Content: content,
		Status: prompt.StatusDraft, Enabled: true, Version: 1,
	}
}

func TestManagePromptOutlineAndStats(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)
	ctx := userCtx("user@example.com", "analyst")

	outline, res := callPrompt(ctx, t, h, managePromptInput{Command: cmdOutline, Name: "daily-sales"})
	require.False(t, res.IsError, resultText(res))
	sections, ok := outline["sections"].([]any)
	require.True(t, ok)
	require.Len(t, sections, 4)

	stats, res := callPrompt(ctx, t, h, managePromptInput{Command: cmdStats, Name: "daily-sales"})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, float64(len(procedure)), stats["size_bytes"])
	assert.Equal(t, textpatch.DocStats(procedure).Hash, stats["hash"])
	assert.NotContains(t, stats, "content")
}

func TestManagePromptGetContentAndLocate(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)
	ctx := userCtx("user@example.com", "analyst")

	section, res := callPrompt(ctx, t, h, managePromptInput{
		Command: cmdGetContent, Name: "daily-sales", Section: "## Step 2: Summarize",
	})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, "## Step 2: Summarize\n\nGroup by region and compute totals.\n\n", section["content"])

	lines, res := callPrompt(ctx, t, h, managePromptInput{
		Command: cmdGetContent, Name: "daily-sales", LineStart: 1, LineEnd: 1,
	})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, "# Daily Sales Report\n", lines["content"])

	located, res := callPrompt(ctx, t, h, managePromptInput{
		Command: cmdLocate, Name: "daily-sales", Find: "Group by region",
	})
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, float64(1), located["count"])
	matches, ok := located["matches"].([]any)
	require.True(t, ok)
	first, ok := matches[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "## Step 2: Summarize", first["section"])
}

func TestManagePromptPatchAppliesToAnUnreviewedPrompt(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)
	ctx := userCtx("user@example.com", "analyst")

	got, res := callPrompt(ctx, t, h, managePromptInput{
		Command: cmdPatch, Name: "daily-sales",
		Edits: []textpatch.Edit{{Find: "Step 2: Summarize", Replace: "Step 2: Aggregate"}},
	})
	require.False(t, res.IsError, resultText(res))

	assert.Equal(t, "updated", got["status"])
	assert.Contains(t, got["diff"], "+## Step 2: Aggregate")
	assert.NotContains(t, got, "content", "the response never echoes the new body")

	stored := store.prompts["daily-sales"].Content
	assert.Equal(t, strings.Replace(procedure, "Step 2: Summarize", "Step 2: Aggregate", 1), stored)
}

func TestManagePromptPatchOfApprovedSharedPromptPends(t *testing.T) {
	h, store := newVersionedTestHandle()
	store.prompts["daily-sales"] = &prompt.Prompt{
		ID: "p1", Name: "daily-sales", Scope: prompt.ScopeGlobal, Content: procedure,
		Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}

	got, res := callPrompt(adminCtx(), t, h, managePromptInput{
		Command: cmdPatch, Name: "daily-sales",
		Edits: []textpatch.Edit{{Find: "Step 3: Publish", Replace: "Step 3: Distribute"}},
	})
	require.False(t, res.IsError, resultText(res))

	assert.Equal(t, "pending_approval", got["status"])
	assert.Equal(t, float64(2), got["pending_version"])
	assert.Contains(t, got["diff"], "+## Step 3: Distribute")

	assert.Equal(t, procedure, store.prompts["daily-sales"].Content,
		"the approved version keeps being served until an admin approves the draft")
	require.Len(t, store.drafts["p1"], 1)
	assert.Contains(t, store.drafts["p1"][0].Content, "Step 3: Distribute")
	assert.Equal(t, prompt.VersionStatusDraft, store.drafts["p1"][0].Status)
}

func TestManagePromptPatchDryRunWritesNothing(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)
	ctx := userCtx("user@example.com", "analyst")

	got, res := callPrompt(ctx, t, h, managePromptInput{
		Command: cmdPatch, Name: "daily-sales", DryRun: true,
		Edits: []textpatch.Edit{{Find: "Step 1: Pull the data", Replace: "Step 1: Extract"}},
	})
	require.False(t, res.IsError, resultText(res))

	assert.Equal(t, true, got["dry_run"])
	assert.Contains(t, got["diff"], "+## Step 1: Extract")
	assert.Equal(t, procedure, store.prompts["daily-sales"].Content)
	assert.Empty(t, store.drafts["p1"])
}

func TestManagePromptPatchRefusesStaleBase(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)
	ctx := userCtx("user@example.com", "analyst")

	_, res := callPrompt(ctx, t, h, managePromptInput{
		Command: cmdPatch, Name: "daily-sales", BaseVersion: 4,
		Edits: []textpatch.Edit{{Find: "Step 1", Replace: "Step One"}},
	})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), textpatch.CodeStaleBase)
	assert.Equal(t, procedure, store.prompts["daily-sales"].Content)
}

func TestManagePromptPatchRefusesAmbiguousAnchor(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, "alpha step\nbeta step\n")
	ctx := userCtx("user@example.com", "analyst")

	_, res := callPrompt(ctx, t, h, managePromptInput{
		Command: cmdPatch, Name: "daily-sales",
		Edits: []textpatch.Edit{{Find: "step", Replace: "phase"}},
	})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), textpatch.CodeAmbiguous)
	assert.Equal(t, "alpha step\nbeta step\n", store.prompts["daily-sales"].Content)
}

func TestManagePromptPatchAuthorization(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)

	_, res := callPrompt(userCtx("someone-else@example.com", "analyst"), t, h, managePromptInput{
		Command: cmdPatch, Name: "daily-sales",
		Edits: []textpatch.Edit{{Find: "Step 1", Replace: "Step One"}},
	})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "your own")

	store.prompts["system-prompt"] = &prompt.Prompt{
		ID: "p2", Name: "system-prompt", Scope: prompt.ScopeGlobal,
		Source: prompt.SourceSystem, Content: procedure, Enabled: true,
	}
	_, res = callPrompt(adminCtx(), t, h, managePromptInput{
		Command: cmdPatch, Name: "system-prompt",
		Edits: []textpatch.Edit{{Find: "Step 1", Replace: "Step One"}},
	})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "read-only")
}

func TestManagePromptContentCommandsRequireAName(t *testing.T) {
	h, _ := newVersionedTestHandle()
	for _, cmd := range []string{cmdPatch, cmdLocate, cmdGetContent, cmdOutline, cmdStats, cmdDiff} {
		_, res := callPrompt(adminCtx(), t, h, managePromptInput{Command: cmd})
		require.True(t, res.IsError, cmd)
		assert.Contains(t, resultText(res), "name is required", cmd)
	}
}

func TestManagePromptDiffComparesDraftAgainstApproved(t *testing.T) {
	h, store := newVersionedTestHandle()
	store.prompts["daily-sales"] = &prompt.Prompt{
		ID: "p1", Name: "daily-sales", Scope: prompt.ScopeGlobal, Content: procedure,
		Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}

	_, res := callPrompt(adminCtx(), t, h, managePromptInput{
		Command: cmdPatch, Name: "daily-sales",
		Edits: []textpatch.Edit{{Find: "Step 3: Publish", Replace: "Step 3: Distribute"}},
	})
	require.False(t, res.IsError, resultText(res))

	got, res := callPrompt(adminCtx(), t, h, managePromptInput{Command: cmdDiff, Name: "daily-sales"})
	require.False(t, res.IsError, resultText(res))

	assert.Equal(t, float64(1), got["from_version"])
	assert.Equal(t, float64(2), got["to_version"])
	diff, ok := got["diff"].(string)
	require.True(t, ok)
	assert.Contains(t, diff, "-## Step 3: Publish")
	assert.Contains(t, diff, "+## Step 3: Distribute")
	assert.NotContains(t, diff, "Step 1", "only the changed step is reported")
}

func TestManagePromptDiffReportsAMissingVersion(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)

	_, res := callPrompt(userCtx("user@example.com", "analyst"), t, h, managePromptInput{
		Command: cmdDiff, Name: "daily-sales", FromVersion: 1, ToVersion: 9,
	})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "version 9 not found")
}

func TestManagePromptDiffNeedsVersioningSupport(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["daily-sales"] = &prompt.Prompt{
		ID: "p1", Name: "daily-sales", Scope: prompt.ScopePersonal,
		OwnerEmail: "user@example.com", Content: procedure, Enabled: true, Version: 1,
	}

	_, res := callPrompt(userCtx("user@example.com", "analyst"), t, h, managePromptInput{
		Command: cmdDiff, Name: "daily-sales",
	})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "versioning is unavailable")
}

func TestManagePromptDiffWithNoEarlierVersion(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)

	_, res := callPrompt(userCtx("user@example.com", "analyst"), t, h, managePromptInput{
		Command: cmdDiff, Name: "daily-sales",
	})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "no earlier version")
}

func TestManagePromptLocateReportsABadQuery(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)
	ctx := userCtx("user@example.com", "analyst")

	_, res := callPrompt(ctx, t, h, managePromptInput{Command: cmdLocate, Name: "daily-sales"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), textpatch.CodeBadEdit)

	_, res = callPrompt(ctx, t, h, managePromptInput{Command: cmdLocate, Name: "daily-sales", Pattern: "("})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), textpatch.CodeBadPattern)
}

func TestManagePromptContentCommandsRespectVisibility(t *testing.T) {
	h, store := newVersionedTestHandle()
	seedPersonalPrompt(store, procedure)

	for _, cmd := range []string{cmdOutline, cmdStats, cmdGetContent} {
		_, res := callPrompt(userCtx("stranger@example.com", "analyst"), t, h, managePromptInput{
			Command: cmd, Name: "daily-sales",
		})
		require.True(t, res.IsError, cmd)
		assert.Contains(t, resultText(res), "your own personal prompts", cmd)
	}

	_, res := callPrompt(adminCtx(), t, h, managePromptInput{Command: cmdOutline, Name: "missing"})
	require.True(t, res.IsError)
	assert.Contains(t, resultText(res), "not found")
}

func TestManagePromptSchemaAdvertisesTheSharedGrammar(t *testing.T) {
	schema, ok := promptschema.ManagePrompt(testCommandNames()).(map[string]any)
	require.True(t, ok)
	props, ok := schema["properties"].(map[string]any)
	require.True(t, ok)

	for name, shared := range textpatch.PropertiesMap() {
		got, present := props[name]
		require.True(t, present, "manage_prompt must advertise %q", name)
		if name == "search" || name == "description" || name == "content" {
			continue
		}
		assert.Equal(t, shared, got, "property %q must be the shared grammar", name)
	}

	command, ok := props["command"].(map[string]any)
	require.True(t, ok)
	enum, ok := command["enum"].([]string)
	require.True(t, ok)
	for _, cmd := range []string{cmdPatch, cmdLocate, cmdGetContent, cmdOutline, cmdStats, cmdDiff} {
		assert.Contains(t, enum, cmd)
	}
}

func TestWithExtraFoldsThePatchReportIntoTheOutcome(t *testing.T) {
	base := map[string]any{"status": "updated"}
	got := withExtra(base, map[string]any{"diff": "@@", "lines": 3})
	assert.Equal(t, "updated", got["status"])
	assert.Equal(t, "@@", got["diff"])
	assert.Equal(t, 3, got["lines"])
	assert.Equal(t, base, withExtra(base, nil))
}
