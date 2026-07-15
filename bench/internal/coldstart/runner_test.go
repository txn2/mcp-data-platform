package coldstart

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/claudecli"
	"github.com/txn2/mcp-data-platform/bench/internal/curriculum"
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

type authCtxKey struct{}

func testLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakePlatform is a minimal a3 platform: the MCP tools cold-start drives
// (platform_info, memory_capture, search, apply_knowledge) over streamable HTTP,
// plus the admin knowledge and audit REST. A lesson's fact is modeled as its
// trap class: capture records it on an insight, apply marks the class "applied",
// and search reports the applied classes so a downstream evaluator can act on
// promoted knowledge — exactly the surfacing path the real suite exercises.
type fakePlatform struct {
	mu         sync.Mutex
	minted     atomic.Int64
	seq        int64
	insights   []lifecycleapi.Insight
	changesets []lifecycleapi.Changeset
	events     []auditapi.Event
	applied    map[string]bool // trap class -> promoted
	httpSrv    *httptest.Server
}

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	fp := &fakePlatform{applied: map[string]bool{}}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-coldstart", Version: "fake-1.0.0"}, nil)
	fp.addPlatformInfo(server)
	fp.addMemoryCapture(server)
	fp.addSearch(server)
	fp.addApplyKnowledge(server)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/admin/knowledge/insights", fp.listInsights)
	mux.HandleFunc("GET /api/v1/admin/knowledge/insights/{id}", fp.getInsight)
	mux.HandleFunc("PUT /api/v1/admin/knowledge/insights/{id}/status", fp.putStatus)
	mux.HandleFunc("GET /api/v1/admin/knowledge/changesets", fp.listChangesets)
	mux.HandleFunc("GET /api/v1/admin/knowledge/changesets/{id}", fp.getChangeset)
	mux.HandleFunc("/api/v1/admin/audit/events", fp.serveAudit)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), authCtxKey{}, r.Header.Get("Authorization"))
		mcpHandler.ServeHTTP(w, r.WithContext(ctx))
	}))
	fp.httpSrv = httptest.NewServer(mux)
	t.Cleanup(fp.httpSrv.Close)
	return fp
}

func (fp *fakePlatform) emailFromAuth(auth string) string {
	v := strings.TrimPrefix(auth, "Bearer ")
	if v == "testkey" {
		return "bench-admin@apikey.local"
	}
	if suffix, ok := strings.CutPrefix(v, "testkey-"); ok {
		return "bench-agent-" + suffix + "@apikey.local"
	}
	return v + "@apikey.local"
}

func (fp *fakePlatform) callerEmail(ctx context.Context) string {
	auth, _ := ctx.Value(authCtxKey{}).(string)
	return fp.emailFromAuth(auth)
}

func sessionSchema(extra map[string]*jsonschema.Schema) *jsonschema.Schema {
	props := map[string]*jsonschema.Schema{"session_id": {Type: "string"}}
	maps.Copy(props, extra)
	return &jsonschema.Schema{Type: "object", Properties: props, Required: []string{"session_id"}}
}

func (fp *fakePlatform) addPlatformInfo(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "platform_info", Description: "orientation"},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			payload := map[string]any{"session_id": fmt.Sprintf("dps_%d", fp.minted.Add(1)), "version": "fake-1.0.0"}
			raw, _ := json.Marshal(payload)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: payload}, nil, nil
		})
}

// addMemoryCapture records a pending insight owned by the caller. The insight
// text is the fact's trap class, so apply can mark that class promoted.
func (fp *fakePlatform) addMemoryCapture(server *mcp.Server) {
	schema := sessionSchema(map[string]*jsonschema.Schema{
		"text": {Type: "string"}, "category": {Type: "string"},
		"entity_urns": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
	})
	mcp.AddTool(server, &mcp.Tool{Name: "memory_capture", Description: "save knowledge", InputSchema: schema},
		func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			fp.mu.Lock()
			defer fp.mu.Unlock()
			fp.seq++
			id := "in-" + strconv.FormatInt(fp.seq, 10)
			text, _ := args["text"].(string)
			fp.insights = append(fp.insights, lifecycleapi.Insight{
				ID: id, CreatedAt: time.Unix(fp.seq, 0).UTC(), CapturedBy: fp.callerEmail(ctx),
				InsightText: text, Status: "pending", EntityURNs: firstURNSlice(args["entity_urns"]),
			})
			fp.recordLocked(args, "memory_capture")
			return okResult("captured " + id), nil, nil
		})
}

// addSearch reports the promoted trap classes so an evaluator can answer from
// promoted knowledge. It records an enrichment-bearing audit row once anything
// has been promoted, modeling the delivery-side coverage signal.
func (fp *fakePlatform) addSearch(server *mcp.Server) {
	schema := sessionSchema(map[string]*jsonschema.Schema{"query": {Type: "string"}})
	mcp.AddTool(server, &mcp.Tool{Name: "search", Description: "discover knowledge", InputSchema: schema},
		func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			fp.mu.Lock()
			defer fp.mu.Unlock()
			classes := make([]string, 0, len(fp.applied))
			for c := range fp.applied {
				classes = append(classes, c)
			}
			sort.Strings(classes)
			fp.recordLocked(args, "search")
			return okResult("APPLIED KNOWLEDGE: " + strings.Join(classes, ",")), nil, nil
		})
}

func (fp *fakePlatform) addApplyKnowledge(server *mcp.Server) {
	schema := sessionSchema(map[string]*jsonschema.Schema{
		"action": {Type: "string"}, "entity_urn": {Type: "string"},
		"insight_ids": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
	})
	mcp.AddTool(server, &mcp.Tool{Name: "apply_knowledge", Description: "promote", InputSchema: schema},
		func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			fp.mu.Lock()
			defer fp.mu.Unlock()
			ids := stringSlice(args["insight_ids"])
			urn, _ := args["entity_urn"].(string)
			fp.seq++
			csID := "cs-" + strconv.FormatInt(fp.seq, 10)
			fp.changesets = append(fp.changesets, lifecycleapi.Changeset{ID: csID, TargetURN: urn, SourceInsightIDs: ids, AppliedBy: fp.callerEmail(ctx)})
			for _, id := range ids {
				for i := range fp.insights {
					if fp.insights[i].ID == id {
						fp.insights[i].Status = "applied"
						fp.insights[i].ChangesetRef = csID
						fp.applied[fp.insights[i].InsightText] = true
					}
				}
			}
			fp.recordLocked(args, "apply_knowledge")
			return okResult("applied " + csID), nil, nil
		})
}

// recordLocked appends an audit row; a call carries enrichment once any
// knowledge has been promoted. The caller holds fp.mu.
func (fp *fakePlatform) recordLocked(args map[string]any, tool string) {
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return
	}
	fp.events = append(fp.events, auditapi.Event{
		Timestamp: time.Now().UTC(), DurationMS: 2, SessionID: sessionID, ToolName: tool,
		Success: true, EventKind: "mcp_tool_call", EnrichmentApplied: len(fp.applied) > 0,
	})
}

func (fp *fakePlatform) listInsights(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fp.mu.Lock()
	defer fp.mu.Unlock()
	var out []lifecycleapi.Insight
	for _, in := range fp.insights {
		if v := q.Get("captured_by"); v != "" && in.CapturedBy != v {
			continue
		}
		if v := q.Get("status"); v != "" && in.Status != v {
			continue
		}
		if v := q.Get("entity_urn"); v != "" && !in.LinksEntity(v) {
			continue
		}
		out = append(out, in)
	}
	writeJSON(w, map[string]any{"data": out, "total": len(out)})
}

func (fp *fakePlatform) getInsight(w http.ResponseWriter, r *http.Request) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, in := range fp.insights {
		if in.ID == r.PathValue("id") {
			writeJSON(w, in)
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func (fp *fakePlatform) putStatus(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for i := range fp.insights {
		if fp.insights[i].ID == r.PathValue("id") {
			fp.insights[i].Status = body.Status
			writeJSON(w, map[string]string{"status": "ok"})
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func (fp *fakePlatform) listChangesets(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fp.mu.Lock()
	defer fp.mu.Unlock()
	var out []lifecycleapi.Changeset
	for _, cs := range fp.changesets {
		if v := q.Get("entity_urn"); v != "" && cs.TargetURN != v {
			continue
		}
		out = append(out, cs)
	}
	writeJSON(w, map[string]any{"data": out, "total": len(out)})
}

func (fp *fakePlatform) getChangeset(w http.ResponseWriter, r *http.Request) {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, cs := range fp.changesets {
		if cs.ID == r.PathValue("id") {
			writeJSON(w, cs)
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func (fp *fakePlatform) serveAudit(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session_id")
	fp.mu.Lock()
	var matched []auditapi.Event
	for _, e := range fp.events {
		if e.SessionID == sessionID {
			matched = append(matched, e)
		}
	}
	fp.mu.Unlock()
	writeJSON(w, map[string]any{"data": matched, "total": len(matched)})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func okResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func firstURNSlice(v any) []string {
	s := stringSlice(v)
	if len(s) == 0 {
		return nil
	}
	return s[:1]
}

func stringSlice(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// knowledgeAdapter answers only from what the platform surfaces: a teacher
// captures its lesson's class, an evaluator searches and answers correctly iff
// its task's class has been promoted (present in the search result).
type knowledgeAdapter struct {
	mode           string // "teach" | "eval"
	class, urn     string
	correct, wrong string
}

func (a *knowledgeAdapter) Model() string { return "knowledge-test" }

func (a *knowledgeAdapter) Complete(_ context.Context, _ string, msgs []llm.Message, _ []llm.ToolDef) (llm.Message, llm.Usage, error) {
	usedTool, lastResult := false, ""
	for _, m := range msgs {
		for _, tr := range m.ToolResults {
			usedTool, lastResult = true, tr.Text
		}
	}
	usage := llm.Usage{InputTokens: 10, OutputTokens: 5}
	if a.mode == StageTeach {
		if !usedTool {
			return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Name: "memory_capture",
				Args: map[string]any{"text": a.class, "category": "business_context", "entity_urns": []any{a.urn}}}}}, usage, nil
		}
		return llm.Message{Role: "assistant", Text: "saved " + a.class}, usage, nil
	}
	if !usedTool {
		return llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{{ID: "1", Name: "search", Args: map[string]any{"query": a.class}}}}, usage, nil
	}
	ans := a.wrong
	if strings.Contains(lastResult, a.class) {
		ans = a.correct
	}
	return llm.Message{Role: "assistant", Text: "FINAL ANSWER: " + ans}, usage, nil
}

const ordersURN = "urn:li:dataset:(urn:li:dataPlatform:trino,memory.bench.orders,PROD)"

// testCurriculum is two lessons unlocking two eval tasks, one per class.
func testCurriculum() curriculum.Curriculum {
	return curriculum.Curriculum{
		ID: "cs-test", Title: "test", EvalSuite: "s3",
		Lessons: []curriculum.Lesson{
			{ID: "cs-units", Title: "units", TrapClass: "units_cents", Fact: "cents", EntityURN: ordersURN,
				Sink: protocol.SinkDataHub, BudgetToolCalls: 5, Teach: protocol.TeachStage{Prompt: "remember cents"}},
			{ID: "cs-net", Title: "net", TrapClass: "net_revenue", Fact: "net", EntityURN: ordersURN,
				Sink: protocol.SinkKnowledgePage, BudgetToolCalls: 5,
				Page:  &protocol.PagePayload{Slug: "net", Title: "Net", Body: "net policy"},
				Teach: protocol.TeachStage{Prompt: "remember net"}},
		},
	}
}

func numericTask(id, class string, value float64) task.Task {
	return task.Task{ID: id, Suite: "s3", Prompt: "q", Arms: []string{"a3"}, TrapClasses: []string{class},
		BudgetToolCalls: 5, Grading: task.Grading{Kind: task.GradeNumeric, Value: &value, AbsTolerance: 0.01}}
}

func writeFixtures(t *testing.T, cur curriculum.Curriculum, tasks []task.Task) (curDir, tasksDir string) {
	t.Helper()
	curDir, tasksDir = t.TempDir(), t.TempDir()
	raw, err := yaml.Marshal(cur)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(curDir, cur.ID+".yaml"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tk := range tasks {
		b, err := yaml.Marshal(tk)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tasksDir, tk.ID+".yaml"), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return curDir, tasksDir
}

// testFactory maps teach units to their lesson class and eval units to their
// task's correct/wrong answers.
func testFactory(cur curriculum.Curriculum, tasks []task.Task) AdapterFactory {
	lessons := map[string]curriculum.Lesson{}
	for _, l := range cur.Lessons {
		lessons[l.ID] = l
	}
	byTask := map[string]task.Task{}
	for _, tk := range tasks {
		byTask[tk.ID] = tk
	}
	return func(unitID, stage string) (llm.Adapter, error) {
		if stage == StageTeach {
			l := lessons[unitID]
			return &knowledgeAdapter{mode: StageTeach, class: l.TrapClass, urn: l.EntityURN}, nil
		}
		tk := byTask[unitID]
		correct := strconv.FormatFloat(*tk.Grading.Value, 'f', 2, 64)
		wrong := strconv.FormatFloat(*tk.Grading.Value+1000, 'f', 2, 64)
		return &knowledgeAdapter{mode: StageEval, class: tk.TrapClasses[0], correct: correct, wrong: wrong}, nil
	}
}

func testOptions(fp *fakePlatform, curDir, tasksDir string, factory AdapterFactory) Options {
	return Options{
		Target:        target.Target{BaseURL: fp.httpSrv.URL, Credential: "testkey"},
		HTTPTimeout:   5 * time.Second,
		Arm:           "a3",
		K:             1,
		CurriculumDir: curDir,
		TasksDir:      tasksDir,
		Factory:       factory,
		AuditTimeout:  2 * time.Second,
		IdentityKeys:  64,
		LLMProvider:   "scripted",
		Log:           testLogger(),
	}
}

// TestColdStartCurveClimbs is the integration test: it wires the real Run over a
// fake platform and asserts the learning curve climbs as lessons are promoted —
// baseline near zero, each promotion unlocking its trap class — proving the
// teach -> capture -> promote -> eval loop is correctly assembled.
func TestColdStartCurveClimbs(t *testing.T) {
	cur := testCurriculum()
	tasks := []task.Task{
		numericTask("s3-units-a", "units_cents", 100),
		numericTask("s3-net-a", "net_revenue", 50),
	}
	curDir, tasksDir := writeFixtures(t, cur, tasks)
	fp := newFakePlatform(t)
	opts := testOptions(fp, curDir, tasksDir, testFactory(cur, tasks))
	opts.TranscriptDir = t.TempDir()
	flushes := 0
	opts.OnCheckpoint = func(*Results) { flushes++ }

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if flushes != 3 {
		t.Errorf("OnCheckpoint fired %d times, want one per checkpoint (3)", flushes)
	}
	if entries, _ := os.ReadDir(opts.TranscriptDir); len(entries) == 0 {
		t.Error("expected per-episode transcripts to be written")
	}

	if len(res.Checkpoints) != 3 {
		t.Fatalf("want 3 checkpoints (baseline + 2 lessons), got %d", len(res.Checkpoints))
	}
	acc := []float64{res.Checkpoints[0].Accuracy, res.Checkpoints[1].Accuracy, res.Checkpoints[2].Accuracy}
	if acc[0] != 0 || acc[1] != 0.5 || acc[2] != 1 {
		t.Errorf("learning curve = %v, want [0 0.5 1]", acc)
	}
	// Every lesson captured and promoted.
	if res.Metrics.LessonsCaptured != 2 || res.Metrics.LessonsPromoted != 2 {
		t.Errorf("lessons captured/promoted = %d/%d, want 2/2", res.Metrics.LessonsCaptured, res.Metrics.LessonsPromoted)
	}
	// The delivery signal climbs: no enrichment at the empty baseline, full at the end.
	if res.Checkpoints[0].EnrichmentCoverage != 0 || res.Checkpoints[2].EnrichmentCoverage == 0 {
		t.Errorf("coverage should climb from 0, got %v -> %v", res.Checkpoints[0].EnrichmentCoverage, res.Checkpoints[2].EnrichmentCoverage)
	}
	// Per-trap-class flips at the right checkpoint.
	if res.Checkpoints[1].ByTrapClass["units_cents"].Accuracy != 1 || res.Checkpoints[1].ByTrapClass["net_revenue"].Accuracy != 0 {
		t.Errorf("after units lesson, units should be 1.0 and net 0.0, got %+v", res.Checkpoints[1].ByTrapClass)
	}
	// Teacher and evaluator identities are disjoint.
	assertDistinctIdentities(t, res)
}

func assertDistinctIdentities(t *testing.T, res *Results) {
	t.Helper()
	teachers := map[string]bool{}
	for _, l := range res.Lessons {
		teachers[l.Episode.Email] = true
	}
	for _, cp := range res.Checkpoints {
		for _, a := range cp.Attempts {
			if teachers[a.Email] {
				t.Errorf("evaluator %s reused a teacher identity", a.Email)
			}
		}
	}
}

func TestGuardPoolRejectsSmallPool(t *testing.T) {
	cur := testCurriculum()
	if err := guardPool(cur, Options{K: 3, IdentityKeys: 2}); err == nil {
		t.Error("expected guardPool to reject a pool smaller than the run needs")
	}
	if err := guardPool(cur, Options{K: 1, IdentityKeys: 0}); err == nil {
		t.Error("expected guardPool to reject a zero pool")
	}
	// A pool sized exactly to the need is accepted.
	need := maxIdentitySeq(len(cur.Lessons), 1)
	if err := guardPool(cur, Options{K: 1, IdentityKeys: need}); err != nil {
		t.Errorf("exact-fit pool rejected: %v", err)
	}
}

// claudeEvalStream is a canned `claude -p` eval transcript: mint, search, answer.
const claudeEvalStream = `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"i0","name":"mcp__bench__platform_info","input":{}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"i0","is_error":false,"content":"{\"session_id\":\"dps_cc_1\",\"version\":\"fake-1.0.0\"}"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"s1","name":"mcp__bench__search","input":{"query":"units"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","is_error":false,"content":"APPLIED KNOWLEDGE: units_cents"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: 100.00"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 100.00","session_id":"cc-1","usage":{"input_tokens":30,"output_tokens":8}}`

// TestClaudeCLIEpisode isolates the claude-cli episode path: a stubbed client
// returns a canned eval transcript, and the harness maps the client result,
// reads audit best effort by the threaded handle, and writes the transcript.
func TestClaudeCLIEpisode(t *testing.T) {
	fp := newFakePlatform(t)
	fp.events = append(fp.events, auditapi.Event{DurationMS: 5, SessionID: "dps_cc_1", ToolName: "search", Success: true})

	runner, err := claudecli.New(claudecli.Options{
		Model: "claude-sonnet-5",
		Exec: func(context.Context, claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(claudeEvalStream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	env := &runEnv{
		opts: Options{
			Target:      target.Target{BaseURL: fp.httpSrv.URL, Credential: "testkey"},
			HTTPTimeout: 10 * time.Second, Arm: "a3", ClaudeCLI: runner,
			IdentityKeys: 32, AuditTimeout: 5 * time.Second, TranscriptDir: t.TempDir(),
		},
		log:   testLogger(),
		audit: auditapi.New(fp.httpSrv.URL, target.Target{BaseURL: fp.httpSrv.URL, Credential: "testkey"}.HTTPClient(10*time.Second)),
	}
	rec := env.runEpisode(context.Background(), episodeSpec{stage: StageEval, unitID: "s3-units-a", seq: 7, prompt: "q", system: "sys", budget: 5})
	if rec.err != "" {
		t.Fatalf("claude-cli episode error: %s", rec.err)
	}
	if !gradeEval(rec.finalAnswer, task.Grading{Kind: task.GradeNumeric, Value: new(100.0), AbsTolerance: 0.01}) {
		t.Errorf("claude-cli answer %q did not grade correct", rec.finalAnswer)
	}
	if rec.sessionID != "dps_cc_1" {
		t.Errorf("handle = %q, want dps_cc_1", rec.sessionID)
	}
	if entries, _ := os.ReadDir(env.opts.TranscriptDir); len(entries) == 0 {
		t.Error("claude-cli transcript not written")
	}
}

func TestGradeEval(t *testing.T) {
	numeric := task.Grading{Kind: task.GradeNumeric, Value: new(42.0), AbsTolerance: 0.5}
	if !gradeEval("FINAL ANSWER: 42.1", numeric) {
		t.Error("in-tolerance numeric should grade correct")
	}
	if gradeEval("FINAL ANSWER: 99", numeric) {
		t.Error("out-of-tolerance numeric should grade incorrect")
	}
	if gradeEval("FINAL ANSWER: 42", task.Grading{Kind: task.GradeNumeric}) {
		t.Error("nil expected value must not grade correct")
	}
	entity := task.Grading{Kind: task.GradeEntity, Aliases: []string{"North"}, WrongAliases: []string{"South"}}
	if !gradeEval("FINAL ANSWER: North", entity) {
		t.Error("matching alias should grade correct")
	}
	if gradeEval("FINAL ANSWER: South", entity) {
		t.Error("wrong alias should grade incorrect")
	}
	if gradeEval("FINAL ANSWER: x", task.Grading{Kind: task.GradeExecSQL}) {
		t.Error("unsupported grading kind must not grade correct")
	}
}

func TestLoadEvalTasksRejectsExecSQL(t *testing.T) {
	dir := t.TempDir()
	raw, _ := yaml.Marshal(task.Task{ID: "s3-x", Suite: "s3", Prompt: "q", Arms: []string{"a3"},
		BudgetToolCalls: 5, ExpectedSQL: "SELECT 1", Grading: task.Grading{Kind: task.GradeExecSQL}})
	_ = os.WriteFile(filepath.Join(dir, "s3-x.yaml"), raw, 0o600)
	if _, err := loadEvalTasks(dir, "s3", "a3"); err == nil {
		t.Error("expected exec_sql eval task to be rejected")
	}
}
