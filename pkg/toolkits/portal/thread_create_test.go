package portal

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/selfcall"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/portal"
)

// portalStub stands in for the platform's own HTTP handler: it records the
// request a create sends and who it was served for, and answers as told.
type portalStub struct {
	method, path string
	caller       mcpcontext.Caller
	served       bool
	body         map[string]any
	status       int
	answer       string
}

func (p *portalStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.method, p.path = r.Method, r.URL.Path
	p.caller, p.served = selfcall.From(r.Context())
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &p.body)
	}
	w.WriteHeader(p.status)
	_, _ = io.WriteString(w, p.answer)
}

// callerCtx is a tool call authenticated as a viewer, as the tool-call
// middleware records it.
func callerCtx() context.Context {
	return mcpcontext.WithCaller(strangerCtx(), mcpcontext.Caller{UserID: otherID, Email: "stranger@example.com", Roles: []string{"analyst"}})
}

func createToolkit(t *testing.T, stub *portalStub) *Toolkit {
	t.Helper()
	tk := threadToolkit(t, &fakeThreadStore{}, nil)
	tk.SetSelfHandler(stub)
	return tk
}

// TestHandleCreateThread_OpensThroughThePortalAsTheCaller holds #2371: the
// thread is opened by POST /api/v1/portal/threads, served as the caller, with
// the tool's fields as the portal takes them, and the thread it answers with
// is returned.
func TestHandleCreateThread_OpensThroughThePortalAsTheCaller(t *testing.T) {
	stub := &portalStub{status: http.StatusCreated, answer: `{"id":"thr_1","target_type":"asset","asset_id":"asset_1","author_email":"stranger@example.com","visibility":"shared"}`}
	tk := createToolkit(t, stub)

	yes := true
	res, _, err := tk.handleManageFeedback(callerCtx(), nil, manageFeedbackInput{
		Action: fbActionCreate, AssetID: "asset_1", Body: "Is Q3 complete?", Kind: portal.ThreadKindQuestion,
		Title: "Q3", Visibility: "shared", Anchor: map[string]any{"line": 12.0}, TargetVersion: 3, RequiresResolution: &yes,
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "%v", decodeResult(t, res))

	assert.Equal(t, http.MethodPost, stub.method)
	assert.Equal(t, threadsRoute, stub.path)
	assert.True(t, stub.served, "the request was not marked as served for the caller")
	assert.Equal(t, otherID, stub.caller.UserID)
	assert.Equal(t, []string{"analyst"}, stub.caller.Roles)
	assert.Equal(t, map[string]any{
		"kind": "question", "target_type": "asset", "asset_id": "asset_1", "anchor": map[string]any{"line": 12.0},
		"target_version": 3.0, "title": "Q3", "requires_resolution": true, "body": "Is Q3 complete?", "visibility": "shared",
	}, stub.body)
	assert.Equal(t, "thr_1", decodeResult(t, res)["id"])
}

// TestHandleCreateThread_ReportsThePortalsRefusal holds #2371: a caller the
// portal refuses gets the portal's own words and status.
func TestHandleCreateThread_ReportsThePortalsRefusal(t *testing.T) {
	for _, tc := range []struct {
		answer string
		want   string
	}{
		{`{"type":"about:blank","title":"Forbidden","status":403,"detail":"access denied"}`, "the portal refused to open the thread: access denied (HTTP 403)"},
		{`{"title":"Forbidden","status":403}`, "the portal refused to open the thread: Forbidden (HTTP 403)"},
		{`not json`, "the portal refused to open the thread: not json (HTTP 403)"},
		{strings.Repeat("x", 400), "the portal refused to open the thread: " + strings.Repeat("x", 300) + "... (HTTP 403)"},
	} {
		tk := createToolkit(t, &portalStub{status: http.StatusForbidden, answer: tc.answer})
		res, _, _ := tk.handleCreateThread(callerCtx(), manageFeedbackInput{AssetID: "asset_9", Body: "x"})
		require.True(t, res.IsError)
		assert.Equal(t, tc.want, decodeResult(t, res)["error"])
	}
}

// TestHandleCreateThread_RefusesWhatThePortalCannotBeAsked covers the input a
// create refuses before the portal is asked, and a process with no HTTP API.
func TestHandleCreateThread_RefusesWhatThePortalCannotBeAsked(t *testing.T) {
	stub := &portalStub{status: http.StatusCreated, answer: `{}`}
	for _, tc := range []struct {
		name  string
		ctx   context.Context
		input manageFeedbackInput
		want  string
	}{
		{"no caller", strangerCtx(), manageFeedbackInput{AssetID: "a", Body: "x"}, "signed-in caller"},
		{"no body", callerCtx(), manageFeedbackInput{AssetID: "a", Body: "  "}, "body is required for the create action"},
		{"no target", callerCtx(), manageFeedbackInput{Body: "x"}, createScopeErr},
		{"two targets", callerCtx(), manageFeedbackInput{AssetID: "a", PromptID: "p", Body: "x"}, createScopeErr},
		{"standalone with an id", callerCtx(), manageFeedbackInput{TargetType: "standalone", AssetID: "a", Body: "x"}, createScopeErr},
		{"type and id disagree", callerCtx(), manageFeedbackInput{TargetType: "collection", AssetID: "a", Body: "x"}, createScopeErr},
		{"anchor not an object", callerCtx(), manageFeedbackInput{AssetID: "a", Body: "x", Anchor: "[1]"}, "anchor must be a JSON object"},
		{"anchor of another type", callerCtx(), manageFeedbackInput{AssetID: "a", Body: "x", Anchor: 4.0}, "anchor must be a JSON object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _, _ := createToolkit(t, stub).handleCreateThread(tc.ctx, tc.input)
			require.True(t, res.IsError)
			assert.Contains(t, decodeResult(t, res)["error"], tc.want)
		})
	}

	noHTTP := threadToolkit(t, &fakeThreadStore{}, nil)
	res, _, _ := noHTTP.handleCreateThread(callerCtx(), manageFeedbackInput{TargetType: "standalone", Body: "x"})
	require.True(t, res.IsError)
	assert.Contains(t, decodeResult(t, res)["error"], "HTTP 503")

	noThreads := New(Config{Name: "test", S3Bucket: "b"})
	res, _, _ = noThreads.handleCreateThread(callerCtx(), manageFeedbackInput{TargetType: "standalone", Body: "x"})
	assert.Equal(t, threadsUnavail, decodeResult(t, res)["error"])
}

// TestCreatePayload_EveryTargetAndAnchorForm sends each target the schema
// names, and the anchor as an object and as a string holding one.
func TestCreatePayload_EveryTargetAndAnchorForm(t *testing.T) {
	for _, tc := range []struct {
		input manageFeedbackInput
		want  string
	}{
		{manageFeedbackInput{AssetID: "a"}, "asset"},
		{manageFeedbackInput{CollectionID: "c"}, "collection"},
		{manageFeedbackInput{PromptID: "p"}, "prompt"},
		{manageFeedbackInput{KnowledgePageID: "k"}, "knowledge_page"},
		{manageFeedbackInput{TargetType: "knowledge_page", KnowledgePageID: "k"}, "knowledge_page"},
		{manageFeedbackInput{TargetType: "standalone"}, "standalone"},
	} {
		got, errText := createPayload(tc.input)
		assert.Empty(t, errText)
		assert.Equal(t, tc.want, got.TargetType)
		assert.Equal(t, portal.ThreadKindComment, got.Kind, "kind defaults to comment")
	}
	for _, anchor := range []any{map[string]any{"line": 7.0}, `{"line": 7}`} {
		got, errText := createPayload(manageFeedbackInput{AssetID: "a", Anchor: anchor})
		require.Empty(t, errText)
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(got.Anchor, &decoded))
		assert.Equal(t, map[string]any{"line": 7.0}, decoded)
	}
	got, _ := createPayload(manageFeedbackInput{AssetID: "a", Anchor: " "})
	assert.Nil(t, got.Anchor)
}

// TestLoadThread_TheAuthorReadsOnThePortalsTerms holds the decision #2371
// took: a caller the owner-or-editor rule does not reach may read and reply
// to a thread they authored when the portal lets them read it, and not when
// it does not; resolving stays with the owner, an editor or an admin.
func TestLoadThread_TheAuthorReadsOnThePortalsTerms(t *testing.T) {
	authored := &portal.Thread{ID: "thr_9", TargetType: "asset", AssetID: "asset_1", AuthorID: otherID}
	for _, tc := range []struct {
		name     string
		thread   *portal.Thread
		status   int
		moderate bool
		allowed  bool
	}{
		{"author, portal reads", authored, http.StatusOK, false, true},
		{"author, portal refuses", authored, http.StatusForbidden, false, false},
		{"author resolving", authored, http.StatusOK, true, false},
		{"not the author", &portal.Thread{ID: "thr_8", TargetType: "asset", AssetID: "asset_1", AuthorID: "someone"}, http.StatusOK, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &portalStub{status: tc.status, answer: `{}`}
			tk := threadToolkit(t, &fakeThreadStore{getResult: tc.thread}, nil)
			tk.SetSelfHandler(stub)
			thread, errRes := tk.loadThread(callerCtx(), tc.thread.ID, tc.moderate)
			assert.Equal(t, tc.allowed, thread != nil && errRes == nil)
			if tc.allowed {
				assert.Equal(t, http.MethodGet, stub.method)
				assert.Equal(t, threadsRoute+"/"+tc.thread.ID, stub.path)
			}
		})
	}
}

// TestReplyWithoutAThread_NamesCreate holds #2371's error: a reply with no
// thread_id names the action that starts one.
func TestReplyWithoutAThread_NamesCreate(t *testing.T) {
	tk := threadToolkit(t, &fakeThreadStore{}, nil)
	res, _, _ := tk.handleManageFeedback(callerCtx(), nil, manageFeedbackInput{Action: fbActionReply, AssetID: "asset_1", Body: "x"})
	require.True(t, res.IsError)
	assert.Contains(t, decodeResult(t, res)["error"], "use action=create")
}
