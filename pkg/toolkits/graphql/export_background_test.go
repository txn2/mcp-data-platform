package graphql

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/exportasync"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
)

// bgRunner runs a background export on a goroutine apart from the request,
// the way the call-task runner does, and keeps each run in memory.
type bgRunner struct {
	mu   sync.Mutex
	runs map[string]*exportasync.Run
	done chan struct{}
}

func newBGRunner() *bgRunner {
	return &bgRunner{runs: map[string]*exportasync.Run{}, done: make(chan struct{}, 4)}
}

func (r *bgRunner) Start(ctx context.Context, tool, key string, work func(context.Context) (*mcp.CallToolResult, error)) (*exportasync.Run, bool, error) {
	r.mu.Lock()
	if run, ok := r.runs[tool+key]; ok && run.Status == exportasync.StatusRunning {
		cp := *run
		r.mu.Unlock()
		return &cp, false, nil
	}
	run := &exportasync.Run{ID: "dpt_1", Status: exportasync.StatusRunning}
	r.runs[tool+key] = run
	snapshot := *run // the runner answers with the run as it stood, as the real one reads it from its row
	r.mu.Unlock()
	cell := &exportasync.Cell{}
	go func() {
		res, _ := work(exportasync.WithProgress(context.WithoutCancel(ctx), cell))
		raw, _ := json.Marshal(res)
		r.mu.Lock()
		run.Status, run.Result, run.Progress = exportasync.StatusCompleted, raw, cell.Load()
		r.mu.Unlock()
		r.done <- struct{}{}
	}()
	return &snapshot, true, nil
}

func (r *bgRunner) Find(_ context.Context, tool, key string) (*exportasync.Run, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if run, ok := r.runs[tool+key]; ok {
		cp := *run
		return &cp, nil
	}
	return nil, exportasync.ErrNotFound
}

// TestAModelClientsPageWalkRunsInTheBackground is #2212 through
// graphql_export: a page walk from a model client is answered at once with a
// run id and a key, the walk finishes apart from the call, and the same tool
// called with the key answers with the asset it wrote.
func TestAModelClientsPageWalkRunsInTheBackground(t *testing.T) {
	u := newUpstream(t)
	u.respond = func(_ graphQLRequest, callNo int) (int, string) {
		if callNo == 1 {
			return 200, relayPage([]string{"a"}, "c1", true)
		}
		return 200, relayPage([]string{"b"}, "", false)
	}
	tk, assets, _ := exportToolkit(t, u, "namespaced")
	runner := newBGRunner()
	tk.exportDeps.Background = func() exportasync.Runner { return runner }

	model := mcpcontext.WithResultBudget(context.Background(), 20000)
	in := exportInput{
		Connection: "gql", Query: pagedDocument, Name: "products.json",
		Paginate: &PaginateInput{Items: "masterData.product.query.edges", CursorVariable: "after"},
	}
	res, _, err := tk.handleExport(model, nil, in)
	if err != nil || res.IsError {
		t.Fatalf("handleExport: %v %+v", err, res)
	}
	var started exportasync.Answer
	decodeResult(t, res, &started)
	if started.RunID == "" || started.Status != exportasync.StatusRunning || !strings.HasPrefix(started.IdempotencyKey, "bg-") {
		t.Fatalf("answer = %+v", started)
	}
	if !strings.Contains(started.Message, "Call graphql_export again with idempotency_key="+started.IdempotencyKey) {
		t.Errorf("the answer says how to read it again: %q", started.Message)
	}

	select {
	case <-runner.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the background walk did not finish")
	}

	read := in
	read.IdempotencyKey = started.IdempotencyKey
	res, _, err = tk.handleExport(model, nil, read)
	if err != nil || res.IsError {
		t.Fatalf("reading it again: %v %+v", err, res)
	}
	var out exportOutput
	decodeResult(t, res, &out)
	if out.AssetID == "" || out.Pagination == nil || out.Pagination.ItemsMerged != 2 {
		t.Errorf("the read answers with the export's own result: %+v", out)
	}
	if len(assets.inserted) != 1 {
		t.Errorf("%d assets written; the walk ran once", len(assets.inserted))
	}
	finished, _ := runner.Find(context.Background(), "graphql_export", started.IdempotencyKey)
	if got := finished.Progress; got.PagesFetched != 2 || got.ItemsMerged != 2 {
		t.Errorf("progress = %+v", got)
	}
}

// TestBackgroundIsOnlyWhereAskedFor keeps the inline answer for a script or
// REST caller's walk, for async false, and for a deployment with no runner.
func TestBackgroundIsOnlyWhereAskedFor(t *testing.T) {
	u := newUpstream(t)
	u.respond = answer(`{"data":{"dataset":null}}`)
	tk, _, _ := exportToolkit(t, u, "flat")
	runner := newBGRunner()
	tk.exportDeps.Background = func() exportasync.Runner { return runner }

	off := false
	model := mcpcontext.WithResultBudget(context.Background(), 20000)
	res, _, _ := tk.handleExport(model, nil, exportInput{Connection: "gql", Query: datasetDocument, Name: "x", Async: &off})
	var out exportOutput
	decodeResult(t, res, &out)
	if out.AssetID == "" {
		t.Errorf("async false answers inline: %s", resultText(res))
	}

	on := true
	res, _, _ = tk.handleExport(context.Background(), nil, exportInput{Connection: "gql", Query: datasetDocument, Name: "y", Async: &on, TimeoutSeconds: 30})
	var started exportasync.Answer
	decodeResult(t, res, &started)
	if started.Status != exportasync.StatusRunning {
		t.Errorf("async true runs in the background for any caller: %s", resultText(res))
	}
	<-runner.done

	tk.exportDeps.Background = nil
	res, _, _ = tk.handleExport(context.Background(), nil, exportInput{Connection: "gql", Query: datasetDocument, Name: "z", Async: &on})
	decodeResult(t, res, &out)
	if out.AssetID == "" {
		t.Errorf("with no runner the export answers inline: %s", resultText(res))
	}
}
