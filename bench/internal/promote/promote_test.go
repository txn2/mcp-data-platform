package promote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
)

func TestBuildApplyArgsDataHub(t *testing.T) {
	args := BuildApplyArgs(Target{
		Label: "lc-x", EntityURN: "urn:x", Sink: protocol.SinkDataHub, Fact: "amounts are cents",
	}, "ins-1")
	if args["sink"] != "datahub" || args["entity_urn"] != "urn:x" || args["confirm"] != true {
		t.Fatalf("datahub args wrong: %+v", args)
	}
	if ids, ok := args["insight_ids"].([]string); !ok || len(ids) != 1 || ids[0] != "ins-1" {
		t.Fatalf("insight_ids wrong: %+v", args["insight_ids"])
	}
	changes, ok := args["changes"].([]map[string]any)
	if !ok || len(changes) != 1 || changes[0]["change_type"] != "update_description" || changes[0]["detail"] != "amounts are cents" {
		t.Fatalf("datahub changes wrong: %+v", args["changes"])
	}
	if _, hasPage := args["page"]; hasPage {
		t.Error("datahub sink must not carry a page payload")
	}
}

func TestBuildApplyArgsKnowledgePage(t *testing.T) {
	args := BuildApplyArgs(Target{
		Label: "lc-y", EntityURN: "urn:y", Sink: protocol.SinkKnowledgePage,
		Page: &protocol.PagePayload{Slug: "s", Title: "T", Summary: "the fact in one line", Body: "B"},
	}, "ins-2")
	if args["sink"] != "knowledge_page" {
		t.Fatalf("page sink wrong: %+v", args)
	}
	page, ok := args["page"].(map[string]any)
	if !ok || page["slug"] != "s" || page["title"] != "T" || page["body"] != "B" {
		t.Fatalf("page payload wrong: %+v", args["page"])
	}
	// The summary must be sent: search renders a page hit as title plus summary,
	// and on tool surfaces without a page-body fetch it is the only channel the
	// promoted fact reaches an agent through.
	if page["summary"] != "the fact in one line" {
		t.Errorf("page summary not sent: %+v", args["page"])
	}
	if _, hasChanges := args["changes"]; hasChanges {
		t.Error("page sink must not carry datahub changes")
	}
}

// TestWaitForInsight covers the newest-of-many pick, the pending status filter
// passed through, and the timeout (nil, not error) when nothing lands.
func TestWaitForInsight(t *testing.T) {
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	var gotStatus string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStatus = r.URL.Query().Get("status")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []lifecycleapi.Insight{
				{ID: "old", CreatedAt: older, Status: "pending"},
				{ID: "new", CreatedAt: newer, Status: "pending"},
			},
			"total": 2,
		})
	}))
	defer srv.Close()
	life := lifecycleapi.New(srv.URL, srv.Client())

	got, err := WaitForInsight(context.Background(), life, "a@b", "urn:x", time.Time{}, time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got == nil || got.ID != "new" {
		t.Fatalf("expected newest insight 'new', got %+v", got)
	}
	if gotStatus != StatusPending {
		t.Errorf("capture verify must filter on pending status, got %q", gotStatus)
	}
}

// TestWaitForInsightSinceBoundsToThisRun proves the since bound excludes a
// pending insight left by an earlier run (same deterministic identity and URN)
// while a fresh capture is matched. The fake applies the `since` param exactly
// as the admin API does (created_at >=), so the test exercises both the param
// emission and the resulting match window.
func TestWaitForInsightSinceBoundsToThisRun(t *testing.T) {
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	fresh := stale.Add(2 * time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out []lifecycleapi.Insight
		since, err := time.Parse(time.RFC3339, r.URL.Query().Get("since"))
		if err != nil {
			t.Errorf("since param missing or not RFC 3339: %v", err)
		}
		for _, in := range []lifecycleapi.Insight{
			{ID: "stale", CreatedAt: stale, Status: "pending"},
			{ID: "fresh", CreatedAt: fresh, Status: "pending"},
		} {
			if !in.CreatedAt.Before(since) {
				out = append(out, in)
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out, "total": len(out)})
	}))
	defer srv.Close()
	life := lifecycleapi.New(srv.URL, srv.Client())

	got, err := WaitForInsight(context.Background(), life, "a@b", "urn:x", stale.Add(time.Hour), time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if got == nil || got.ID != "fresh" {
		t.Fatalf("expected only the fresh insight to match, got %+v", got)
	}

	// A window after both insights matches nothing: the stale leftover cannot
	// fake a capture (nil, nil is the measured-miss contract).
	got, err = WaitForInsight(context.Background(), life, "a@b", "urn:x", fresh.Add(time.Hour), 20*time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatalf("a missed capture must not be an error: %v", err)
	}
	if got != nil {
		t.Fatalf("stale insight matched despite the since bound: %+v", got)
	}
}

func TestWaitForInsightTimeoutIsMiss(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []lifecycleapi.Insight{}, "total": 0})
	}))
	defer srv.Close()
	life := lifecycleapi.New(srv.URL, srv.Client())

	got, err := WaitForInsight(context.Background(), life, "a@b", "urn:x", time.Time{}, 20*time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatalf("a missed capture must not be an error: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for no insight, got %+v", got)
	}
}

// TestReviewerVerify exercises the three verification outcomes: applied with a
// changeset_ref that sources the insight (pass), not-yet-applied (fail), and the
// fallback that lists the entity's changesets when the ref is unset.
func TestReviewerVerify(t *testing.T) {
	cases := []struct {
		name    string
		insight lifecycleapi.Insight
		handler http.HandlerFunc
		want    bool
	}{
		{
			name:    "applied with sourcing changeset ref",
			insight: lifecycleapi.Insight{ID: "i1", Status: StatusApplied, ChangesetRef: "cs1"},
			want:    true,
		},
		{
			name:    "not yet applied",
			insight: lifecycleapi.Insight{ID: "i2", Status: "approved"},
			want:    false,
		},
		{
			name:    "applied, ref unset, found by entity fallback",
			insight: lifecycleapi.Insight{ID: "i3", Status: StatusApplied},
			want:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(r.URL.Path, "/api/v1/admin/knowledge/insights/"):
					_ = json.NewEncoder(w).Encode(tc.insight)
				case r.URL.Path == "/api/v1/admin/knowledge/changesets/cs1":
					_ = json.NewEncoder(w).Encode(lifecycleapi.Changeset{ID: "cs1", SourceInsightIDs: []string{tc.insight.ID}})
				case r.URL.Path == "/api/v1/admin/knowledge/changesets":
					_ = json.NewEncoder(w).Encode(map[string]any{
						"data":  []lifecycleapi.Changeset{{ID: "cs9", SourceInsightIDs: []string{tc.insight.ID}}},
						"total": 1,
					})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			r := Reviewer{Life: lifecycleapi.New(srv.URL, srv.Client())}
			got, err := r.verify(context.Background(), Target{EntityURN: "urn:x"}, tc.insight.ID)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if got != tc.want {
				t.Errorf("verify = %v, want %v", got, tc.want)
			}
		})
	}
}

// applyFake is a minimal platform: an MCP server exposing platform_info +
// apply_knowledge over streamable HTTP, plus the admin insight/changeset REST
// the Reviewer verifies against. It lets Apply be tested end to end (approve ->
// apply_knowledge -> API verify) through genuine protocol wiring.
type applyFake struct {
	mu       sync.Mutex
	approved map[string]bool
	// applyError makes apply_knowledge return a plain tool error (a measured
	// refusal); applyRefusalCode additionally attaches the platform error
	// contract's structured code (a pre-audit refusal, e.g. session_expired).
	applyError       bool
	applyRefusalCode string
	srv              *httptest.Server
}

func newApplyFake(t *testing.T, applyError bool) *applyFake {
	t.Helper()
	f := &applyFake{approved: map[string]bool{}, applyError: applyError}
	server := mcp.NewServer(&mcp.Implementation{Name: "apply-fake", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "platform_info", Description: "orientation"},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			payload := map[string]any{"session_id": "dps_test", "version": "fake-1.0.0"}
			raw, _ := json.Marshal(payload)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}, StructuredContent: payload}, nil, nil
		})
	schema := &jsonschema.Schema{Type: "object", Properties: map[string]*jsonschema.Schema{
		"session_id": {Type: "string"}, "action": {Type: "string"}, "entity_urn": {Type: "string"},
	}, Required: []string{"session_id"}}
	mcp.AddTool(server, &mcp.Tool{Name: ApplyToolName, Description: "promote", InputSchema: schema},
		func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
			if f.applyRefusalCode != "" {
				return &mcp.CallToolResult{
					IsError:           true,
					Content:           []mcp.Content{&mcp.TextContent{Text: "SESSION_EXPIRED: mint a new handle"}},
					StructuredContent: map[string]any{"error": map[string]any{"code": f.applyRefusalCode}},
				}, nil, nil
			}
			if f.applyError {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "write refused"}}}, nil, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "applied"}}}, nil, nil
		})
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("PUT /api/v1/admin/knowledge/insights/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.approved[r.PathValue("id")] = true
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("GET /api/v1/admin/knowledge/insights/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		// The insight reports applied only once approved and applied without error.
		status := "approved"
		f.mu.Lock()
		if f.approved[id] && !f.applyError {
			status = StatusApplied
		}
		f.mu.Unlock()
		_ = json.NewEncoder(w).Encode(lifecycleapi.Insight{ID: id, Status: status, ChangesetRef: "cs1"})
	})
	mux.HandleFunc("GET /api/v1/admin/knowledge/changesets/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(lifecycleapi.Changeset{ID: r.PathValue("id"), SourceInsightIDs: []string{"ins-1"}})
	})
	mux.Handle("/", mcpHandler)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func TestReviewerApply(t *testing.T) {
	cases := []struct {
		name       string
		applyError bool
		want       bool
	}{
		{name: "promotes and verifies", applyError: false, want: true},
		{name: "apply tool error is a measured miss", applyError: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newApplyFake(t, tc.applyError)
			ctx := context.Background()
			session, err := mcpc.New(f.srv.URL, f.srv.Client()).Connect(ctx)
			if err != nil {
				t.Fatalf("connect: %v", err)
			}
			defer func() { _ = session.Close() }()
			info, err := mcpc.Mint(ctx, session)
			if err != nil {
				t.Fatalf("mint: %v", err)
			}
			r := Reviewer{Life: lifecycleapi.New(f.srv.URL, f.srv.Client())}
			got, err := r.Apply(ctx, session, info.Handle, Target{Label: "cs-x", EntityURN: "urn:x", Sink: protocol.SinkDataHub, Fact: "f"}, "ins-1")
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			if got != tc.want {
				t.Errorf("Apply = %v, want %v", got, tc.want)
			}
			if !f.approved["ins-1"] {
				t.Error("Apply must approve the insight before applying")
			}
		})
	}
}

// TestReviewerApplyPreAuditRefusalIsHarnessError proves a platform refusal
// issued outer to the audit middleware (session_expired, rate_limited, ...) on
// the admin session is a harness error, not a measured miss: scoring it as a
// refusal would silently flatline the promote metric on a harness defect.
func TestReviewerApplyPreAuditRefusalIsHarnessError(t *testing.T) {
	f := newApplyFake(t, false)
	f.applyRefusalCode = "session_expired"
	ctx := context.Background()
	session, err := mcpc.New(f.srv.URL, f.srv.Client()).Connect(ctx)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = session.Close() }()
	info, err := mcpc.Mint(ctx, session)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	r := Reviewer{Life: lifecycleapi.New(f.srv.URL, f.srv.Client())}
	got, err := r.Apply(ctx, session, info.Handle, Target{Label: "cs-x", EntityURN: "urn:x", Sink: protocol.SinkDataHub, Fact: "f"}, "ins-1")
	if err == nil {
		t.Fatal("a pre-audit platform refusal must be a harness error, not a measured miss")
	}
	if got {
		t.Error("a refused apply must not report promoted")
	}
	if !strings.Contains(err.Error(), "session_expired") {
		t.Errorf("error should carry the refusal code, got: %v", err)
	}
}

// TestReviewerVerifyRolledBackFails ensures a rolled-back changeset does not
// count as a live promotion.
func TestReviewerVerifyRolledBackFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/changesets/cs1") {
			_ = json.NewEncoder(w).Encode(lifecycleapi.Changeset{ID: "cs1", SourceInsightIDs: []string{"i1"}, RolledBack: true})
			return
		}
		_ = json.NewEncoder(w).Encode(lifecycleapi.Insight{ID: "i1", Status: StatusApplied, ChangesetRef: "cs1"})
	}))
	defer srv.Close()
	r := Reviewer{Life: lifecycleapi.New(srv.URL, srv.Client())}
	got, err := r.verify(context.Background(), Target{EntityURN: "urn:x"}, "i1")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got {
		t.Error("a rolled-back changeset must not count as promoted")
	}
}
