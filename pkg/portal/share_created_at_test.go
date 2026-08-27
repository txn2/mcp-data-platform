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

	"github.com/txn2/mcp-data-platform/pkg/prompt"
)

// A share create response carries the created_at the row was written with
// (#1511). The handler used to render the Share value it had built, whose
// CreatedAt no caller ever set, so a client rendering "shared on" from the
// response it had just received showed year 1 while a list of the same share
// returned the real timestamp.
func TestCreateShareResponseCarriesStoredCreatedAt(t *testing.T) {
	decodeShare := func(t *testing.T, w *httptest.ResponseRecorder) Share {
		t.Helper()
		require.Equal(t, http.StatusCreated, w.Code)
		var resp shareResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		return resp.Share
	}

	t.Run("asset share", func(t *testing.T) {
		shares := &mockShareStore{}
		h := gateAssetHandler(&mockAssetStore{getAsset: gateAsset()}, shares, gateOwner())

		body := `{"shared_with_email":"bob@example.com","permission":"viewer"}`
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/assets/a1/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		got := decodeShare(t, w)
		assert.False(t, got.CreatedAt.IsZero(), "the response must not carry a zero created_at")
		require.NotNil(t, shares.inserted)
		assert.Equal(t, shares.inserted.CreatedAt, got.CreatedAt,
			"the response must carry the timestamp the row was stored with")
	})

	t.Run("collection share", func(t *testing.T) {
		shares := &mockCollectionShareStore{}
		h := gateCollectionHandler(&collHandlerMockCollStore{getColl: gateCollection()}, shares, gateOwner())

		body := `{"shared_with_email":"bob@example.com","permission":"viewer"}`
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/collections/coll-1/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		got := decodeShare(t, w)
		assert.False(t, got.CreatedAt.IsZero(), "the response must not carry a zero created_at")
		require.NotNil(t, shares.inserted)
		assert.Equal(t, shares.inserted.CreatedAt, got.CreatedAt,
			"the response must carry the timestamp the row was stored with")
	})

	t.Run("prompt share", func(t *testing.T) {
		pstore := newMockPromptStore()
		pstore.prompts["report"] = &prompt.Prompt{
			ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: gateOwner().Email,
		}
		shares := &mockShareStore{}
		h := NewHandler(Deps{
			AssetStore:  NewNoopAssetStore(),
			ShareStore:  shares,
			PromptStore: pstore,
			AdminRoles:  []string{gateAdminRole},
		}, testAuthMiddleware(gateOwner()))

		body := `{"shared_with_email":"bob@example.com","permission":"viewer"}`
		r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/api/v1/portal/prompts/p1/shares", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)

		got := decodeShare(t, w)
		assert.False(t, got.CreatedAt.IsZero(), "the response must not carry a zero created_at")
		require.NotNil(t, shares.inserted)
		assert.Equal(t, shares.inserted.CreatedAt, got.CreatedAt,
			"the response must carry the timestamp the row was stored with")
	})
}
