package attachhttp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/prompt"
	"github.com/txn2/mcp-data-platform/pkg/resource"
)

// fakePromptStore is a prompt.Store with only the reads this handler makes.
type fakePromptStore struct {
	prompt.Store
	byID map[string]*prompt.Prompt
	err  error
}

func (f *fakePromptStore) GetByID(_ context.Context, id string) (*prompt.Prompt, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.byID[id], nil
}

// fakeAttachments records the writes the handler performs.
type fakeAttachments struct {
	links      map[string][]prompt.Attachment
	byResource map[string][]string
	attached   []prompt.Attachment
	detached   [2]string
	reordered  []string
	listErr    error
	attachErr  error
	detachErr  error
	reorderErr error
	resErr     error
}

func (f *fakeAttachments) Attach(_ context.Context, a prompt.Attachment) error {
	if f.attachErr != nil {
		return f.attachErr
	}
	f.attached = append(f.attached, a)
	f.links[a.PromptID] = append(f.links[a.PromptID], a)
	return nil
}

func (f *fakeAttachments) Detach(_ context.Context, promptID, resourceID string) error {
	f.detached = [2]string{promptID, resourceID}
	return f.detachErr
}

func (f *fakeAttachments) ListByPrompt(_ context.Context, id string) ([]prompt.Attachment, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.links[id], nil
}

func (f *fakeAttachments) ListByResource(_ context.Context, id string) ([]string, error) {
	if f.resErr != nil {
		return nil, f.resErr
	}
	return f.byResource[id], nil
}

func (f *fakeAttachments) Reorder(_ context.Context, _ string, ids []string) error {
	if f.reorderErr != nil {
		return f.reorderErr
	}
	f.reordered = ids
	return nil
}

// fakeResources is a resource.Store with only Get implemented.
type fakeResources struct {
	resource.Store
	byID map[string]*resource.Resource
	err  error
}

func (f *fakeResources) Get(_ context.Context, id string) (*resource.Resource, error) {
	if f.err != nil {
		return nil, f.err
	}
	res, ok := f.byID[id]
	if !ok {
		// The Postgres store reports a missing row as a wrapped
		// sql.ErrNoRows, not as (nil, nil); a fake that returned nil would
		// let broken-link handling pass here and fail in production.
		return nil, fmt.Errorf("scanning resource: %w", sql.ErrNoRows)
	}
	return res, nil
}

const (
	ownerEmail = "owner@example.com"
	ownerSub   = "sub-owner"
)

// fixture builds a handler over one personal prompt, one persona prompt, and
// three resources spanning the scope rule's cases.
func fixture(t *testing.T, who *Identity) (*Handler, *fakeAttachments) {
	t.Helper()
	att := &fakeAttachments{links: map[string][]prompt.Attachment{}, byResource: map[string][]string{}}
	h := New(Deps{
		Store: &fakePromptStore{byID: map[string]*prompt.Prompt{
			"p1": {ID: "p1", Name: "daily-report", Scope: prompt.ScopePersonal, OwnerEmail: ownerEmail},
			"p2": {ID: "p2", Name: "team-sop", Scope: prompt.ScopePersona, Personas: []string{"analyst"}},
		}},
		Attachments: att,
		Resources: &fakeResources{byID: map[string]*resource.Resource{
			"tpl":     {ID: "tpl", Scope: resource.ScopeGlobal, DisplayName: "Q4 Template", MIMEType: "text/markdown", SizeBytes: 21, URI: "u-tpl", Path: "templates"},
			"rubric":  {ID: "rubric", Scope: resource.ScopePersona, ScopeID: "analyst", DisplayName: "Analyst Rubric", URI: "u-rubric"},
			"private": {ID: "private", Scope: resource.ScopeUser, ScopeID: ownerSub, DisplayName: "My Draft", URI: "u-private"},
		}},
		Caller: func(*http.Request) *Identity { return who },
	})
	require.NotNil(t, h)
	return h, att
}

// owner is the personal prompt's owner: a plain user, no admin rights.
func owner() *Identity {
	return &Identity{Sub: ownerSub, Email: ownerEmail, Personas: []string{"analyst"}}
}

// serve routes one request through the registered mux.
func serve(t *testing.T, h *Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux()
	h.Register(mux, "/api/v1/portal", func(next http.Handler) http.Handler { return next })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body)))
	return rec
}

// decodeList reads an attachment list response.
func decodeList(t *testing.T, rec *httptest.ResponseRecorder) listResponse {
	t.Helper()
	var out listResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

func TestNewRequiresCollaborators(t *testing.T) {
	caller := func(*http.Request) *Identity { return nil }
	assert.Nil(t, New(Deps{Attachments: &fakeAttachments{}, Resources: &fakeResources{}, Caller: caller}))
	assert.Nil(t, New(Deps{Store: &fakePromptStore{}, Resources: &fakeResources{}, Caller: caller}))
	assert.Nil(t, New(Deps{Store: &fakePromptStore{}, Attachments: &fakeAttachments{}, Caller: caller}))
	assert.Nil(t, New(Deps{Store: &fakePromptStore{}, Attachments: &fakeAttachments{}, Resources: &fakeResources{}}))
}

// TestRegisterOnNilHandlerIsNoop proves the composition root can call Register
// unconditionally on a deployment with no attachment support.
func TestRegisterOnNilHandlerIsNoop(t *testing.T) {
	var h *Handler
	mux := http.NewServeMux()
	assert.NotPanics(t, func() {
		h.Register(mux, "/api/v1/portal", func(next http.Handler) http.Handler { return next })
	})
}

func TestUnauthenticatedIsRejected(t *testing.T) {
	h, _ := fixture(t, nil)
	rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

// TestAttachGlobalResourceToPersonalPrompt is the happy path: the link is
// created and the response is the prompt's new attachment list.
func TestAttachGlobalResourceToPersonalPrompt(t *testing.T) {
	h, att := fixture(t, owner())
	rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"tpl"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	require.Len(t, att.attached, 1)
	assert.Equal(t, "p1", att.attached[0].PromptID)
	assert.Equal(t, "tpl", att.attached[0].ResourceID)
	assert.Equal(t, ownerEmail, att.attached[0].AttachedBy, "the link records who added the material")

	got := decodeList(t, rec)
	require.Len(t, got.Data, 1)
	assert.Equal(t, "Q4 Template", got.Data[0].DisplayName)
	assert.Equal(t, "templates", got.Data[0].Path)
	assert.False(t, got.Data[0].Broken)
}

// TestAttachPrivateResourceToSharedPromptIsRefused is acceptance criterion 2:
// a user-scoped resource cannot be attached to a persona prompt, and the
// rejection names the resource.
func TestAttachPrivateResourceToSharedPromptIsRefused(t *testing.T) {
	h, att := fixture(t, &Identity{Sub: ownerSub, Email: ownerEmail, IsAdmin: true})
	rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p2/attachments", `{"resource_id":"private"}`)
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "My Draft")
	assert.Empty(t, att.attached, "nothing may be persisted when the scope check fails")
}

// TestAttachAnotherUsersPrivateResourceIsRefused covers the ownership half of
// the rule for the two caller kinds, which fail at different gates.
//
// A platform admin has write authority over any user scope, so they clear the
// read-or-administer gate and are stopped by the ownership rule with a conflict
// that names the resource; attaching another user's private material would
// build an SOP only that user can read.
//
// A plain user has neither, so they are stopped at the read gate and told the
// resource does not exist, which is what keeps the route from being an
// existence probe.
func TestAttachAnotherUsersPrivateResourceIsRefused(t *testing.T) {
	t.Run("admin is refused by the ownership rule", func(t *testing.T) {
		h, att := fixture(t, &Identity{Sub: "sub-admin", Email: "admin@example.com", IsAdmin: true})
		rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"private"}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
		assert.Contains(t, rec.Body.String(), "another user's private resource")
		assert.Empty(t, att.attached)
	})

	t.Run("a plain user is told it does not exist", func(t *testing.T) {
		// Their own personal prompt, someone else's private resource.
		h, att := fixture(t, &Identity{Sub: ownerSub, Email: ownerEmail})
		h.deps.Resources = &fakeResources{byID: map[string]*resource.Resource{
			"private": {ID: "private", Scope: resource.ScopeUser, ScopeID: "sub-someone-else", DisplayName: "Their Draft"},
		}}
		rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"private"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
		assert.NotContains(t, rec.Body.String(), "Their Draft")
		assert.Empty(t, att.attached)
	})
}

// TestAttachOwnPrivateResourceToOwnPromptIsAllowed is the positive case the
// ownership rule must not break: a private draft on the author's own prompt.
func TestAttachOwnPrivateResourceToOwnPromptIsAllowed(t *testing.T) {
	h, att := fixture(t, owner())
	rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"private"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, att.attached, 1)
	assert.Equal(t, "private", att.attached[0].ResourceID)
}

// TestAttachUnreadableResourceReportsNotFound proves the handler does not
// distinguish "forbidden" from "absent", so a caller cannot probe for the
// existence of resources outside their scope.
func TestAttachUnreadableResourceReportsNotFound(t *testing.T) {
	h, _ := fixture(t, &Identity{Sub: "sub-other", Email: "other@example.com", Personas: []string{"engineer"}})
	// The prompt must be theirs to edit, so use an admin-less owner of p1 who
	// simply cannot read the analyst rubric.
	h2, _ := fixture(t, &Identity{Sub: ownerSub, Email: ownerEmail, Personas: []string{"engineer"}})
	rec := serve(t, h2, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"rubric"}`)
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NotContains(t, rec.Body.String(), "Analyst Rubric", "the name of an unreadable resource must not leak")

	recMissing := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"nope"}`)
	assert.Equal(t, http.StatusForbidden, recMissing.Code, "a non-owner is refused before the resource is even read")
}

func TestAttachValidation(t *testing.T) {
	h, _ := fixture(t, owner())

	t.Run("missing resource_id", func(t *testing.T) {
		rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("malformed body", func(t *testing.T) {
		rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("unknown prompt", func(t *testing.T) {
		rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/nope/attachments", `{"resource_id":"tpl"}`)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// TestNonOwnerCannotChangeAttachments proves attaching material is treated as
// editing the prompt.
func TestNonOwnerCannotChangeAttachments(t *testing.T) {
	h, _ := fixture(t, &Identity{Sub: "sub-other", Email: "other@example.com"})
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"tpl"}`},
		{http.MethodPut, "/api/v1/portal/prompts/p1/attachments", `{"resource_ids":[]}`},
		{http.MethodDelete, "/api/v1/portal/prompts/p1/attachments/tpl", ""},
	} {
		rec := serve(t, h, tc.method, tc.path, tc.body)
		assert.Equal(t, http.StatusForbidden, rec.Code, tc.method+" "+tc.path)
	}
}

// TestNonAdminCannotEditSharedPromptAttachments keeps the attachment surface in
// step with who may edit a shared prompt's content.
func TestNonAdminCannotEditSharedPromptAttachments(t *testing.T) {
	h, _ := fixture(t, owner())
	rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p2/attachments", `{"resource_id":"tpl"}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
}

// TestListFlagsBrokenLink is acceptance criterion 4 on the portal side: a
// deleted resource still appears, flagged, so the author can remove it.
// TestListRefusesAnotherUsersPersonalPrompt closes the read side of the
// surface: the attachment list names the material and its attacher, so it must
// be gated on prompt visibility, not merely on the prompt existing.
func TestListRefusesAnotherUsersPersonalPrompt(t *testing.T) {
	h, att := fixture(t, &Identity{Sub: "sub-other", Email: "other@example.com"})
	att.links["p1"] = []prompt.Attachment{{PromptID: "p1", ResourceID: "tpl"}}

	rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
	assert.Equal(t, http.StatusNotFound, rec.Code,
		"another user's personal prompt must not disclose its attachments")
	assert.NotContains(t, rec.Body.String(), "Q4 Template")
	assert.NotContains(t, rec.Body.String(), ownerEmail)
}

// TestListAllowsSharedPromptAndAdmin is the admission half: a shared prompt is
// listable by anyone who can see it, and an admin sees any prompt's.
func TestListAllowsSharedPromptAndAdmin(t *testing.T) {
	h, att := fixture(t, &Identity{Sub: "sub-other", Email: "other@example.com"})
	att.links["p2"] = []prompt.Attachment{{PromptID: "p2", ResourceID: "tpl"}}
	rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p2/attachments", "")
	assert.Equal(t, http.StatusOK, rec.Code)

	admin, adminAtt := fixture(t, &Identity{Sub: "sub-admin", Email: "admin@example.com", IsAdmin: true})
	adminAtt.links["p1"] = []prompt.Attachment{{PromptID: "p1", ResourceID: "tpl"}}
	adminRec := serve(t, admin, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
	assert.Equal(t, http.StatusOK, adminRec.Code)
}

// TestUnreadableRowWithholdsTheAttacher proves the flagged row carries no third
// party's identity: the caller cannot read the material, so they have no claim
// on who added it.
func TestUnreadableRowWithholdsTheAttacher(t *testing.T) {
	h, att := fixture(t, &Identity{Sub: ownerSub, Email: ownerEmail, Personas: []string{"engineer"}})
	att.links["p1"] = []prompt.Attachment{
		{PromptID: "p1", ResourceID: "rubric", AttachedBy: "someone.else@example.com"},
	}
	rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
	require.Equal(t, http.StatusOK, rec.Code)
	got := decodeList(t, rec)
	require.Len(t, got.Data, 1)
	assert.True(t, got.Data[0].Unreadable)
	assert.Empty(t, got.Data[0].AttachedBy)
}

func TestListFlagsBrokenLink(t *testing.T) {
	h, att := fixture(t, owner())
	att.links["p1"] = []prompt.Attachment{{PromptID: "p1", ResourceID: "deleted", Position: 0}}

	rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
	require.Equal(t, http.StatusOK, rec.Code)
	got := decodeList(t, rec)
	require.Len(t, got.Data, 1)
	assert.True(t, got.Data[0].Broken)
	assert.Equal(t, "deleted", got.Data[0].ResourceID)
	assert.Empty(t, got.Data[0].DisplayName)
}

// TestListFlagsUnreadableWithoutLeaking proves an attachment the caller cannot
// read is marked, not described.
func TestListFlagsUnreadableWithoutLeaking(t *testing.T) {
	h, att := fixture(t, &Identity{Sub: ownerSub, Email: ownerEmail, Personas: []string{"engineer"}})
	att.links["p1"] = []prompt.Attachment{{PromptID: "p1", ResourceID: "rubric"}}

	rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
	got := decodeList(t, rec)
	require.Len(t, got.Data, 1)
	assert.True(t, got.Data[0].Unreadable)
	assert.Empty(t, got.Data[0].DisplayName)
	assert.Empty(t, got.Data[0].URI)
}

func TestListErrors(t *testing.T) {
	t.Run("prompt read fails", func(t *testing.T) {
		h := New(Deps{
			Store:       &fakePromptStore{err: errors.New("db down")},
			Attachments: &fakeAttachments{links: map[string][]prompt.Attachment{}},
			Resources:   &fakeResources{},
			Caller:      func(*http.Request) *Identity { return owner() },
		})
		rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("attachment read fails", func(t *testing.T) {
		h, att := fixture(t, owner())
		att.listErr = errors.New("db down")
		rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("resource read fails", func(t *testing.T) {
		att := &fakeAttachments{links: map[string][]prompt.Attachment{"p1": {{ResourceID: "tpl"}}}}
		h := New(Deps{
			Store:       &fakePromptStore{byID: map[string]*prompt.Prompt{"p1": {ID: "p1", Scope: prompt.ScopePersonal, OwnerEmail: ownerEmail}}},
			Attachments: att,
			Resources:   &fakeResources{err: errors.New("db down")},
			Caller:      func(*http.Request) *Identity { return owner() },
		})
		rec := serve(t, h, http.MethodGet, "/api/v1/portal/prompts/p1/attachments", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestDetach(t *testing.T) {
	h, att := fixture(t, owner())

	t.Run("success", func(t *testing.T) {
		rec := serve(t, h, http.MethodDelete, "/api/v1/portal/prompts/p1/attachments/tpl", "")
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, [2]string{"p1", "tpl"}, att.detached)
	})

	t.Run("unknown link is 404", func(t *testing.T) {
		att.detachErr = prompt.ErrAttachmentNotFound
		rec := serve(t, h, http.MethodDelete, "/api/v1/portal/prompts/p1/attachments/nope", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("store failure is 500", func(t *testing.T) {
		att.detachErr = errors.New("db down")
		rec := serve(t, h, http.MethodDelete, "/api/v1/portal/prompts/p1/attachments/tpl", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

func TestReorder(t *testing.T) {
	h, att := fixture(t, owner())

	t.Run("success", func(t *testing.T) {
		rec := serve(t, h, http.MethodPut, "/api/v1/portal/prompts/p1/attachments", `{"resource_ids":["logo","tpl"]}`)
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.Equal(t, []string{"logo", "tpl"}, att.reordered)
	})

	t.Run("malformed body", func(t *testing.T) {
		rec := serve(t, h, http.MethodPut, "/api/v1/portal/prompts/p1/attachments", `{`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("unattached id is a conflict", func(t *testing.T) {
		att.reorderErr = prompt.ErrAttachmentNotFound
		rec := serve(t, h, http.MethodPut, "/api/v1/portal/prompts/p1/attachments", `{"resource_ids":["smuggled"]}`)
		assert.Equal(t, http.StatusConflict, rec.Code)
	})

	t.Run("store failure is 500", func(t *testing.T) {
		att.reorderErr = errors.New("db down")
		rec := serve(t, h, http.MethodPut, "/api/v1/portal/prompts/p1/attachments", `{"resource_ids":[]}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("list failure after a successful reorder is 500", func(t *testing.T) {
		h2, att2 := fixture(t, owner())
		att2.reorderErr = nil
		att2.listErr = errors.New("db down")
		rec := serve(t, h2, http.MethodPut, "/api/v1/portal/prompts/p1/attachments", `{"resource_ids":[]}`)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}

// TestByResourceListsDependentPrompts backs the resource detail view: an
// operator about to delete a file can see what depends on it.
func TestByResourceListsDependentPrompts(t *testing.T) {
	h, att := fixture(t, owner())
	att.byResource["tpl"] = []string{"p1", "p2"}

	rec := serve(t, h, http.MethodGet, "/api/v1/portal/resources/tpl/prompts", "")
	require.Equal(t, http.StatusOK, rec.Code)
	var out struct {
		Data  []promptRef `json:"data"`
		Total int         `json:"total"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	assert.Equal(t, 2, out.Total)
	assert.Equal(t, "daily-report", out.Data[0].Name)
	assert.Equal(t, "team-sop", out.Data[1].Name)
}

// TestByResourceHidesOtherUsersPersonalPrompts proves the dependency answer is
// scoped to the asker: someone else's personal prompt must not be disclosed by
// this route.
func TestByResourceHidesOtherUsersPersonalPrompts(t *testing.T) {
	h, att := fixture(t, &Identity{Sub: "sub-other", Email: "other@example.com"})
	att.byResource["tpl"] = []string{"p1", "p2"}

	rec := serve(t, h, http.MethodGet, "/api/v1/portal/resources/tpl/prompts", "")
	var out struct {
		Data []promptRef `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	require.Len(t, out.Data, 1)
	assert.Equal(t, "team-sop", out.Data[0].Name)
}

func TestByResourceErrors(t *testing.T) {
	t.Run("unknown resource", func(t *testing.T) {
		h, _ := fixture(t, owner())
		rec := serve(t, h, http.MethodGet, "/api/v1/portal/resources/nope/prompts", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("unreadable resource is reported as absent", func(t *testing.T) {
		h, _ := fixture(t, &Identity{Sub: "sub-other", Email: "other@example.com", Personas: []string{"engineer"}})
		rec := serve(t, h, http.MethodGet, "/api/v1/portal/resources/rubric/prompts", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("resource read fails", func(t *testing.T) {
		h := New(Deps{
			Store:       &fakePromptStore{},
			Attachments: &fakeAttachments{links: map[string][]prompt.Attachment{}},
			Resources:   &fakeResources{err: errors.New("db down")},
			Caller:      func(*http.Request) *Identity { return owner() },
		})
		rec := serve(t, h, http.MethodGet, "/api/v1/portal/resources/tpl/prompts", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("attachment lookup fails", func(t *testing.T) {
		h, att := fixture(t, owner())
		att.resErr = errors.New("db down")
		rec := serve(t, h, http.MethodGet, "/api/v1/portal/resources/tpl/prompts", "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("a dangling prompt id is skipped", func(t *testing.T) {
		h, att := fixture(t, owner())
		att.byResource["tpl"] = []string{"vanished"}
		rec := serve(t, h, http.MethodGet, "/api/v1/portal/resources/tpl/prompts", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"total":0`)
	})
}

// TestAttachStoreFailureIsReported keeps a persistence failure from being
// reported as success.
func TestAttachStoreFailureIsReported(t *testing.T) {
	h, att := fixture(t, owner())
	att.attachErr = errors.New("db down")
	rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"tpl"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestAttachListFailureAfterWriteIsReported covers the read-back that renders
// the response.
func TestAttachListFailureAfterWriteIsReported(t *testing.T) {
	h, att := fixture(t, owner())
	att.listErr = errors.New("db down")
	rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p1/attachments", `{"resource_id":"tpl"}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

// TestAdminMayEditAnyPromptsAttachments confirms the admin surface reaches
// shared prompts, which is the whole reason it is mounted separately.
func TestAdminMayEditAnyPromptsAttachments(t *testing.T) {
	h, att := fixture(t, &Identity{Sub: "sub-admin", Email: "admin@example.com", IsAdmin: true})
	rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p2/attachments", `{"resource_id":"tpl"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Len(t, att.attached, 1)
	assert.Equal(t, "p2", att.attached[0].PromptID)
}

// TestPersonaResourceOnMatchingPersonaPromptIsAllowed is the positive case of
// the persona rule: the material and the prompt reach the same audience.
func TestPersonaResourceOnMatchingPersonaPromptIsAllowed(t *testing.T) {
	h, att := fixture(t, &Identity{Sub: "sub-admin", Email: "admin@example.com", Personas: []string{"analyst"}, IsAdmin: true})
	rec := serve(t, h, http.MethodPost, "/api/v1/portal/prompts/p2/attachments", `{"resource_id":"rubric"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Len(t, att.attached, 1)
}
