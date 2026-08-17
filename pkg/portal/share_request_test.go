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

// shareTestAsset is the asset every share in this file targets, owned by
// testUser so the owner-only share gate passes.
func shareTestAsset() *Asset {
	return &Asset{ID: "asset-1", OwnerID: testUser.UserID, OwnerEmail: testUser.Email, Name: "Q3 Revenue"}
}

// shareHandlerWithNotifier wires the asset share routes over a recording
// notifier so the tests can assert what a share announced, not just what it
// stored.
func shareHandlerWithNotifier(user *User) (*Handler, *recordingNotifier, *mockShareStore) {
	rec := &recordingNotifier{}
	shares := &mockShareStore{}
	deps := Deps{
		AssetStore:    &mockAssetStore{getAsset: shareTestAsset()},
		ShareStore:    shares,
		S3Client:      &mockS3Client{},
		S3Bucket:      "test-bucket",
		PublicBaseURL: "https://example.com",
		RateLimit:     RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
		Notifier:      rec,
	}
	return NewHandler(deps, testAuthMiddleware(user)), rec, shares
}

// postShare creates an asset share with the given JSON body.
func postShare(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(context.Background(),
		"POST", "/api/v1/portal/assets/asset-1/shares", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// shareRoute posts a create-share request to one of the three share routes.
type shareRoute struct {
	kind string
	post func(body string) *httptest.ResponseRecorder
}

// shareRouteSet holds one poster per share route, each over its own handler so
// one route's stores cannot answer for another's.
type shareRouteSet struct {
	asset      shareRoute
	collection shareRoute
	prompt     shareRoute
}

// shareRoutes returns a poster for each of the asset, collection, and prompt
// share routes.
func shareRoutes(t *testing.T) shareRouteSet {
	t.Helper()

	post := func(h *Handler, path string) func(string) *httptest.ResponseRecorder {
		return func(body string) *httptest.ResponseRecorder {
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			return w
		}
	}

	assetHandler, _, _ := shareHandlerWithNotifier(testUser)

	collHandler := newTestHandlerWithCollections(
		&mockAssetStore{}, &mockCollectionShareStore{},
		&collHandlerMockCollStore{getColl: baseCollection()}, &mockS3Client{}, testUser,
	)

	prompts := newMockPromptStore()
	prompts.prompts["report"] = &prompt.Prompt{
		ID: "p1", Name: "report", Scope: prompt.ScopePersonal, OwnerEmail: testUser.Email,
	}
	promptHandler := NewHandler(Deps{
		AssetStore:  NewNoopAssetStore(),
		ShareStore:  &mockShareStore{},
		PromptStore: prompts,
	}, testAuthMiddleware(testUser))

	return shareRouteSet{
		asset:      shareRoute{"asset", post(assetHandler, "/api/v1/portal/assets/asset-1/shares")},
		collection: shareRoute{"collection", post(collHandler, "/api/v1/portal/collections/coll-1/shares")},
		prompt:     shareRoute{"prompt", post(promptHandler, "/api/v1/portal/prompts/p1/shares")},
	}
}

func TestCreateShareNotifyFlag(t *testing.T) {
	t.Run("omitted notifies", func(t *testing.T) {
		h, rec, _ := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"shared_with_email":"bob@example.com"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, 1, rec.shares, "a share with no notify field must notify")
	})

	t.Run("true notifies", func(t *testing.T) {
		h, rec, _ := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"shared_with_email":"bob@example.com","notify":true}`)
		require.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, 1, rec.shares)
	})

	t.Run("false shares quietly", func(t *testing.T) {
		h, rec, shares := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"shared_with_email":"bob@example.com","notify":false}`)
		require.Equal(t, http.StatusCreated, w.Code)
		assert.Zero(t, rec.shares, "notify:false must fire no notification")
		// The share itself is unaffected: only the email is suppressed.
		require.NotNil(t, shares.inserted)
		assert.Equal(t, "bob@example.com", shares.inserted.SharedWithEmail)
	})
}

func TestCreateShareMessage(t *testing.T) {
	t.Run("travels to the notification", func(t *testing.T) {
		h, rec, shares := shareHandlerWithNotifier(testUser)
		w := postShare(t, h,
			`{"shared_with_email":"bob@example.com","message":"Here is the Q3 breakdown you asked about"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		assert.Equal(t, "Here is the Q3 breakdown you asked about", rec.last.Message)
		// The note is not part of the share: nothing persists it.
		require.NotNil(t, shares.inserted)
		assert.Equal(t, defaultNoticeText, shares.inserted.NoticeText)
	})

	t.Run("suppressed with notify false", func(t *testing.T) {
		h, rec, _ := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"shared_with_email":"bob@example.com","notify":false,"message":"quiet note"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		assert.Zero(t, rec.shares)
	})

	for name, body := range map[string]string{
		"html anchor": `{"shared_with_email":"bob@example.com","message":"see <a href=\"http://x.io\">here</a>"}`,
		"bare tag":    `{"shared_with_email":"bob@example.com","message":"<b>urgent</b>"}`,
		"url":         `{"shared_with_email":"bob@example.com","message":"details at https://x.io/report"}`,
		"www host":    `{"shared_with_email":"bob@example.com","message":"details at www.x.io"}`,
		"javascript":  `{"shared_with_email":"bob@example.com","message":"javascript:alert(1)"}`,
		"too long":    `{"shared_with_email":"bob@example.com","message":"` + strings.Repeat("a", 501) + `"}`,
	} {
		t.Run("rejected: "+name, func(t *testing.T) {
			h, rec, _ := shareHandlerWithNotifier(testUser)
			w := postShare(t, h, body)
			assert.Equal(t, http.StatusBadRequest, w.Code)
			assert.Zero(t, rec.shares, "a rejected share must notify nobody")
		})
	}

	t.Run("plain prose with comparison operators is accepted", func(t *testing.T) {
		h, _, _ := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"shared_with_email":"bob@example.com","message":"margin > 40% and churn < 2%"}`)
		assert.Equal(t, http.StatusCreated, w.Code)
	})
}

func TestCreateShareDisplayNameEmail(t *testing.T) {
	t.Run("stores the bare address", func(t *testing.T) {
		h, rec, shares := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"shared_with_email":"Example User <Bob@Example.COM>"}`)
		require.Equal(t, http.StatusCreated, w.Code)

		var resp shareResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Equal(t, "bob@example.com", resp.Share.SharedWithEmail)
		require.NotNil(t, shares.inserted)
		assert.Equal(t, "bob@example.com", shares.inserted.SharedWithEmail)
		assert.Equal(t, 1, rec.shares)
	})

	t.Run("rejects an address that cannot be extracted", func(t *testing.T) {
		for _, input := range []string{"Example User <not-an-address>", "not-an-address", "a@b", "<>"} {
			h, _, _ := shareHandlerWithNotifier(testUser)
			w := postShare(t, h, `{"shared_with_email":"`+input+`"}`)
			assert.Equal(t, http.StatusBadRequest, w.Code, "input %q must be rejected", input)
		}
	})
}

// TestCreateShareExpiryIsPublicOnly pins the one rule that decides a share's
// lifetime (#1279): a public link is a bearer credential and must expire,
// while every other share resolves against who the viewer is and ends when the
// owner revokes it.
func TestCreateShareExpiryIsPublicOnly(t *testing.T) {
	t.Run("public link accepts expires_in", func(t *testing.T) {
		h, _, _ := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"access_mode":"public","expires_in":"24h"}`)
		require.Equal(t, http.StatusCreated, w.Code)

		var resp shareResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		require.NotNil(t, resp.Share.ExpiresAt)
	})

	t.Run("public link with a dead-on-arrival lifetime is refused", func(t *testing.T) {
		for _, body := range []string{
			`{"access_mode":"public","expires_in":"0s"}`,
			`{"access_mode":"public","expires_in":"-1h"}`,
		} {
			h, _, shares := shareHandlerWithNotifier(testUser)
			w := postShare(t, h, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body %s", body)
			assert.Contains(t, w.Body.String(), "positive duration")
			assert.Nil(t, shares.inserted, "a refused share must store nothing")
		}
	})

	t.Run("public link without expires_in is refused", func(t *testing.T) {
		h, _, shares := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"access_mode":"public"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "expires_in is required")
		assert.Nil(t, shares.inserted, "a refused share must store nothing")
	})

	t.Run("authenticated link never expires", func(t *testing.T) {
		for _, body := range []string{`{}`, `{"access_mode":"authenticated"}`} {
			h, _, shares := shareHandlerWithNotifier(testUser)
			w := postShare(t, h, body)
			require.Equal(t, http.StatusCreated, w.Code, "body %s", body)

			var resp shareResponse
			require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
			assert.Equal(t, AccessModeAuthenticated, resp.Share.AccessMode)
			assert.Nil(t, resp.Share.ExpiresAt,
				"a link only signed-in users can open ends by revocation, not a clock")
			require.NotNil(t, shares.inserted)
			assert.Nil(t, shares.inserted.ExpiresAt, "the stored share must carry no expiry either")
		}
	})

	t.Run("expires_in on an authenticated link is refused", func(t *testing.T) {
		for _, body := range []string{`{"expires_in":"24h"}`, `{"access_mode":"authenticated","expires_in":"24h"}`} {
			h, _, shares := shareHandlerWithNotifier(testUser)
			w := postShare(t, h, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body %s", body)
			assert.Contains(t, w.Body.String(), "only signed-in users can open")
			assert.Nil(t, shares.inserted, "a refused share must store nothing")
		}
	})

	t.Run("assets and collections answer alike", func(t *testing.T) {
		// The share routes are separate handlers over one buildShare, so this
		// asserts the lifetime rule reaches the collection route too, not only
		// the asset route the rest of this file exercises. A prompt is not
		// shared by link at all (createPromptShare requires a recipient), so
		// it is covered below rather than here.
		routes := shareRoutes(t)
		for _, route := range []shareRoute{routes.asset, routes.collection} {
			for _, tc := range []struct {
				name string
				body string
				want int
			}{
				{"authenticated link is created", `{}`, http.StatusCreated},
				{"expires_in on an authenticated link", `{"expires_in":"24h"}`, http.StatusBadRequest},
				{"public link without expires_in", `{"access_mode":"public"}`, http.StatusBadRequest},
				{"public link with expires_in", `{"access_mode":"public","expires_in":"24h"}`, http.StatusCreated},
			} {
				w := route.post(tc.body)
				assert.Equal(t, tc.want, w.Code, "%s: %s", route.kind, tc.name)
			}
		}
	})

	t.Run("a prompt share is addressed to a person and never expires", func(t *testing.T) {
		route := shareRoutes(t).prompt

		w := route.post(`{"shared_with_email":"bob@example.com"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		var resp shareResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Nil(t, resp.Share.ExpiresAt)

		w = route.post(`{"shared_with_email":"bob@example.com","expires_in":"24h"}`)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "addressed to a person")
	})

	t.Run("named share never expires", func(t *testing.T) {
		h, _, _ := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"shared_with_email":"bob@example.com"}`)
		require.Equal(t, http.StatusCreated, w.Code)

		var resp shareResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		assert.Nil(t, resp.Share.ExpiresAt, "a share addressed to a person ends by revocation, not a clock")
	})

	t.Run("expires_in with a recipient is refused", func(t *testing.T) {
		for _, body := range []string{
			`{"shared_with_email":"bob@example.com","expires_in":"24h"}`,
			`{"shared_with_user_id":"u-2","expires_in":"24h"}`,
			`{"shared_with_email":"bob@example.com","access_mode":"restricted","expires_in":"24h"}`,
		} {
			h, _, shares := shareHandlerWithNotifier(testUser)
			w := postShare(t, h, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body %s", body)
			assert.Contains(t, w.Body.String(), "addressed to a person")
			assert.Nil(t, shares.inserted, "a refused share must store nothing")
		}
	})
}
