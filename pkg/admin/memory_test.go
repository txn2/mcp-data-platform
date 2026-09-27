package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

type stubMemoryLister struct{ calls int }

func (s *stubMemoryLister) List(context.Context, memory.Filter) ([]memory.Record, int, error) {
	s.calls++
	return []memory.Record{{ID: "m1", CreatedBy: "alice@example.com"}}, 1, nil
}

// The admin memory list sits behind the same gate as every admin route: a
// caller the admin authenticator does not resolve (a non-admin persona) is
// refused before the store is read.
func TestMemoryRecordsRoute_AdminGate(t *testing.T) {
	for _, tc := range []struct {
		name  string
		user  *User
		code  int
		reads int
	}{
		{"admin lists", &User{UserID: "admin-1", Roles: []string{testRoleAdmin}}, http.StatusOK, 1},
		{"non-admin refused", nil, http.StatusUnauthorized, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubMemoryLister{}
			h := NewHandler(Deps{MemoryRecords: store, PersonaRegistry: &mockPersonaRegistry{}},
				RequirePersona(&mockAuthenticator{user: tc.user}))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
				"/api/v1/admin/memory/records", http.NoBody))
			assert.Equal(t, tc.code, w.Code)
			assert.Equal(t, tc.reads, store.calls)
		})
	}
}
