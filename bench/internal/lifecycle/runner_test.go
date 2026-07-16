package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// authCtxKey carries the request Authorization header into tool handlers so the
// fake can attribute a capture to the identity that made it (the runner queries
// captures back by that identity's derived email).
type authCtxKey struct{}

// fakePlatform assembles a real MCP server (platform_info, memory_capture,
// search, apply_knowledge) over streamable HTTP plus the admin knowledge and
// audit REST surface, so the lifecycle runner is exercised through genuine
// protocol wiring and genuine API-verified state transitions.
type fakePlatform struct {
	mu             sync.Mutex
	base           string
	minted         atomic.Int64
	seq            int64
	insights       []lifecycleapi.Insight
	changesets     []lifecycleapi.Changeset
	events         []auditapi.Event
	httpSrv        *httptest.Server
	applyFails     bool   // apply_knowledge returns a tool error (measured miss)
	noChangesetRef bool   // apply records the changeset but leaves insight.changeset_ref empty
	surfaceText    string // appended to every search result (models cross-enrichment surfacing the fact)
}

func newFakePlatform(t *testing.T) *fakePlatform {
	t.Helper()
	fp := &fakePlatform{base: "testkey"}
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-lifecycle", Version: "1.0.0"}, nil)
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

// emailFromAuth derives a captured_by email from a Bearer credential, mirroring
// pkg/auth: the base key resolves to the admin identity, a rotated "<base>-NNN"
// key to bench-agent-NNN@apikey.local.
func (fp *fakePlatform) emailFromAuth(auth string) string {
	v := strings.TrimPrefix(auth, "Bearer ")
	if v == fp.base {
		return "bench-admin@apikey.local"
	}
	if suffix, ok := strings.CutPrefix(v, fp.base+"-"); ok {
		return "bench-agent-" + suffix + "@apikey.local"
	}
	return v + "@apikey.local"
}

func callerEmail(ctx context.Context, fp *fakePlatform) string {
	auth, _ := ctx.Value(authCtxKey{}).(string)
	return fp.emailFromAuth(auth)
}

func sessionSchema(extra map[string]*jsonschema.Schema, required ...string) *jsonschema.Schema {
	props := map[string]*jsonschema.Schema{
		"session_id": {Type: "string", Description: "platform-injected session handle"},
	}
	maps.Copy(props, extra)
	return &jsonschema.Schema{Type: "object", Properties: props, Required: append(required, "session_id")}
}

func (fp *fakePlatform) addPlatformInfo(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{Name: "platform_info", Description: "orientation"},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			handle := fmt.Sprintf("dps_fake_%d", fp.minted.Add(1))
			payload := map[string]any{"session_id": handle, "version": "fake-1.0.0"}
			raw, _ := json.Marshal(payload)
			return &mcp.CallToolResult{
				Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
				StructuredContent: payload,
			}, nil, nil
		})
}

// addMemoryCapture records a knowledge insight owned by the calling identity. A
// capture whose category is "correction" supersedes the caller's prior live
// insights on the same entity, modeling recall-first supersede.
func (fp *fakePlatform) addMemoryCapture(server *mcp.Server) {
	schema := sessionSchema(map[string]*jsonschema.Schema{
		"text":        {Type: "string"},
		"entity_urns": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
		"category":    {Type: "string"},
	}, "text")
	mcp.AddTool(server, &mcp.Tool{Name: "memory_capture", Description: "save knowledge", InputSchema: schema},
		func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			email := callerEmail(ctx, fp)
			text, _ := args["text"].(string)
			category, _ := args["category"].(string)
			urn := firstURN(args["entity_urns"])
			fp.mu.Lock()
			if category == "correction" {
				// Mirror the platform: recall-first supersede only retracts PENDING
				// insights; a reviewed (applied) insight is never clobbered.
				for i := range fp.insights {
					in := &fp.insights[i]
					if in.CapturedBy == email && in.LinksEntity(urn) && in.Status == "pending" {
						in.Status = "superseded"
					}
				}
			}
			fp.seq++
			id := "in-" + strconv.FormatInt(fp.seq, 10)
			fp.insights = append(fp.insights, lifecycleapi.Insight{
				ID: id, CreatedAt: time.Unix(fp.seq, 0).UTC(), CapturedBy: email,
				Category: category, InsightText: text, Status: "pending",
				EntityURNs: urnSlice(urn),
			})
			fp.recordLocked(args, "memory_capture")
			fp.mu.Unlock()
			return okResult("captured " + id), nil, nil
		})
}

func (fp *fakePlatform) addSearch(server *mcp.Server) {
	schema := sessionSchema(map[string]*jsonschema.Schema{"query": {Type: "string"}})
	mcp.AddTool(server, &mcp.Tool{Name: "search", Description: "discover knowledge", InputSchema: schema},
		func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			fp.mu.Lock()
			fp.recordLocked(args, "search")
			// surfaceText models cross-enrichment carrying the promoted fact into a
			// search result, so the transfer-surfaced instrumentation can be exercised
			// end to end. Empty (the default) means the fact never surfaces.
			text := "search results"
			if fp.surfaceText != "" {
				text += "\n" + fp.surfaceText
			}
			fp.mu.Unlock()
			return okResult(text), nil, nil
		})
}

// addApplyKnowledge marks the source insights applied and records a changeset
// linking them, mirroring the reviewer-side promotion.
func (fp *fakePlatform) addApplyKnowledge(server *mcp.Server) {
	schema := sessionSchema(map[string]*jsonschema.Schema{
		"action":      {Type: "string"},
		"entity_urn":  {Type: "string"},
		"insight_ids": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
		"sink":        {Type: "string"},
	}, "action")
	mcp.AddTool(server, &mcp.Tool{Name: "apply_knowledge", Description: "promote", InputSchema: schema},
		func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			email := callerEmail(ctx, fp)
			urn, _ := args["entity_urn"].(string)
			ids := stringSlice(args["insight_ids"])
			fp.mu.Lock()
			defer fp.mu.Unlock()
			fp.recordLocked(args, "apply_knowledge")
			if fp.applyFails {
				return &mcp.CallToolResult{IsError: true,
					Content: []mcp.Content{&mcp.TextContent{Text: "datahub write refused"}}}, nil, nil
			}
			fp.seq++
			csID := "cs-" + strconv.FormatInt(fp.seq, 10)
			fp.changesets = append(fp.changesets, lifecycleapi.Changeset{
				ID: csID, TargetURN: urn, ChangeType: "update_description",
				SourceInsightIDs: ids, AppliedBy: email, RolledBack: false,
			})
			for _, id := range ids {
				for i := range fp.insights {
					if fp.insights[i].ID == id {
						fp.insights[i].Status = "applied"
						if !fp.noChangesetRef {
							fp.insights[i].ChangesetRef = csID
						}
					}
				}
			}
			return okResult("applied " + csID), nil, nil
		})
}

// recordLocked appends an audit row for a call that carried a session handle.
// The caller holds fp.mu.
func (fp *fakePlatform) recordLocked(args map[string]any, tool string) {
	sessionID, _ := args["session_id"].(string)
	if sessionID == "" {
		return
	}
	fp.events = append(fp.events, auditapi.Event{
		Timestamp: time.Now().UTC(), DurationMS: 3, SessionID: sessionID,
		ToolName: tool, Success: true, EventKind: "mcp_tool_call",
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
	writeJSON(w, map[string]any{"data": out, "total": len(out), "page": 1, "per_page": 100})
}

func (fp *fakePlatform) getInsight(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, in := range fp.insights {
		if in.ID == id {
			writeJSON(w, in)
			return
		}
	}
	http.Error(w, "not found", http.StatusNotFound)
}

func (fp *fakePlatform) putStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Status string `json:"status"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for i := range fp.insights {
		if fp.insights[i].ID == id {
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
		if v := q.Get("applied_by"); v != "" && cs.AppliedBy != v {
			continue
		}
		out = append(out, cs)
	}
	writeJSON(w, map[string]any{"data": out, "total": len(out), "page": 1, "per_page": 100})
}

func (fp *fakePlatform) getChangeset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	fp.mu.Lock()
	defer fp.mu.Unlock()
	for _, cs := range fp.changesets {
		if cs.ID == id {
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
	writeJSON(w, map[string]any{"data": matched, "total": len(matched), "page": 1, "per_page": 200})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func okResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

func firstURN(v any) string { return firstOf(stringSlice(v)) }

func firstOf(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}

func urnSlice(urn string) []string {
	if urn == "" {
		return nil
	}
	return []string{urn}
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

// --- protocol fixtures + scripts ---

const benchURN = "urn:li:dataset:(urn:li:dataPlatform:trino,memory.bench.orders,PROD)"

func numericGrade(v, tol float64) task.Grading {
	return task.Grading{Kind: task.GradeNumeric, Value: &v, AbsTolerance: tol}
}

// okProtocol is a full-success promote+transfer lifecycle (no supersede stage;
// promote and update are mutually exclusive).
func okProtocol() protocol.Protocol {
	return protocol.Protocol{
		ID: "lc-ok", Title: "OK lifecycle", Fact: "Net revenue is amount minus discount over completed orders.",
		EntityURN: benchURN, Sink: protocol.SinkDataHub, BudgetToolCalls: 10,
		Teach:    protocol.TeachStage{Prompt: "Remember: revenue is net."},
		Recall:   protocol.RecallStage{Prompt: "net revenue 2025?", Grading: numericGrade(123.45, 0.5)},
		Transfer: &protocol.RecallStage{Prompt: "net revenue 2025?", Grading: numericGrade(123.45, 0.5)},
		Abstain:  &protocol.AbstainStage{Prompt: "refund rate for Antarctica?"},
	}
}

// updateProtocol is a full-success supersede lifecycle (no promote/transfer).
func updateProtocol() protocol.Protocol {
	p := okProtocol()
	p.ID = "lc-upd"
	p.Transfer = nil
	p.Update = &protocol.UpdateStage{
		Prompt: "Correction: the primary region is defined by net revenue.", Fact: "Primary region is by net revenue.",
		Recall:          protocol.RecallStage{Prompt: "net revenue 2025 now?", Grading: numericGrade(200, 0.5)},
		SupersededValue: new(123.45),
	}
	return p
}

// okScript plays the promote+transfer run.
func okScript() map[string]llm.Script {
	return map[string]llm.Script{
		"lc-ok": {
			StageTeach:    {captureStep("definition"), {FinalText: "saved"}},
			StageRecall:   {searchStep(), {FinalText: "FINAL ANSWER: 123.45"}},
			StageTransfer: {searchStep(), {FinalText: "FINAL ANSWER: 123.45"}},
			StageAbstain:  {{FinalText: "FINAL ANSWER: INSUFFICIENT INFORMATION"}},
		},
	}
}

// updateScript plays the supersede run: teach, recall, a correction capture that
// supersedes the pending teach insight, the flipped recall, and an abstention.
func updateScript() map[string]llm.Script {
	return map[string]llm.Script{
		"lc-upd": {
			StageTeach:        {captureStep("definition"), {FinalText: "saved"}},
			StageRecall:       {searchStep(), {FinalText: "FINAL ANSWER: 123.45"}},
			StageUpdate:       {captureStep("correction"), {FinalText: "saved"}},
			StageUpdateRecall: {searchStep(), {FinalText: "FINAL ANSWER: 200.00"}},
			StageAbstain:      {{FinalText: "FINAL ANSWER: INSUFFICIENT INFORMATION"}},
		},
	}
}

func captureStep(category string) llm.Step {
	return llm.Step{ToolCalls: []llm.ToolCall{{Name: "memory_capture", Args: map[string]any{
		"text": "net revenue fact", "entity_urns": []any{benchURN}, "category": category,
	}}}}
}

func searchStep() llm.Step {
	return llm.Step{ToolCalls: []llm.ToolCall{{Name: "search", Args: map[string]any{"query": "revenue"}}}}
}

// scriptFactory returns an AdapterFactory over per-protocol, per-stage scripts.
func scriptFactory(scripts map[string]llm.Script) AdapterFactory {
	return func(protocolID, stage string) (llm.Adapter, error) {
		s, ok := scripts[protocolID]
		if !ok {
			return nil, fmt.Errorf("no script for protocol %s", protocolID)
		}
		steps, ok := s[stage]
		if !ok {
			return nil, fmt.Errorf("no script for %s/%s", protocolID, stage)
		}
		return llm.NewScripted(steps), nil
	}
}

func writeProtocols(t *testing.T, dir string, protocols ...protocol.Protocol) {
	t.Helper()
	for _, p := range protocols {
		raw, err := yaml.Marshal(p)
		if err != nil {
			t.Fatalf("marshal protocol: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, p.ID+".yaml"), raw, 0o600); err != nil {
			t.Fatalf("write protocol: %v", err)
		}
	}
}

func runOptions(fp *fakePlatform, dir string, factory AdapterFactory) Options {
	return Options{
		Target:       target.Target{BaseURL: fp.httpSrv.URL, Credential: fp.base},
		HTTPTimeout:  10 * time.Second,
		Arm:          "a3",
		K:            1,
		ProtocolsDir: dir,
		Factory:      factory,
		LLMProvider:  "scripted",
		AuditTimeout: 2 * time.Second,
		IdentityKeys: 16,
		Log:          slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
	}
}

func TestFullLifecycleSucceeds(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	writeProtocols(t, dir, okProtocol())

	res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(okScript())))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(res.Runs))
	}
	run := res.Runs[0]
	if run.Error != "" {
		t.Fatalf("unexpected harness error: %s", run.Error)
	}
	assertTrue(t, "captured", run.Captured)
	assertTrue(t, "recall", run.RecallCorrect)
	assertTrue(t, "surfaced", run.RecallSurfaced)
	assertTrue(t, "promoted", run.Promoted)
	assertTrue(t, "transfer", run.TransferCorrect)
	assertTrue(t, "abstain", run.AbstainCorrect)
	if run.UpdateCorrect != nil || run.Duplicated != nil {
		t.Fatalf("promote protocol must not run the update stage: %+v", run)
	}
	if !run.Passed() {
		t.Fatal("full lifecycle should pass")
	}
	m := res.Metrics
	if m.PassK.Rate != 1 || m.CaptureRate.Rate != 1 || m.TransferRate.Rate != 1 {
		t.Fatalf("metrics not all-1: %+v", m)
	}
	if res.Manifest.PlatformVersion != "fake-1.0.0" {
		t.Fatalf("platform version = %q, want fake-1.0.0", res.Manifest.PlatformVersion)
	}
	if res.Manifest.Model != "scripted" {
		t.Fatalf("model = %q, want scripted", res.Manifest.Model)
	}
}

func TestUpdateLifecycleSucceeds(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	writeProtocols(t, dir, updateProtocol())

	res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(updateScript())))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	run := res.Runs[0]
	if run.Error != "" {
		t.Fatalf("unexpected harness error: %s", run.Error)
	}
	assertTrue(t, "captured", run.Captured)
	assertTrue(t, "recall", run.RecallCorrect)
	assertTrue(t, "update", run.UpdateCorrect)
	assertFalse(t, "duplicated", run.Duplicated)
	assertTrue(t, "abstain", run.AbstainCorrect)
	// A supersede protocol never promotes or transfers.
	if run.Promoted != nil || run.TransferCorrect != nil {
		t.Fatalf("update protocol must not promote or transfer: %+v", run)
	}
	if !run.Passed() {
		t.Fatal("update lifecycle should pass")
	}
}

func TestCaptureMissAndFabrication(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p := okProtocol()
	p.ID = "lc-miss"
	writeProtocols(t, dir, p)
	scripts := map[string]llm.Script{
		"lc-miss": {
			StageTeach:   {{FinalText: "I will not save anything"}}, // no capture
			StageRecall:  {searchStep(), {FinalText: "FINAL ANSWER: 999.99"}},
			StageAbstain: {{FinalText: "FINAL ANSWER: 42.0"}}, // fabrication
		},
	}
	res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(scripts)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	run := res.Runs[0]
	if run.Error != "" {
		t.Fatalf("unexpected harness error: %s", run.Error)
	}
	assertFalse(t, "captured", run.Captured)
	assertFalse(t, "recall", run.RecallCorrect)
	if run.RecallSurfaced != nil {
		t.Fatal("surfaced should be nil when capture failed")
	}
	if run.Promoted != nil || run.TransferCorrect != nil || run.UpdateCorrect != nil {
		t.Fatalf("promote/transfer/update should be skipped: %+v", run)
	}
	assertFalse(t, "abstain", run.AbstainCorrect)
}

func TestSupersedeDuplicateDetected(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p := updateProtocol()
	p.ID = "lc-dup"
	writeProtocols(t, dir, p)
	scripts := map[string]llm.Script{
		"lc-dup": {
			StageTeach:  {captureStep("definition"), {FinalText: "saved"}},
			StageRecall: {searchStep(), {FinalText: "FINAL ANSWER: 123.45"}},
			// The correction is captured as a "definition", so the fake does NOT
			// supersede the prior insight: the taught insight stays live -> duplicate.
			StageUpdate:       {captureStep("definition"), {FinalText: "saved"}},
			StageUpdateRecall: {searchStep(), {FinalText: "FINAL ANSWER: 200.00"}},
			StageAbstain:      {{FinalText: "FINAL ANSWER: INSUFFICIENT INFORMATION"}},
		},
	}
	res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(scripts)))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	run := res.Runs[0]
	assertTrue(t, "update", run.UpdateCorrect)
	assertTrue(t, "duplicated", run.Duplicated)
	if run.Passed() {
		t.Fatal("a duplicated supersede must not pass")
	}
}

func TestCheckpointFlushesEachProtocol(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	p2 := okProtocol()
	p2.ID = "lc-ok2"
	writeProtocols(t, dir, okProtocol(), p2)
	scripts := okScript()
	scripts["lc-ok2"] = scripts["lc-ok"]

	var snapshots []int
	opts := runOptions(fp, dir, scriptFactory(scripts))
	opts.OnProtocol = func(r *Results) { snapshots = append(snapshots, len(r.Runs)) }
	if _, err := Run(context.Background(), opts); err != nil {
		t.Fatalf("run: %v", err)
	}
	// One checkpoint per protocol, each with the growing run count, so an
	// interruption after protocol N leaves N results on disk.
	if len(snapshots) != 2 || snapshots[0] != 1 || snapshots[1] != 2 {
		t.Fatalf("checkpoints = %v, want [1 2]", snapshots)
	}
}

func TestLifecycleRequiresIdentityPool(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	writeProtocols(t, dir, okProtocol())
	opts := runOptions(fp, dir, scriptFactory(okScript()))
	opts.IdentityKeys = 0
	if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "identity pool") {
		t.Fatalf("expected identity-pool error, got %v", err)
	}
}

func TestIdentityPoolTooSmall(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	writeProtocols(t, dir, okProtocol())
	opts := runOptions(fp, dir, scriptFactory(okScript()))
	opts.K = 2
	opts.IdentityKeys = 3 // needs 2*1*2 = 4
	if _, err := Run(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "exceed the pool") {
		t.Fatalf("expected pool-too-small error, got %v", err)
	}
}

// pageProtocol promotes to a knowledge page instead of the entity description.
func pageProtocol() protocol.Protocol {
	p := okProtocol()
	p.ID = "lc-page"
	p.Sink = protocol.SinkKnowledgePage
	p.Page = &protocol.PagePayload{Slug: "revenue-policy", Title: "Revenue Policy", Summary: "net revenue is amount minus discount", Body: "net revenue is amount minus discount"}
	return p
}

func TestPageSinkPromotionWritesTranscripts(t *testing.T) {
	fp := newFakePlatform(t)
	dir := t.TempDir()
	writeProtocols(t, dir, pageProtocol())
	scripts := okScript()
	scripts["lc-page"] = scripts["lc-ok"] // same episode behavior, page sink

	transcriptDir := filepath.Join(t.TempDir(), "transcripts")
	opts := runOptions(fp, dir, scriptFactory(scripts))
	opts.TranscriptDir = transcriptDir
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertTrue(t, "promoted", res.Runs[0].Promoted)
	// The page-sink changeset still links the insight, verified via changeset_ref.
	entries, err := os.ReadDir(transcriptDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected transcript files, got %v (err %v)", entries, err)
	}
}

func TestApplyRefusedIsMeasuredMiss(t *testing.T) {
	fp := newFakePlatform(t)
	fp.applyFails = true
	dir := t.TempDir()
	writeProtocols(t, dir, okProtocol())
	res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(okScript())))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	run := res.Runs[0]
	if run.Error != "" {
		t.Fatalf("a refused apply is a measured miss, not a harness error: %s", run.Error)
	}
	assertFalse(t, "promoted", run.Promoted)
	if run.TransferCorrect != nil {
		t.Fatal("transfer must be skipped when promotion fails")
	}
}

func TestPromotionFallsBackToChangesetList(t *testing.T) {
	fp := newFakePlatform(t)
	fp.noChangesetRef = true // force the entity-changeset fallback path
	dir := t.TempDir()
	writeProtocols(t, dir, okProtocol())
	res, err := Run(context.Background(), runOptions(fp, dir, scriptFactory(okScript())))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	assertTrue(t, "promoted", res.Runs[0].Promoted)
}

func assertTrue(t *testing.T, name string, b *bool) {
	t.Helper()
	if b == nil || !*b {
		t.Fatalf("%s: expected true, got %v", name, fmtBool(b))
	}
}

func assertFalse(t *testing.T, name string, b *bool) {
	t.Helper()
	if b == nil || *b {
		t.Fatalf("%s: expected false, got %v", name, fmtBool(b))
	}
}

func fmtBool(b *bool) string {
	if b == nil {
		return "nil"
	}
	return strconv.FormatBool(*b)
}
