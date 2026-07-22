package promptlayer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/audit"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// --- capturing audit logger ---

type capturingAuditLogger struct {
	events []middleware.AuditEvent
}

func (l *capturingAuditLogger) Log(_ context.Context, ev middleware.AuditEvent) error {
	l.events = append(l.events, ev)
	return nil
}

// --- version-capable mock store ---

// mockVersionStore extends the in-memory prompt store with a recording
// versioning capability, mirroring the postgres store's shape.
type mockVersionStore struct {
	*mockPromptStore
	drafts      map[string][]prompt.Version // by prompt id
	nextVersion int
	versionErr  error
}

func newMockVersionStore() *mockVersionStore {
	return &mockVersionStore{
		mockPromptStore: newMockPromptStore(),
		drafts:          map[string][]prompt.Version{},
		nextVersion:     2,
	}
}

// The read methods return copies, mirroring the real store (a row scanned from
// the database is never aliased to the stored state): a handler mutating its
// loaded prompt must not change what the store serves until a write lands.
func clonePrompt(p *prompt.Prompt) *prompt.Prompt {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}

func (m *mockVersionStore) Get(ctx context.Context, name string) (*prompt.Prompt, error) {
	p, err := m.mockPromptStore.Get(ctx, name)
	return clonePrompt(p), err
}

func (m *mockVersionStore) GetPersonal(ctx context.Context, ownerEmail, name string) (*prompt.Prompt, error) {
	p, err := m.mockPromptStore.GetPersonal(ctx, ownerEmail, name)
	return clonePrompt(p), err
}

func (m *mockVersionStore) GetByID(ctx context.Context, id string) (*prompt.Prompt, error) {
	p, err := m.mockPromptStore.GetByID(ctx, id)
	return clonePrompt(p), err
}

func (m *mockVersionStore) UpdateWithVersion(ctx context.Context, p *prompt.Prompt, _ string) error {
	if m.versionErr != nil {
		return m.versionErr
	}
	p.Version = m.nextVersion
	return m.Update(ctx, p)
}

func (m *mockVersionStore) CreateDraftVersion(_ context.Context, promptID string, proposed *prompt.Prompt, author string) (int, error) {
	n := m.nextVersion
	m.drafts[promptID] = append(m.drafts[promptID], prompt.Version{
		PromptID: promptID, Version: n, Content: proposed.Content,
		Author: author, Status: prompt.VersionStatusDraft,
	})
	return n, nil
}

func (m *mockVersionStore) ListVersions(_ context.Context, promptID string) ([]prompt.Version, error) {
	return m.drafts[promptID], nil
}

func (m *mockVersionStore) GetVersion(_ context.Context, promptID string, version int) (*prompt.Version, error) {
	for _, v := range m.drafts[promptID] {
		if v.Version == version {
			return &v, nil
		}
	}
	return nil, nil //nolint:nilnil // interface contract
}

func (m *mockVersionStore) ApproveVersion(ctx context.Context, promptID string, version int, approver string) (*prompt.Prompt, error) {
	if m.versionErr != nil {
		return nil, m.versionErr
	}
	v, _ := m.GetVersion(ctx, promptID, version)
	p, _ := m.GetByID(ctx, promptID)
	now := time.Now().UTC()
	p.Content = v.Content
	p.Version = version
	p.Status = prompt.StatusApproved
	p.ApprovedBy = approver
	p.ApprovedAt = &now
	return p, nil
}

func (*mockVersionStore) RejectVersion(context.Context, string, int) error { return nil }

var _ prompt.VersionStore = (*mockVersionStore)(nil)

// newVersionedTestHandle builds a Handle over a version-capable store.
func newVersionedTestHandle() (*Handle, *mockVersionStore) {
	h, _ := newTestHandle()
	store := newMockVersionStore()
	h.store = store
	return h, store
}

// A content edit to an approved global prompt through manage_prompt does not
// touch the live row: it returns pending_approval with the draft version, and
// the store still serves the approved content.
func TestManagePrompt_Update_ApprovedSharedContentEditPends(t *testing.T) {
	h, store := newVersionedTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "approved body",
		Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}

	res, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{
		Command: "update", Name: "report", Content: "proposed body",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out))
	assert.Equal(t, "pending_approval", out["status"])
	assert.Equal(t, float64(2), out["pending_version"])
	assert.Contains(t, out["message"], "draft version 2")

	assert.Equal(t, "approved body", store.prompts["report"].Content,
		"the live row keeps serving the approved snapshot")
	require.Len(t, store.drafts["p1"], 1)
	assert.Equal(t, "proposed body", store.drafts["p1"][0].Content)
	assert.Equal(t, "admin@example.com", store.drafts["p1"][0].Author)
}

// A gated content edit combined with a non-versioned change is rejected whole.
func TestManagePrompt_Update_MixedGatedEditRejected(t *testing.T) {
	h, store := newVersionedTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "approved body",
		Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}

	res, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{
		Command: "update", Name: "report", Content: "proposed body", Status: prompt.StatusDeprecated,
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(res), "separate updates")
	assert.Equal(t, "approved body", store.prompts["report"].Content)
	assert.Empty(t, store.drafts["p1"], "no draft is created for a rejected mixed edit")
}

// A metadata-only edit to an approved shared prompt applies directly through
// the versioned update.
func TestManagePrompt_Update_MetadataEditAppliesDirectly(t *testing.T) {
	h, store := newVersionedTestHandle()
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "approved body",
		Description: "old", Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}

	res, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{
		Command: "update", Name: "report", Description: "new description",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out))
	assert.Equal(t, "updated", out["status"])
	assert.Equal(t, float64(2), out["version"], "the applied edit advances the version")
	assert.Equal(t, "new description", store.prompts["report"].Description)
}

// A personal prompt's content edit applies silently (no review), still
// through the versioned update.
func TestManagePrompt_Update_PersonalContentEditAppliesSilently(t *testing.T) {
	h, store := newVersionedTestHandle()
	store.prompts["mine"] = &prompt.Prompt{
		ID: "p2", Name: "mine", Scope: prompt.ScopePersonal, OwnerEmail: "user@example.com",
		Content: "v1 body", Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}

	res, _, err := h.handleManagePrompt(userCtx("user@example.com", "analyst"), managePromptInput{
		Command: "update", Name: "mine", Content: "v2 body",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))
	assert.Equal(t, "v2 body", store.prompts["mine"].Content)
	assert.Empty(t, store.drafts["p2"])
}

// prompts/get on a database prompt records a prompt_serve audit event carrying
// the prompt id and version, and stamps provenance into the result _meta.
func TestGetByName_EmitsServeAuditAndProvenanceMeta(t *testing.T) {
	h, store := newTestHandle()
	logger := &capturingAuditLogger{}
	h.SetAuditLogger(logger)
	approvedAt := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "body {x}",
		Status: prompt.StatusApproved, ApprovedBy: "jane@example.com", ApprovedAt: &approvedAt,
		Enabled: true, Version: 4,
	}

	res, ok := h.GetByName(context.Background(), "bob@example.com", nil, "global-report", map[string]string{"x": "Y"})
	require.True(t, ok)

	assert.Equal(t, 4, res.Meta["prompt_version"])
	assert.Equal(t, "mcp:prompt:p1", res.Meta["prompt_reference"])
	assert.Equal(t, "jane@example.com", res.Meta["prompt_approved_by"])
	assert.Equal(t, "2026-06-12T10:00:00Z", res.Meta["prompt_approved_at"])

	require.Len(t, logger.events, 1)
	ev := logger.events[0]
	assert.Equal(t, string(audit.EventTypePromptServe), ev.EventKind)
	assert.Equal(t, serveSurfacePromptsGet, ev.ToolName)
	assert.Equal(t, "bob@example.com", ev.UserEmail, "prompts/get has no PlatformContext; the resolved email backstops")
	assert.Equal(t, "p1", ev.Parameters["prompt_id"])
	assert.Equal(t, 4, ev.Parameters["version"])
	assert.True(t, ev.Success)
}

// A miss emits nothing.
func TestGetByName_MissEmitsNoServeEvent(t *testing.T) {
	h, _ := newTestHandle()
	logger := &capturingAuditLogger{}
	h.SetAuditLogger(logger)

	_, ok := h.GetByName(context.Background(), "bob@example.com", nil, "global-missing", nil)
	assert.False(t, ok)
	assert.Empty(t, logger.events)
}

// manage_prompt use records the serve event with the caller identity from the
// platform context and reports the version in the provenance block.
func TestUse_EmitsServeAuditWithVersionProvenance(t *testing.T) {
	h, store := newTestHandle()
	logger := &capturingAuditLogger{}
	h.SetAuditLogger(logger)
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "body",
		Status: prompt.StatusApproved, Enabled: true, Version: 3,
	}

	res, _, err := h.handleManagePrompt(userCtx("bob@example.com", "analyst"), managePromptInput{
		Command: cmdUse, Name: "report",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, resultText(res))

	var out map[string]any
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out))
	prov, ok := out["prompt"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(3), prov["version"])

	require.Len(t, logger.events, 1)
	ev := logger.events[0]
	assert.Equal(t, string(audit.EventTypePromptServe), ev.EventKind)
	assert.Equal(t, serveSurfaceUse, ev.ToolName)
	assert.Equal(t, "bob@example.com", ev.UserEmail)
	assert.Equal(t, "analyst", ev.Persona)
	assert.Equal(t, 3, ev.Parameters["version"])
}

// --- usage reader ---

type stubUsageReader struct {
	usage map[string]prompt.Usage
	err   error
}

func (s *stubUsageReader) PromptUsage(_ context.Context, _ []string) (map[string]prompt.Usage, error) {
	return s.usage, s.err
}

// manage_prompt get populates the audit-derived usage fields when a usage
// reader is bound, and leaves them zero when the prompt was never served.
func TestManagePrompt_Get_PopulatesUsage(t *testing.T) {
	h, store := newTestHandle()
	last := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	h.SetUsageReader(&stubUsageReader{usage: map[string]prompt.Usage{
		"p1": {RunCount: 37, LastRunAt: &last},
	}})
	store.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "body", Enabled: true,
	}
	store.prompts["unused"] = &prompt.Prompt{
		ID: "p2", Name: "unused", Scope: prompt.ScopeGlobal, Content: "body", Enabled: true,
	}

	res, _, err := h.handleManagePrompt(adminCtx(), managePromptInput{Command: "get", Name: "report"})
	require.NoError(t, err)
	var out prompt.Prompt
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &out))
	assert.Equal(t, int64(37), out.RunCount)
	require.NotNil(t, out.LastRunAt)

	res, _, err = h.handleManagePrompt(adminCtx(), managePromptInput{Command: "get", Name: "unused"})
	require.NoError(t, err)
	var never prompt.Prompt
	require.NoError(t, json.Unmarshal([]byte(resultText(res)), &never))
	assert.Zero(t, never.RunCount, "a never-served prompt reports zero usage")
	assert.Nil(t, never.LastRunAt)
}

// --- wrapper capability forwarding ---

// The notifying wrapper preserves the versioning capability and fires
// list_changed on the version writes that change served content, but not on
// draft creation (which changes nothing served).
func TestWrapStore_ForwardsVersionCapabilityAndNotifies(t *testing.T) {
	base := newMockVersionStore()
	base.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopeGlobal, Content: "body",
		Status: prompt.StatusApproved, Enabled: true, Version: 1,
	}
	notified := 0
	wrapped := wrapStore(base, func() { notified++ })

	vs, ok := wrapped.(prompt.VersionStore)
	require.True(t, ok, "the wrapper preserves the versioning capability")

	_, err := vs.CreateDraftVersion(context.Background(), "p1", base.prompts["report"], "a@example.com")
	require.NoError(t, err)
	assert.Zero(t, notified, "a draft changes nothing served: no notification")

	require.NoError(t, vs.UpdateWithVersion(context.Background(), base.prompts["report"], "a@example.com"))
	assert.Equal(t, 1, notified, "an applied versioned update notifies")

	_, err = vs.ApproveVersion(context.Background(), "p1", 2, "admin@example.com")
	require.NoError(t, err)
	assert.Equal(t, 2, notified, "an approved draft changes served content: notifies")
}

// Version-write failures pass through the notifying wrapper unwrapped and
// fire no notification.
func TestWrapStore_VersionWriteErrorsDoNotNotify(t *testing.T) {
	base := newMockVersionStore()
	base.versionErr = errors.New("db down")
	notified := 0
	vs, ok := wrapStore(base, func() { notified++ }).(prompt.VersionStore)
	require.True(t, ok)

	err := vs.UpdateWithVersion(context.Background(), &prompt.Prompt{ID: "p1"}, "a@example.com")
	assert.ErrorIs(t, err, base.versionErr)
	_, err = vs.ApproveVersion(context.Background(), "p1", 2, "admin@example.com")
	assert.ErrorIs(t, err, base.versionErr)
	assert.Zero(t, notified)
}

// A usage-reader failure leaves the prompt's usage fields empty instead of
// failing the read.
func TestApplyUsage_ReaderErrorLeavesFieldsEmpty(t *testing.T) {
	h, _ := newTestHandle()
	h.SetUsageReader(&stubUsageReader{err: errors.New("db down")})
	pr := &prompt.Prompt{ID: "p1", Name: "report"}
	h.applyUsage(context.Background(), pr)
	assert.Zero(t, pr.RunCount)
	assert.Nil(t, pr.LastRunAt)
}

// A search-only base (the pre-versioning shape) still round-trips without the
// capability, and a plain base yields neither extension.
func TestWrapStore_CapabilityMatrix(t *testing.T) {
	plain := wrapStore(newMockPromptStore(), func() {})
	_, hasVersions := plain.(prompt.VersionStore)
	assert.False(t, hasVersions)
	_, hasSearch := plain.(prompt.Searcher)
	assert.False(t, hasSearch)

	versioned := wrapStore(newMockVersionStore(), func() {})
	_, hasVersions = versioned.(prompt.VersionStore)
	assert.True(t, hasVersions)
	_, hasSearch = versioned.(prompt.Searcher)
	assert.False(t, hasSearch)
}
