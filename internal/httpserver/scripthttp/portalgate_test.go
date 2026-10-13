package scripthttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGatePortalRefusesAPersonaThatHidesAutomations(t *testing.T) {
	hidden := &PortalIdentity{UserID: "u4", Persona: "viewer", AutomationsHidden: true}
	hiddenAdmin := &PortalIdentity{UserID: "u5", Persona: "viewer", AutomationsHidden: true, IsAdmin: true}
	cases := []struct {
		name   string
		caller *PortalIdentity
		want   int
	}{
		{"persona hides automations", hidden, http.StatusForbidden},
		{"administrator under a persona that hides them", hiddenAdmin, http.StatusOK},
		{"persona that shows them", owner, http.StatusOK},
		{"no caller on the request", nil, http.StatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			reached := false
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			gate := GatePortal(func(*http.Request) *PortalIdentity { return c.caller })
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/scripts", http.NoBody)
			w := httptest.NewRecorder()
			gate(next).ServeHTTP(w, req)

			assert.Equal(t, c.want, w.Code)
			assert.Equal(t, c.want == http.StatusOK, reached, "the route runs only when the gate lets the caller through")
			if c.want == http.StatusForbidden {
				assert.Contains(t, w.Body.String(), errAutomationsHidden)
			}
		})
	}
}

// TestGatedWrapRunsTheGateInsideTheAuthentication: the authentication puts
// the caller on the request first, and the gate reads them after it.
func TestGatedWrapRunsTheGateInsideTheAuthentication(t *testing.T) {
	type key struct{}
	authenticate := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hidden := &PortalIdentity{UserID: "u4", AutomationsHidden: true}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), key{}, hidden)))
		})
	}
	user := func(r *http.Request) *PortalIdentity {
		id, _ := r.Context().Value(key{}).(*PortalIdentity)
		return id
	}
	handler := GatedWrap(authenticate, user)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/api/v1/portal/scripts", http.NoBody))
	assert.Equal(t, http.StatusForbidden, w.Code, "the gate saw the caller the authentication put on the request")
}
