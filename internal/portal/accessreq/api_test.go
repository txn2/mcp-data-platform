package accessreq

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
)

// memRequests keeps the Requests contract: one pending request per requester
// and item, MaxPerDay a day, an answer recorded once.
type memRequests struct {
	mu   sync.Mutex
	rows []*Request
}

func (m *memRequests) Open(_ context.Context, r Request) (*Request, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, x := range m.rows {
		if x.TargetType == r.TargetType && x.TargetID == r.TargetID && x.RequesterID == r.RequesterID {
			if x.Status == StatusPending {
				return x, false, nil
			}
			n++
		}
	}
	if n >= MaxPerDay {
		return nil, false, ErrTooMany
	}
	r.Status, r.CreatedAt = StatusPending, time.Now()
	m.rows = append(m.rows, &r)
	return &r, true, nil
}

func (m *memRequests) Pending(_ context.Context, tt, id, who string) (*Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.rows {
		if x.TargetType == tt && x.TargetID == id && x.RequesterID == who && x.Status == StatusPending {
			return x, nil
		}
	}
	return nil, ErrNotFound
}

func (m *memRequests) Get(_ context.Context, id string) (*Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.rows {
		if x.ID == id {
			c := *x
			return &c, nil
		}
	}
	return nil, ErrNotFound
}

func (m *memRequests) ListForOwner(_ context.Context, owner, tt, id string, all bool) ([]Request, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Request{}
	for _, x := range m.rows {
		if x.Status == StatusPending && (all || strings.EqualFold(x.OwnerEmail, owner)) && (tt == "" || (x.TargetType == tt && x.TargetID == id)) {
			out = append(out, *x)
		}
	}
	return out, nil
}

func (m *memRequests) Decide(_ context.Context, id string, d Decision) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, x := range m.rows {
		if x.ID == id {
			if x.Status != StatusPending {
				return ErrDecided
			}
			x.Status, x.Permission, x.DecidedBy, x.ShareID = d.Status, d.Permission, d.DecidedBy, d.ShareID
			return nil
		}
	}
	return ErrNotFound
}

type fixture struct {
	mux       *http.ServeMux
	store     *memRequests
	owner     int
	requester []string
	shared    []string
	reveal    bool
}

func newFixture() *fixture {
	f := &fixture{store: &memRequests{}}
	f.mux = http.NewServeMux()
	Register(f.mux, func(h http.Handler) http.Handler { return h }, Deps{
		Store: f.store,
		Resolve: func(_ context.Context, tt, id string) (Target, error) {
			if id == "missing" {
				return Target{}, errors.New("no")
			}
			return Target{Name: "Quarterly " + tt, OwnerID: "owner", OwnerEmail: "owner@example.com"}, nil
		},
		CanOpen: func(_ context.Context, u *access.User, _, id string) bool {
			return u.UserID == "owner" || id == "open"
		},
		IsAdmin: func(u *access.User) bool { return u.UserID == "admin" },
		Share: func(_ context.Context, tt, id, _, to, perm string) (string, error) {
			f.shared = append(f.shared, tt+":"+id+":"+to+":"+perm)
			return "share-1", nil
		},
		NotifyOwner:     func(context.Context, *Request, string) { f.owner++ },
		NotifyRequester: func(_ context.Context, r *Request, _ string) { f.requester = append(f.requester, r.Status) },
		Settings:        func(context.Context) Settings { return Settings{RevealNames: f.reveal, ExpireDays: 14} },
	})
	return f
}

func (f *fixture) do(user, method, path, body string) (w *httptest.ResponseRecorder, out map[string]any) {
	r := httptest.NewRequestWithContext(context.Background(), method, path, strings.NewReader(body))
	if user != "" {
		r = r.WithContext(access.ContextWithUser(r.Context(), &access.User{UserID: user, Email: user + "@example.com"}))
	}
	w = httptest.NewRecorder()
	f.mux.ServeHTTP(w, r)
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w, out
}

// A person who cannot open an item asks once, the owner is told once, and
// approving creates the share and tells the requester (#2090).
func TestRequestApproveFlow(t *testing.T) {
	f := newFixture()
	w, page := f.do("reader", http.MethodGet, "/api/v1/portal/assets/a1/access-request", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, false, page["has_access"])
	assert.Equal(t, "owner@example.com", page["owner_email"])
	assert.Nil(t, page["name"], "names are hidden unless the deployment reveals them")

	w, req := f.do("reader", http.MethodPost, "/api/v1/portal/assets/a1/access-requests", `{"message":"for the review"}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	w, again := f.do("reader", http.MethodPost, "/api/v1/portal/assets/a1/access-requests", `{}`)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, req["id"], again["id"])
	assert.Equal(t, 1, f.owner, "a second request while one is pending notifies nobody")

	_, page = f.do("reader", http.MethodGet, "/api/v1/portal/assets/a1/access-request", "")
	assert.NotNil(t, page["pending"])

	w, list := f.do("owner", http.MethodGet, "/api/v1/portal/access-requests", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Len(t, list["requests"], 1)

	id, _ := req["id"].(string)
	w, _ = f.do("reader", http.MethodPost, "/api/v1/portal/access-requests/"+id+"/approve", `{"permission":"viewer"}`)
	assert.Equal(t, http.StatusNotFound, w.Code, "only the owner or an administrator answers")
	w, _ = f.do("owner", http.MethodPost, "/api/v1/portal/access-requests/"+id+"/approve", `{"permission":"owner"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w, decided := f.do("owner", http.MethodPost, "/api/v1/portal/access-requests/"+id+"/approve", `{"permission":"editor"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, StatusApproved, decided["status"])
	assert.Equal(t, []string{"asset:a1:reader@example.com:editor"}, f.shared)
	assert.Equal(t, []string{StatusApproved}, f.requester)
	w, _ = f.do("owner", http.MethodPost, "/api/v1/portal/access-requests/"+id+"/decline", "")
	assert.Equal(t, http.StatusConflict, w.Code, "answered once")
}

func TestRequestDeclineAndRefusals(t *testing.T) {
	f := newFixture()
	f.reveal = true
	_, page := f.do("reader", http.MethodGet, "/api/v1/portal/prompts/p1/access-request", "")
	assert.Equal(t, "Quarterly prompt", page["name"], "a deployment that reveals names shows it")

	w, req := f.do("reader", http.MethodPost, "/api/v1/portal/collections/c1/access-requests", "")
	require.Equal(t, http.StatusCreated, w.Code)
	declineID, _ := req["id"].(string)
	w, _ = f.do("admin", http.MethodPost, "/api/v1/portal/access-requests/"+declineID+"/decline", "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{StatusDeclined}, f.requester)

	assert.Equal(t, http.StatusConflict, func() int {
		w, _ := f.do("reader", http.MethodPost, "/api/v1/portal/assets/open/access-requests", "")
		return w.Code
	}())
	assert.Equal(t, http.StatusNotFound, func() int {
		w, _ := f.do("reader", http.MethodPost, "/api/v1/portal/assets/missing/access-requests", "")
		return w.Code
	}())
	assert.Equal(t, http.StatusUnauthorized, func() int { w, _ := f.do("", http.MethodGet, "/api/v1/portal/access-requests", ""); return w.Code }())
	assert.Equal(t, http.StatusBadRequest, func() int {
		w, _ := f.do("reader", http.MethodPost, "/api/v1/portal/assets/a2/access-requests", `{"message":"`+strings.Repeat("x", 1001)+`"}`)
		return w.Code
	}())
	for range MaxPerDay {
		w, r := f.do("reader", http.MethodPost, "/api/v1/portal/assets/a3/access-requests", "")
		require.Equal(t, http.StatusCreated, w.Code)
		rid, _ := r["id"].(string)
		w, _ = f.do("owner", http.MethodPost, "/api/v1/portal/access-requests/"+rid+"/decline", "")
		require.Equal(t, http.StatusOK, w.Code)
	}
	w, _ = f.do("reader", http.MethodPost, "/api/v1/portal/assets/a3/access-requests", "")
	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestSettingsValidateAndExpiry(t *testing.T) {
	require.NoError(t, Defaults().Validate())
	assert.Error(t, Settings{ExpireDays: 0}.Validate())
	assert.True(t, ValidTarget(TargetPrompt))
	assert.False(t, ValidTarget("page"))
	r := Request{Status: StatusPending, ExpiresAt: time.Now().Add(-time.Minute)}
	r.reported(time.Now())
	assert.Equal(t, StatusExpired, r.Status)
}
