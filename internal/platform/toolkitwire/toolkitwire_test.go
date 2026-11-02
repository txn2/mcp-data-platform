package toolkitwire

import (
	"database/sql"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	postgreskit "github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// recording is a toolkit that records its own metrics.
type recording struct {
	got *observability.Metrics
	set bool
}

func (*recording) Kind() string                          { return "fake" }
func (*recording) Name() string                          { return "fake" }
func (*recording) Connection() string                    { return "" }
func (*recording) RegisterTools(*mcp.Server)             {}
func (*recording) Tools() []string                       { return nil }
func (*recording) SetSemanticProvider(semantic.Provider) {}
func (*recording) SetQueryProvider(query.Provider)       {}
func (*recording) Close() error                          { return nil }
func (r *recording) SetMetrics(m *observability.Metrics) { r.got, r.set = m, true }

func TestMetricsReachesEveryToolkitThatRecords(t *testing.T) {
	r := &recording{}
	pg := postgreskit.NewMulti(postgreskit.MultiConfig{DefaultName: "x"})
	t.Cleanup(func() { _ = pg.Close() })
	Metrics([]registry.Toolkit{r, pg}, nil)
	if !r.set {
		t.Fatal("the recorder did not reach a toolkit that records its own metrics")
	}
}

func TestPostgresGuardNeedsAPlatformDatabase(t *testing.T) {
	pg := postgreskit.NewMulti(postgreskit.MultiConfig{DefaultName: "x"})
	t.Cleanup(func() { _ = pg.Close() })
	// No database: nothing to protect, nothing installed, and a save is not
	// asked to reach its database.
	PostgresGuard([]registry.Toolkit{pg}, nil)
	if err := pg.ValidateConnection("c", map[string]any{"host": "127.0.0.1", "port": 9, "database": "d", "user": "u", "sslmode": "disable"}); err != nil {
		t.Fatalf("validate without a guard: %v", err)
	}
	db, err := sql.Open("postgres", "postgres://u:p@127.0.0.1:9/d?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	PostgresGuard([]registry.Toolkit{&recording{}, pg}, db)
}
