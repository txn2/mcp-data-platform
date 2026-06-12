package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
)

// fakeLinker is a configurable ThreadLinker for capture_insight tests.
type fakeLinker struct {
	gotThreadIDs []string
	gotInsightID string
	linkedReturn []string
	err          error
}

func (f *fakeLinker) LinkInsight(_ context.Context, threadIDs []string, insightID, _, _ string) ([]string, error) {
	f.gotThreadIDs = threadIDs
	f.gotInsightID = insightID
	if f.err != nil {
		return nil, f.err
	}
	return f.linkedReturn, f.err
}

func captureOutput(t *testing.T, res *mcp.CallToolResult) captureInsightOutput {
	t.Helper()
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	var out captureInsightOutput
	require.NoError(t, json.Unmarshal([]byte(tc.Text), &out))
	return out
}

func TestCaptureInsight_ThreadLink(t *testing.T) {
	t.Run("links and reports unlinked thread ids", func(t *testing.T) {
		spy := &fullSpyStore{}
		tk, err := New(testName, spy)
		require.NoError(t, err)
		linker := &fakeLinker{linkedReturn: []string{"thr_1"}} // thr_2 not linked
		tk.SetThreadLinker(linker)

		res, _, callErr := tk.handleCaptureInsight(context.Background(), nil, captureInsightInput{
			Category:    testCategory,
			InsightText: testInsightText,
			ThreadIDs:   []string{"thr_1", "thr_2"},
		})
		require.Nil(t, callErr)
		require.False(t, res.IsError)
		require.Len(t, spy.Insights, 1)

		out := captureOutput(t, res)
		// The linker was called with the insight's own id.
		assert.Equal(t, out.InsightID, linker.gotInsightID)
		assert.Equal(t, []string{"thr_1", "thr_2"}, linker.gotThreadIDs)
		assert.Equal(t, 1, out.LinkedThreadCount)
		assert.Equal(t, []string{"thr_2"}, out.UnlinkedThreadIDs)
	})

	t.Run("no thread_ids leaves output unchanged (no regression)", func(t *testing.T) {
		spy := &fullSpyStore{}
		tk, err := New(testName, spy)
		require.NoError(t, err)
		linker := &fakeLinker{}
		tk.SetThreadLinker(linker)

		res, _, callErr := tk.handleCaptureInsight(context.Background(), nil, captureInsightInput{
			Category:    testCategory,
			InsightText: testInsightText,
		})
		require.Nil(t, callErr)
		require.False(t, res.IsError)

		// Linker not invoked; result carries no thread fields.
		assert.Nil(t, linker.gotThreadIDs)
		out := captureOutput(t, res)
		assert.Equal(t, 0, out.LinkedThreadCount)
		assert.Nil(t, out.UnlinkedThreadIDs)

		// And the raw JSON omits the thread fields entirely.
		tc := res.Content[0].(*mcp.TextContent) //nolint:errcheck // test assertion
		assert.NotContains(t, tc.Text, "linked_thread_count")
		assert.NotContains(t, tc.Text, "unlinked_thread_ids")
	})

	t.Run("link failure does not fail the capture; all reported unlinked", func(t *testing.T) {
		spy := &fullSpyStore{}
		tk, err := New(testName, spy)
		require.NoError(t, err)
		tk.SetThreadLinker(&fakeLinker{err: errors.New("db down")})

		res, _, callErr := tk.handleCaptureInsight(context.Background(), nil, captureInsightInput{
			Category:    testCategory,
			InsightText: testInsightText,
			ThreadIDs:   []string{"thr_1"},
		})
		require.Nil(t, callErr)
		require.False(t, res.IsError) // best-effort: insight still captured
		require.Len(t, spy.Insights, 1)

		out := captureOutput(t, res)
		assert.Equal(t, 0, out.LinkedThreadCount)
		assert.Equal(t, []string{"thr_1"}, out.UnlinkedThreadIDs)
	})

	t.Run("thread_ids supplied but no linker wired reports all unlinked", func(t *testing.T) {
		spy := &fullSpyStore{}
		tk, err := New(testName, spy)
		require.NoError(t, err)
		// No SetThreadLinker call.

		res, _, callErr := tk.handleCaptureInsight(context.Background(), nil, captureInsightInput{
			Category:    testCategory,
			InsightText: testInsightText,
			ThreadIDs:   []string{"thr_1", "thr_2"},
		})
		require.Nil(t, callErr)
		require.False(t, res.IsError)

		out := captureOutput(t, res)
		assert.Equal(t, 0, out.LinkedThreadCount)
		assert.Equal(t, []string{"thr_1", "thr_2"}, out.UnlinkedThreadIDs)
	})
}

func TestMissingFrom(t *testing.T) {
	assert.Nil(t, missingFrom(nil, []string{"a"}))
	assert.Equal(t, []string{"b"}, missingFrom([]string{"a", "b"}, []string{"a"}))
	assert.Nil(t, missingFrom([]string{"a"}, []string{"a"}))
}

func TestInsightActor(t *testing.T) {
	id, email := insightActor(nil)
	assert.Equal(t, "", id)
	assert.Equal(t, "", email)

	id, email = insightActor(&middleware.PlatformContext{UserID: "u1", UserEmail: "u1@example.com"})
	assert.Equal(t, "u1", id)
	assert.Equal(t, "u1@example.com", email)
}
