package connbackfill

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// mockTK is a minimal registry.Toolkit. A fallback data kind (trino) surfaces
// one connection named after the toolkit.
type mockTK struct{ kind, name string }

func (m *mockTK) Kind() string                        { return m.kind }
func (m *mockTK) Name() string                        { return m.name }
func (m *mockTK) Connection() string                  { return m.name }
func (*mockTK) RegisterTools(*mcp.Server)             {}
func (*mockTK) Tools() []string                       { return nil }
func (*mockTK) SetSemanticProvider(semantic.Provider) {}
func (*mockTK) SetQueryProvider(query.Provider)       {}
func (*mockTK) Close() error                          { return nil }

// listerTK adds the ConnectionLister capability (multi-connection kind).
type listerTK struct {
	mockTK
	conns []toolkit.ConnectionDetail
}

func (l *listerTK) ListConnections() []toolkit.ConnectionDetail { return l.conns }

func testToolkits(t *testing.T) []registry.Toolkit {
	t.Helper()
	reg := registry.NewRegistry()
	require.NoError(t, reg.Register(&mockTK{kind: "trino", name: "prod"}))
	require.NoError(t, reg.Register(&listerTK{
		mockTK: mockTK{kind: "api", name: "gw"},
		conns:  []toolkit.ConnectionDetail{{Name: "stripe"}, {Name: "prometheus"}},
	}))
	return reg.All()
}

func TestRun_InsertsEveryConnectionInsertOnly(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	// Run iterates registry.All(), which ranges a map, so the insert order is
	// non-deterministic. The contract is "every connection inserted, insert-only",
	// not a specific order (ON CONFLICT DO NOTHING is order-independent), so match
	// the expectations in any order.
	mock.MatchExpectationsInOrder(false)
	for _, c := range [][2]string{{"trino", "prod"}, {"api", "stripe"}, {"api", "prometheus"}} {
		mock.ExpectExec("INSERT INTO connection_instances .*ON CONFLICT .*DO NOTHING").
			WithArgs(c[0], c[1], "").
			WillReturnResult(sqlmock.NewResult(0, 1))
	}

	Run(context.Background(), db, testToolkits(t))

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRun_NilDBNoOp(t *testing.T) {
	assert.NotPanics(t, func() { Run(context.Background(), nil, testToolkits(t)) })
}

func TestRun_ContinuesOnError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close() //nolint:errcheck // test cleanup

	// Every insert fails; the sweep logs and continues, attempting all three.
	mock.MatchExpectationsInOrder(false)
	for range 3 {
		mock.ExpectExec("INSERT INTO connection_instances").
			WillReturnError(errors.New("db down"))
	}

	Run(context.Background(), db, testToolkits(t))

	assert.NoError(t, mock.ExpectationsWereMet(), "all three inserts attempted despite errors")
}
