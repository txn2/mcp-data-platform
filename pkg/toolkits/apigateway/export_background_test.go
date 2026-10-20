package apigateway

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
// answering with a snapshot of the run as the call-task runner does.
type bgRunner struct {
	mu   sync.Mutex
	runs map[string]*exportasync.Run
	done chan struct{}
}

func (r *bgRunner) Start(ctx context.Context, tool, key string, work func(context.Context) (*mcp.CallToolResult, error)) (*exportasync.Run, bool, error) {
	r.mu.Lock()
	run := &exportasync.Run{ID: "dpt_1", Status: exportasync.StatusRunning}
	r.runs[tool+key] = run
	snapshot := *run
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

// TestExportWalk_AModelClientsWalkRunsInTheBackground is #2212 through
// api_export: a model client's page walk answers at once with a run and a
// key, every page is walked apart from the call with its progress reported,
// and the same tool called with the key answers with the asset.
func TestExportWalk_AModelClientsWalkRunsInTheBackground(t *testing.T) {
	up := (&pagedUpstream{t: t, pages: 3, perPage: 10, mode: "cursor"}).start()
	store, s3 := &fakeExportAssetStore{}, &fakeExportS3Client{}
	deps := defaultExportDeps(store, &fakeExportVersionStore{}, s3)
	runner := &bgRunner{runs: map[string]*exportasync.Run{}, done: make(chan struct{}, 1)}
	deps.Background = func() exportasync.Runner { return runner }
	tk := buildExportTestToolkit(t, up.srv.URL, &deps)

	model := mcpcontext.WithResultBudget(context.Background(), 20000)
	in := exportInput{
		Connection: "crm", Method: "GET", Path: "/v1/changelog", Name: "changelog.json",
		Query: map[string]any{"per_page": 10}, Paginate: &PaginateInput{Items: "data", CursorParam: "cursor", MaxPages: 10},
	}
	res, _, err := tk.handleExport(model, &mcp.CallToolRequest{}, in)
	if err != nil || res.IsError {
		t.Fatalf("handleExport: %v %+v", err, res)
	}
	var started exportasync.Answer
	if err := json.Unmarshal([]byte(renderedText(t, res)), &started); err != nil {
		t.Fatal(err)
	}
	if started.Status != exportasync.StatusRunning || !strings.Contains(started.Message, "Call api_export again with idempotency_key="+started.IdempotencyKey) {
		t.Fatalf("answer = %+v", started)
	}

	select {
	case <-runner.done:
	case <-time.After(10 * time.Second):
		t.Fatal("the background walk did not finish")
	}
	finished, _ := runner.Find(context.Background(), exportToolName, started.IdempotencyKey)
	if finished.Progress.PagesFetched != 3 || finished.Progress.ItemsMerged != 30 {
		t.Errorf("progress = %+v", finished.Progress)
	}

	read := in
	read.IdempotencyKey = started.IdempotencyKey
	res, _, err = tk.handleExport(model, &mcp.CallToolRequest{}, read)
	if err != nil || res.IsError {
		t.Fatalf("reading it again: %v %+v", err, res)
	}
	if text := renderedText(t, res); !strings.Contains(text, `"asset_id"`) || !strings.Contains(text, `"pages_fetched": 3`) && !strings.Contains(text, `"pages_fetched":3`) {
		t.Errorf("the read answers with the export's own result: %s", text)
	}
	if len(store.inserted) != 1 || store.inserted[0].IdempotencyKey != started.IdempotencyKey {
		t.Errorf("one asset, carrying the key: %+v", store.inserted)
	}
}
