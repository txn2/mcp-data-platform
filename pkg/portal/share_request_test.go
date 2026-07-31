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

func TestCreateShareExpiryIsLinkOnly(t *testing.T) {
	t.Run("link share accepts expires_in", func(t *testing.T) {
		h, _, _ := shareHandlerWithNotifier(testUser)
		w := postShare(t, h, `{"expires_in":"24h"}`)
		require.Equal(t, http.StatusCreated, w.Code)

		var resp shareResponse
		require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
		require.NotNil(t, resp.Share.ExpiresAt)
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
		} {
			h, _, shares := shareHandlerWithNotifier(testUser)
			w := postShare(t, h, body)
			assert.Equal(t, http.StatusBadRequest, w.Code, "body %s", body)
			assert.Nil(t, shares.inserted, "a refused share must store nothing")
		}
	})
}
