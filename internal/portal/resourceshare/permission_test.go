package resourceshare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

// A file share's permission changes in place, under the rule the portal's
// shares keep: an address with no account can only view (#2291).
func TestChangeAFileSharePermission(t *testing.T) {
	f := newFixture()
	f.svc.d.HasAccount = func(_ context.Context, email string) bool { return email == "reader@example.com" }

	w := f.do("owner@example.com", http.MethodPost, "/api/v1/resources/r1/shares", `{"shared_with_email":"reader@example.com","notify":false}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var share portaldomain.Share
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &share))
	path := "/api/v1/resources/r1/shares/" + share.ID

	w = f.do("owner@example.com", http.MethodPatch, path, `{"permission":"editor"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Equal(t, portaldomain.PermissionEditor, f.shares.rows[0].Permission)

	w = f.do("reader@example.com", http.MethodPatch, path, `{"permission":"viewer"}`)
	assert.Equal(t, http.StatusForbidden, w.Code, "a recipient does not change their own grant")
	w = f.do("owner@example.com", http.MethodPatch, path, `{`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	w = f.do("owner@example.com", http.MethodPatch, "/api/v1/resources/r1/shares/missing", `{"permission":"viewer"}`)
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = f.do("signed-out", http.MethodPatch, path, `{"permission":"viewer"}`)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	w = f.do("owner@example.com", http.MethodPost, "/api/v1/resources/r1/shares", `{"shared_with_email":"guest@example.org","permission":"editor","notify":false}`)
	assert.Equal(t, http.StatusBadRequest, w.Code, "an address with no account is not made an editor")
	w = f.do("owner@example.com", http.MethodPost, "/api/v1/resources/r1/shares", `{"shared_with_email":"guest@example.org","notify":false}`)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &share))
	w = f.do("owner@example.com", http.MethodPatch, "/api/v1/resources/r1/shares/"+share.ID, `{"permission":"editor"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	f.shares.err = errors.New("down")
	w = f.do("owner@example.com", http.MethodPatch, path, `{"permission":"viewer"}`)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
