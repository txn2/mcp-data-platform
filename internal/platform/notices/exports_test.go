package notices

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/calltask"
)

type fakeExports struct {
	tasks []calltask.Task
	err   error
	asked string
}

func (f *fakeExports) EndedBackground(_ context.Context, userID string, _ time.Time, _ int) ([]calltask.Task, error) {
	f.asked = userID
	return f.tasks, f.err
}

// The briefing names each background export that ended since the watermark,
// with the key that reads it again (#2212).
func TestBuildReportsEndedBackgroundExports(t *testing.T) {
	ended := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	exports := &fakeExports{tasks: []calltask.Task{{
		ID: "dpt_1", ToolName: "graphql_export", Status: "completed", IdempotencyKey: "k1",
		StatusMessage: "exported 3 pages", UpdatedAt: ended,
	}}}
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, &fakeMarks{mark: &testMark})
	h.exports = exports

	digest := h.Build(context.Background(), testCaller())
	require.NotNil(t, digest)
	assert.Equal(t, callerID, exports.asked)
	assert.Equal(t, []ExportNotice{{
		Tool: "graphql_export", RunID: "dpt_1", Status: "completed", IdempotencyKey: "k1",
		Message: "exported 3 pages", EndedAt: "2026-08-10T12:00:00Z",
	}}, digest.Exports())
}

// A failed read leaves the exports out and does not fail the briefing.
func TestBuildWithoutTheExportsItCouldNotRead(t *testing.T) {
	h := testHandle(&fakeAssets{}, &fakeShares{}, &fakeThreads{}, &fakeMarks{mark: &testMark})
	h.exports = &fakeExports{err: errors.New("db down")}
	// Nothing else to report either, so there is no briefing.
	assert.Empty(t, h.Build(context.Background(), testCaller()).Exports())

	var none *Digest
	assert.Nil(t, none.Exports())
}
