package callrun

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func watched(handlerID string) watchRun {
	return watchRun{
		handler: callhandler.Handler{ID: handlerID, Kind: callhandler.KindWatch},
		t: target{
			script:  &script.Script{ID: "s1", Name: "watch"},
			version: &script.Version{ID: "v1", Version: 2},
			runAs:   approval.RunAs{Approver: "admin@example.com", Roles: []string{"admin"}},
		},
		call: map[string]any{"tool": "api_invoke_endpoint"},
	}
}

// TestWatchQueue_WritesABatch counts each watch's backlog once, stores runs
// past the bound skipped, and tells each script's workers once.
func TestWatchQueue_WritesABatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	q := NewWatchQueue(db, 2)
	assert.Nil(t, NewWatchQueue(nil, 0))
	assert.Equal(t, DefaultMaxQueued, NewWatchQueue(db, 0).MaxQueued)

	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM script_runs WHERE call_handler_id")).WithArgs("h1").
		WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_runs")).
		WithArgs(sqlmock.AnyArg(), "s1", "v1", 2, script.RunStatusPending, sqlmock.AnyArg(), "admin@example.com", sqlmock.AnyArg(), sqlmock.AnyArg(), "h1", "").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_runs")).
		WithArgs(sqlmock.AnyArg(), "s1", "v1", 2, runstate.StatusSkippedBacklog, sqlmock.AnyArg(), "admin@example.com", sqlmock.AnyArg(), sqlmock.AnyArg(), "h1", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_notify")).WillReturnResult(sqlmock.NewResult(0, 0))
	require.NoError(t, q.writeBatch(context.Background(), []watchRun{watched("h1"), watched("h1")}))
	require.NoError(t, mock.ExpectationsWereMet())

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COUNT").WillReturnError(errors.New("down"))
	mock.ExpectRollback()
	require.Error(t, q.writeBatch(context.Background(), []watchRun{watched("h1")}))

	bad := watched("h2")
	bad.handler.Args = map[string]any{"nope": 1}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	mock.ExpectRollback()
	require.ErrorContains(t, q.writeBatch(context.Background(), []watchRun{bad}), "arguments")

	mock.ExpectBegin().WillReturnError(errors.New("down"))
	require.Error(t, q.writeBatch(context.Background(), []watchRun{watched("h1")}))
}

// TestWatchQueue_RunsUntilItsContextEnds and drops a run it has no room for.
func TestWatchQueue_RunsUntilItsContextEnds(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	q := NewWatchQueue(db, 5)
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT COUNT").WillReturnRows(sqlmock.NewRows([]string{"n"}).AddRow(0))
	mock.ExpectExec("INSERT INTO script_runs").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO script_runs").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectExec("pg_notify").WillReturnResult(sqlmock.NewResult(0, 0))
	q.add(watched("h1"))
	q.add(watched("h1"))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { q.Run(ctx); close(done) }()
	require.Eventually(t, func() bool { return mock.ExpectationsWereMet() == nil }, 5*time.Second, 10*time.Millisecond)
	cancel()
	<-done

	full := &WatchQueue{ch: make(chan watchRun)}
	full.add(watched("h1"))
	assert.Empty(t, full.ch, "a full queue drops the run rather than make the call wait")
}

type sweeper struct{ n atomic.Int32 }

func (s *sweeper) Sweep(context.Context, time.Time) error {
	s.n.Add(1)
	return nil
}

// TestRuntime starts the evaluator, reads the index, and stops.
func TestRuntime(t *testing.T) {
	var none *Runtime
	require.NoError(t, none.Start(context.Background()))
	require.NoError(t, none.Stop(context.Background()))
	w := newWorld()
	w.add("a", callhandler.KindPolicy, allowSrc, nil)
	e := &Evaluator{Index: &callhandler.Index{Reader: w}, Store: w, Scripts: w, Approvals: w}
	e.Watches = &WatchQueue{ch: make(chan watchRun, 1)}
	rt := &Runtime{Evaluator: e, Sweeper: &sweeper{}}
	require.NoError(t, rt.Start(context.Background()))
	assert.True(t, e.Index.Any("billing-api"), "the index is read before the first call")
	require.NoError(t, rt.Stop(context.Background()))

	failing := &Runtime{Evaluator: &Evaluator{Index: &callhandler.Index{Reader: errReader{}}}}
	require.NoError(t, failing.Start(context.Background()), "a failed first read is logged; the next call reads again")
	require.NoError(t, failing.Stop(context.Background()))
}

type errReader struct{}

func (errReader) Enabled(context.Context) ([]callhandler.Handler, error) {
	return nil, errors.New("down")
}

// TestResultShapes is how a tool result reaches a transform and comes back.
func TestResultShapes(t *testing.T) {
	text := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"a":1}`}}}
	assert.Equal(t, map[string]any{"a": float64(1)}, resultMap(text))
	plain := &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hello"}}}
	assert.Equal(t, map[string]any{"text": "hello"}, resultMap(plain))
	replaceResult(plain, map[string]any{"text": "changed"})
	assert.Equal(t, "changed", firstText(plain))
	assert.Nil(t, plain.StructuredContent)
	structured := &mcp.CallToolResult{StructuredContent: map[string]any{"status": 200.0}}
	replaceResult(structured, map[string]any{"status": 200, "body": "x"})
	assert.Equal(t, map[string]any{"status": 200, "body": "x"}, structured.StructuredContent)
	assert.True(t, succeeded(structured))
	assert.True(t, succeeded(&mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "no status"}}}))
	assert.False(t, succeeded(&mcp.CallToolResult{IsError: true}))
	assert.Empty(t, firstText(&mcp.CallToolResult{}))
	replaceResult(text, map[string]any{"bad": make(chan int)})
	assert.Equal(t, "{}", firstText(text), "a result that cannot be written becomes empty, never the original")
	e := &Evaluator{now: func() time.Time { return time.Unix(10, 0) }}
	assert.Equal(t, time.Unix(10, 0), e.clock())
}

// TestMiddleware_AnOlderClientIsAskedInBand: a client on an earlier protocol
// revision that declares elicitation is asked during the call.
func TestMiddleware_AnOlderClientIsAskedInBand(t *testing.T) {
	w := newWorld()
	w.add("h", callhandler.KindPolicy, holdSrc, nil)
	f := newFixture(t, w)
	asked := 0
	opts := &mcp.ClientOptions{ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
		asked++
		return &mcp.ElicitResult{Action: "decline"}, nil
	}}
	_, text := call(t, f.connect(t, opts, "2025-11-25"), echoArgs)
	assert.Equal(t, 1, asked)
	assert.Contains(t, text, "declined")
}

// TestWatchQueue_GathersABurst: with a linger, the writer takes the runs that
// join within it into one write, returning at once with a full batch or when
// its context ends; with none, only the runs already waiting.
func TestWatchQueue_GathersABurst(t *testing.T) {
	q := &WatchQueue{Linger: time.Hour, ch: make(chan watchRun, batchSize+5)}
	for range batchSize + 5 {
		q.ch <- watched("h1")
	}
	assert.Len(t, q.gather(context.Background(), watched("h1")), batchSize, "a full batch is written without waiting out the linger")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for len(q.ch) > 0 {
		<-q.ch
	}
	assert.Len(t, q.gather(ctx, watched("h1")), 1, "a context that ended writes what it has")
	none := &WatchQueue{ch: make(chan watchRun, 4)}
	none.ch <- watched("h1")
	assert.Len(t, none.gather(context.Background(), watched("h1")), 2)
	assert.Equal(t, DefaultLinger, NewWatchQueue(&sql.DB{}, 0).Linger)
}
