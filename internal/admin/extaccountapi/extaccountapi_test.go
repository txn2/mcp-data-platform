package extaccountapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/extaccount"
)

type fakeService struct {
	accounts  []extaccount.Account
	createErr error
	opErr     error
	requested string
	claimErr  error
	calls     []string
}

func (f *fakeService) Create(_ context.Context, n extaccount.NewAccount, by string) (extaccount.Account, error) {
	f.calls = append(f.calls, "create:"+n.Email+":"+by)
	return extaccount.Account{ID: "a1", Email: n.Email, Persona: n.Persona}, f.createErr
}

func (f *fakeService) List(context.Context) ([]extaccount.Account, error) { return f.accounts, f.opErr }

func (f *fakeService) Extend(_ context.Context, id string, at *time.Time) error {
	f.calls = append(f.calls, "extend:"+id+":"+boolStr(at != nil))
	return f.opErr
}

func (f *fakeService) Suspend(_ context.Context, id string) error {
	f.calls = append(f.calls, "suspend:"+id)
	return f.opErr
}

func (f *fakeService) Resume(_ context.Context, id string) error {
	f.calls = append(f.calls, "resume:"+id)
	return f.opErr
}

func (f *fakeService) Remove(_ context.Context, id string) error {
	f.calls = append(f.calls, "remove:"+id)
	return f.opErr
}

func (f *fakeService) RequestLink(_ context.Context, email string) { f.requested = email }

func (f *fakeService) Claim(_ context.Context, _ http.ResponseWriter, _ string) (extaccount.Account, error) {
	return extaccount.Account{}, f.claimErr
}

func boolStr(b bool) string {
	if b {
		return "set"
	}
	return "cleared"
}

type fakeSettings struct{ v extaccount.Settings }

func (s *fakeSettings) Read(context.Context) extaccount.Settings { return s.v }
func (s *fakeSettings) Write(_ context.Context, v extaccount.Settings, _ string) error {
	s.v = v
	return nil
}

func newMux(svc *fakeService, settings *fakeSettings) *http.ServeMux {
	mux := http.NewServeMux()
	pass := func(h http.Handler) http.Handler { return h }
	RegisterAdmin(mux, pass, Config{
		Service: svc, Settings: settings, Author: func(*http.Request) string { return "admin@example.com" },
		SharedItems: func(context.Context, string) (int, error) { return 3, nil },
		Now:         func() time.Time { return time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC) },
	})
	RegisterPublic(mux, svc)
	return mux
}

func do(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func TestAdminRoutes(t *testing.T) {
	svc := &fakeService{accounts: []extaccount.Account{{ID: "a1", Email: "c@example.org", Persona: "client"}}}
	settings := &fakeSettings{v: extaccount.Defaults()}
	mux := newMux(svc, settings)

	w := do(mux, http.MethodGet, usersPath, "")
	require.Equal(t, http.StatusOK, w.Code)
	var list UserList
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(t, list.Users, 1)
	assert.Equal(t, "active", list.Users[0].Status)
	assert.Equal(t, 3, list.Users[0].SharedItems)

	w = do(mux, http.MethodPost, usersPath, `{"email":"c@example.org","name":"C","persona":"client"}`)
	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	assert.Contains(t, svc.calls, "create:c@example.org:admin@example.com")
	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodPost, usersPath, `{"bogus":1}`).Code)

	assert.Equal(t, http.StatusNoContent, do(mux, http.MethodPost, usersPath+"/a1/extend", `{"expires_at":"2027-01-01T00:00:00Z"}`).Code)
	assert.Equal(t, http.StatusNoContent, do(mux, http.MethodPost, usersPath+"/a1/extend", `{"expires_at":null}`).Code)
	assert.Equal(t, http.StatusNoContent, do(mux, http.MethodPost, usersPath+"/a1/suspend", "").Code)
	assert.Equal(t, http.StatusNoContent, do(mux, http.MethodPost, usersPath+"/a1/resume", "").Code)
	assert.Equal(t, http.StatusNoContent, do(mux, http.MethodDelete, usersPath+"/a1", "").Code)
	assert.Equal(t, []string{"create:c@example.org:admin@example.com", "extend:a1:set", "extend:a1:cleared", "suspend:a1", "resume:a1", "remove:a1"}, svc.calls)

	assert.Equal(t, http.StatusOK, do(mux, http.MethodGet, settingsPath, "").Code)
	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodPut, settingsPath, `{"session_hours":0,"invite_hours":1,"link_minutes":1}`).Code)
	w = do(mux, http.MethodPut, settingsPath, `{"session_hours":24,"invite_hours":48,"link_minutes":10}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 24, settings.v.SessionHours)
}

func TestAdminRoutesMapEachError(t *testing.T) {
	for err, want := range map[error]int{
		extaccount.ErrNotFound: http.StatusNotFound,
		extaccount.ErrExists:   http.StatusConflict,
		extaccount.ErrInternal: http.StatusConflict,
		extaccount.ErrPersona:  http.StatusBadRequest,
		extaccount.ErrInvalid:  http.StatusBadRequest,
		errors.New("down"):     http.StatusInternalServerError,
	} {
		svc := &fakeService{createErr: err, opErr: err}
		mux := newMux(svc, &fakeSettings{})
		assert.Equal(t, want, do(mux, http.MethodPost, usersPath, `{"email":"c@example.org","persona":"client"}`).Code, err.Error())
		assert.Equal(t, want, do(mux, http.MethodPost, usersPath+"/a1/suspend", "").Code, err.Error())
	}
	assert.Equal(t, http.StatusInternalServerError, do(newMux(&fakeService{opErr: errors.New("down")}, &fakeSettings{}), http.MethodGet, usersPath, "").Code)
}

func TestPublicRoutes(t *testing.T) {
	svc := &fakeService{}
	mux := newMux(svc, &fakeSettings{})
	w := do(mux, http.MethodPost, RequestPath, `{"email":"c@example.org"}`)
	assert.Equal(t, http.StatusAccepted, w.Code)
	assert.Equal(t, "c@example.org", svc.requested)

	w = do(mux, http.MethodGet, extaccount.ClaimPath+"?token=t", "")
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, signedInPath, w.Header().Get("Location"))

	for _, err := range []error{extaccount.ErrLink, errors.New("down")} {
		svc.claimErr = err
		w = do(mux, http.MethodGet, extaccount.ClaimPath+"?token=t", "")
		assert.Equal(t, failedPath, w.Header().Get("Location"))
	}
}

// The route the claim handler is mounted on is the one the service writes
// into every emailed link.
func TestClaimPathIsTheLinkPath(t *testing.T) {
	if claimPath != extaccount.ClaimPath {
		t.Fatalf("claim route %q, emailed links open %q", claimPath, extaccount.ClaimPath)
	}
}

// A body that is not the request, or settings out of range, is refused.
func TestAdminRoutesRefuseBadBodies(t *testing.T) {
	mux := newMux(&fakeService{}, &fakeSettings{v: extaccount.Defaults()})
	for path, body := range map[string]string{
		usersPath + "/a1/extend": `{"expires_at":`,
		settingsPath:             `{"session_hours":`,
	} {
		method := http.MethodPost
		if path == settingsPath {
			method = http.MethodPut
		}
		assert.Equal(t, http.StatusBadRequest, do(mux, method, path, body).Code, path)
	}
	assert.Equal(t, http.StatusBadRequest, do(mux, http.MethodPut, settingsPath, `{"session_hours":0,"invite_hours":1,"link_minutes":1}`).Code)
}
