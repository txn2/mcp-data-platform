package callrun

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/platform/callhandler"
)

// TestRecentCalls reads the requester's calls of the request's operation in
// the last day, each with its item count and what came of it.
func TestRecentCalls(t *testing.T) {
	w := newWorld()
	w.add("p", callhandler.KindPolicy, holdSrc, func(h *callhandler.Handler) { h.ItemsPath = "subscribers" })
	e := w.evaluator(t)
	e.Shapes = func(string) Shapes { return methodShapes{} }
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	req := &approval.Request{
		SubjectKind: approval.SubjectCall, RequesterEmail: "jo@example.com",
		CallTool: "api_invoke_endpoint", CallConnection: "billing-api", CallOperation: "addSubscribers",
	}
	at := time.Date(2026, 11, 13, 15, 41, 0, 0, time.UTC)
	body := `{"connection":"billing-api","method":"POST","path":"/lists/7","body":{"subscribers":[1,2,3,4,5]}}`
	mock.ExpectQuery(regexp.QuoteMeta("FROM audit_logs")).WithArgs("jo@example.com", "billing-api", "api_invoke_endpoint", sqlmock.AnyArg(), recentLimit).
		WillReturnRows(sqlmock.NewRows([]string{"timestamp", "parameters", "success", "handlers"}).
			AddRow(at, []byte(body), false, []byte(`[{"outcome":"require_approval"}]`)).
			AddRow(at.Add(-time.Hour), []byte(body), true, []byte(`[{"outcome":"require_approval"},{"outcome":"allowance"}]`)).
			AddRow(at.Add(-2*time.Hour), []byte(body), false, []byte(`[{"outcome":"deny"}]`)).
			AddRow(at.Add(-3*time.Hour), []byte(body), false, []byte(`[]`)).
			AddRow(at.Add(-4*time.Hour), []byte(body), true, []byte(`[]`)).
			AddRow(at.Add(-5*time.Hour), []byte(`{"connection":"billing-api","method":"DELETE","path":"/lists/7"}`), true, []byte(`[]`)))
	got, err := e.RecentCalls(context.Background(), db, req)
	require.NoError(t, err)
	require.Len(t, got, 5, "a call of another operation is left out")
	assert.Equal(t, RecentCall{At: at, Items: 5, Outcome: RecentHeld}, got[0])
	assert.Equal(t, []string{RecentHeld, RecentAllowance, RecentRefused, RecentFailed, RecentRan},
		[]string{got[0].Outcome, got[1].Outcome, got[2].Outcome, got[3].Outcome, got[4].Outcome})

	mock.ExpectQuery("FROM audit_logs").WillReturnError(errors.New("down"))
	_, err = e.RecentCalls(context.Background(), db, req)
	require.Error(t, err)
	mock.ExpectQuery("FROM audit_logs").WillReturnRows(sqlmock.NewRows([]string{"timestamp"}).AddRow(at))
	_, err = e.RecentCalls(context.Background(), db, req)
	require.Error(t, err)

	none, err := e.RecentCalls(context.Background(), db, &approval.Request{SubjectKind: approval.SubjectScriptVersion})
	require.NoError(t, err)
	assert.Equal(t, []RecentCall{}, none, "a request that is not a call's has none, never null")
}

// methodShapes reads a DELETE as deleteList and anything else as
// addSubscribers.
type methodShapes struct{}

func (methodShapes) CallShape(_ context.Context, _ string, args map[string]any) Shape {
	if args["method"] == "DELETE" {
		return Shape{Operation: "deleteList", Method: "DELETE", Path: "/lists/7", Params: map[string]string{}}
	}
	return Shape{Operation: "addSubscribers", Method: "POST", Path: "/lists/7", Params: map[string]string{}}
}
