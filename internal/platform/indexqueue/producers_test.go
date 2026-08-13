package indexqueue

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/promptindex"
	"github.com/txn2/mcp-data-platform/pkg/indexjobs"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/tools/toolsindex"
)

// TestNew_BindsProducersForRegisteredKinds is the wiring half of #1256: New
// points a write path's producer at the job store, but only for a kind whose
// consumer actually registered. A job for an unregistered kind is one the worker
// can only fail terminally, so that producer stays a no-op.
func TestNew_BindsProducersForRegisteredKinds(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	// tools always registers; prompts does not here (no prompt store reported).
	tools := indexjobs.NewProducer(toolsindex.SourceKind)
	prompts := indexjobs.NewProducer(promptindex.SourceKind)

	h := New(Config{
		DB: db, Embedder: testEmbedder(), ModelName: "m",
		Producers: []*indexjobs.Producer{tools, nil, prompts},
	})
	require.NotNil(t, h)

	// The bound producer reaches the real Postgres job store: the insert lands on
	// this connection, which is what proves Bind wired the queue's own store.
	mock.ExpectQuery("INSERT INTO index_jobs").
		WithArgs(toolsindex.SourceKind, "unit-1", string(indexjobs.TriggerWrite)).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(7)))
	mock.ExpectExec("SELECT pg_notify").WillReturnResult(sqlmock.NewResult(0, 0))

	tools.NotifyWrite(context.Background(), "unit-1")
	assert.NoError(t, mock.ExpectationsWereMet())

	// The unregistered kind's producer was left unbound: no statement is issued,
	// so the connection sees nothing and the (empty) expectation set still holds.
	prompts.NotifyWrite(context.Background(), "p-1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// TestNew_NoProducersIsFine covers the deployment that passes none: the queue
// assembles exactly as before and the reconciler remains the only path.
func TestNew_NoProducersIsFine(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	h := New(Config{DB: db, Embedder: testEmbedder(), ModelName: "m"})
	require.NotNil(t, h)
	assert.NotPanics(t, func() { h.bindProducers(nil) })
}
