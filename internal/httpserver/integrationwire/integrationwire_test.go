package integrationwire

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/txn2/mcp-data-platform/internal/platform/integration"
	"github.com/txn2/mcp-data-platform/pkg/admin"
)

func TestActor(t *testing.T) {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
	assert.Equal(t, integration.Actor{}, Actor(r))
	r = r.WithContext(admin.ContextWithUser(r.Context(), &admin.User{UserID: "u1", Roles: []string{"admin"}}))
	assert.Equal(t, integration.Actor{Email: "u1", Roles: []string{"admin"}, Admin: true}, Actor(r))
	r = r.WithContext(admin.ContextWithUser(r.Context(), &admin.User{UserID: "u1", Email: "ops@example.com"}))
	assert.Equal(t, "ops@example.com", Actor(r).Email)
	Mount(http.NewServeMux(), nil, nil)
}
