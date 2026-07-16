package coldstart

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/promote"
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
	// Preflight contamination knobs (all empty = clean baseline).
	entityDesc map[string]string            // urn -> pre-existing description
	pages      []lifecycleapi.KnowledgePage // pre-existing knowledge pages
	// Failure knobs, set before the run starts (all zero = healthy platform).
	infoFailAfter int64 // platform_info mints fail after this many (0 = never)
	insightsFail  bool  // insights list returns 500
	approveFail   bool  // insight status PUT returns 500
	applyRefuse   bool  // apply_knowledge returns a plain tool error
	httpSrv       *httptest.Server
}

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	fp := &fakePlatform{applied: map[string]bool{}, entityDesc: map[string]string{}}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-coldstart", Version: "fake-1.0.0"}, nil)
	fp.addPlatformInfo(server)
	fp.addMemoryCapture(server)
	fp.addSearch(server)
	fp.addApplyKnowledge(server)
	fp.addGetEntity(server)
	fp.addGatedQuery(server)

	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/admin/knowledge/insights", fp.listInsights)
	mux.HandleFunc("GET /api/v1/admin/knowledge/insights/{id}", fp.getInsight)
	mux.HandleFunc("PUT /api/v1/admin/knowledge/insights/{id}/status", fp.putStatus)
	mux.HandleFunc("GET /api/v1/admin/knowledge/changesets", fp.listChangesets)
	mux.HandleFunc("GET /api/v1/admin/knowledge/changesets/{id}", fp.getChangeset)
	mux.HandleFunc("GET /api/v1/portal/knowledge-pages", fp.listPages)
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
			n := fp.minted.Add(1)
			if fp.infoFailAfter > 0 && n > fp.infoFailAfter {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "mint refused"}}}, nil, nil
			}
			payload := map[string]any{"session_id": fmt.Sprintf("dps_%d", n), "version": "fake-1.0.0"}
			raw, _ := json.Marshal(payload)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: payload}, nil, nil
		})
}

// addGatedQuery models the platform's search-first gate: a query tool that
// always refuses with the structured pre-audit error contract (no audit row).
func (fp *fakePlatform) addGatedQuery(server *mcp.Server) {
	schema := sessionSchema(map[string]*jsonschema.Schema{"sql": {Type: "string"}})
	mcp.AddTool(server, &mcp.Tool{Name: "trino_query", Description: "query", InputSchema: schema},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				IsError:           true,
				Content:           []mcp.Content{&mcp.TextContent{Text: "SEARCH_REQUIRED: call search first"}},
				StructuredContent: map[string]any{"error": map[string]any{"code": "search_required"}},
			}, nil, nil
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
			if fp.applyRefuse {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "change declined"}}}, nil, nil
			}
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
			fp.applySinkLocked(urn, args)
			fp.recordLocked(args, "apply_knowledge")
			return okResult("applied " + csID), nil, nil
		})
}

// applySinkLocked models the platform landing an applied change in its sink,
// which the Reviewer's post-apply sink read-back verifies: a datahub change
// becomes the entity's effective description, a page payload becomes a live
// knowledge page. The caller holds fp.mu.
func (fp *fakePlatform) applySinkLocked(urn string, args map[string]any) {
	if changes, ok := args["changes"].([]any); ok && len(changes) > 0 {
		if change, ok := changes[0].(map[string]any); ok {
			detail, _ := change["detail"].(string)
			fp.entityDesc[urn] = detail
		}
	}
	if page, ok := args["page"].(map[string]any); ok {
		slug, _ := page["slug"].(string)
		summary, _ := page["summary"].(string)
		fp.pages = append(fp.pages, lifecycleapi.KnowledgePage{ID: "kp-" + slug, Slug: slug, Summary: summary})
	}
}

// addGetEntity serves the preflight's baseline read and the promote sink
// read-back: an entity's effective description (the entityDesc knob models a
// prior run's promotion or an a2 seed; empty is the clean baseline).
func (fp *fakePlatform) addGetEntity(server *mcp.Server) {
	schema := sessionSchema(map[string]*jsonschema.Schema{"urn": {Type: "string"}})
	mcp.AddTool(server, &mcp.Tool{Name: "datahub_get_entity", Description: "entity metadata", InputSchema: schema},
		func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			urn, _ := args["urn"].(string)
			fp.mu.Lock()
			desc := fp.entityDesc[urn]
			fp.mu.Unlock()
			raw, _ := json.Marshal(map[string]any{"urn": urn, "type": "DATASET", "description": desc})
			return okResult(string(raw)), nil, nil
		})
}

// listPages serves the portal knowledge-pages list the preflight scans.
func (fp *fakePlatform) listPages(w http.ResponseWriter, _ *http.Request) {
	fp.mu.Lock()
	pages := append([]lifecycleapi.KnowledgePage{}, fp.pages...)
	fp.mu.Unlock()
	writeJSON(w, map[string]any{"pages": pages, "total": len(pages)})
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
	if fp.insightsFail {
		http.Error(w, "insights unavailable", http.StatusInternalServerError)
		return
	}
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
	if fp.approveFail {
		http.Error(w, "status update unavailable", http.StatusInternalServerError)
		return
	}
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
				Page:  &protocol.PagePayload{Slug: "net", Title: "Net", Summary: "net = amount - discount", Body: "net policy"},
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
	// Every append is flushed before the next long-running step (settle, eval),
	// so an interruption never discards a completed checkpoint OR a completed
	// teach+promote lesson. For 2 lessons the (lessons, checkpoints) snapshot
	// sequence is pinned: baseline cp, lesson 1, cp 1, lesson 2, cp 2.
	var flushed [][2]int
	opts.OnCheckpoint = func(r *Results) {
		flushed = append(flushed, [2]int{len(r.Lessons), len(r.Checkpoints)})
	}

	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	wantFlushed := [][2]int{{0, 1}, {1, 1}, {1, 2}, {2, 2}, {2, 3}}
	if fmt.Sprint(flushed) != fmt.Sprint(wantFlushed) {
		t.Errorf("flush snapshots (lessons, checkpoints) = %v, want %v (flush-after-append ordering broken)", flushed, wantFlushed)
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

// assertDistinctIdentities checks the run's identity discipline: no evaluator
// reuses a teacher identity, and every evaluator identity is globally unique
// across ALL checkpoints (scoped to its one checkpoint+repeat) — a reused
// evaluator would carry its own discovery scope and memories forward,
// contaminating a later checkpoint's measurement.
func assertDistinctIdentities(t *testing.T, res *Results) {
	t.Helper()
	teachers := map[string]bool{}
	for _, l := range res.Lessons {
		teachers[l.Episode.Email] = true
	}
	type slot struct{ checkpoint, repeat int }
	owner := map[string]slot{}
	for _, cp := range res.Checkpoints {
		for _, a := range cp.Attempts {
			if teachers[a.Email] {
				t.Errorf("evaluator %s reused a teacher identity", a.Email)
			}
			s := slot{cp.Index, a.Repeat}
			if prev, ok := owner[a.Email]; ok && prev != s {
				t.Errorf("evaluator %s used at checkpoint %d repeat %d AND checkpoint %d repeat %d; evaluator identities must be globally unique",
					a.Email, prev.checkpoint, prev.repeat, s.checkpoint, s.repeat)
			}
			owner[a.Email] = s
		}
	}
}

// TestIdentitySequences pins the identity math: teachers occupy 1..n, and every
// checkpoint's evaluators start above them and never collide across
// checkpoints or repeats. The exact values guard the arithmetic a refactor
// could silently shift (which would reuse identities and contaminate a run).
func TestIdentitySequences(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"teacherSeq(0)", teacherSeq(0), 1},
		{"teacherSeq(5)", teacherSeq(5), 6},
		{"evaluatorSeq(0,1,6,1)", evaluatorSeq(0, 1, 6, 1), 7},
		{"evaluatorSeq(6,1,6,1)", evaluatorSeq(6, 1, 6, 1), 13},
		{"evaluatorSeq(6,3,6,3)", evaluatorSeq(6, 3, 6, 3), 27},
		{"maxIdentitySeq(6,1)", maxIdentitySeq(6, 1), 13},
		{"maxIdentitySeq(6,3)", maxIdentitySeq(6, 3), 27},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}

// countingFactory wraps a factory and counts adapter builds, so a test can
// assert a refused run spent no episode.
func countingFactory(inner AdapterFactory, calls *atomic.Int64) AdapterFactory {
	return func(unitID, stage string) (llm.Adapter, error) {
		calls.Add(1)
		return inner(unitID, stage)
	}
}

// TestPreflightRefusesContaminatedBaseline proves each contamination source —
// a pre-existing entity description, a leftover insight on a (teacher, URN)
// pair, a knowledge page with a curriculum slug — aborts the run before any
// adapter (LLM episode) is built, with the remediation in the error.
func TestPreflightRefusesContaminatedBaseline(t *testing.T) {
	cur := testCurriculum()
	tasks := []task.Task{numericTask("s3-units-a", "units_cents", 100)}
	cases := map[string]func(fp *fakePlatform){
		"entity description": func(fp *fakePlatform) {
			fp.entityDesc[ordersURN] = "Amounts are integer cents."
		},
		"leftover insight": func(fp *fakePlatform) {
			// The baseline requires an EMPTY insight store: any leftover trips the
			// preflight, even one captured by a non-curriculum identity (an S5
			// run's teacher) on a non-curriculum anchor.
			fp.insights = append(fp.insights, lifecycleapi.Insight{
				ID: "in-old", CreatedAt: time.Unix(1, 0), CapturedBy: "bench-agent-042@apikey.local",
				InsightText: "old", Status: "applied", EntityURNs: []string{"urn:li:dataset:other"},
			})
		},
		"leftover knowledge page": func(fp *fakePlatform) {
			// Any page trips it, curriculum slug or not (e.g. an S5 lc-* page).
			fp.pages = append(fp.pages, lifecycleapi.KnowledgePage{ID: "kp-1", Slug: "focus-region-definition"})
		},
	}
	for name, contaminate := range cases {
		t.Run(name, func(t *testing.T) {
			curDir, tasksDir := writeFixtures(t, cur, tasks)
			fp := newFakePlatform(t)
			contaminate(fp)
			var adapterCalls atomic.Int64
			opts := testOptions(fp, curDir, tasksDir, countingFactory(testFactory(cur, tasks), &adapterCalls))
			res, err := Run(context.Background(), opts)
			if err == nil {
				t.Fatal("run accepted a contaminated baseline")
			}
			if !strings.Contains(err.Error(), "remediation") {
				t.Errorf("preflight error missing remediation guidance: %v", err)
			}
			if res != nil {
				t.Errorf("a refused run must not return results, got %+v", res)
			}
			if n := adapterCalls.Load(); n != 0 {
				t.Errorf("preflight refusal built %d adapter(s); it must abort before any episode is spent", n)
			}
		})
	}
}

// TestEntityContamination covers the aspect checks the integration variants do
// not exercise: tags and deprecation left by an a2 seed, and a clean entity.
func TestEntityContamination(t *testing.T) {
	if got := entityContamination(preflightEntity{}); got != "" {
		t.Errorf("clean entity flagged: %s", got)
	}
	if got := entityContamination(preflightEntity{Description: "  "}); got != "" {
		t.Errorf("whitespace-only description flagged: %s", got)
	}
	if got := entityContamination(preflightEntity{Description: "docs"}); got == "" {
		t.Error("description contamination not flagged")
	}
	if got := entityContamination(preflightEntity{Tags: []json.RawMessage{[]byte(`{"name":"pii"}`)}}); got == "" {
		t.Error("tag contamination not flagged")
	}
	deprecated := preflightEntity{}
	deprecated.Deprecation = &struct {
		Deprecated bool `json:"deprecated"`
	}{Deprecated: true}
	if got := entityContamination(deprecated); got == "" {
		t.Error("deprecation contamination not flagged")
	}
}

// TestParsePreflightEntityIgnoresTrailingText proves the entity JSON is parsed
// even when enrichment middleware appends context after it.
func TestParsePreflightEntityIgnoresTrailingText(t *testing.T) {
	got, err := parsePreflightEntity(`{"description":"d"}` + "\n--- Semantic Context ---\nmore text")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got.Description != "d" {
		t.Errorf("description = %q, want d", got.Description)
	}
	if _, err := parsePreflightEntity("not json"); err == nil {
		t.Error("non-JSON result must error")
	}
}

// TestSettleRunsBetweenPromoteAndEval proves the settle pause runs after a
// successful DATAHUB-sink promote and before the following eval checkpoint,
// with the configured duration — and is skipped for a page-sink promote (page
// hits are served live from the portal store; only DataHub table context is
// cached). The injected sleeper records instead of sleeping, so no test
// real-sleeps.
func TestSettleRunsBetweenPromoteAndEval(t *testing.T) {
	cur := testCurriculum() // lesson 1 datahub sink, lesson 2 page sink
	tasks := []task.Task{
		numericTask("s3-units-a", "units_cents", 100),
		numericTask("s3-net-a", "net_revenue", 50),
	}
	curDir, tasksDir := writeFixtures(t, cur, tasks)
	fp := newFakePlatform(t)

	// Sequence log: the factory records episode starts, the sleeper records
	// settles, so ordering (promote -> settle -> eval) is assertable.
	var mu sync.Mutex
	var events []string
	factory := func(unitID, stage string) (llm.Adapter, error) {
		mu.Lock()
		events = append(events, stage+":"+unitID)
		mu.Unlock()
		return testFactory(cur, tasks)(unitID, stage)
	}
	opts := testOptions(fp, curDir, tasksDir, factory)
	opts.Settle = 5 * time.Minute
	var slept []time.Duration
	opts.SettleSleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		events = append(events, "settle")
		slept = append(slept, d)
		mu.Unlock()
		return nil
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// Both lessons promote, but only the datahub-sink lesson settles.
	if len(slept) != 1 || slept[0] != 5*time.Minute {
		t.Fatalf("settle sleeps = %v, want one of 5m (datahub-sink lesson only)", slept)
	}
	if res.Manifest.Settle != "5m0s" {
		t.Errorf("manifest settle = %q, want 5m0s (pacing must be recorded on kept results)", res.Manifest.Settle)
	}
	assertSettleOrdering(t, events)
}

// assertSettleOrdering checks every settle event lands after a teach episode
// and before the next eval episode.
func assertSettleOrdering(t *testing.T, events []string) {
	t.Helper()
	for i, ev := range events {
		if ev != "settle" {
			continue
		}
		if i == 0 || !strings.HasPrefix(events[i-1], StageTeach+":") {
			t.Errorf("settle at %d not immediately after a teach episode: %v", i, events)
		}
		if i+1 >= len(events) || !strings.HasPrefix(events[i+1], StageEval+":") {
			t.Errorf("settle at %d not immediately before an eval episode: %v", i, events)
		}
	}
}

// TestSleepRespectsContext covers the real (non-injected) sleeper: it completes
// a tiny pause and aborts immediately on a canceled context, so a settle
// window never blocks an interrupted run.
func TestSleepRespectsContext(t *testing.T) {
	e := &runEnv{opts: Options{}}
	if err := e.sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := e.sleep(ctx, time.Hour); err == nil {
		t.Fatal("a canceled context must interrupt the settle sleep")
	}
}

// TestSettleSkippedForUnpromotedLesson proves an unpromoted lesson triggers no
// settle pause (nothing changed in the enrichment layer).
func TestSettleSkippedForUnpromotedLesson(t *testing.T) {
	cur := testCurriculum()
	tasks := []task.Task{numericTask("s3-units-a", "units_cents", 100)}
	curDir, tasksDir := writeFixtures(t, cur, tasks)
	fp := newFakePlatform(t)

	// A factory whose teach episodes never capture: WaitForInsight misses, so no
	// lesson promotes (Captured=false is a measured outcome, not an error).
	factory := func(unitID, stage string) (llm.Adapter, error) {
		if stage == StageTeach {
			return llm.NewScripted([]llm.Step{{FinalText: "noted, but not saved"}}), nil
		}
		return testFactory(cur, tasks)(unitID, stage)
	}
	opts := testOptions(fp, curDir, tasksDir, factory)
	opts.AuditTimeout = 50 * time.Millisecond // capture-verify miss window
	opts.Settle = 5 * time.Minute
	settles := 0
	opts.SettleSleep = func(context.Context, time.Duration) error {
		settles++
		return nil
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Metrics.LessonsPromoted != 0 {
		t.Fatalf("expected no promotions, got %d", res.Metrics.LessonsPromoted)
	}
	if settles != 0 {
		t.Errorf("settle fired %d time(s) for unpromoted lessons; it must be skipped", settles)
	}
}

// newTestEnv builds a runEnv over the fake platform for driving teachAndPromote
// and runEpisode directly (the branch tests below), mirroring Run's wiring.
func newTestEnv(fp *fakePlatform, factory AdapterFactory) *runEnv {
	tgt := target.Target{BaseURL: fp.httpSrv.URL, Credential: "testkey"}
	life := lifecycleapi.New(fp.httpSrv.URL, tgt.HTTPClient(5*time.Second))
	return &runEnv{
		opts: Options{
			Target: tgt, HTTPTimeout: 5 * time.Second, Arm: "a3", K: 1,
			Factory: factory, AuditTimeout: 300 * time.Millisecond, IdentityKeys: 64,
			Log: testLogger(),
		},
		log:      testLogger(),
		audit:    auditapi.New(fp.httpSrv.URL, tgt.HTTPClient(5*time.Second)),
		life:     life,
		reviewer: promote.Reviewer{Life: life, Log: testLogger()},
	}
}

// capturingTeachFactory returns a factory whose teach episodes capture the
// lesson's fact, sufficient for driving teachAndPromote through its
// post-capture branches.
func capturingTeachFactory(l curriculum.Lesson) AdapterFactory {
	return func(string, string) (llm.Adapter, error) {
		return &knowledgeAdapter{mode: StageTeach, class: l.TrapClass, urn: l.EntityURN}, nil
	}
}

// TestTeachAndPromoteBranches covers every teachAndPromote outcome: harness
// errors carry their stage prefix, while a capture miss and an apply refusal
// are measured outcomes (no error). Each error string is what a paid run's
// lessons[].error would show, so the prefixes are pinned.
func TestTeachAndPromoteBranches(t *testing.T) {
	lesson := testCurriculum().Lessons[0]
	cases := []struct {
		name          string
		configure     func(fp *fakePlatform) AdapterFactory
		wantErrPrefix string // "" = measured outcome, no harness error
		wantCaptured  *bool
		wantPromoted  *bool
	}{
		{
			name: "teach harness error",
			configure: func(*fakePlatform) AdapterFactory {
				return func(string, string) (llm.Adapter, error) { return nil, errors.New("adapter down") }
			},
			wantErrPrefix: "build adapter",
		},
		{
			name: "capture verify error",
			configure: func(fp *fakePlatform) AdapterFactory {
				fp.insightsFail = true
				return capturingTeachFactory(lesson)
			},
			wantErrPrefix: "capture verify: ",
		},
		{
			name: "capture miss is measured",
			configure: func(*fakePlatform) AdapterFactory {
				// The teach episode never reaches the capture tool.
				return func(string, string) (llm.Adapter, error) {
					return llm.NewScripted([]llm.Step{{FinalText: "noted"}}), nil
				}
			},
			wantCaptured: new(false),
		},
		{
			name: "admin session error",
			configure: func(fp *fakePlatform) AdapterFactory {
				fp.infoFailAfter = 1 // the teach mint succeeds; the admin mint fails
				return capturingTeachFactory(lesson)
			},
			wantErrPrefix: "admin session: ",
			wantCaptured:  new(true),
		},
		{
			name: "apply error",
			configure: func(fp *fakePlatform) AdapterFactory {
				fp.approveFail = true
				return capturingTeachFactory(lesson)
			},
			wantErrPrefix: "promote: ",
			wantCaptured:  new(true),
		},
		{
			name: "apply refusal is measured",
			configure: func(fp *fakePlatform) AdapterFactory {
				fp.applyRefuse = true
				return capturingTeachFactory(lesson)
			},
			wantCaptured: new(true),
			wantPromoted: new(false),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fp := newFakePlatform(t)
			env := newTestEnv(fp, tc.configure(fp))
			defer env.closeAdmin()
			lr := env.teachAndPromote(context.Background(), lesson, 1)
			if tc.wantErrPrefix == "" && lr.Error != "" {
				t.Fatalf("measured outcome produced a harness error: %s", lr.Error)
			}
			if tc.wantErrPrefix != "" && !strings.Contains(lr.Error, tc.wantErrPrefix) {
				t.Fatalf("error = %q, want it to contain %q", lr.Error, tc.wantErrPrefix)
			}
			assertBoolPtr(t, "captured", lr.Captured, tc.wantCaptured)
			assertBoolPtr(t, "promoted", lr.Promoted, tc.wantPromoted)
		})
	}
}

func assertBoolPtr(t *testing.T, field string, got, want *bool) {
	t.Helper()
	if want == nil {
		return
	}
	if got == nil {
		t.Errorf("%s = nil, want %v", field, *want)
		return
	}
	if *got != *want {
		t.Errorf("%s = %v, want %v", field, *got, *want)
	}
}

// TestRunSurfacesHarnessFailures proves a run with any harness-level episode
// failure returns a non-nil error (nonzero exit), so a paid run can never
// silently publish a curve with unaccounted episodes.
func TestRunSurfacesHarnessFailures(t *testing.T) {
	cur := testCurriculum()
	tasks := []task.Task{numericTask("s3-units-a", "units_cents", 100)}
	curDir, tasksDir := writeFixtures(t, cur, tasks)
	fp := newFakePlatform(t)
	inner := testFactory(cur, tasks)
	factory := func(unitID, stage string) (llm.Adapter, error) {
		if stage == StageEval && unitID == "s3-units-a" {
			return nil, errors.New("adapter down")
		}
		return inner(unitID, stage)
	}
	res, err := Run(context.Background(), testOptions(fp, curDir, tasksDir, factory))
	if err == nil {
		t.Fatal("run with harness failures exited clean")
	}
	if res == nil || res.Metrics.HarnessFailures == 0 {
		t.Fatalf("results must still carry the failed attempts, got %+v", res)
	}
}

// TestEpisodeClassifiesRefusalsAndTransportErrors drives one episode whose
// tools hit all three classifications: an audited call (search), a pre-audit
// structured refusal (the gated trino_query, no audit row expected), and a
// transport-level error (an unknown tool name, indeterminate). The audit
// read-back bounds only converge when the classification is right: counting
// the refusal as audited would demand a row the platform never wrote.
func TestEpisodeClassifiesRefusalsAndTransportErrors(t *testing.T) {
	fp := newFakePlatform(t)
	steps := []llm.Step{
		{ToolCalls: []llm.ToolCall{{Name: "search", Args: map[string]any{"query": "q"}}}},
		{ToolCalls: []llm.ToolCall{{Name: "trino_query", Args: map[string]any{"sql": "SELECT 1"}}}},
		{ToolCalls: []llm.ToolCall{{Name: "no_such_tool", Args: map[string]any{}}}},
		{FinalText: "FINAL ANSWER: done"},
	}
	env := newTestEnv(fp, func(string, string) (llm.Adapter, error) { return llm.NewScripted(steps), nil })
	rec := env.runEpisode(context.Background(), episodeSpec{stage: StageEval, unitID: "s3-x", seq: 9, prompt: "q", system: "sys", budget: 10})
	if rec.err != "" {
		t.Fatalf("episode error: %s", rec.err)
	}
	if !rec.searchCalled {
		t.Error("search-called signal not recorded")
	}
	// Two of the three calls errored (refusal + transport), one succeeded.
	if rec.toolErrors != 2 {
		t.Errorf("tool errors = %d, want 2", rec.toolErrors)
	}
	// Only the search is audited: the refusal never reached the audit layer and
	// the transport error is indeterminate (bounded above, not below).
	if rec.audit.AuditedCalls != 1 {
		t.Errorf("audited calls = %d, want 1 (refusal/transport misclassified)", rec.audit.AuditedCalls)
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
// The Exec stub also pins credential rotation: the per-episode MCP config must
// authenticate as the episode's pool identity (seq 7 -> testkey-007), because a
// wrong credential would silently collapse every evaluator onto one identity.
func TestClaudeCLIEpisode(t *testing.T) {
	fp := newFakePlatform(t)
	fp.events = append(fp.events, auditapi.Event{DurationMS: 5, SessionID: "dps_cc_1", ToolName: "search", Success: true})

	var gotCredential string
	runner, err := claudecli.New(claudecli.Options{
		Model: "claude-sonnet-5",
		Exec: func(_ context.Context, spec claudecli.CommandSpec) ([]byte, []byte, error) {
			raw, err := os.ReadFile(filepath.Join(spec.Dir, "mcp-config.json"))
			if err != nil {
				t.Errorf("read per-episode mcp config: %v", err)
			}
			var cfg struct {
				MCPServers map[string]struct {
					Headers map[string]string `json:"headers"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(raw, &cfg); err != nil {
				t.Errorf("parse per-episode mcp config: %v", err)
			}
			gotCredential = strings.TrimPrefix(cfg.MCPServers["bench"].Headers["Authorization"], "Bearer ")
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
	if want := pool.Credential("testkey", 7, 32); gotCredential != want {
		t.Errorf("episode credential = %q, want %q (pool rotation broken)", gotCredential, want)
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

// claudeNoHandleStream is a canned transcript where claude drove a successful
// bench call but never minted a dps_ handle via platform_info.
const claudeNoHandleStream = `{"type":"system","subtype":"init","mcp_servers":[{"name":"bench","status":"connected"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"s1","name":"mcp__bench__search","input":{"query":"units"}}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"s1","is_error":false,"content":"ok"}]}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"FINAL ANSWER: 100.00"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"FINAL ANSWER: 100.00","session_id":"cc-2","usage":{"input_tokens":30,"output_tokens":8}}`

// TestClaudeCLIEpisodeNoHandleWithSuccesses proves a claude-cli episode that
// reports successful tool calls but no dps_ handle is a harness error, not a
// silent zero-coverage attempt: with no handle a successful data call is
// impossible (the session gate refuses un-threaded calls), so the inconsistency
// must surface rather than degrade the coverage curve.
func TestClaudeCLIEpisodeNoHandleWithSuccesses(t *testing.T) {
	fp := newFakePlatform(t)
	runner, err := claudecli.New(claudecli.Options{
		Model: "claude-sonnet-5",
		Exec: func(_ context.Context, _ claudecli.CommandSpec) ([]byte, []byte, error) {
			return []byte(claudeNoHandleStream), nil, nil
		},
	})
	if err != nil {
		t.Fatalf("claudecli.New: %v", err)
	}
	env := &runEnv{
		opts: Options{
			Target:      target.Target{BaseURL: fp.httpSrv.URL, Credential: "testkey"},
			HTTPTimeout: 10 * time.Second, Arm: "a3", ClaudeCLI: runner,
			IdentityKeys: 32, AuditTimeout: time.Second,
		},
		log:   testLogger(),
		audit: auditapi.New(fp.httpSrv.URL, target.Target{BaseURL: fp.httpSrv.URL, Credential: "testkey"}.HTTPClient(10*time.Second)),
	}
	rec := env.runEpisode(context.Background(), episodeSpec{stage: StageEval, unitID: "s3-units-a", seq: 7, prompt: "q", budget: 5})
	if rec.err == "" || !strings.Contains(rec.err, "no dps_ handle") {
		t.Fatalf("episode err = %q, want a no-handle harness error", rec.err)
	}
}

// TestClaudeCLIEpisodeAuditReadFailureRecorded proves a failed audit read-back
// lands on the episode result instead of silently zeroing the audit metrics:
// the fake platform has no audit rows for the handle, so WaitForSession times
// out and the loss must be visible on the record.
func TestClaudeCLIEpisodeAuditReadFailureRecorded(t *testing.T) {
	fp := newFakePlatform(t) // no audit events appended for dps_cc_1
	runner, err := claudecli.New(claudecli.Options{
		Model: "claude-sonnet-5",
		Exec: func(_ context.Context, _ claudecli.CommandSpec) ([]byte, []byte, error) {
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
			IdentityKeys: 32, AuditTimeout: 200 * time.Millisecond,
		},
		log:   testLogger(),
		audit: auditapi.New(fp.httpSrv.URL, target.Target{BaseURL: fp.httpSrv.URL, Credential: "testkey"}.HTTPClient(10*time.Second)),
	}
	rec := env.runEpisode(context.Background(), episodeSpec{stage: StageEval, unitID: "s3-units-a", seq: 7, prompt: "q", budget: 5})
	if rec.err != "" {
		t.Fatalf("episode err = %q, want graded episode (audit loss is not a harness failure)", rec.err)
	}
	if rec.auditReadErr == "" {
		t.Fatal("auditReadErr empty: a lost audit read-back must be recorded, not silently zeroed")
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
