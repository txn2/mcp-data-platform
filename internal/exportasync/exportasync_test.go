package exportasync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeRunner holds runs in memory, keyed by tool and key, and runs work
// synchronously so a test reads its end at once.
type fakeRunner struct {
	runs     map[string]*Run
	startErr error
	hold     bool // leave started runs running
}

func newFake() *fakeRunner { return &fakeRunner{runs: map[string]*Run{}} }

func (f *fakeRunner) Start(ctx context.Context, tool, key string, work func(context.Context) (*mcp.CallToolResult, error)) (*Run, bool, error) {
	if f.startErr != nil {
		return nil, false, f.startErr
	}
	if r, ok := f.runs[tool+key]; ok && r.Status == StatusRunning {
		return r, false, nil
	}
	run := &Run{ID: "dpt_" + key, Status: StatusRunning, StartedAt: time.Unix(0, 0)}
	f.runs[tool+key] = run
	if !f.hold {
		cell := &Cell{}
		res, err := work(WithProgress(ctx, cell))
		if err != nil {
			return nil, false, fmt.Errorf("work: %w", err)
		}
		raw, err := json.Marshal(res)
		if err != nil {
			return nil, false, fmt.Errorf("encoding the result: %w", err)
		}
		run.Status, run.Result, run.Progress = StatusCompleted, raw, cell.Load()
	}
	return run, true, nil
}

func (f *fakeRunner) Find(_ context.Context, tool, key string) (*Run, error) {
	if r, ok := f.runs[tool+key]; ok {
		return r, nil
	}
	return nil, ErrNotFound
}

func text(res *mcp.CallToolResult) string {
	content, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return content.Text
}

func TestWanted(t *testing.T) {
	on, off := true, false
	r := newFake()
	for _, tc := range []struct {
		runner             Runner
		async              *bool
		walk, model, wants bool
	}{
		{r, nil, true, true, true},   // a model client's page walk
		{r, nil, true, false, false}, // a script's or REST caller's walk
		{r, nil, false, true, false}, // one response
		{r, &on, false, false, true}, // asked for
		{r, &off, true, true, false}, // refused
		{nil, &on, true, true, false},
	} {
		if got := Wanted(tc.runner, tc.async, tc.walk, tc.model); got != tc.wants {
			t.Errorf("Wanted(%v, walk=%v, model=%v) = %v", tc.async, tc.walk, tc.model, got)
		}
	}
}

// TestBeginAndResume is the round trip a caller makes (#2212): start, read
// while it runs, read once it completed, and a key in flight joined rather
// than started twice.
func TestBeginAndResume(t *testing.T) {
	ctx := context.Background()
	r := newFake()
	r.hold = true
	calls := 0
	work := func(ctx context.Context) (*mcp.CallToolResult, error) {
		calls++
		Report(ctx, 3, 30)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"asset_id":"a1"}`}}}, nil
	}

	res := Begin(ctx, Background{Runner: r, Tool: "graphql_export", Key: "k1", How: "Call again."}, work)
	var a Answer
	if err := json.Unmarshal([]byte(text(res)), &a); err != nil {
		t.Fatal(err)
	}
	if a.RunID != "dpt_k1" || a.Status != StatusRunning || a.IdempotencyKey != "k1" || !strings.Contains(a.Message, "Call again.") {
		t.Errorf("start answer = %+v", a)
	}
	res = Begin(ctx, Background{Runner: r, Tool: "graphql_export", Key: "k1", How: "Call again."}, work)
	if !strings.Contains(text(res), "already running; this call joined it") {
		t.Errorf("a second start under a key in flight joins: %s", text(res))
	}
	if calls != 0 {
		t.Errorf("work ran %d times while held", calls)
	}

	read, prior, handled := Resume(ctx, r, "graphql_export", "k1", "Call again.")
	if !handled || prior != nil || !strings.Contains(text(read), `"status":"running"`) {
		t.Errorf("a running export is read back as running: %v %v", handled, text(read))
	}

	r.hold = false
	delete(r.runs, "graphql_exportk2")
	Begin(ctx, Background{Runner: r, Tool: "graphql_export", Key: "k2"}, work)
	read, _, handled = Resume(ctx, r, "graphql_export", "k2", "")
	if !handled || text(read) != `{"asset_id":"a1"}` {
		t.Errorf("a completed export answers with its own result: %v %s", handled, text(read))
	}
	if p := r.runs["graphql_exportk2"].Progress; p.PagesFetched != 3 || p.ItemsMerged != 30 {
		t.Errorf("progress = %+v", p)
	}

	if _, _, handled := Resume(ctx, r, "graphql_export", "none", ""); handled {
		t.Error("an unknown key is not handled")
	}
	if _, _, handled := Resume(ctx, nil, "graphql_export", "k1", ""); handled {
		t.Error("no runner handles nothing")
	}
}

// TestAFailedRunIsRunAgainNamingTheFailure: a key whose run failed starts a
// new run, and the answer says what failed.
func TestAFailedRunIsRunAgainNamingTheFailure(t *testing.T) {
	ctx := context.Background()
	r := newFake()
	r.runs["api_exportk"] = &Run{ID: "dpt_old", Status: StatusFailed, Message: "the server shut down"}
	_, prior, handled := Resume(ctx, r, "api_export", "k", "")
	if handled || prior == nil {
		t.Fatalf("a failed run is handed back as prior: %v %v", handled, prior)
	}
	r.runs["api_exportk"].Status = StatusFailed
	delete(r.runs, "api_exportk")
	r.hold = true
	res := Begin(ctx, Background{Runner: r, Tool: "api_export", Key: "k", How: "Read it again.", Prior: prior}, nil)
	if !strings.Contains(text(res), "(dpt_old) failed: the server shut down It was started again.") {
		t.Errorf("answer = %s", text(res))
	}
	r.startErr = errors.New("db down")
	if res := Begin(ctx, Background{Runner: r, Tool: "api_export", Key: "k"}, nil); !res.IsError {
		t.Error("a start that fails is a failed call")
	}
}

func TestReportWithoutACellAndAnswerOfAFailure(t *testing.T) {
	Report(context.Background(), 1, 1) // an inline export records nothing, and does not panic
	a := AnswerOf(&Run{ID: "x", Status: StatusFailed, Message: "boom"}, "k", "")
	if a.Message != "The background export failed: boom" {
		t.Errorf("message = %q", a.Message)
	}
	if _, err := Result(&Run{Result: json.RawMessage("not json")}); err == nil {
		t.Error("an unreadable result is reported")
	}
	key, err := NewKey()
	if err != nil || !strings.HasPrefix(key, "bg-") || len(key) != 3+2*keyBytes {
		t.Errorf("key = %q, %v", key, err)
	}
}

// TestStartMintsTheKeyAndBoundsTheTimeout: a launch with no key mints one
// into the argument it points at, and one with no timeout takes the
// deployment's ceiling.
func TestStartMintsTheKeyAndBoundsTheTimeout(t *testing.T) {
	r := newFake()
	r.hold = true
	key, timeout := "", 0
	res := Start(context.Background(), Launch{Runner: r, Tool: "api_export", Key: &key, Timeout: &timeout, MaxTimeout: 30 * time.Minute}, nil)
	if !strings.HasPrefix(key, "bg-") || timeout != 1800 {
		t.Fatalf("key %q timeout %d", key, timeout)
	}
	if !strings.Contains(text(res), "Call api_export again with idempotency_key="+key) {
		t.Errorf("answer = %s", text(res))
	}
	given, ask := "mine", 60
	Start(context.Background(), Launch{Runner: r, Tool: "api_export", Key: &given, Timeout: &ask, MaxTimeout: time.Hour}, nil)
	if given != "mine" || ask != 60 {
		t.Errorf("a caller's own key and timeout are kept: %q %d", given, ask)
	}
	if RunnerOf(nil) != nil || RunnerOf(func() Runner { return r }) != r {
		t.Error("RunnerOf")
	}
}
