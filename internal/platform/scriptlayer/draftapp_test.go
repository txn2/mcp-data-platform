package scriptlayer

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// A draft names the application it runs in, resolved for the caller; none
// names nothing, and a deployment keeping no applications refuses one
// (#2709).
func TestDraftApplication(t *testing.T) {
	h := &Handle{}
	ctx := middleware.WithPlatformContext(context.Background(), &middleware.PlatformContext{UserEmail: "jane@example.com", IsAdmin: true})
	id, conn, err := h.draftApplication(ctx, "")
	require.NoError(t, err)
	assert.Empty(t, id+conn)
	_, _, err = h.draftApplication(ctx, "shop")
	require.ErrorContains(t, err, "keeps no applications")

	var asked struct {
		ref, email string
		admin      bool
	}
	h.SetDraftApplications(func(_ context.Context, ref, email string, admin bool) (string, string, error) {
		asked.ref, asked.email, asked.admin = ref, email, admin
		return "app_1", "orders", nil
	})
	id, conn, err = h.draftApplication(ctx, "shop")
	require.NoError(t, err)
	assert.Equal(t, [2]string{"app_1", "orders"}, [2]string{id, conn})
	assert.Equal(t, "shop", asked.ref)
	assert.True(t, asked.admin)
}
