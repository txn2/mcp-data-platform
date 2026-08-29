package promptlayer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/prompt/attachserve"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// The attachment tests below run through the real assembled prompt path per the
// CLAUDE.md standard: a live mcp.Server, the real prompt-visibility middleware
// (which is what resolves the caller and puts a PlatformContext on the context
// the resolver reads), the real resolver over real stores, and a client session
// making genuine prompts/get and tools/call requests. A unit test that handed
// the resolver a pre-built claims struct would prove nothing about whether the
// caller's identity actually reaches it.

// attachStore is an in-memory prompt.AttachmentStore for the integration tests.
type attachStore struct {
	links map[string][]prompt.Attachment
}

func (a *attachStore) Attach(_ context.Context, at prompt.Attachment) error {
	a.links[at.PromptID] = append(a.links[at.PromptID], at)
	return nil
}
func (*attachStore) Detach(context.Context, string, string) error { return nil }

func (a *attachStore) ListByPrompt(_ context.Context, id string) ([]prompt.Attachment, error) {
	return a.links[id], nil
}

func (*attachStore) ListByResource(context.Context, string) ([]string, error) { return nil, nil }
func (*attachStore) Reorder(context.Context, string, []string) error          { return nil }

// resStore is an in-memory resource.Store.
type resStore struct {
	byID   map[string]*resource.Resource
	getErr error
}

func (*resStore) Insert(context.Context, resource.Resource) error { return nil }

func (r *resStore) Get(_ context.Context, id string) (*resource.Resource, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	res, ok := r.byID[id]
	if !ok {
		// The Postgres store reports a missing row as a wrapped
		// sql.ErrNoRows, not as (nil, nil); a fake that returned nil would
		// let broken-link handling pass here and fail in production.
		return nil, fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
	}
	return res, nil
}

func (*resStore) GetByIDs(context.Context, []string) (map[string]*resource.Resource, error) {
	return map[string]*resource.Resource{}, nil
}

func (*resStore) GetByURI(context.Context, string) (*resource.Resource, error) {
	return nil, nil //nolint:nilnil // interface contract: not-found is (nil, nil)
}

func (*resStore) List(context.Context, resource.Filter) ([]resource.Resource, int, error) {
	return nil, 0, nil
}
func (*resStore) Update(context.Context, string, resource.Update) error { return nil }
func (*resStore) Move(context.Context, []resource.Move) error {
	return errors.New("resStore does not move resources")
}
func (*resStore) Delete(context.Context, string) error { return nil }

// blobStore is an in-memory blob backend.
type blobStore struct{ byKey map[string]string }

func (b *blobStore) GetObject(_ context.Context, _, key string) (body []byte, contentType string, err error) {
	text, ok := b.byKey[key]
	if !ok {
		return nil, "", errors.New("no such key")
	}
	return []byte(text), "", nil
}

const (
	sopOwner = "sarah@example.com"
	// servedPromptName is the scope-prefixed name the fixture prompt is served
	// under: it is global, so prompts/get resolves it as global-<name>.
	servedPromptName = "global-quarterly-review"
)

// recordingStore counts the writes that actually reach the backing store, so a
// test can prove the attachment guard refused *before* persisting rather than
// after. The in-memory mock hands back the same pointer the handler mutated, so
// inspecting the returned prompt cannot distinguish the two.
type recordingStore struct {
	*mockPromptStore
	updates int
}

func (r *recordingStore) Update(ctx context.Context, p *prompt.Prompt) error {
	r.updates++
	return r.mockPromptStore.Update(ctx, p)
}

// withAttachments wires a handle whose one prompt carries the given attachment
// links, over the standard resource fixture: a global markdown template, a
// global PNG, and an analyst-only rubric.
func withAttachments(t *testing.T, links []prompt.Attachment) *Handle {
	t.Helper()
	// Built through New so the store is the production-wrapped one: the
	// attachment guard lives on that wrapper, so a handle assembled by hand
	// would not exercise it.
	store := newMockPromptStore()
	h := New(Config{Store: store, AdminPersona: "admin", Registry: registry.NewRegistry()})
	pr := &prompt.Prompt{
		ID: "p1", Name: "quarterly-review", Scope: prompt.ScopeGlobal, Enabled: true, Status: prompt.StatusApproved,
		Description: "Produce the quarterly review", Content: "Write the quarterly review.",
	}
	store.prompts["quarterly-review"] = pr

	h.SetAttachmentResolver(attachserve.New(attachserve.Deps{
		Attachments: &attachStore{links: map[string][]prompt.Attachment{"p1": links}},
		Resources: &resStore{byID: map[string]*resource.Resource{
			"tpl": {
				ID: "tpl", Scope: resource.ScopeGlobal, DisplayName: "Quarterly Template",
				Description: "Fill every section", MIMEType: "text/markdown", SizeBytes: 30,
				S3Key: "k/tpl.md", URI: "mcp://global/templates/quarterly.md",
			},
			"logo": {
				ID: "logo", Scope: resource.ScopeGlobal, DisplayName: "Brand Header",
				MIMEType: "image/png", SizeBytes: 8192,
				S3Key: "k/logo.png", URI: "mcp://global/brand/header.png",
			},
			"rubric": {
				ID: "rubric", Scope: resource.ScopePersona, ScopeID: "analyst",
				DisplayName: "Analyst Rubric", MIMEType: "text/markdown", SizeBytes: 12,
				S3Key: "k/rubric.md", URI: "mcp://persona/analyst/rubric.md",
			},
		}},
		Blobs: &blobStore{byKey: map[string]string{
			"k/tpl.md":    "# Quarterly Review\n\n## Findings\n",
			"k/rubric.md": "rubric body",
		}},
		Bucket: "bkt",
	}))
	return h
}

// getPromptThroughServer runs a real prompts/get for one caller.
func getPromptThroughServer(t *testing.T, h *Handle) *mcp.GetPromptResult {
	t.Helper()
	session, cleanup := connectServingClient(t, h, sopOwner)
	defer cleanup()
	res, err := session.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: servedPromptName})
	require.NoError(t, err)
	return res
}

// TestPromptsGet_DeliversTemplateEmbeddedAndImageLinked is acceptance criterion
// 1: an author attaches a text template and a binary image, and a real
// prompts/get returns the template's contents embedded and the image as a
// resource link, after the prompt text.
func TestPromptsGet_DeliversTemplateEmbeddedAndImageLinked(t *testing.T) {
	h := withAttachments(t, []prompt.Attachment{
		{PromptID: "p1", ResourceID: "tpl", Position: 0},
		{PromptID: "p1", ResourceID: "logo", Position: 1},
	})

	res := getPromptThroughServer(t, h)
	require.Len(t, res.Messages, 4, "prompt text, framing, embedded template, linked image")

	body, ok := res.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "Write the quarterly review.", body.Text, "the procedure comes first, unchanged")

	framing, ok := res.Messages[1].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, framing.Text, "authoritative")

	embedded, ok := res.Messages[2].Content.(*mcp.EmbeddedResource)
	require.True(t, ok, "the text template must arrive as an embedded resource")
	assert.Equal(t, "# Quarterly Review\n\n## Findings\n", embedded.Resource.Text)
	assert.Equal(t, "text/markdown", embedded.Resource.MIMEType)
	assert.Equal(t, "mcp://global/templates/quarterly.md", embedded.Resource.URI)

	link, ok := res.Messages[3].Content.(*mcp.ResourceLink)
	require.True(t, ok, "the binary image must arrive as a resource link")
	assert.Equal(t, "mcp://global/brand/header.png", link.URI)
	assert.Equal(t, "Brand Header", link.Name)
	require.NotNil(t, link.Size)
	assert.Equal(t, int64(8192), *link.Size)
}

// TestPromptsGet_SurvivesTheWire is acceptance criterion 5: the result of a
// prompt carrying both content forms round-trips through the real protocol
// codec, which is what a schema violation would break.
func TestPromptsGet_SurvivesTheWire(t *testing.T) {
	h := withAttachments(t, []prompt.Attachment{
		{PromptID: "p1", ResourceID: "tpl"},
		{PromptID: "p1", ResourceID: "logo"},
	})
	// The client session already decoded this from the wire; re-encoding each
	// content item pins the emitted forms to the protocol's discriminators.
	res := getPromptThroughServer(t, h)

	types := make([]string, 0, len(res.Messages))
	for _, m := range res.Messages {
		raw, err := json.Marshal(m.Content)
		require.NoError(t, err)
		var wire struct {
			Type string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(raw, &wire))
		types = append(types, wire.Type)
	}
	assert.Equal(t, []string{"text", "text", "resource", "resource_link"}, types)
}

// TestPromptsGet_WithoutAttachmentsIsUnchanged is the compatibility claim: a
// prompt with no attachments serves exactly one message, as before.
func TestPromptsGet_WithoutAttachmentsIsUnchanged(t *testing.T) {
	h := withAttachments(t, nil)
	res := getPromptThroughServer(t, h)
	require.Len(t, res.Messages, 1)
}

// TestPromptsGet_WithoutResolverIsUnchanged covers a deployment with managed
// resources disabled: no resolver is bound and serving is untouched.
func TestPromptsGet_WithoutResolverIsUnchanged(t *testing.T) {
	store := newMockPromptStore()
	h := New(Config{Store: store, AdminPersona: "admin", Registry: registry.NewRegistry()})
	store.prompts["quarterly-review"] = &prompt.Prompt{
		ID: "p1", Name: "quarterly-review", Scope: prompt.ScopeGlobal, Enabled: true, Status: prompt.StatusApproved,
		Content: "Write the quarterly review.",
	}
	res := getPromptThroughServer(t, h)
	require.Len(t, res.Messages, 1)
}

// TestPromptsGet_WithholdsUnreadableAttachment is acceptance criterion 3
// end to end: a caller outside the analyst persona receives the prompt with the
// attachment noted as undelivered, not an error and not its contents.
//
// It also proves the identity plumbing works: the resolver only knows the
// caller is not an analyst because the visibility middleware put a
// PlatformContext on the context it passes down.
func TestPromptsGet_WithholdsUnreadableAttachment(t *testing.T) {
	h := withAttachments(t, []prompt.Attachment{{PromptID: "p1", ResourceID: "rubric"}})

	res := getPromptThroughServer(t, h)
	require.Len(t, res.Messages, 2, "the prompt text plus the withheld note")

	note, ok := res.Messages[1].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, note.Text, "not delivered")
	assert.Contains(t, note.Text, "not permitted to read")
	assert.NotContains(t, note.Text, "Analyst Rubric", "the name must not leak")
	assert.NotContains(t, note.Text, "rubric body", "the contents must not leak")
}

// TestPromptsGet_DeletedResourceDegradesGracefully is acceptance criterion 4:
// the prompt still serves and the missing material is noted.
func TestPromptsGet_DeletedResourceDegradesGracefully(t *testing.T) {
	h := withAttachments(t, []prompt.Attachment{{PromptID: "p1", ResourceID: "deleted-yesterday"}})

	res := getPromptThroughServer(t, h)
	require.Len(t, res.Messages, 2)
	body, ok := res.Messages[0].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Equal(t, "Write the quarterly review.", body.Text, "the procedure still serves")

	note, ok := res.Messages[1].Content.(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, note.Text, "no longer exist")
}

// useThroughTool runs a real manage_prompt use through an assembled server.
func useThroughTool(t *testing.T, h *Handle, name string) *mcp.CallToolResult {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0.0.0"}, nil)
	h.RegisterTool(server)
	// The tool path gets its identity from the PlatformContext the tool-call
	// middleware sets; stand in for it with the same context value the real
	// middleware writes.
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx = middleware.WithPlatformContext(ctx, &middleware.PlatformContext{
				UserID: sopOwner, UserEmail: sopOwner,
			})
			return next(ctx, method, req)
		}
	})
	session, cleanup := connectTestClient(t, server)
	defer cleanup()

	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "manage_prompt",
		Arguments: map[string]any{"command": "use", "name": name},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "manage_prompt use failed: %+v", res.Content)
	return res
}

// TestManagePromptUse_ReportsAndDeliversAttachments is acceptance criterion 1
// on the tool surface: the use response lists the materials in its provenance
// and carries them as protocol content, template embedded and image linked.
func TestManagePromptUse_ReportsAndDeliversAttachments(t *testing.T) {
	h := withAttachments(t, []prompt.Attachment{
		{PromptID: "p1", ResourceID: "tpl", Position: 0},
		{PromptID: "p1", ResourceID: "logo", Position: 1},
	})

	res := useThroughTool(t, h, "quarterly-review")

	first, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var payload struct {
		Status      string           `json:"status"`
		Content     string           `json:"content"`
		Attachments []map[string]any `json:"attachments"`
	}
	require.NoError(t, json.Unmarshal([]byte(first.Text), &payload))
	assert.Equal(t, "resolved", payload.Status)
	assert.Equal(t, "Write the quarterly review.", payload.Content)

	require.Len(t, payload.Attachments, 2, "the agent must be able to state what it received")
	assert.Equal(t, "embedded", payload.Attachments[0]["availability"])
	assert.Equal(t, "# Quarterly Review\n\n## Findings\n", payload.Attachments[0]["content"])
	assert.Equal(t, "linked", payload.Attachments[1]["availability"])
	assert.NotContains(t, payload.Attachments[1], "content")

	require.Len(t, res.Content, 4, "JSON payload, framing, embedded template, linked image")
	_, ok = res.Content[2].(*mcp.EmbeddedResource)
	assert.True(t, ok, "the template must also arrive in protocol form")
	_, ok = res.Content[3].(*mcp.ResourceLink)
	assert.True(t, ok, "the image must also arrive in protocol form")
}

// TestManagePromptUse_WithoutAttachmentsOmitsTheField keeps the response shape
// unchanged for the prompts that carry no materials.
func TestManagePromptUse_WithoutAttachmentsOmitsTheField(t *testing.T) {
	h := withAttachments(t, nil)
	res := useThroughTool(t, h, "quarterly-review")
	require.Len(t, res.Content, 1)

	first, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(first.Text), &payload))
	assert.NotContains(t, payload, "attachments")
}

// TestPromotionBlockedByNarrowerAttachment is acceptance criterion 2 through
// the assembled write path: a personal prompt carrying an analyst-only rubric
// cannot be promoted to global, and the refusal names the resource.
//
// The check is not a call the update handler makes; it is a property of the
// shared store every write path crosses, which is why the test drives a real
// manage_prompt update rather than the guard function.
func TestPromotionBlockedByNarrowerAttachment(t *testing.T) {
	store := &recordingStore{mockPromptStore: newMockPromptStore()}
	h := New(Config{Store: store, AdminPersona: "admin", Registry: registry.NewRegistry()})
	store.prompts["draft-sop:"+sopOwner] = &prompt.Prompt{
		ID: "p1", Name: "draft-sop", Scope: prompt.ScopePersonal, OwnerEmail: sopOwner,
		Content: "body", Enabled: true,
	}
	h.SetAttachmentResolver(attachserve.New(attachserve.Deps{
		Attachments: &attachStore{links: map[string][]prompt.Attachment{
			"p1": {{PromptID: "p1", ResourceID: "rubric"}},
		}},
		Resources: &resStore{byID: map[string]*resource.Resource{
			"rubric": {
				ID: "rubric", Scope: resource.ScopePersona, ScopeID: "analyst",
				DisplayName: "Analyst Rubric", MIMEType: "text/markdown", SizeBytes: 12, URI: "u",
			},
		}},
	}))

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: sopOwner, UserEmail: sopOwner,
	})

	blocked, _, err := h.handleManagePrompt(ctx, managePromptInput{
		Command: "update", Name: "draft-sop", RequestedScope: prompt.ScopeGlobal,
	})
	require.NoError(t, err)
	require.True(t, blocked.IsError, "the promotion request must be refused")
	msg, ok := blocked.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	assert.Contains(t, msg.Text, "Analyst Rubric", "the author must learn which attachment to fix")

	// The refusal happens before persistence: nothing reached the store.
	assert.Zero(t, store.updates, "a blocked promotion must not write")
}

// TestPromotionAllowedOnceAttachmentIsWideEnough is the other half of criterion
// 2: the same promotion succeeds when the material reaches the new audience.
func TestPromotionAllowedOnceAttachmentIsWideEnough(t *testing.T) {
	store := &recordingStore{mockPromptStore: newMockPromptStore()}
	h := New(Config{Store: store, AdminPersona: "admin", Registry: registry.NewRegistry()})
	store.prompts["draft-sop:"+sopOwner] = &prompt.Prompt{
		ID: "p1", Name: "draft-sop", Scope: prompt.ScopePersonal, OwnerEmail: sopOwner,
		Content: "body", Enabled: true,
	}
	h.SetAttachmentResolver(attachserve.New(attachserve.Deps{
		Attachments: &attachStore{links: map[string][]prompt.Attachment{
			"p1": {{PromptID: "p1", ResourceID: "tpl"}},
		}},
		Resources: &resStore{byID: map[string]*resource.Resource{
			"tpl": {ID: "tpl", Scope: resource.ScopeGlobal, DisplayName: "Quarterly Template", URI: "u"},
		}},
	}))

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: sopOwner, UserEmail: sopOwner,
	})
	res, _, err := h.handleManagePrompt(ctx, managePromptInput{
		Command: "update", Name: "draft-sop", RequestedScope: prompt.ScopeGlobal,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "a global attachment must not block promotion: %+v", res.Content)

	assert.Positive(t, store.updates, "an allowed promotion must reach the store")
	stored, err := store.GetPersonal(ctx, sopOwner, "draft-sop")
	require.NoError(t, err)
	assert.True(t, stored.ReviewRequested)
	assert.Equal(t, prompt.ScopeGlobal, stored.RequestedScope)
}

// TestDeletedAttachmentDoesNotFreezeThePrompt is the write-side half of
// graceful degradation, and the regression that matters most: the promotion
// guard reads every attachment's scope before any prompt write, so a resource
// deleted out from under a prompt must not make that prompt permanently
// unwritable. Renaming it, disabling it, or promoting it has to keep working.
func TestDeletedAttachmentDoesNotFreezeThePrompt(t *testing.T) {
	store := &recordingStore{mockPromptStore: newMockPromptStore()}
	h := New(Config{Store: store, AdminPersona: "admin", Registry: registry.NewRegistry()})
	store.prompts["draft-sop:"+sopOwner] = &prompt.Prompt{
		ID: "p1", Name: "draft-sop", Scope: prompt.ScopePersonal, OwnerEmail: sopOwner,
		Content: "body", Enabled: true,
	}
	h.SetAttachmentResolver(attachserve.New(attachserve.Deps{
		Attachments: &attachStore{links: map[string][]prompt.Attachment{
			"p1": {{PromptID: "p1", ResourceID: "deleted-yesterday"}},
		}},
		// Empty store: the resource is gone, which the real store reports as a
		// wrapped sql.ErrNoRows rather than as a nil result.
		Resources: &resStore{byID: map[string]*resource.Resource{}},
	}))

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: sopOwner, UserEmail: sopOwner,
	})
	res, _, err := h.handleManagePrompt(ctx, managePromptInput{
		Command: "update", Name: "draft-sop", Description: "still editable",
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "a broken attachment must not block edits: %+v", res.Content)
	assert.Positive(t, store.updates)
}

// TestUnreadableAttachmentStoreBlocksTheWrite is the opposite case: an
// attachment whose scope cannot be determined fails closed, because publishing
// to a wider audience on an unknown scope is the outcome the guard exists to
// prevent.
func TestUnreadableAttachmentStoreBlocksTheWrite(t *testing.T) {
	store := &recordingStore{mockPromptStore: newMockPromptStore()}
	h := New(Config{Store: store, AdminPersona: "admin", Registry: registry.NewRegistry()})
	store.prompts["draft-sop:"+sopOwner] = &prompt.Prompt{
		ID: "p1", Name: "draft-sop", Scope: prompt.ScopePersonal, OwnerEmail: sopOwner,
		Content: "body", Enabled: true,
	}
	h.SetAttachmentResolver(attachserve.New(attachserve.Deps{
		Attachments: &attachStore{links: map[string][]prompt.Attachment{
			"p1": {{PromptID: "p1", ResourceID: "x"}},
		}},
		Resources: &resStore{getErr: errors.New("db down")},
	}))

	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{
		UserID: sopOwner, UserEmail: sopOwner,
	})
	res, _, err := h.handleManagePrompt(ctx, managePromptInput{
		Command: "update", Name: "draft-sop", RequestedScope: prompt.ScopeGlobal,
	})
	require.NoError(t, err)
	assert.True(t, res.IsError)
	assert.Zero(t, store.updates)
}
