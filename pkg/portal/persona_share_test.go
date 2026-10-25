package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func personaShareHandler(user *User, notifier Notifier) *Handler {
	h := NewHandler(Deps{
		AssetStore: &mockAssetStore{getAsset: &Asset{ID: "a1", OwnerID: user.UserID, OwnerEmail: user.Email, Name: "Report"}},
		ShareStore: &mockShareStore{},
		S3Client:   &mockS3Client{},
		AdminRoles: []string{"admin"},
		Notifier:   notifier,
		PersonaResolver: func(roles []string) *PersonaInfo {
			if len(roles) > 0 && roles[0] == "analyst" {
				return &PersonaInfo{Name: "analyst"}
			}
			return &PersonaInfo{Name: "admin"}
		},
		Personas: func() []SharePersona {
			return []SharePersona{{Name: "analyst", DisplayName: "Analyst"}, {Name: "finance", DisplayName: "Finance"}}
		},
		PersonaMembers: func(_ context.Context, name string) []string {
			if name == "analyst" {
				return []string{"a@example.com", "b@example.com"}
			}
			return nil
		},
	}, testAuthMiddleware(user))
	return h
}

// Everyone who shares is offered every persona (#2089, #2290).
func TestSharePersonasAreEveryPersona(t *testing.T) {
	for _, c := range []struct {
		roles []string
		want  []string
	}{
		{[]string{"analyst"}, []string{"analyst", "finance"}},
		{[]string{"admin"}, []string{"analyst", "finance"}},
	} {
		h := personaShareHandler(&User{UserID: "u1", Email: "u1@example.com", Roles: c.roles}, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/share-personas", http.NoBody))
		require.Equal(t, http.StatusOK, w.Code)
		var got sharePersonaList
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
		names := make([]string, 0, len(got.Personas))
		for _, p := range got.Personas {
			names = append(names, p.Name)
		}
		assert.Equal(t, c.want, names, c.roles)
	}
}

// A share addressed to a persona is made and notifies each member, the
// caller's own persona or another (#2290); a persona that does not exist, and
// a share naming a persona and a person, are refused.
func TestSharingWithAPersona(t *testing.T) {
	rec := &recordingNotifier{}
	h := personaShareHandler(&User{UserID: "u1", Email: "u1@example.com", Roles: []string{"analyst"}}, rec)
	post := func(body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/assets/a1/shares", strings.NewReader(body)))
		return w
	}
	w := post(`{"shared_with_persona":"analyst","permission":"editor"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var created shareResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.Equal(t, "analyst", created.Share.SharedWithPersona)
	assert.Equal(t, portaldomain.PermissionEditor, created.Share.Permission)
	assert.Equal(t, 2, rec.shares, "each member is notified")

	assert.Equal(t, http.StatusCreated, post(`{"shared_with_persona":"finance"}`).Code, "another persona")
	assert.Equal(t, http.StatusBadRequest, post(`{"shared_with_persona":"nobody"}`).Code, "no such persona")
	assert.Equal(t, http.StatusBadRequest, post(`{"shared_with_persona":"analyst","shared_with_email":"x@example.com"}`).Code)
}

// A persona share opens for whoever resolves to the persona when they open
// the item, and for nobody else (#2089).
func TestAPersonaShareMatchesTheCallersPersonaNow(t *testing.T) {
	shares := &mockShareStore{listByAsset: []Share{{ID: "s1", AssetID: "a1", SharedWithPersona: "analyst", Permission: portaldomain.PermissionViewer}}}
	checker := access.New(access.Config{Shares: shares})
	user := &access.User{UserID: "u9", Email: "u9@example.com"}
	perm, err := checker.AssetSharePermission(portaldomain.WithCallerPersona(context.Background(), "analyst"), "a1", user)
	require.NoError(t, err)
	assert.Equal(t, portaldomain.PermissionViewer, perm)
	perm, err = checker.AssetSharePermission(portaldomain.WithCallerPersona(context.Background(), "finance"), "a1", user)
	require.NoError(t, err)
	assert.Empty(t, perm, "someone outside the persona")
	perm, _ = checker.AssetSharePermission(context.Background(), "a1", user)
	assert.Empty(t, perm, "a caller with no persona")
}

// With no session there is nothing to list; with no persona registry there
// is no persona to share with.
func TestSharePersonasWithoutWhatTheyNeed(t *testing.T) {
	user := &User{UserID: "u1", Email: "u1@example.com", Roles: []string{"analyst"}}
	h := personaShareHandler(user, nil)
	w := httptest.NewRecorder()
	h.listSharePersonas(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/share-personas", http.NoBody))
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	assert.Empty(t, h.sharePersonas(nil))
	h.deps.Personas = nil
	assert.Empty(t, h.sharePersonas(user))
}
