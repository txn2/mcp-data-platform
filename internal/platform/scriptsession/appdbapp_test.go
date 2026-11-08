package scriptsession

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/producedby"
	"github.com/txn2/mcp-data-platform/pkg/toolkits/postgres"
)

// TestRunAs is the application whose run role a script's statements log in
// as (#2539): asked once per connection with the script the session runs,
// named by a draft or by the run's producer.
func TestRunAs(t *testing.T) {
	asked := 0
	apps := Applications{RunAs: func(_ context.Context, scriptID, connection string) string {
		asked++
		if scriptID == "s1" && connection == "orders" {
			return "app_1"
		}
		return ""
	}}
	draft := &appdbCaller{ctx: WithScript(context.Background(), "s1"), db: &AppDB{Applications: apps}}
	assert.Equal(t, "app_1", draft.runAs("orders"))
	assert.Equal(t, "app_1", draft.runAs("orders"))
	assert.Equal(t, 1, asked, "asked once per connection")
	assert.Empty(t, draft.runAs("ledger"))
	assert.Equal(t, "app_1", postgres.IdentityOf(draft.identity(context.Background(), "orders")).Application)

	run := &appdbCaller{ctx: producedby.With(context.Background(), producedby.Producer{Kind: producedby.KindScript, ID: "s1"}), db: &AppDB{Applications: apps}}
	assert.Equal(t, "app_1", run.runAs("orders"), "a run names its script by its producer")
	session := &appdbCaller{ctx: producedby.With(context.Background(), producedby.Producer{Kind: producedby.KindSession, ID: "x"}), db: &AppDB{Applications: apps}}
	assert.Empty(t, session.runAs("orders"))
	none := &appdbCaller{ctx: WithScript(context.Background(), ""), db: &AppDB{}}
	assert.Empty(t, none.runAs("orders"), "no script, or nothing installed, runs as no application")
}

// TestRunAs_RecordedByTheCaller: an endpoint request records its script's
// application from the stamp it read, and no statement asks again.
func TestRunAs_RecordedByTheCaller(t *testing.T) {
	asked := 0
	apps := Applications{RunAs: func(context.Context, string, string) string { asked++; return "app_asked" }}
	ctx := producedby.With(context.Background(), producedby.Producer{Kind: producedby.KindScript, ID: "s1"})

	held := &appdbCaller{ctx: WithRunAs(ctx, "app_1", "orders"), db: &AppDB{Applications: apps}}
	assert.Equal(t, "app_1", held.runAs("orders"))
	assert.Empty(t, held.runAs("ledger"), "the application's schema is on another connection")

	inNone := &appdbCaller{ctx: WithRunAs(ctx, "", ""), db: &AppDB{Applications: apps}}
	assert.Empty(t, inNone.runAs("orders"), "a script in no application runs as no application")
	assert.Zero(t, asked, "a recorded application is never asked for")
}

// TestReword names the application holding a schema a statement was refused
// on, when it is not the script's own (#2539), and leaves every other
// refusal as the database wrote it.
func TestReword(t *testing.T) {
	apps := Applications{
		RunAs: func(context.Context, string, string) string { return "app_mine" },
		Holder: func(_ context.Context, _, schema string) (string, string) {
			switch schema {
			case "billing":
				return "app_billing", "billing"
			case "mine":
				return "app_mine", "mine"
			}
			return "", ""
		},
	}
	c := &appdbCaller{ctx: WithScript(context.Background(), "s1"), db: &AppDB{Applications: apps}}
	denied := func(schema string) error {
		return NewRefusal("statement failed: 42501", map[string]any{
			"code": postgres.CodeStatementFailed, "message": "statement failed: 42501",
			"postgres": map[string]any{"sqlstate": "42501", "message": "permission denied for schema " + schema},
		})
	}
	err := c.reword("orders", denied("billing"))
	refusal, ok := errors.AsType[*RefusalError](err)
	require.True(t, ok)
	assert.Equal(t, CodeSchemaOfAnotherApplication, refusal.Code)
	assert.Contains(t, refusal.Text, `belongs to application "billing"`)
	assert.Contains(t, refusal.Text, "its endpoints and its exports")
	assert.Equal(t, "billing", refusal.Envelope["application"])
	assert.NotNil(t, refusal.Envelope["postgres"], "what the server said is kept for the script")

	for name, in := range map[string]error{
		"the script's own schema":     denied("mine"),
		"a schema no application has": denied("loose"),
		"another refusal":             NewRefusal("x", map[string]any{"postgres": map[string]any{"sqlstate": "23505", "message": "duplicate"}}),
		"not a refusal":               errors.New("down"),
	} {
		assert.Same(t, in, c.reword("orders", in), name)
	}
	assert.Equal(t, denied("billing").Error(), c.reword("", denied("billing")).Error(), "no connection, no lookup")
}

// TestConnectionOf reads the connection a call names, or its transaction's.
func TestConnectionOf(t *testing.T) {
	c := &appdbCaller{txs: map[string]AppDBTx{"tx1": fakeConnTx{"ledger"}}}
	assert.Equal(t, "orders", c.connectionOf(map[string]any{argConnection: "orders"}))
	assert.Equal(t, "ledger", c.connectionOf(map[string]any{argTransaction: "tx1"}))
	assert.Empty(t, c.connectionOf(map[string]any{argTransaction: "gone"}))
}

type fakeConnTx struct{ conn string }

func (f fakeConnTx) Connection() string { return f.conn }
func (fakeConnTx) Query(context.Context, postgres.Statement) (*postgres.Result, error) {
	return nil, errUnused
}

func (fakeConnTx) Execute(context.Context, postgres.Statement) (*postgres.Result, error) {
	return nil, errUnused
}

func (fakeConnTx) Batch(context.Context, []postgres.Statement) ([]*postgres.Result, error) {
	return nil, nil
}
func (fakeConnTx) Commit(context.Context) error              { return nil }
func (fakeConnTx) Rollback()                                 {}
func (fakeConnTx) Savepoint(context.Context) (string, error) { return "", nil }
func (fakeConnTx) Release(context.Context, string) error     { return nil }
func (fakeConnTx) RollbackTo(context.Context, string) error  { return nil }
func (fakeConnTx) OpenCursor(context.Context, postgres.Statement, int) (AppDBCursor, error) {
	return nil, errUnused
}

// errUnused answers a call these tests never make.
var errUnused = errors.New("not used here")

func (fakeConnTx) CopyIn(context.Context, postgres.CopyIn) (int64, error) { return 0, nil }
