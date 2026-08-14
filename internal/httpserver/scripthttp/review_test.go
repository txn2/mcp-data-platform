package scripthttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The review-queue and rejection halves of the stub store.

func (s *stubStore) ListPendingReviews(context.Context) ([]script.PendingReview, error) {
	return s.pending, s.pendingErr
}

func (s *stubStore) RejectVersion(_ context.Context, _ string, version int) error {
	if s.rejectErr != nil {
		return s.rejectErr
	}
	s.rejected = append(s.rejected, version)
	return nil
}

const reviewQueuePath = "/api/v1/admin/scripts/reviews"

func TestListPendingReviews(t *testing.T) {
	t.Run("an empty queue is an empty list, not an absent one", func(t *testing.T) {
		store := newStore()
		store.pending = []script.PendingReview{}
		rec := serve(t, store, http.MethodGet, reviewQueuePath, "")
		require.Equal(t, http.StatusOK, rec.Code)
		body := decode(t, rec)
		assert.Equal(t, float64(0), body["total"])
		assert.Equal(t, []any{}, body["data"])
	})

	t.Run("pending rows carry what a queue row shows", func(t *testing.T) {
		store := newStore()
		store.pending = []script.PendingReview{{
			ScriptID: "script_1", ScriptName: "daily", Version: 2,
			VersionStatus: script.VersionStatusDraft, Author: "jane@example.com",
			AuthorRoles: []string{"analyst"}, FirstApproval: false,
			CreatedAt: time.Now().UTC().AddDate(0, 0, -3),
		}}
		rec := serve(t, store, http.MethodGet, reviewQueuePath, "")
		require.Equal(t, http.StatusOK, rec.Code)
		body := decode(t, rec)
		assert.Equal(t, float64(1), body["total"])
		data, ok := body["data"].([]any)
		require.True(t, ok, rec.Body.String())
		row, ok := data[0].(map[string]any)
		require.True(t, ok, rec.Body.String())
		assert.Equal(t, "daily", row["script_name"])
		assert.Equal(t, float64(2), row["version"])
		assert.Equal(t, []any{"analyst"}, row["author_roles"])
	})

	t.Run("a store failure is an error, never an empty queue", func(t *testing.T) {
		store := newStore()
		store.pendingErr = errors.New("boom")
		rec := serve(t, store, http.MethodGet, reviewQueuePath, "")
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("the literal route is not shadowed by the script wildcard", func(t *testing.T) {
		store := newStore()
		// A script whose id would collide with the queue path segment.
		store.scripts[0].ID = "reviews"
		store.pending = []script.PendingReview{}
		rec := serve(t, store, http.MethodGet, reviewQueuePath, "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), `"total":0`)
	})
}

func TestRejectVersion(t *testing.T) {
	t.Run("a pending draft is rejected", func(t *testing.T) {
		store := newStore()
		store.version.Status = script.VersionStatusDraft
		rec := serve(t, store, http.MethodPost, "/api/v1/admin/scripts/script_1/versions/1/reject", "")
		require.Equal(t, http.StatusOK, rec.Code)
		assert.Equal(t, []int{1}, store.rejected)
		assert.Equal(t, script.VersionStatusRejected, decode(t, rec)["status"])
	})

	t.Run("a version that is not a pending draft is a conflict", func(t *testing.T) {
		store := newStore()
		store.rejectErr = fmt.Errorf("version 1 is not a pending draft: %w", script.ErrVersionConflict)
		rec := serve(t, store, http.MethodPost, "/api/v1/admin/scripts/script_1/versions/1/reject", "")
		assert.Equal(t, http.StatusConflict, rec.Code)
	})

	t.Run("a store failure never echoes its detail", func(t *testing.T) {
		store := newStore()
		store.rejectErr = errors.New("pq: relation script_versions does not exist")
		rec := serve(t, store, http.MethodPost, "/api/v1/admin/scripts/script_1/versions/1/reject", "")
		require.Equal(t, http.StatusInternalServerError, rec.Code)
		assert.NotContains(t, rec.Body.String(), "pq:")
	})

	t.Run("an unknown version is not found", func(t *testing.T) {
		rec := serve(t, newStore(), http.MethodPost, "/api/v1/admin/scripts/script_1/versions/9/reject", "")
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// TestGetVersion_CarriesTheApprovedBaseline is the capability diff's other
// half: a reviewer reads a change against what the script executes today, and
// both halves come from one response so they cannot describe two moments.
func TestGetVersion_CarriesTheApprovedBaseline(t *testing.T) {
	approvedAt := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	store := newStore()
	store.scripts[0].ApprovedVersionID = "sver_approved"
	store.version.Version = 2
	store.version.Status = script.VersionStatusDraft
	store.byID = map[string]*script.Version{
		"sver_approved": {
			ID: "sver_approved", ScriptID: "script_1", Version: 1,
			Source:     `res = platform.query(connection="warehouse", sql="SELECT 1")` + "\n",
			ApprovedBy: "admin@example.com", ApprovedAt: &approvedAt,
			Grants: script.Grants{
				Roles: []string{"analyst"}, Connections: []string{"warehouse"},
				Capabilities: []string{script.CapabilityQuery},
			},
		},
	}

	rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/script_1/versions/2", "")
	require.Equal(t, http.StatusOK, rec.Code)
	body := decode(t, rec)

	approved, ok := body["approved"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, float64(1), approved["version"])
	assert.Equal(t, "admin@example.com", approved["approved_by"])

	grants, ok := approved["grants"].(map[string]any)
	require.True(t, ok, rec.Body.String())
	assert.Equal(t, []any{script.CapabilityQuery}, grants["capabilities"],
		"the reviewer reads the new grant against the one the script holds today")

	diff, _ := approved["source_diff"].(string)
	assert.Contains(t, diff, "v1 (approved)")
	assert.Contains(t, diff, "+platform.export", "the added call is visible as an addition")
}

// TestGetVersion_FirstApprovalHasNoBaseline: a script with no approved version
// executes nothing, so there is no diff to draw. The field is absent rather
// than an empty object, which is how the surface distinguishes "first
// approval" from "no change".
func TestGetVersion_FirstApprovalHasNoBaseline(t *testing.T) {
	rec := serve(t, newStore(), http.MethodGet, "/api/v1/admin/scripts/script_1/versions/1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	_, present := decode(t, rec)["approved"]
	assert.False(t, present)
}

// TestGetVersion_UnreadableBaselineIsOmittedNotFabricated: a gate pointing at a
// version that cannot be read must not be reported as a first approval — the
// review is answered without the comparison instead.
func TestGetVersion_UnreadableBaselineIsOmittedNotFabricated(t *testing.T) {
	store := newStore()
	store.scripts[0].ApprovedVersionID = "sver_gone"
	store.byID = map[string]*script.Version{}

	rec := serve(t, store, http.MethodGet, "/api/v1/admin/scripts/script_1/versions/1", "")
	require.Equal(t, http.StatusOK, rec.Code)
	_, present := decode(t, rec)["approved"]
	assert.False(t, present)
}
