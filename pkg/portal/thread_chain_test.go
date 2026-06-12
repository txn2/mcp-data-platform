package portal

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/toolkits/knowledge"
)

type mockChangesetReader struct {
	changesets []knowledge.Changeset
	err        error
	gotFilter  knowledge.ChangesetFilter
}

func (m *mockChangesetReader) ListChangesets(_ context.Context, f knowledge.ChangesetFilter) ([]knowledge.Changeset, int, error) {
	m.gotFilter = f
	return m.changesets, len(m.changesets), m.err
}

func chainHandler(threads *mockThreadStore, reader ChangesetReader, user *User) *Handler {
	return NewHandler(Deps{
		AssetStore:      ownedAsset("u1"),
		ShareStore:      &mockShareStore{},
		ThreadStore:     threads,
		ChangesetReader: reader,
		AdminRoles:      []string{"admin"},
		RateLimit:       RateLimitConfig{RequestsPerMinute: 600, BurstSize: 100},
	}, testAuthMiddleware(user))
}

func assetThreadStore(insightID string) *mockThreadStore {
	return &mockThreadStore{getResult: &Thread{
		ID: "t1", TargetType: targetTypeAsset, AssetID: "asset_1", InsightID: insightID,
	}}
}

func TestGetThreadChain(t *testing.T) {
	user := &User{UserID: "u1", Email: "u1@example.com"}

	t.Run("returns the thread -> insight -> changeset chain", func(t *testing.T) {
		reader := &mockChangesetReader{changesets: []knowledge.Changeset{
			{ID: "cs_1", TargetURN: "urn:li:dataset:x", ChangeType: "update_description"},
		}}
		h := chainHandler(assetThreadStore("ins_1"), reader, user)
		w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/t1/chain", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var resp threadChainResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Equal(t, "ins_1", resp.InsightID)
		require.Len(t, resp.Changesets, 1)
		assert.Equal(t, "cs_1", resp.Changesets[0].ID)
		assert.Equal(t, "urn:li:dataset:x", resp.Changesets[0].TargetURN)
		// The reader is queried by the thread's insight id (the JSONB containment).
		assert.Equal(t, "ins_1", reader.gotFilter.SourceInsightID)
	})

	t.Run("thread with no insight id returns empty chain and skips the reader", func(t *testing.T) {
		reader := &mockChangesetReader{}
		h := chainHandler(assetThreadStore(""), reader, user)
		w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/t1/chain", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var resp threadChainResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Empty(t, resp.Changesets)
		assert.Equal(t, "", reader.gotFilter.SourceInsightID) // reader never called
	})

	t.Run("nil changeset reader returns empty chain", func(t *testing.T) {
		h := chainHandler(assetThreadStore("ins_1"), nil, user)
		w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/t1/chain", nil)
		require.Equal(t, http.StatusOK, w.Code)

		var resp threadChainResponse
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		assert.Empty(t, resp.Changesets)
	})

	t.Run("changeset reader error returns 500", func(t *testing.T) {
		reader := &mockChangesetReader{err: errors.New("boom")}
		h := chainHandler(assetThreadStore("ins_1"), reader, user)
		w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/t1/chain", nil)
		assert.Equal(t, http.StatusInternalServerError, w.Code)
	})

	t.Run("non-owner is denied", func(t *testing.T) {
		stranger := &User{UserID: "u2", Email: "u2@example.com"}
		h := chainHandler(assetThreadStore("ins_1"), &mockChangesetReader{}, stranger)
		w := doThreadReq(t, h, http.MethodGet, "/api/v1/portal/threads/t1/chain", nil)
		assert.Equal(t, http.StatusForbidden, w.Code)
	})
}
