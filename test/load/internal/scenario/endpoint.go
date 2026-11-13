package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/test/load/internal/harness"
	"github.com/txn2/mcp-data-platform/test/load/internal/mcpc"
	"github.com/txn2/mcp-data-platform/test/load/internal/report"
)

// endpointOp is the operation the endpoint's requests are recorded under.
const endpointOp = "endpoint_request"

// endpointConnection is the application database connection the scenario's
// platform config names.
const endpointConnection = "load-target"

// EndpointBoundEnv names the p99 bound in milliseconds the scenario holds the
// endpoint to. Unset reports the percentiles and holds it to none.
const EndpointBoundEnv = "ENDPOINT_P99_BOUND_MS"

// endpointAppDB holds an authenticated endpoint at 200 requests a second, each
// running five statements in one read-only transaction on an application
// database with the statement cache on (#2544): the load
// TestIssue2402_AnEndpointHolds200RequestsASecondWithTheCacheOn drives, on a
// runner the stack has to itself, where a latency bound means something.
type endpointAppDB struct {
	path string
}

func (*endpointAppDB) Name() string { return "endpoint-appdb" }
func (*endpointAppDB) Description() string {
	return "an authenticated endpoint at 200/s, five statements each on an application database"
}

func (*endpointAppDB) Defaults() harness.RunDefaults {
	return harness.RunDefaults{Concurrency: 64, Duration: 30 * time.Second, Warmup: 10 * time.Second, RatePerSec: 200}
}

// endpointHandler is the five statements; %[1]s is the schema, %[2]s the
// connection.
const endpointHandler = `def handle(req):
    """Runs five statements in one transaction."""
    def read(tx):
        """Runs the five reads."""
        tx.query("SELECT count(*) AS n FROM %[1]s.orders")
        tx.query("SELECT count(*) AS n FROM %[1]s.items")
        tx.query("SELECT n FROM %[1]s.counter WHERE id = :id", params = {"id": 1})
        tx.query("SELECT count(*) AS n FROM %[1]s.orders WHERE total > :t", params = {"t": "1.00"})
        tx.query("SELECT count(*) AS n FROM %[1]s.items WHERE qty > :q", params = {"q": 0})
        return 5
    return {"json": {"statements": platform.transaction("%[2]s", read, read_only = True)}}

def main():
    """Drafts one request."""
    platform.result(handle({"params": {}, "caller": None}))
`

// endpointMigrations build the tables the handler reads.
var endpointMigrations = []string{
	"CREATE TABLE orders (id int PRIMARY KEY, total numeric(10,2) NOT NULL)",
	"CREATE TABLE items (order_id int NOT NULL REFERENCES orders (id), sku text NOT NULL, qty int NOT NULL)",
	"CREATE TABLE counter (id int PRIMARY KEY, n int NOT NULL); INSERT INTO counter VALUES (1, 0)",
}

// Setup builds the application, its schema, the script and its binding.
func (s *endpointAppDB) Setup(ctx context.Context, env *harness.Env) error {
	session, err := env.MCP.Connect(ctx)
	if err != nil {
		return fmt.Errorf("endpoint-appdb setup: %w", err)
	}
	defer func() { _ = session.Close() }()
	run := strconv.FormatInt(time.Now().Unix(), 36)
	schema, name := "load_ep_"+run, "load-endpoint-"+run
	if err := s.buildSchema(ctx, session, name, schema); err != nil {
		return err
	}
	if err := saveHandler(ctx, session, name, fmt.Sprintf(endpointHandler, schema, endpointConnection)); err != nil {
		return err
	}
	if _, err := call(ctx, session, "manage_script", map[string]any{
		"command": "handler_set", "kind": "endpoint", "name": name, "endpoint": name, "methods": []string{"GET"}, "path": "/",
		"auth_mode": "authenticated", "callers": []string{"*"}, "rate_limit": 100000, "caller_rate_limit": 100000,
	}); err != nil {
		return err
	}
	s.path = "/endpoints/" + name + "/"
	return nil
}

func (*endpointAppDB) buildSchema(ctx context.Context, session *mcp.ClientSession, name, schema string) error {
	if _, err := call(ctx, session, "manage_application", map[string]any{
		"command": "create", "name": name, "description": "The endpoint-appdb load scenario's schema",
	}); err != nil {
		return err
	}
	if _, err := call(ctx, session, "manage_application", map[string]any{
		"command": "add_part", "application": name, "kind": "schema", "part_id": endpointConnection + "/" + schema,
	}); err != nil {
		return err
	}
	for i, up := range endpointMigrations {
		if _, err := call(ctx, session, "manage_schema", map[string]any{
			"command": "add", "connection": endpointConnection, "schema": schema,
			"version": i + 1, "name": fmt.Sprintf("m%d", i+1), "up": up,
		}); err != nil {
			return err
		}
	}
	_, err := call(ctx, session, "manage_schema", map[string]any{"command": "apply", "connection": endpointConnection, "schema": schema})
	return err
}

// saveHandler drafts source and saves it with a test replaying the draft,
// which the save gate requires of every script.
func saveHandler(ctx context.Context, session *mcp.ClientSession, name, source string) error {
	ran, err := call(ctx, session, "manage_script", map[string]any{"command": "run_draft", "name": name, "source": source})
	if err != nil {
		return err
	}
	recording, _ := ran["recording"].(string)
	if ran["status"] != "succeeded" || recording == "" {
		return fmt.Errorf("endpoint-appdb setup: the draft did not succeed: %v", ran)
	}
	result, err := json.Marshal(ran["result"])
	if err != nil {
		return fmt.Errorf("endpoint-appdb setup: %w", err)
	}
	test := "\ndef test_recorded_draft():\n    \"\"\"The recorded draft replays to what it produced.\"\"\"\n" +
		"    testing.replay(" + strconv.Quote(recording) + ")\n    main()\n    out = testing.outputs()\n" +
		"    assert.eq(out.result, json.decode(" + strconv.Quote(string(result)) + "))\n"
	_, err = call(ctx, session, "manage_script", map[string]any{"command": "create", "name": name, "source": source + test})
	return err
}

// call is one setup tool call, its JSON result, and its failure as an error.
func call(ctx context.Context, session *mcp.ClientSession, tool string, args map[string]any) (map[string]any, error) {
	res := mcpc.Call(ctx, session, tool, args)
	if err := res.Err(); err != nil {
		return nil, fmt.Errorf("endpoint-appdb setup: %s %v: %w", tool, args["command"], err)
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(res.Text), &out); err != nil {
		return nil, fmt.Errorf("endpoint-appdb setup: %s %v answered %q: %w", tool, args["command"], res.Text, err)
	}
	return out, nil
}

func (*endpointAppDB) Teardown(context.Context, *harness.Env) {}

func (s *endpointAppDB) NewWorker(_ context.Context, env *harness.Env, _ int) (harness.Worker, error) {
	return &endpointWorker{url: env.Target.URL(s.path), env: env}, nil
}

// Assess holds every request to success, always states the percentiles, and
// holds p99 to the bound the workflow states when it states one.
func (*endpointAppDB) Assess(_ *harness.Env, rep *report.Report) []report.Assertion {
	out := []report.Assertion{errorRateAssertion(rep, endpointOp, 0)}
	return append(out, latencyAssertion(rep, os.Getenv(EndpointBoundEnv)))
}

// latencyAssertion reads the endpoint's percentiles against bound, which is
// milliseconds or empty for none.
func latencyAssertion(rep *report.Report, bound string) report.Assertion {
	const name = "latency:" + endpointOp
	for _, o := range rep.Operations {
		if o.Operation != endpointOp {
			continue
		}
		msg := fmt.Sprintf("p50 %.1fms, p99 %.1fms over %d requests", o.P50Ms, o.P99Ms, o.Count)
		if strings.TrimSpace(bound) == "" {
			return report.Assertion{Name: name, Passed: true, Message: msg + "; no bound stated"}
		}
		limit, err := strconv.ParseFloat(strings.TrimSpace(bound), 64)
		if err != nil {
			return report.Assertion{Name: name, Message: fmt.Sprintf("%s is %q, not a number", EndpointBoundEnv, bound)}
		}
		return report.Assertion{Name: name, Passed: o.P99Ms <= limit, Message: fmt.Sprintf("%s; bound %.1fms", msg, limit)}
	}
	return report.Assertion{Name: name, Message: "no samples recorded for " + endpointOp}
}

// endpointWorker sends one request per iteration.
type endpointWorker struct {
	url string
	env *harness.Env
}

// errEndpointStatus is an answer other than 200.
var errEndpointStatus = errors.New("the endpoint did not answer 200")

// Iterate reads each response to its end before closing it, so the connection
// is reused (#2544): a response closed unread costs the next request a new
// connection.
func (w *endpointWorker) Iterate(ctx context.Context) {
	_ = w.env.Timed(endpointOp, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.url, http.NoBody)
		if err != nil {
			return fmt.Errorf("building the request: %w", err)
		}
		res, err := w.env.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("sending the request: %w", err)
		}
		defer func() { _ = res.Body.Close() }()
		if _, err := io.Copy(io.Discard, res.Body); err != nil {
			return fmt.Errorf("reading the response: %w", err)
		}
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("%w: %d", errEndpointStatus, res.StatusCode)
		}
		return nil
	})
}

func (*endpointWorker) Close() {}
