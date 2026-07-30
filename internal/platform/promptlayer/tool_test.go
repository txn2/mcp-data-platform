package promptlayer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// --- handleManagePrompt dispatch ---

func TestHandleManagePrompt_UnknownCommand(t *testing.T) {
	h, _ := newTestHandle()
	r, _, _ := h.handleManagePrompt(context.Background(), managePromptInput{Command: "bogus"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "unknown command")
}

// --- handlePromptCreate ---

func TestHandlePromptCreate_Success(t *testing.T) {
	h, store := newTestHandle()
	r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "my-prompt", Content: "hello {topic}", Scope: "global",
	})
	assert.False(t, r.IsError)
	assert.Contains(t, store.prompts, "my-prompt")
	assert.Equal(t, "global", store.prompts["my-prompt"].Scope)
}

// TestHandlePromptCreate_AdminSharedLandsApproved locks the #1124 lifecycle
// rule: the admin creating a shared prompt is its approver, so the prompt is
// born approved (and therefore searchable) with the approval stamped.
func TestHandlePromptCreate_AdminSharedLandsApproved(t *testing.T) {
	h, store := newTestHandle()
	for _, scope := range []string{prompt.ScopeGlobal, prompt.ScopePersona} {
		name := "shared-" + scope
		r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{
			Name: name, Content: "c", Scope: scope, Personas: []string{"analyst"},
		})
		require.False(t, r.IsError, resultText(r))
		created := store.prompts[name]
		assert.Equal(t, prompt.StatusApproved, created.Status, scope)
		assert.Equal(t, "admin@example.com", created.ApprovedBy, scope)
		require.NotNil(t, created.ApprovedAt, scope)
	}
}

// TestHandlePromptCreate_PersonalStaysDraft: personal prompts publish through
// the promotion flow, so a create at personal scope lands draft for admins and
// non-admins alike.
func TestHandlePromptCreate_PersonalStaysDraft(t *testing.T) {
	h, store := newTestHandle()
	r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "admin-personal", Content: "c", Scope: prompt.ScopePersonal,
	})
	require.False(t, r.IsError)
	assert.Equal(t, prompt.StatusDraft, store.prompts["admin-personal"].Status)
	assert.Empty(t, store.prompts["admin-personal"].ApprovedBy)

	r, _, _ = h.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "user-personal", Content: "c",
	})
	require.False(t, r.IsError)
	assert.Equal(t, prompt.StatusDraft, store.prompts["user-personal"].Status)
}

// --- collection placement (#1124) ---

func TestHandlePromptCreate_WithCollection(t *testing.T) {
	h, store := newTestCollectionHandle()
	store.collections["col1"] = &prompt.Collection{ID: "col1", Name: "Sales"}

	r, _, _ := h.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "placed", Content: "c", CollectionID: new("col1"),
	})
	require.False(t, r.IsError, resultText(r))
	assert.Equal(t, "col1", store.prompts["placed"].CollectionID)
}

func TestHandlePromptCreate_UnknownCollection(t *testing.T) {
	h, store := newTestCollectionHandle()
	r, _, _ := h.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "placed", Content: "c", CollectionID: new("nope"),
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not found")
	assert.NotContains(t, store.prompts, "placed", "a bad collection id must not create a half-placed prompt")
}

func TestHandlePromptCreate_CollectionsUnavailable(t *testing.T) {
	h, _ := newTestHandle() // plain store: no collection capability
	r, _, _ := h.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "placed", Content: "c", CollectionID: new("col1"),
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not available")
}

func TestHandlePromptUpdate_AssignAndClearCollection(t *testing.T) {
	h, store := newTestCollectionHandle()
	store.collections["col1"] = &prompt.Collection{ID: "col1", Name: "Sales"}
	store.prompts["mine"] = &prompt.Prompt{
		ID: "p1", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com",
		Content: "c", Enabled: true,
	}
	ctx := userCtx("user@example.com", "analyst")

	r, _, _ := h.handlePromptUpdate(ctx, managePromptInput{Name: "mine", CollectionID: new("col1")})
	require.False(t, r.IsError, resultText(r))
	assert.Equal(t, "col1", store.prompts["mine"].CollectionID)

	r, _, _ = h.handlePromptUpdate(ctx, managePromptInput{Name: "mine", CollectionID: new("")})
	require.False(t, r.IsError, resultText(r))
	assert.Empty(t, store.prompts["mine"].CollectionID)
}

func TestHandlePromptUpdate_UnknownCollection(t *testing.T) {
	h, store := newTestCollectionHandle()
	store.prompts["mine"] = &prompt.Prompt{
		ID: "p1", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com",
		Content: "c", Enabled: true,
	}
	r, _, _ := h.handlePromptUpdate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "mine", CollectionID: new("nope"),
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not found")
}

func TestHandlePromptUpdate_CollectionStoreError(t *testing.T) {
	h, store := newTestCollectionHandle()
	store.setErr = errors.New("pq: down")
	store.prompts["mine"] = &prompt.Prompt{
		ID: "p1", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com",
		Content: "c", Enabled: true,
	}
	r, _, _ := h.handlePromptUpdate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "mine", CollectionID: new(""),
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "failed to assign collection")
	assert.NotContains(t, resultText(r), "pq:")
}

func TestHandlePromptCreate_InvalidName(t *testing.T) {
	h, _ := newTestHandle()
	r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "INVALID NAME!", Content: "content",
	})
	assert.True(t, r.IsError)
}

func TestHandlePromptCreate_MissingContent(t *testing.T) {
	h, _ := newTestHandle()
	r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{Name: "test"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "content is required")
}

func TestHandlePromptCreate_InvalidScope(t *testing.T) {
	h, _ := newTestHandle()
	r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "test", Content: "c", Scope: "invalid",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "invalid scope")
}

func TestHandlePromptCreate_NonAdminDeniedGlobalScope(t *testing.T) {
	h, _ := newTestHandle()
	r, _, _ := h.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "test", Content: "c", Scope: "global",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "only admins")
}

func TestHandlePromptCreate_NonAdminPersonalOK(t *testing.T) {
	h, store := newTestHandle()
	r, _, _ := h.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "my-personal", Content: "content",
	})
	assert.False(t, r.IsError)
	assert.Equal(t, prompt.ScopePersonal, store.prompts["my-personal"].Scope)
	assert.Equal(t, "user@example.com", store.prompts["my-personal"].OwnerEmail)
}

func TestHandlePromptCreate_NilPersonasDefaultsToEmpty(t *testing.T) {
	h, store := newTestHandle()
	r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "no-personas", Content: "content", Scope: "personal",
		// Personas intentionally omitted (nil)
	})
	assert.False(t, r.IsError)
	assert.Equal(t, []string{}, store.prompts["no-personas"].Personas)
}

func TestHandlePromptCreate_StoreErrorDoesNotLeakToNonAdmin(t *testing.T) {
	h, store := newTestHandle()
	store.createErr = fmt.Errorf("pq: null value in column \"personas\" violates not-null constraint (23502)")
	// A non-admin creating a personal prompt reaches the store; the raw DB error
	// (which carries SQL/schema detail) must not leak to a non-admin caller.
	r, _, _ := h.handlePromptCreate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "test", Content: "content",
	})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to create prompt")
	assert.NotContains(t, text, "pq:")
	assert.NotContains(t, text, "23502")
}

func TestHandlePromptCreate_StoreErrorAdminSeesDetail(t *testing.T) {
	h, store := newTestHandle()
	store.createErr = fmt.Errorf("pq: null value in column \"personas\" violates not-null constraint (23502)")
	// Admins are platform operators: they get the underlying error to diagnose
	// failures (admin-gated, per the self-describing error contract).
	r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "test", Content: "content",
	})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to create prompt")
	assert.Contains(t, text, "pq:")
	assert.Contains(t, text, "23502")
}

func TestHandlePromptCreate_StoreError(t *testing.T) {
	h, store := newTestHandle()
	store.createErr = fmt.Errorf("db down")
	r, _, _ := h.handlePromptCreate(adminCtx(), managePromptInput{
		Name: "test", Content: "content",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "failed to create")
}

// --- handlePromptUpdate ---

func TestHandlePromptUpdate_Success(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["old"] = &prompt.Prompt{
		ID: "id-1", Name: "old", Content: "old-content",
		Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com", Enabled: true,
	}
	r, _, _ := h.handlePromptUpdate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "old", Content: "new-content",
	})
	assert.False(t, r.IsError)
	assert.Equal(t, "new-content", store.prompts["old"].Content)
}

func TestHandlePromptUpdate_NotFound(t *testing.T) {
	h, _ := newTestHandle()
	r, _, _ := h.handlePromptUpdate(adminCtx(), managePromptInput{Name: "missing"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not found")
}

func TestHandlePromptUpdate_NonAdminDeniedNonPersonal(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["global"] = &prompt.Prompt{
		ID: "id-1", Name: "global", Scope: prompt.ScopeGlobal,
	}
	r, _, _ := h.handlePromptUpdate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "global", Content: "hacked",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "non-admins")
}

func TestHandlePromptUpdate_NonAdminDeniedOtherUser(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["other"] = &prompt.Prompt{
		ID: "id-1", Name: "other", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com",
	}
	r, _, _ := h.handlePromptUpdate(userCtx("alice@example.com", "analyst"), managePromptInput{
		Name: "other", Content: "hacked",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "your own")
}

func TestHandlePromptUpdate_ScopeChangeByNonAdmin(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["mine"] = &prompt.Prompt{
		ID: "id-1", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com",
	}
	r, _, _ := h.handlePromptUpdate(userCtx("user@example.com", "analyst"), managePromptInput{
		Name: "mine", Scope: "global",
	})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "only admins")
}

func TestHandlePromptUpdate_StoreGetError(t *testing.T) {
	h, store := newTestHandle()
	store.getErr = fmt.Errorf("pq: connection refused")
	r, _, _ := h.handlePromptUpdate(adminCtx(), managePromptInput{Name: "test", Content: "c"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to get prompt")
	assert.Contains(t, text, "pq:") // admin sees detail (admin-gated error contract)
}

func TestHandlePromptUpdate_StoreUpdateError(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["test"] = &prompt.Prompt{
		ID: "id-1", Name: "test", Scope: prompt.ScopeGlobal,
	}
	store.updateErr = fmt.Errorf("pq: disk full")
	r, _, _ := h.handlePromptUpdate(adminCtx(), managePromptInput{Name: "test", Content: "c"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to update prompt")
	assert.Contains(t, text, "pq:") // admin sees detail (admin-gated error contract)
}

// --- handlePromptDelete ---

func TestHandlePromptDelete_Success(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["del"] = &prompt.Prompt{
		ID: "id-1", Name: "del", Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com",
	}
	r, _, _ := h.handlePromptDelete(userCtx("user@example.com", "analyst"), managePromptInput{Name: "del"})
	assert.False(t, r.IsError)
	assert.NotContains(t, store.prompts, "del")
}

func TestHandlePromptDelete_NotFound(t *testing.T) {
	h, _ := newTestHandle()
	r, _, _ := h.handlePromptDelete(adminCtx(), managePromptInput{Name: "missing"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not found")
}

func TestHandlePromptDelete_NonAdminDeniedNonPersonal(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["global"] = &prompt.Prompt{
		ID: "id-1", Name: "global", Scope: prompt.ScopeGlobal,
	}
	r, _, _ := h.handlePromptDelete(userCtx("user@example.com", "analyst"), managePromptInput{Name: "global"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "non-admins")
}

func TestHandlePromptDelete_StoreGetError(t *testing.T) {
	h, store := newTestHandle()
	store.getErr = fmt.Errorf("pq: timeout")
	r, _, _ := h.handlePromptDelete(adminCtx(), managePromptInput{Name: "test"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to get prompt")
	assert.Contains(t, text, "pq:") // admin sees detail (admin-gated error contract)
}

func TestHandlePromptDelete_StoreDeleteError(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["test"] = &prompt.Prompt{
		ID: "id-1", Name: "test", Scope: prompt.ScopeGlobal,
	}
	store.deleteErr = fmt.Errorf("pq: constraint violation")
	r, _, _ := h.handlePromptDelete(adminCtx(), managePromptInput{Name: "test"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to delete prompt")
	assert.Contains(t, text, "pq:") // admin sees detail (admin-gated error contract)
}

// --- handlePromptList ---

func TestHandlePromptList_Admin(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["a"] = &prompt.Prompt{ID: "1", Name: "a", Scope: prompt.ScopeGlobal, Enabled: true}
	store.prompts["b"] = &prompt.Prompt{ID: "2", Name: "b", Scope: prompt.ScopePersonal, Enabled: true, OwnerEmail: "u@x.com"}
	r, _, _ := h.handlePromptList(adminCtx(), managePromptInput{Command: "list"})
	assert.False(t, r.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &resp))
	countVal, _ := resp["count"].(float64)
	count := int(countVal)
	assert.Equal(t, 2, count)
}

func TestHandlePromptList_NonAdminNoScope(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["personal"] = &prompt.Prompt{
		ID: "1", Name: "personal", Scope: prompt.ScopePersonal, Enabled: true, OwnerEmail: "user@example.com",
		Status: prompt.StatusDraft,
	}
	store.prompts["global"] = &prompt.Prompt{
		ID: "2", Name: "global", Scope: prompt.ScopeGlobal, Enabled: true, Status: prompt.StatusApproved,
	}
	// Another owner's draft shared prompt is invisible to non-admins, matching
	// ranked search; the caller's own prompts are visible at any scope and
	// status (#1124).
	store.prompts["draft-global"] = &prompt.Prompt{
		ID: "3", Name: "draft-global", Scope: prompt.ScopeGlobal, Enabled: true, Status: prompt.StatusDraft,
		OwnerEmail: "someone-else@example.com",
	}
	store.prompts["my-deprecated-global"] = &prompt.Prompt{
		ID: "4", Name: "my-deprecated-global", Scope: prompt.ScopeGlobal, Enabled: true,
		Status: prompt.StatusDeprecated, OwnerEmail: "user@example.com",
	}
	r, _, _ := h.handlePromptList(userCtx("user@example.com", "analyst"), managePromptInput{Command: "list"})
	assert.False(t, r.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &resp))
	countVal, _ := resp["count"].(float64)
	count := int(countVal)
	assert.Equal(t, 3, count) // own personal (draft ok) + own deprecated global + approved global
	assert.Equal(t, "user@example.com", resp["caller_email"])
}

func TestHandlePromptList_NonAdminWithScope(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["g1"] = &prompt.Prompt{ID: "1", Name: "g1", Scope: prompt.ScopeGlobal, Enabled: true, Status: prompt.StatusApproved}
	store.prompts["g2-draft"] = &prompt.Prompt{
		ID: "3", Name: "g2-draft", Scope: prompt.ScopeGlobal, Enabled: true, Status: prompt.StatusDraft,
		OwnerEmail: "someone-else@example.com",
	}
	store.prompts["g3-mine-draft"] = &prompt.Prompt{
		ID: "4", Name: "g3-mine-draft", Scope: prompt.ScopeGlobal, Enabled: true, Status: prompt.StatusDraft,
		OwnerEmail: "user@example.com",
	}
	store.prompts["p1"] = &prompt.Prompt{ID: "2", Name: "p1", Scope: prompt.ScopePersonal, Enabled: true, OwnerEmail: "user@example.com"}
	r, _, _ := h.handlePromptList(userCtx("user@example.com", "analyst"), managePromptInput{
		Command: "list", Scope: "global",
	})
	assert.False(t, r.IsError)

	var resp map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(r)), &resp))
	countVal, _ := resp["count"].(float64)
	count := int(countVal)
	assert.Equal(t, 2, count) // the approved global + the caller's own draft global
}

func TestHandlePromptList_StoreError(t *testing.T) {
	h, store := newTestHandle()
	store.listErr = fmt.Errorf("pq: too many connections")
	r, _, _ := h.handlePromptList(adminCtx(), managePromptInput{Command: "list"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to list prompts")
	assert.Contains(t, text, "pq:") // admin sees detail (admin-gated error contract)
}

func TestPromptErrorDetail(t *testing.T) {
	h := &Handle{adminPersona: "admin"}
	cause := fmt.Errorf("pq: null value in column \"tags\" (23502)")

	t.Run("admin sees underlying detail", func(t *testing.T) {
		r := h.promptErrorDetail(adminCtx(), "failed to create prompt", cause)
		assert.True(t, r.IsError)
		text := resultText(r)
		assert.Contains(t, text, "failed to create prompt")
		assert.Contains(t, text, "pq:")
		assert.Contains(t, text, "23502")
	})

	t.Run("non-admin with request id gets breadcrumb only", func(t *testing.T) {
		pc := middleware.NewPlatformContext("req-xyz")
		pc.PersonaName = "analyst"
		pc.UserEmail = "user@example.com"
		ctx := middleware.WithPlatformContext(context.Background(), pc)
		r := h.promptErrorDetail(ctx, "failed to create prompt", cause)
		text := resultText(r)
		assert.Contains(t, text, "failed to create prompt")
		assert.Contains(t, text, "req-xyz")
		assert.NotContains(t, text, "pq:")
		assert.NotContains(t, text, "23502")
	})

	t.Run("non-admin without request id gets generic message", func(t *testing.T) {
		r := h.promptErrorDetail(userCtx("user@example.com", "analyst"), "failed to create prompt", cause)
		text := resultText(r)
		assert.Equal(t, "failed to create prompt", text)
		assert.NotContains(t, text, "pq:")
	})
}

// --- handlePromptGet ---

func TestHandlePromptGet_Found(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["test"] = &prompt.Prompt{
		ID: "id-1", Name: "test", Content: "content", Scope: prompt.ScopeGlobal,
	}
	r, _, _ := h.handlePromptGet(adminCtx(), managePromptInput{Name: "test"})
	assert.False(t, r.IsError)
	assert.Contains(t, resultText(r), "test")
}

func TestHandlePromptGet_NotFound(t *testing.T) {
	h, _ := newTestHandle()
	r, _, _ := h.handlePromptGet(adminCtx(), managePromptInput{Name: "missing"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "not found")
}

func TestHandlePromptGet_NonAdminDeniedOtherPersonal(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["secret"] = &prompt.Prompt{
		ID: "id-1", Name: "secret", Scope: prompt.ScopePersonal, OwnerEmail: "bob@example.com",
	}
	r, _, _ := h.handlePromptGet(userCtx("alice@example.com", "engineer"), managePromptInput{Name: "secret"})
	assert.True(t, r.IsError)
	assert.Contains(t, resultText(r), "your own")
}

func TestHandlePromptGet_StoreError(t *testing.T) {
	h, store := newTestHandle()
	store.getErr = fmt.Errorf("pq: connection reset")
	r, _, _ := h.handlePromptGet(adminCtx(), managePromptInput{Name: "test"})
	assert.True(t, r.IsError)
	text := resultText(r)
	assert.Contains(t, text, "failed to get prompt")
	assert.Contains(t, text, "pq:") // admin sees detail (admin-gated error contract)
}

// --- applyPromptUpdates ---

func TestApplyPromptUpdates(t *testing.T) {
	existing := &prompt.Prompt{Name: "test", Scope: prompt.ScopePersonal}
	msg := applyPromptUpdates(existing, managePromptInput{
		DisplayName: "New Display",
		Description: "New Desc",
		Content:     "New Content",
		Category:    "cat",
		Arguments:   []prompt.Argument{{Name: "a"}},
		Personas:    []string{"analyst"},
	}, true)
	assert.Empty(t, msg)
	assert.Equal(t, "New Display", existing.DisplayName)
	assert.Equal(t, "New Desc", existing.Description)
	assert.Equal(t, "New Content", existing.Content)
	assert.Equal(t, "cat", existing.Category)
	assert.Len(t, existing.Arguments, 1)
	assert.Equal(t, []string{"analyst"}, existing.Personas)
}

// --- schema and helpers ---

func TestManagePromptSchema(t *testing.T) {
	schema := managePromptSchema()
	assert.NotNil(t, schema)

	m, ok := schema.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "object", m["type"])

	props, ok := m["properties"].(map[string]any)
	assert.True(t, ok)
	assert.Contains(t, props, "command")
	assert.Contains(t, props, "name")
	assert.Contains(t, props, "content")
	assert.Contains(t, props, "scope")
	assert.Contains(t, props, "personas")
	assert.Contains(t, props, "search")

	required, ok := m["required"].([]string)
	assert.True(t, ok)
	assert.Contains(t, required, "command")
}

func TestPromptErrorResult(t *testing.T) {
	result := promptErrorResult("something went wrong")
	assert.True(t, result.IsError)
	assert.Len(t, result.Content, 1)
}

func TestPromptJSONResult(t *testing.T) {
	result, meta, err := promptJSONResult(map[string]string{"status": "ok"})
	assert.NoError(t, err)
	assert.Nil(t, meta)
	assert.False(t, result.IsError)
	assert.Len(t, result.Content, 1)
}

func TestResolveEmail_Anonymous(t *testing.T) {
	email := resolveEmail(t.Context())
	assert.Equal(t, "anonymous", email)
}

func TestResolveEmail_FromContext(t *testing.T) {
	email := resolveEmail(userCtx("alice@example.com", "analyst"))
	assert.Equal(t, "alice@example.com", email)
}

func TestIsAdminPersona_NoContext(t *testing.T) {
	h := &Handle{adminPersona: "admin"}
	assert.False(t, h.isAdminPersona(t.Context()))
}

func TestIsAdminPersona_AdminContext(t *testing.T) {
	h := &Handle{adminPersona: "admin"}
	assert.True(t, h.isAdminPersona(adminCtx()))
}

func TestIsBuiltinDisabled(t *testing.T) {
	tests := []struct {
		name     string
		config   map[string]bool
		prompt   string
		expected bool
	}{
		{"nil map", nil, "explore-available-data", false},
		{"not in map", map[string]bool{}, "explore-available-data", false},
		{"enabled", map[string]bool{"explore-available-data": true}, "explore-available-data", false},
		{"disabled", map[string]bool{"explore-available-data": false}, "explore-available-data", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handle{builtinPrompts: tt.config}
			assert.Equal(t, tt.expected, h.isBuiltinDisabled(tt.prompt))
		})
	}
}

func TestCheckPromotionNameFree(t *testing.T) {
	h, store := newTestHandle()
	ctx := context.Background()

	// Not a promotion: old scope is not personal.
	assert.Empty(t, h.checkPromotionNameFree(ctx,
		&prompt.Prompt{ID: "g", Name: "x", Scope: prompt.ScopeGlobal}, prompt.ScopeGlobal))

	// Not a promotion: still personal.
	assert.Empty(t, h.checkPromotionNameFree(ctx,
		&prompt.Prompt{ID: "p", Name: "x", Scope: prompt.ScopePersonal}, prompt.ScopePersonal))

	// Promotion, name free in the shared namespace.
	assert.Empty(t, h.checkPromotionNameFree(ctx,
		&prompt.Prompt{ID: "p1", Name: "free", Scope: prompt.ScopeGlobal}, prompt.ScopePersonal))

	// Promotion, name already owned by a different shared prompt.
	store.prompts["taken"] = &prompt.Prompt{ID: "g1", Name: "taken", Scope: prompt.ScopeGlobal}
	msg := h.checkPromotionNameFree(ctx,
		&prompt.Prompt{ID: "p1", Name: "taken", Scope: prompt.ScopeGlobal}, prompt.ScopePersonal)
	assert.Contains(t, msg, "already used")
}

// resolveManagedPrompt prefers the caller's personal prompt by default, but an
// explicit shared scope targets the global/persona prompt of the same name.
func TestResolveManagedPrompt_ScopePreference(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{ID: "g", Name: "report", Scope: prompt.ScopeGlobal, Content: "global"}
	store.prompts["personal:report"] = &prompt.Prompt{
		ID: "p", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: "admin@x", Content: "personal",
	}

	ctx := context.Background()

	got, err := h.resolveManagedPrompt(ctx, "report", "admin@x", "", "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "p", got.ID, "default resolution prefers the caller's personal prompt")

	got, err = h.resolveManagedPrompt(ctx, "report", "admin@x", prompt.ScopeGlobal, "")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "g", got.ID, "explicit global scope targets the shared prompt")
}

// manage_prompt update with requested_scope flags the owner's personal prompt
// for the admin promotion queue without changing its scope.
func TestManagePrompt_RequestPromotion(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "id1", Name: "report", Scope: prompt.ScopePersonal,
		OwnerEmail: "u@x", Content: "x", Enabled: true,
	}

	ctx := userCtx("u@x", "analyst")
	_, _, err := h.handleManagePrompt(ctx, managePromptInput{
		Command: "update", Name: "report",
		RequestedScope: prompt.ScopePersona, RequestedPersonas: []string{"analyst"},
	})
	require.NoError(t, err)

	got := store.prompts["report"]
	assert.True(t, got.ReviewRequested, "promotion request flag set")
	assert.Equal(t, prompt.ScopePersona, got.RequestedScope)
	assert.Equal(t, []string{"analyst"}, got.RequestedPersonas)
	assert.Equal(t, prompt.ScopePersonal, got.Scope, "scope must not change on request")
}

// A non-personal prompt cannot be flagged for promotion.
func TestManagePrompt_RequestPromotion_RejectsNonPersonal(t *testing.T) {
	h, store := newTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "id1", Name: "report", Scope: prompt.ScopeGlobal, Content: "x", Enabled: true,
	}

	res, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{
		Command: "update", Name: "report", RequestedScope: prompt.ScopePersona, RequestedPersonas: []string{"analyst"},
	})
	require.NoError(t, err)
	assert.True(t, res.IsError, "expected an error result for non-personal promotion request")
}
