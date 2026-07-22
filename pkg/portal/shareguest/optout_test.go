package shareguest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// resubRecorder captures resubscribe calls for handler tests.
type resubRecorder struct {
	mu     sync.Mutex
	emails []string
	err    error
}

func (r *resubRecorder) resubscribe(_ context.Context, email string) error {
	if r.err != nil {
		return r.err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.emails = append(r.emails, email)
	return nil
}

func (r *resubRecorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.emails...)
}

// newOptOutService builds a service over share whose recipient reads as
// opted out (or not), with the resubscribe recorder wired in.
func newOptOutService(share ShareInfo, optedOut bool, optOutErr error, rec *resubRecorder) *Service {
	cfg := Config{
		Resolve: func(_ context.Context, token string) (ShareInfo, bool) {
			if token == share.Token {
				return share, true
			}
			return ShareInfo{}, false
		},
		Brand: Brand{Name: "ACME Data"},
		OptOutStatus: func(context.Context, string) (bool, error) {
			return optedOut, optOutErr
		},
	}
	if rec != nil {
		cfg.Resubscribe = rec.resubscribe
	}
	return New(cfg)
}

// anonDenial is the default-case denial the opt-out notice renders in.
func anonDenial() Denial {
	return Denial{
		Status:         http.StatusForbidden,
		Message:        "sign in required",
		Token:          "tok1",
		RecipientEmail: "bob@example.com",
	}
}

func TestDenyShowsOptOutNoticeAndResubscribe(t *testing.T) {
	svc := newOptOutService(fixtureShare(), true, nil, &resubRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), anonDenial())

	body := w.Body.String()
	assert.Contains(t, body, "opted out of notification emails")
	assert.Contains(t, body, "/portal/view/tok1/resubscribe")
	assert.Contains(t, body, "Resume notification emails")
	assert.NotContains(t, body, "bob@example.com", "the page must never display the recipient address")
}

func TestDenyOmitsOptOutNoticeWhenNotOptedOut(t *testing.T) {
	svc := newOptOutService(fixtureShare(), false, nil, &resubRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), anonDenial())

	assert.NotContains(t, w.Body.String(), "opted out")
	assert.NotContains(t, w.Body.String(), "resubscribe")
}

func TestDenyOmitsOptOutNoticeWithoutCallback(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), anonDenial())

	assert.NotContains(t, w.Body.String(), "opted out")
}

// TestDenyOptOutLookupFailureOmitsNotice pins the notice as informational
// only: a preference-store failure must not break the landing page.
func TestDenyOptOutLookupFailureOmitsNotice(t *testing.T) {
	svc := newOptOutService(fixtureShare(), true, errors.New("db down"), &resubRecorder{})
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), anonDenial())

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.NotContains(t, w.Body.String(), "opted out")
	assert.Contains(t, w.Body.String(), "This item was shared privately", "the page itself must still render")
}

func TestDenyOptOutNoticeWithoutResubscribeAction(t *testing.T) {
	svc := newOptOutService(fixtureShare(), true, nil, nil)
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), anonDenial())

	body := w.Body.String()
	assert.Contains(t, body, "opted out of notification emails")
	assert.NotContains(t, body, "resubscribe", "no opt-back-in action without a wired callback")
}

// TestDenyWrongAccountOmitsOptOutNotice pins the notice to the anonymous
// default case: a signed-in wrong-account viewer is not the re-engagement
// audience.
func TestDenyWrongAccountOmitsOptOutNotice(t *testing.T) {
	svc := newOptOutService(fixtureShare(), true, nil, &resubRecorder{})
	d := anonDenial()
	d.SignedInEmail = "carol@example.com"
	w := httptest.NewRecorder()
	svc.Deny(w, denyReq(""), d)

	assert.NotContains(t, w.Body.String(), "opted out")
}

func TestHandleResubscribeRestoresDelivery(t *testing.T) {
	rec := &resubRecorder{}
	svc := newOptOutService(fixtureShare(), true, nil, rec)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/portal/view/tok1/resubscribe", http.NoBody)
	r.SetPathValue("token", "tok1")
	svc.HandleResubscribe(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	var body map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, resubscribeResponse, body["message"])
	assert.Equal(t, []string{"bob@example.com"}, rec.calls())
}

// TestHandleResubscribeUniformResponse proves every non-qualifying case is a
// silent no-op behind the identical body, so the endpoint confirms nothing
// about any share to a caller who merely holds a URL.
func TestHandleResubscribeUniformResponse(t *testing.T) {
	tests := []struct {
		name  string
		share ShareInfo
		token string
	}{
		{name: "unknown token", share: fixtureShare(), token: "other"},
		{name: "empty token", share: fixtureShare(), token: ""},
		{name: "public share", share: ShareInfo{ID: "sh1", Token: "tok1", RecipientEmail: "bob@example.com", Public: true}, token: "tok1"},
		{name: "revoked share", share: ShareInfo{ID: "sh1", Token: "tok1", RecipientEmail: "bob@example.com", Revoked: true}, token: "tok1"},
		{name: "no recipient", share: ShareInfo{ID: "sh1", Token: "tok1"}, token: "tok1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := &resubRecorder{}
			svc := newOptOutService(tc.share, true, nil, rec)

			w := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
				"/portal/view/x/resubscribe", http.NoBody)
			r.SetPathValue("token", tc.token)
			svc.HandleResubscribe(w, r)

			assert.Equal(t, http.StatusOK, w.Code)
			var body map[string]string
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, resubscribeResponse, body["message"])
			assert.Empty(t, rec.calls(), "a non-qualifying share must write nothing")
		})
	}
}

func TestHandleResubscribeWithoutCallbackIsInert(t *testing.T) {
	svc := newTestService(t, fixtureShare(), newMemLinkStore(), &mailRecorder{})
	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/portal/view/tok1/resubscribe", http.NoBody)
	r.SetPathValue("token", "tok1")
	svc.HandleResubscribe(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), resubscribeResponse)
}

func TestHandleResubscribeWriteFailureStaysSilent(t *testing.T) {
	rec := &resubRecorder{err: errors.New("db down")}
	svc := newOptOutService(fixtureShare(), true, nil, rec)

	w := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/portal/view/tok1/resubscribe", http.NoBody)
	r.SetPathValue("token", "tok1")
	svc.HandleResubscribe(w, r)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), resubscribeResponse)
}
