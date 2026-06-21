package portal

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/memory"
)

// hybridSearchColumns is the column order HybridSearch scans: the 17 raw
// record columns followed by the per-arm vec_score and lex_match signals.
// sqlmock scans positionally, so only the count/order matter.
var hybridSearchColumns = []string{
	"id", "created_at", "updated_at", "created_by", "persona", "dimension", "sink_class",
	"content", "category", "confidence", "source",
	"entity_urns", "related_columns", "metadata",
	"status", "stale_reason", "stale_at", "last_verified",
	"vec_score", "lex_match",
}

func addHybridSearchRow(rows *sqlmock.Rows, id, createdBy string, vecScore float64) {
	now := time.Now()
	rows.AddRow(
		id, now, now, createdBy, "analyst", memory.DimensionKnowledge, "business_knowledge",
		"content for "+id, "business_context", "high", "user",
		[]byte("[]"), []byte("[]"), []byte("{}"),
		"active", nil, nil, nil,
		vecScore, true,
	)
}

// TestIntegration_SearchMyMemories_RealStoreEnforcesOwnerScope wires the
// real portal Handler to a real memory.PostgresStore (sqlmock at the DB
// boundary) and a configured embedder, then sends a real HTTP request
// through the real mux + auth middleware. It proves the end-to-end claim
// that matters for #516: the authenticated caller's email actually arrives
// as the created_by SQL predicate (the per-user search boundary), and the
// relevance score travels back to the JSON response. A unit test with a
// mocked store cannot prove the store is invoked with the right scope
// through the real wiring; this does.
func TestIntegration_SearchMyMemories_RealStoreEnforcesOwnerScope(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	store := memory.NewPostgresStore(db)

	const callerEmail = "alice@example.com"

	// HybridSearch binds $1=vector, $2=query, then the scope predicates.
	// With only created_by set (the request sends no dimension/status), the
	// owner scope must land at $3 = the caller's email. If the handler
	// failed to scope by the caller, this expectation would not be met.
	rows := sqlmock.NewRows(hybridSearchColumns)
	addHybridSearchRow(rows, "mem-1", callerEmail, 0.88)
	mock.ExpectQuery("UNION ALL").
		WithArgs(sqlmock.AnyArg(), "churn analysis", callerEmail).
		WillReturnRows(rows)

	h := NewHandler(Deps{
		AssetStore:        &mockAssetStore{},
		ShareStore:        &mockShareStore{},
		MemoryStore:       store,
		EmbeddingProvider: &fakeEmbedder{vec: []float32{0.1, 0.2, 0.3}},
	}, testAuthMiddleware(&User{UserID: "u1", Email: callerEmail}))

	req := httptest.NewRequestWithContext(context.Background(), "GET",
		"/api/v1/portal/memory/records/search?q=churn+analysis", http.NoBody)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, mock.ExpectationsWereMet(),
		"the real store must be queried with the caller's email as the created_by scope")

	var resp struct {
		Total int `json:"total"`
		Data  []struct {
			ID    string  `json:"id"`
			Score float64 `json:"score"`
		} `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Equal(t, 1, resp.Total)
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "mem-1", resp.Data[0].ID)
	// HybridSearch returns the fused vec+lexical score (the fusion formula
	// itself is unit-tested in pkg/memory); here we only assert a real
	// non-zero ranking score survived the round trip to JSON.
	assert.Positive(t, resp.Data[0].Score, "a real relevance score must reach the JSON response")
}
