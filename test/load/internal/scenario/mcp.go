package scenario

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/test/load/internal/harness"
	"github.com/txn2/mcp-data-platform/test/load/internal/mcpc"
	"github.com/txn2/mcp-data-platform/test/load/internal/report"
)

// searchIntent and seedSQL drive the MCP tool-call scenarios against the E2E
// compose stack (memory.e2e_test.test_orders is seeded by
// test/e2e/init/trino/setup.sql). search satisfies the search-first gate and is
// itself audited, so it doubles as the audited call for audit-burst and soak.
const (
	searchIntent = "orders"
	seedSQL      = "SELECT order_id, status FROM memory.e2e_test.test_orders LIMIT 5"
)

// sessionWorker holds one long-lived MCP session and reconnects it after a
// transport failure. Reused by the tool-call, audit-burst, and soak scenarios,
// which differ only in which tools they call per iteration.
type sessionWorker struct {
	env  *harness.Env
	sess *mcp.ClientSession
}

func (w *sessionWorker) ensure(ctx context.Context) error {
	if w.sess != nil {
		return nil
	}
	s, err := w.env.MCP.Connect(ctx)
	if err != nil {
		return err
	}
	w.sess = s
	return nil
}

// call records one tool call under op, including any reconnect cost on the
// iteration where a prior transport error dropped the session.
func (w *sessionWorker) call(ctx context.Context, op, tool string, args map[string]any) {
	_ = w.env.Timed(op, func() error {
		if err := w.ensure(ctx); err != nil {
			return err
		}
		res := mcpc.Call(ctx, w.sess, tool, args)
		if res.TransportErr != nil {
			_ = w.sess.Close()
			w.sess = nil
		}
		return res.Err()
	})
}

func (w *sessionWorker) Close() {
	if w.sess != nil {
		_ = w.sess.Close()
	}
}

// noopSetup / noopTeardown are shared by scenarios with no one-time work.
type noopSetup struct{}

func (noopSetup) Setup(context.Context, *harness.Env) error { return nil }
func (noopSetup) Teardown(context.Context, *harness.Env)    {}

// --- mcp-tool-call ---

// mcpToolCall issues an authenticated search followed by a trino_query per
// iteration on a persistent session — the platform's primary hot path
// (auth, authz, search-first gate, enrichment, audit).
type mcpToolCall struct{ noopSetup }

func (*mcpToolCall) Name() string { return "mcp-tool-call" }
func (*mcpToolCall) Description() string {
	return "authenticated MCP sessions issuing search then trino_query"
}

func (*mcpToolCall) Defaults() harness.RunDefaults {
	return harness.RunDefaults{Concurrency: 16, Duration: 30 * time.Second, Warmup: 5 * time.Second}
}

func (s *mcpToolCall) NewWorker(_ context.Context, env *harness.Env, _ int) (harness.Worker, error) {
	return &toolCallWorker{sessionWorker: sessionWorker{env: env}}, nil
}

func (*mcpToolCall) Assess(_ *harness.Env, rep *report.Report) []report.Assertion {
	return []report.Assertion{
		errorRateAssertion(rep, "search", 0.02),
		errorRateAssertion(rep, "trino_query", 0.02),
	}
}

type toolCallWorker struct{ sessionWorker }

func (w *toolCallWorker) Iterate(ctx context.Context) {
	w.call(ctx, "search", "search", map[string]any{"intent": searchIntent})
	w.call(ctx, "trino_query", "trino_query", map[string]any{"sql": seedSQL})
}

// --- audit-burst ---

// auditBurst hammers the audited search tool at high concurrency to push past
// the async audit queue (default cap 1024) and move audit_events_dropped_total.
// Run once in async delivery (expect drops) and once in sync delivery (expect
// no drops, higher latency) to record the documented loss model.
type auditBurst struct{ noopSetup }

func (*auditBurst) Name() string { return "audit-burst" }
func (*auditBurst) Description() string {
	return "high-concurrency audited tool calls sized past the async audit queue"
}

func (*auditBurst) Defaults() harness.RunDefaults {
	return harness.RunDefaults{Concurrency: 64, Duration: 20 * time.Second, Warmup: 3 * time.Second}
}

func (s *auditBurst) NewWorker(_ context.Context, env *harness.Env, _ int) (harness.Worker, error) {
	return &searchWorker{sessionWorker: sessionWorker{env: env}}, nil
}

func (*auditBurst) Assess(_ *harness.Env, rep *report.Report) []report.Assertion {
	return []report.Assertion{
		errorRateAssertion(rep, "search", 0.05),
		counterDeltaInfo(rep, "audit_events_dropped_total"),
	}
}

// --- soak ---

// soak holds a fixed moderate request rate for the configured duration and
// asserts flat goroutine and RSS at the end. The specified soak is 1 hour; the
// default duration here is 15m and is overridden by --duration (pass 1h for the
// full spec run).
type soak struct{ noopSetup }

func (*soak) Name() string        { return "soak" }
func (*soak) Description() string { return "fixed moderate rate; asserts flat memory and goroutines" }
func (*soak) Defaults() harness.RunDefaults {
	return harness.RunDefaults{Concurrency: 8, Duration: 15 * time.Minute, Warmup: 30 * time.Second, RatePerSec: 5}
}

func (s *soak) NewWorker(_ context.Context, env *harness.Env, _ int) (harness.Worker, error) {
	return &searchWorker{sessionWorker: sessionWorker{env: env}}, nil
}

func (*soak) Assess(_ *harness.Env, rep *report.Report) []report.Assertion {
	return []report.Assertion{
		stabilityAssertion(rep, "go_goroutines", 0.50),
		stabilityAssertion(rep, "process_resident_memory_bytes", 0.25),
		errorRateAssertion(rep, "search", 0.02),
	}
}

// searchWorker issues one audited search per iteration on a persistent session.
type searchWorker struct{ sessionWorker }

func (w *searchWorker) Iterate(ctx context.Context) {
	w.call(ctx, "search", "search", map[string]any{"intent": searchIntent})
}

// --- mcp-session-churn ---

// mcpSessionChurn connects a fresh MCP session (full initialize handshake),
// makes one cheap call, and tears it down every iteration — pressuring the
// session store's create/destroy path rather than steady tool throughput.
// platform_info is used because it is exempt from the search-first gate and the
// rate limiter, isolating session lifecycle cost.
type mcpSessionChurn struct{ noopSetup }

func (*mcpSessionChurn) Name() string { return "mcp-session-churn" }
func (*mcpSessionChurn) Description() string {
	return "initialize/teardown-heavy load on the session store"
}

func (*mcpSessionChurn) Defaults() harness.RunDefaults {
	return harness.RunDefaults{Concurrency: 16, Duration: 30 * time.Second, Warmup: 5 * time.Second}
}

func (s *mcpSessionChurn) NewWorker(_ context.Context, env *harness.Env, _ int) (harness.Worker, error) {
	return &churnWorker{env: env}, nil
}

func (*mcpSessionChurn) Assess(_ *harness.Env, rep *report.Report) []report.Assertion {
	return []report.Assertion{errorRateAssertion(rep, "session_connect", 0.02)}
}

type churnWorker struct{ env *harness.Env }

func (w *churnWorker) Iterate(ctx context.Context) {
	var sess *mcp.ClientSession
	err := w.env.Timed("session_connect", func() error {
		s, cerr := w.env.MCP.Connect(ctx)
		if cerr != nil {
			return cerr
		}
		sess = s
		return nil
	})
	if err != nil {
		return
	}
	w.call(ctx, sess)
	_ = sess.Close()
}

func (w *churnWorker) call(ctx context.Context, sess *mcp.ClientSession) {
	_ = w.env.Timed("platform_info", func() error {
		return mcpc.Call(ctx, sess, "platform_info", map[string]any{}).Err()
	})
}

func (w *churnWorker) Close() {}
