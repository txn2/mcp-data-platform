package portal

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platformfeedback"
	"github.com/txn2/mcp-data-platform/pkg/mcpcontext"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// fakePlatformFeedback records the post and caller it was handed and answers
// as told.
type fakePlatformFeedback struct {
	caller platformfeedback.Caller
	post   platformfeedback.Post
	res    platformfeedback.Result
	err    error
}

func (f *fakePlatformFeedback) Submit(_ context.Context, c platformfeedback.Caller, p platformfeedback.Post) (platformfeedback.Result, error) {
	f.caller, f.post = c, p
	return f.res, f.err
}

// TestPlatformFeedbackTopic is #2820 through manage_feedback: topic=platform
// posts the fields as the caller, its session and persona; the service's
// refusal reaches the agent with its code; a deployment without the service,
// a target other than standalone, and a topic it does not know are refused.
func TestPlatformFeedbackTopic(t *testing.T) {
	tk := threadToolkit(t, &fakeThreadStore{}, nil)
	in := manageFeedbackInput{
		Topic: topicPlatform, TargetType: threadTargetStandalone, TryingTo: "list shared assets",
		BlockedBy: "no filter", Tool: "manage_asset", Suggestion: "add scope", Calls: []string{"mcp:call:a1"},
	}
	ctx := middleware.WithPlatformContext(
		mcpcontext.WithCaller(context.Background(), mcpcontext.Caller{UserID: "u-1"}),
		&middleware.PlatformContext{SessionID: "dps_1", PersonaName: "analyst"},
	)

	res, _, _ := tk.handleCreateThread(ctx, in)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "not available on this deployment")

	pf := &fakePlatformFeedback{res: platformfeedback.Result{ID: "pfb_1", ReportCount: 1, Message: "Posted."}}
	tk.SetPlatformFeedback(pf)
	res, _, _ = tk.handleCreateThread(ctx, in)
	require.False(t, res.IsError, resultText(t, res))
	assert.Contains(t, resultText(t, res), `"id": "pfb_1"`)
	assert.Equal(t, platformfeedback.Caller{UserID: "u-1", Persona: "analyst", SessionID: "dps_1"}, pf.caller)
	assert.Equal(t, platformfeedback.Post{TryingTo: "list shared assets", BlockedBy: "no filter", Tool: "manage_asset", Suggestion: "add scope", Calls: []string{"mcp:call:a1"}}, pf.post)

	pf.err = &platformfeedback.RefusedError{Code: platformfeedback.CodeLimit, Msg: "this session has posted 5 times"}
	res, _, _ = tk.handleCreateThread(ctx, in)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "this session has posted 5 times")
	assert.Contains(t, resultText(t, res), platformfeedback.CodeLimit)

	pf.err = errors.New("database down")
	res, _, _ = tk.handleCreateThread(ctx, in)
	assert.True(t, res.IsError)
	assert.Contains(t, resultText(t, res), "posting platform feedback")

	other := in
	other.TargetType = "asset"
	res, _, _ = tk.handleCreateThread(ctx, other)
	assert.Contains(t, resultText(t, res), "target_type=standalone")

	other = in
	other.Topic = "billing"
	res, _, _ = tk.handleCreateThread(ctx, other)
	assert.Contains(t, resultText(t, res), "topic is platform, or omitted")
}
