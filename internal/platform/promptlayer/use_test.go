package promptlayer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// useResponse is the decoded manage_prompt use result for assertions.
type useResponse struct {
	Status string `json:"status"`
	Prompt struct {
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		Scope       string `json:"scope"`
		Reference   string `json:"reference"`
		Status      string `json:"status"`
	} `json:"prompt"`
	Arguments  []prompt.Argument `json:"arguments"`
	Content    string            `json:"content"`
	Missing    []string          `json:"missing_required_arguments"`
	Candidates []struct {
		Name  string  `json:"name"`
		Scope string  `json:"scope"`
		Score float64 `json:"score"`
	} `json:"candidates"`
}

func callUse(ctx context.Context, t *testing.T, h *Handle, input managePromptInput) (useResponse, *mcp.CallToolResult) {
	t.Helper()
	input.Command = cmdUse
	res, _, err := h.handleManagePrompt(ctx, input)
	require.NoError(t, err)
	var parsed useResponse
	if !res.IsError {
		require.NoError(t, json.Unmarshal([]byte(resultText(res)), &parsed))
	}
	return parsed, res
}

func TestPromptUse_ExactBareNamePrecedence(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "g1", Name: "report", Scope: prompt.ScopeGlobal, Content: "global body", Enabled: true, Status: prompt.StatusApproved,
	}
	store.prompts["report:sarah"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "personal body", Enabled: true,
	}

	// Sarah's own personal prompt wins the bare name.
	parsed, res := callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{Name: "report"})
	require.False(t, res.IsError)
	assert.Equal(t, "resolved", parsed.Status)
	assert.Equal(t, "personal body", parsed.Content)
	assert.Equal(t, prompt.ScopePersonal, parsed.Prompt.Scope)
	assert.Equal(t, "mcp:prompt:p1", parsed.Prompt.Reference)

	// Another caller gets the global.
	parsed, res = callUse(userCtx("bob@example.com", "analyst"), t, h, managePromptInput{Name: "report"})
	require.False(t, res.IsError)
	assert.Equal(t, "global body", parsed.Content)
}

// TestPromptUse_ExactNameHonorsPublicationGate: exact-name resolution applies
// the same visibility rule as browse and search (#1124). Another owner's draft
// shared prompt does not resolve for a non-admin; the owner and an admin reach
// it at any status.
func TestPromptUse_ExactNameHonorsPublicationGate(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["draft-sop"] = &prompt.Prompt{
		ID: "g1", Name: "draft-sop", Scope: prompt.ScopeGlobal, Content: "draft body",
		Enabled: true, Status: prompt.StatusDraft, OwnerEmail: "owner@example.com",
	}

	_, res := callUse(userCtx("bob@example.com", "analyst"), t, h, managePromptInput{Name: "draft-sop"})
	assert.True(t, res.IsError, "another owner's draft shared prompt must not resolve for a non-admin")

	parsed, res := callUse(userCtx("owner@example.com", "analyst"), t, h, managePromptInput{Name: "draft-sop"})
	require.False(t, res.IsError, "the owner resolves their own draft: %s", resultText(res))
	assert.Equal(t, "draft body", parsed.Content)

	_, res = callUse(adminCtx(), t, h, managePromptInput{Name: "draft-sop"})
	assert.False(t, res.IsError, "an admin resolves any status")
}

func TestPromptUse_SystemPromptResolvable(t *testing.T) {
	// Built-ins are mirrored into the store as system rows; `use` resolves them
	// so "run the explore prompt" works without the static registry.
	h, store := newTestHandle()
	store.prompts["explore-available-data"] = &prompt.Prompt{
		Name: "explore-available-data", Scope: prompt.ScopeGlobal, Status: prompt.StatusApproved,
		Source: prompt.SourceSystem, Content: "builtin body", Enabled: true,
	}

	parsed, res := callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{Name: "explore-available-data"})
	require.False(t, res.IsError)
	assert.Equal(t, "builtin body", parsed.Content)
}

func TestPromptUse_ByReference(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report:sarah"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "personal body", Enabled: true,
	}

	// The owner resolves by mcp:prompt:<id>.
	parsed, res := callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{Name: "mcp:prompt:p1"})
	require.False(t, res.IsError)
	assert.Equal(t, "personal body", parsed.Content)

	// Another non-admin caller cannot reach someone else's personal prompt.
	_, res = callUse(userCtx("bob@example.com", "analyst"), t, h, managePromptInput{Name: "mcp:prompt:p1"})
	assert.True(t, res.IsError, "another user's personal prompt is not found by reference")

	// An admin can.
	_, res = callUse(adminCtx(), t, h, managePromptInput{Name: "mcp:prompt:p1"})
	assert.False(t, res.IsError)

	// A disabled prompt is not served.
	store.prompts["report:sarah"].Enabled = false
	_, res = callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{Name: "mcp:prompt:p1"})
	assert.True(t, res.IsError, "a disabled prompt does not resolve")
}

func TestPromptUse_DisplayName(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["daily-sales-report"] = &prompt.Prompt{
		ID: "g1", Name: "daily-sales-report", DisplayName: "Daily Sales Report",
		Scope: prompt.ScopeGlobal, Content: "global body", Enabled: true, Status: prompt.StatusApproved,
	}

	parsed, res := callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{Name: "daily sales report"})
	require.False(t, res.IsError, "display name resolves case-insensitively: %s", resultText(res))
	assert.Equal(t, "resolved", parsed.Status)
	assert.Equal(t, "daily-sales-report", parsed.Prompt.Name)
	assert.Equal(t, "global body", parsed.Content)
}

func TestPromptUse_DisplayNamePrecedenceAndTie(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report-a"] = &prompt.Prompt{
		ID: "g1", Name: "report-a", DisplayName: "The Report",
		Scope: prompt.ScopeGlobal, Content: "global body", Enabled: true, Status: prompt.StatusApproved,
	}
	store.prompts["report-b:sarah"] = &prompt.Prompt{
		ID: "p1", Name: "report-b", DisplayName: "The Report",
		Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "personal body", Enabled: true,
	}

	// The caller's personal prompt outranks the global of the same display name.
	parsed, res := callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{Name: "The Report"})
	require.False(t, res.IsError)
	assert.Equal(t, "personal body", parsed.Content)

	// Two same-precedence matches are ambiguous: candidates, not a silent pick.
	store.prompts["report-c"] = &prompt.Prompt{
		ID: "g2", Name: "report-c", DisplayName: "The Report",
		Scope: prompt.ScopeGlobal, Content: "other global", Enabled: true, Status: prompt.StatusApproved,
	}
	parsed, res = callUse(userCtx("bob@example.com", "analyst"), t, h, managePromptInput{Name: "The Report"})
	require.False(t, res.IsError)
	assert.Equal(t, "ambiguous", parsed.Status)
	assert.Len(t, parsed.Candidates, 2)
}

func TestPromptUse_ArgsSubstitutionAndMissing(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "g1", Name: "report", Scope: prompt.ScopeGlobal, Status: prompt.StatusApproved, Content: "about {topic} on {date}",
		Arguments: []prompt.Argument{
			{Name: "topic", Required: true},
			{Name: "date", Required: true},
		},
		Enabled: true,
	}

	parsed, res := callUse(userCtx("sarah@example.com", "analyst"), t, h,
		managePromptInput{Name: "report", Args: map[string]string{"topic": "sales"}})
	require.False(t, res.IsError)
	assert.Equal(t, "about sales on {date}", parsed.Content, "provided args substitute")
	assert.Equal(t, []string{"date"}, parsed.Missing, "absent required args are reported")
	require.Len(t, parsed.Arguments, 2, "argument specs travel with the resolved prompt")
}

// searcherStore adds a canned ranking capability to the mock store so the
// ranked fallback path is testable.
type searcherStore struct {
	*mockPromptStore
	scored []prompt.ScoredPrompt
	err    error
}

func (s *searcherStore) Search(_ context.Context, _ prompt.SearchQuery) ([]prompt.ScoredPrompt, error) {
	return s.scored, s.err
}

func TestPromptUse_RankedFallback(t *testing.T) {
	base := newMockPromptStore()
	confident := []prompt.ScoredPrompt{
		{Prompt: prompt.Prompt{ID: "g1", Name: "daily-sales-report", Content: "ranked body"}, Score: 0.9},
		{Prompt: prompt.Prompt{ID: "g2", Name: "weekly-report", Content: "other"}, Score: 0.4},
	}
	h := &Handle{adminPersona: "admin"}
	st := &searcherStore{mockPromptStore: base, scored: confident}
	h.store = st
	ctx := userCtx("sarah@example.com", "analyst")

	// Clear top-score margin resolves the single confident match.
	parsed, res := callUse(ctx, t, h, managePromptInput{Name: "the daily sales thing"})
	require.False(t, res.IsError)
	assert.Equal(t, "resolved", parsed.Status)
	assert.Equal(t, "ranked body", parsed.Content)

	// A narrow margin returns ranked candidates instead.
	st.scored = []prompt.ScoredPrompt{
		{Prompt: prompt.Prompt{ID: "g1", Name: "daily-sales-report"}, Score: 0.61},
		{Prompt: prompt.Prompt{ID: "g2", Name: "weekly-sales-report"}, Score: 0.58},
	}
	parsed, res = callUse(ctx, t, h, managePromptInput{Name: "sales report"})
	require.False(t, res.IsError)
	assert.Equal(t, "ambiguous", parsed.Status)
	require.Len(t, parsed.Candidates, 2)
	assert.InDelta(t, 0.61, parsed.Candidates[0].Score, 1e-9, "scores travel with candidates")

	// No results is an explicit miss.
	st.scored = nil
	_, res = callUse(ctx, t, h, managePromptInput{Name: "nothing like this"})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "no prompt matched")
}

func TestPromptUse_SubstringFallbackWithoutSearcher(t *testing.T) {
	// A store without ranking degrades to substring matching over the caller's
	// visible prompts.
	h, store := newTestHandle()
	store.prompts["daily-sales-report"] = &prompt.Prompt{
		ID: "g1", Name: "daily-sales-report", Scope: prompt.ScopeGlobal, Content: "sales body", Enabled: true,
		Status: prompt.StatusApproved,
	}
	store.prompts["ops-runbook"] = &prompt.Prompt{
		ID: "g2", Name: "ops-runbook", Scope: prompt.ScopeGlobal, Content: "ops body", Enabled: true,
		Status: prompt.StatusApproved,
	}
	ctx := userCtx("sarah@example.com", "analyst")

	parsed, res := callUse(ctx, t, h, managePromptInput{Name: "daily sales"})
	require.False(t, res.IsError, "%s", resultText(res))
	assert.Equal(t, "sales body", parsed.Content, "unique substring match resolves")

	store.prompts["weekly-sales-report"] = &prompt.Prompt{
		ID: "g3", Name: "weekly-sales-report", Scope: prompt.ScopeGlobal, Content: "weekly", Enabled: true,
		Status: prompt.StatusApproved,
	}
	parsed, res = callUse(ctx, t, h, managePromptInput{Name: "sales report"})
	require.False(t, res.IsError)
	assert.Equal(t, "ambiguous", parsed.Status)
	assert.Len(t, parsed.Candidates, 2)

	_, res = callUse(ctx, t, h, managePromptInput{Name: "zzz no such thing"})
	assert.True(t, res.IsError)
}

func TestPromptUse_EmptyNameIsError(t *testing.T) {
	h, _ := newTestHandle()
	_, res := callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{})
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "name is required")
}

func TestPromptUse_DisplayNamePersonaBeatsGlobal(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report-g"] = &prompt.Prompt{
		ID: "g1", Name: "report-g", DisplayName: "Team Report",
		Scope: prompt.ScopeGlobal, Content: "global body", Enabled: true, Status: prompt.StatusApproved,
	}
	store.prompts["report-p"] = &prompt.Prompt{
		ID: "pp1", Name: "report-p", DisplayName: "Team Report",
		Scope: prompt.ScopePersona, Personas: []string{"analyst"}, Content: "persona body", Enabled: true,
		Status: prompt.StatusApproved,
	}

	parsed, res := callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{Name: "Team Report"})
	require.False(t, res.IsError)
	assert.Equal(t, "persona body", parsed.Content, "persona-scope match outranks the global")
}

func TestPromptUse_AdminDisplayNameAndErrors(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report:sarah"] = &prompt.Prompt{
		ID: "p1", Name: "report", DisplayName: "Sarah Report",
		Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com", Content: "sarah body", Enabled: true,
	}

	// The admin visibility path spans all scopes, including others' personal prompts.
	parsed, res := callUse(adminCtx(), t, h, managePromptInput{Name: "Sarah Report"})
	require.False(t, res.IsError)
	assert.Equal(t, "sarah body", parsed.Content)

	// A failing store list degrades to no visible prompts, so free text misses.
	store.listErr = assert.AnError
	_, res = callUse(adminCtx(), t, h, managePromptInput{Name: "anything else"})
	assert.True(t, res.IsError)
	_, res = callUse(userCtx("bob@example.com", "analyst"), t, h, managePromptInput{Name: "anything else"})
	assert.True(t, res.IsError)

	// A failing store read on a reference lookup is a tool error, not a panic.
	store.getErr = assert.AnError
	_, res = callUse(adminCtx(), t, h, managePromptInput{Name: "mcp:prompt:p1"})
	assert.True(t, res.IsError)
}

func TestPromptUse_ProvenanceFields(t *testing.T) {
	h, store := newTestHandle()
	approved := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	store.prompts["report"] = &prompt.Prompt{
		ID: "g1", Name: "report", DisplayName: "The Report", Description: "desc",
		Scope: prompt.ScopeGlobal, Source: prompt.SourceOperator, Status: prompt.StatusApproved,
		OwnerEmail: "admin@example.com", ApprovedBy: "admin@example.com", ApprovedAt: &approved,
		UpdatedAt: approved, Tags: []string{"sales"}, Content: "body", Enabled: true,
	}

	_, res := callUse(userCtx("sarah@example.com", "analyst"), t, h, managePromptInput{Name: "report"})
	require.False(t, res.IsError)
	var raw map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &raw))
	prov, ok := raw["prompt"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "mcp:prompt:g1", prov["reference"])
	assert.Equal(t, prompt.StatusApproved, prov["status"])
	assert.Equal(t, "admin@example.com", prov["approved_by"])
	assert.NotEmpty(t, prov["approved_at"])
	assert.NotEmpty(t, prov["updated_at"])
	assert.Equal(t, []any{"sales"}, prov["tags"])
	assert.Equal(t, "admin@example.com", prov["owner_email"])
}

func TestPromptUse_ByReferenceShareIsIDScoped(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report:sarah"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "sarah@example.com",
		Content: "sarah body", Enabled: true,
	}
	store.prompts["report:eve"] = &prompt.Prompt{
		ID: "p2", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "eve@example.com",
		Content: "eve body", Enabled: true,
	}
	// Eve shared HER prompt with bob; sarah shared nothing.
	h.shareStore = &stubShareLister{promptRefs: []portal.SharedPromptRef{
		{PromptID: "p2", ShareID: "s1", SharedBy: "eve@example.com", Permission: portal.PermissionViewer},
	}}
	ctx := userCtx("bob@example.com", "analyst")

	// The shared prompt resolves by its reference.
	parsed, res := callUse(ctx, t, h, managePromptInput{Name: "mcp:prompt:p2"})
	require.False(t, res.IsError)
	assert.Equal(t, "eve body", parsed.Content)

	// A same-named share grants nothing on a different prompt's reference.
	_, res = callUse(ctx, t, h, managePromptInput{Name: "mcp:prompt:p1"})
	assert.True(t, res.IsError, "share access is matched by prompt ID, not name")
}
