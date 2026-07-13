package pipeline

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/txn2/mcp-data-platform/bench/internal/report"
	"github.com/txn2/mcp-data-platform/bench/internal/task"
)

// fakeExec is an in-memory SQLExecutor that classifies queries into transport
// failures, SQL failures, and successful results, for testing gradeExecSQL's
// error handling without a live warehouse.
type fakeExec struct {
	rows        map[string][]map[string]any
	transportOn string
	sqlErrOn    string
}

func (f *fakeExec) Exec(_ context.Context, sql string) ([]map[string]any, error) {
	switch sql {
	case f.transportOn:
		return nil, &TransportError{Err: errors.New("network down")}
	case f.sqlErrOn:
		return nil, errors.New("trino_query error: line 1: syntax error")
	default:
		return f.rows[sql], nil
	}
}

func (f *fakeExec) Close() error { return nil }

func gradeExecFixture(exec SQLExecutor) *runEnv {
	return &runEnv{sql: exec, opts: Options{Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}}
}

// TestGradeExecSQLErrorClassification verifies the asymmetry fix: a transport
// blip on the candidate query is a harness failure (excluded from accuracy),
// while a genuine SQL error is the agent's graded miss.
func TestGradeExecSQLErrorClassification(t *testing.T) {
	env := gradeExecFixture(&fakeExec{
		rows:        map[string][]map[string]any{"SELECT 1": {{"n": 1.0}}},
		transportOn: "SELECT transport",
		sqlErrOn:    "SELECT bad",
	})
	tk := task.Task{ID: "t1", ExpectedSQL: "SELECT 1", Grading: task.Grading{Kind: task.GradeExecSQL}}
	log := env.opts.Log

	// Candidate transport error -> harness failure, not a wrong answer.
	transportA := &report.Attempt{FinalAnswer: "SELECT transport"}
	env.gradeExecSQL(context.Background(), transportA, tk, log)
	if transportA.Error == "" || transportA.Correct {
		t.Errorf("transport error: got Error=%q Correct=%v, want harness failure", transportA.Error, transportA.Correct)
	}

	// Candidate SQL error -> graded miss (agent's fault), no harness error.
	sqlErrA := &report.Attempt{FinalAnswer: "SELECT bad"}
	env.gradeExecSQL(context.Background(), sqlErrA, tk, log)
	if sqlErrA.Error != "" || sqlErrA.Correct {
		t.Errorf("sql error: got Error=%q Correct=%v, want graded miss", sqlErrA.Error, sqlErrA.Correct)
	}

	// Candidate equal to the reference -> correct.
	okA := &report.Attempt{FinalAnswer: "SELECT 1"}
	env.gradeExecSQL(context.Background(), okA, tk, log)
	if okA.Error != "" || !okA.Correct {
		t.Errorf("correct query: got Error=%q Correct=%v, want correct", okA.Error, okA.Correct)
	}

	// A non-SQL final answer -> graded miss without touching the executor.
	proseA := &report.Attempt{FinalAnswer: "the orders table"}
	env.gradeExecSQL(context.Background(), proseA, tk, log)
	if proseA.Error != "" || proseA.Correct {
		t.Errorf("prose answer: got Error=%q Correct=%v, want graded miss", proseA.Error, proseA.Correct)
	}
}

// TestFormatInstruction verifies each grading kind gets its own answer-format
// rule — in particular exec_sql must NOT receive the entity ("name a table")
// instruction, which would steer the model away from emitting SQL.
func TestFormatInstruction(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{task.GradeNumeric, numericFormat},
		{task.GradeEntity, entityFormat},
		{task.GradeExecSQL, sqlFormat},
	}
	for _, c := range cases {
		if got := formatInstruction(task.Task{Grading: task.Grading{Kind: c.kind}}); got != c.want {
			t.Errorf("formatInstruction(%s) returned the wrong rule", c.kind)
		}
	}
}
