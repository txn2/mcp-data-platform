package pipeline

// API-connection study integration test (#1027): the REAL assembled loop —
// scripted adapter -> real MCP protocol against a b1-shaped fake platform
// whose api_invoke_endpoint proxies to the REAL fixture service -> audit
// read-back -> fixture-state grading -> retrieval and taxonomy analysis.
// Tasks and playback steps come from the real generator, so this also
// proves generator, fixture behavior, and grading agree end to end.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
	"github.com/txn2/mcp-data-platform/bench/internal/apisvc"
	"github.com/txn2/mcp-data-platform/bench/internal/auditapi"
	"github.com/txn2/mcp-data-platform/bench/internal/fixturectl"
	"github.com/txn2/mcp-data-platform/bench/internal/llm"
	"github.com/txn2/mcp-data-platform/bench/internal/report"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// studyTaskIDs are the generated tasks the integration run plays: one
// numeric lookup, one mutation, one irrelevance.
var studyTaskIDs = []string{"p1-order-amount", "p3-cancel-order", "p5-refund"}

// newStudyPlatform assembles the b1-shaped fake platform: platform_info,
// api_list_endpoints over the real tier-0 catalog, and
// api_invoke_endpoint proxying to the real fixture service.
func newStudyPlatform(t *testing.T, fp *fakePlatform, fixtureURL string) {
	t.Helper()
	catalog := apigen.BuildCatalog()
	server := mcp.NewServer(&mcp.Implementation{Name: "fake-b1", Version: "1.0.0"}, nil)
	fp.addPlatformInfo(server)
	addListEndpoints(fp, server, catalog)
	addInvokeEndpoint(t, fp, server, fixtureURL)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/admin/audit/events", fp.serveAudit)
	mux.Handle("/", mcpHandler)
	fp.httpSrv = httptest.NewServer(mux)
	t.Cleanup(fp.httpSrv.Close)
}

// sessionArgSchema declares the shared session_id property.
func sessionArgSchema(props map[string]*jsonschema.Schema) *jsonschema.Schema {
	props["session_id"] = &jsonschema.Schema{Type: "string", Description: "platform-injected session handle"}
	return &jsonschema.Schema{Type: "object", Properties: props}
}

// addListEndpoints serves ranked operations whose id or summary contains
// the query (a lexical-ish stand-in for the real ranking).
func addListEndpoints(fp *fakePlatform, server *mcp.Server, c *apigen.Catalog) {
	schema := sessionArgSchema(map[string]*jsonschema.Schema{
		"connection": {Type: "string"},
		"query":      {Type: "string"},
		"limit":      {Type: "integer"},
	})
	mcp.AddTool(server, &mcp.Tool{Name: "api_list_endpoints", Description: "search endpoints", InputSchema: schema},
		func(_ context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			sessionID, _ := args["session_id"].(string)
			query, _ := args["query"].(string)
			fp.record(auditapi.Event{
				Timestamp: time.Now().UTC(), SessionID: sessionID,
				ToolName: "api_list_endpoints", Success: true, EventKind: "mcp_tool_call",
			})
			var ops []map[string]any
			for _, op := range c.TierOperations(apigen.Tier0) {
				if strings.Contains(op.ID, query) || strings.Contains(op.Summary, query) {
					ops = append(ops, map[string]any{"operation_id": op.ID, "method": op.Method, "path": op.Path})
				}
			}
			raw, _ := json.Marshal(map[string]any{"operations": ops})
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
		})
}

// addInvokeEndpoint proxies method/path/query_params/body to the fixture
// service, the b1 invoke shape.
func addInvokeEndpoint(t *testing.T, fp *fakePlatform, server *mcp.Server, fixtureURL string) {
	t.Helper()
	schema := sessionArgSchema(map[string]*jsonschema.Schema{
		"connection":   {Type: "string"},
		"method":       {Type: "string"},
		"path":         {Type: "string"},
		"query_params": {Type: "object"},
		"body":         {Type: "object"},
	})
	mcp.AddTool(server, &mcp.Tool{Name: "api_invoke_endpoint", Description: "invoke endpoint", InputSchema: schema},
		func(ctx context.Context, _ *mcp.CallToolRequest, args map[string]any) (*mcp.CallToolResult, any, error) {
			sessionID, _ := args["session_id"].(string)
			method, _ := args["method"].(string)
			path, _ := args["path"].(string)
			text, isErr := proxyToFixture(ctx, fixtureURL, method, path, args)
			fp.record(auditapi.Event{
				Timestamp: time.Now().UTC(), SessionID: sessionID,
				ToolName: "api_invoke_endpoint", Success: !isErr, EventKind: "mcp_tool_call",
			})
			return &mcp.CallToolResult{IsError: isErr, Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
		})
}

// proxyToFixture issues the HTTP call the invoke tool describes.
func proxyToFixture(ctx context.Context, base, method, path string, args map[string]any) (string, bool) {
	q := url.Values{}
	if qp, ok := args["query_params"].(map[string]any); ok {
		for k, v := range qp {
			q.Set(k, fmt.Sprint(v))
		}
	}
	target := base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	var body io.Reader
	if b, ok := args["body"].(map[string]any); ok {
		raw, _ := json.Marshal(b)
		body = strings.NewReader(string(raw))
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), target, body)
	if err != nil {
		return "invoke build: " + err.Error(), true
	}
	req.Header.Set("X-API-Key", "fixture-key")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "invoke: " + err.Error(), true
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return string(raw), res.StatusCode >= 400
}

// writeStudyTasks materializes the selected generated tasks as a task dir.
func writeStudyTasks(t *testing.T, tasks []task.Task) string {
	t.Helper()
	dir := t.TempDir()
	for _, tk := range tasks {
		raw, err := yaml.Marshal(tk)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, tk.ID+".yaml"), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestAPIStudyPipeline runs the assembled study loop over generated tasks
// and their generated playback: every attempt grades correct through real
// wiring, retrieval is recorded, refusal grading records its path, and the
// per-attempt fixture reset clears the access log between attempts.
func TestAPIStudyPipeline(t *testing.T) {
	fixture := httptest.NewServer(apisvc.New(apisvc.Options{APIKey: "fixture-key"}))
	t.Cleanup(fixture.Close)

	fp := &fakePlatform{identities: map[string]bool{}}
	newStudyPlatform(t, fp, fixture.URL)

	all := apigen.Tasks(apigen.GenerateState(apigen.BuildCatalog()))
	script := apigen.ScriptedSmoke(all)
	var tasks []task.Task
	for _, tk := range all {
		for _, id := range studyTaskIDs {
			if tk.ID == id {
				tasks = append(tasks, tk)
			}
		}
	}
	if len(tasks) != len(studyTaskIDs) {
		t.Fatalf("selected %d tasks, want %d", len(tasks), len(studyTaskIDs))
	}

	fixtureClient := fixturectl.New(fixture.URL, "fixture-key", 10*time.Second)
	opts := Options{
		Target:       target.Target{BaseURL: fp.httpSrv.URL, Credential: "test-key"},
		HTTPTimeout:  10 * time.Second,
		Arm:          "b1-lex",
		K:            1,
		TasksDir:     writeStudyTasks(t, tasks),
		Factory:      func(tk task.Task) (llm.Adapter, error) { return llm.NewScripted(script[tk.ID]), nil },
		LLMProvider:  "scripted",
		AuditTimeout: 10 * time.Second,
		Fixture:      fixtureClient,
		Log:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	res, err := Run(context.Background(), opts)
	if err != nil {
		t.Fatalf("pipeline: %v", err)
	}
	byID := map[string]report.Attempt{}
	for _, a := range res.Attempts {
		byID[a.TaskID] = a
	}

	lookup := byID["p1-order-amount"]
	if !lookup.Correct || lookup.Error != "" {
		t.Errorf("p1-order-amount: correct=%v error=%q", lookup.Correct, lookup.Error)
	}
	if lookup.Retrieval == nil || !lookup.Retrieval.Hit {
		t.Errorf("p1-order-amount retrieval = %+v, want a hit", lookup.Retrieval)
	}

	mutation := byID["p3-cancel-order"]
	if !mutation.Correct || mutation.Error != "" {
		t.Errorf("p3-cancel-order: correct=%v error=%q detail=%q", mutation.Correct, mutation.Error, mutation.GradeDetail)
	}

	refusal := byID["p5-refund"]
	if !refusal.Correct || refusal.Error != "" {
		t.Errorf("p5-refund: correct=%v error=%q detail=%q", refusal.Correct, refusal.Error, refusal.GradeDetail)
	}
	if refusal.RefusalJudged == nil || *refusal.RefusalJudged {
		t.Errorf("p5-refund refusal_judged = %v, want recorded lexical fallback", refusal.RefusalJudged)
	}

	// The per-attempt reset cleared the log before each attempt, so only
	// the LAST attempt's traffic remains — and p5 makes no catalog calls.
	reqs, err := fixtureClient.Requests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 0 {
		t.Errorf("fixture log has %d entries after the p5 attempt; reset-per-attempt not applied", len(reqs))
	}
}
