//go:build integration

package acceptance

// Issue #2052: the Trino toolkit's cost-estimation and PII-consent prompts
// never ran. elicitation is on by default, but the middleware holding both was
// built only by the single-connection constructor the platform never called.
//
// What this holds, against the running platform, the dev stack's Trino and the
// catalog the stack reads: a trino_query whose EXPLAIN estimates more rows than
// elicitation.cost_estimation.row_threshold (the default 1,000,000) is asked
// about, and declining it stops the query; a trino_query reading a table the
// catalog tags PII is asked for consent; accepting runs the query; the
// estimate's EXPLAIN is counted as trino_queries_total{query_kind="explain"}.
// The client speaks the latest revision (2026-07-28), so the prompts arrive as
// input requests and the client answers by retrying the call. A client pinned
// to 2025-11-25 cannot be asked on this stateless deployment and is not
// refused for it.
//
// Wire forms: trino_query's `sql` and `connection` are typed string and admit
// that one JSON form; each is sent as a literal tools/call param. The answers
// travel as inputResponses on the retried call, which the SDK client builds.

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	issue2052Purpose = "Acceptance for #2052: the cost and PII prompts run before a Trino query."
	// issue2052Writer is the dev connection that accepts writes, used to build
	// the large table; issue2052Reader is the read-only one queries go to.
	issue2052Writer = "acme-scratch"
	issue2052Reader = "acme"
	// issue2052PII is a table the catalog the dev stack reads tags PII on the
	// dataset rather than on any column.
	issue2052PII = "SELECT email FROM warehouse.public.customers"
)

// issue2052Client is a session whose client declares elicitation and answers
// every prompt with answer, recording the message of each.
type issue2052Client struct {
	*client
	mu      sync.Mutex
	asked   []string
	answer  string
	version string
}

func connect2052(t *testing.T, protocolVersion string) *issue2052Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	t.Cleanup(cancel)
	ec := &issue2052Client{answer: "decline", version: protocolVersion}
	httpClient := &http.Client{Transport: authRoundTripper{key: devAPIKey(), base: http.DefaultTransport}}
	mc := mcp.NewClient(&mcp.Implementation{Name: "acceptance-2052", Version: "1.0.0"}, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			ec.mu.Lock()
			defer ec.mu.Unlock()
			ec.asked = append(ec.asked, req.Params.Message)
			return &mcp.ElicitResult{Action: ec.answer}, nil
		},
	})
	session, err := mc.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: baseURL(), HTTPClient: httpClient},
		&mcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
	if err != nil {
		t.Fatalf("no platform answers at %s (%v); start one with `make dev`", baseURL(), err)
	}
	t.Cleanup(func() { _ = session.Close() })
	c := &client{t: t, ctx: ctx, session: session, base: baseURL(), apiKey: devAPIKey()}
	info := c.call("platform_info", nil)
	c.info = info
	c.sessionID, _ = info["session_id"].(string)
	c.call("search", map[string]any{
		"intent": "acceptance suite discovery", "limit": 1,
		"purpose": "The acceptance suite performs the discovery the search-first gate requires.",
	})
	ec.client = c
	return ec
}

func (ec *issue2052Client) prompts() []string {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return append([]string(nil), ec.asked...)
}

func (ec *issue2052Client) setAnswer(a string) {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	ec.answer = a
}

func (ec *issue2052Client) query(t *testing.T, sql string) (*mcp.CallToolResult, string) {
	t.Helper()
	res, text, err := ec.callRaw("trino_query", map[string]any{
		"connection": issue2052Reader, "sql": sql, "purpose": issue2052Purpose,
	})
	if err != nil {
		t.Fatalf("trino_query: transport error: %v", err)
	}
	return res, text
}

// issue2052Large builds a memory-catalog table of 1,100,000 rows on the dev
// Trino, whose EXPLAIN IO estimates more rows than the default threshold.
func issue2052Large(t *testing.T, c *client) string {
	t.Helper()
	table := fmt.Sprintf("memory.default.acc_2052_%d", time.Now().UnixNano())
	res, text, err := c.callRaw("trino_execute", map[string]any{
		"connection": issue2052Writer, "purpose": issue2052Purpose,
		"sql": "CREATE TABLE " + table + " AS SELECT a.x * 10000 + b.y AS n " +
			"FROM UNNEST(sequence(1, 1100)) a(x) CROSS JOIN UNNEST(sequence(1, 1000)) b(y)",
	})
	if err != nil || res.IsError {
		t.Fatalf("creating %s: %v %s", table, err, text)
	}
	t.Cleanup(func() {
		_, _, _ = c.callRaw("trino_execute", map[string]any{
			"connection": issue2052Writer, "purpose": issue2052Purpose, "sql": "DROP TABLE IF EXISTS " + table,
		})
	})
	return table
}

// issue2052ExplainCount reads trino_queries_total{query_kind="explain",status="ok"}
// summed over every replica's metrics endpoint.
func issue2052ExplainCount(t *testing.T) float64 {
	t.Helper()
	var total float64
	for line := range strings.SplitSeq(scrapeRaw(t), "\n") {
		if strings.HasPrefix(line, `trino_queries_total{`) && strings.Contains(line, `query_kind="explain"`) && strings.Contains(line, `status="ok"`) {
			var v float64
			if _, err := fmt.Sscanf(line[strings.LastIndex(line, " ")+1:], "%g", &v); err == nil {
				total += v
			}
		}
	}
	return total
}

// TestIssue2052_AQueryOverTheThresholdIsAskedAbout: the estimate exceeds the
// threshold, the client is asked with the estimate in the message, declining
// stops the query, accepting runs it, and the EXPLAIN is counted.
func TestIssue2052_AQueryOverTheThresholdIsAskedAbout(t *testing.T) {
	ec := connect2052(t, "")
	table := issue2052Large(t, ec.client)
	before := issue2052ExplainCount(t)

	res, text := ec.query(t, "SELECT n FROM "+table)
	asked := ec.prompts()
	if len(asked) != 1 || !strings.Contains(asked[0], "rows (threshold: 1,000,000)") {
		t.Fatalf("the client was asked %q; want one cost prompt naming the threshold", asked)
	}
	if !res.IsError || !strings.Contains(text, "estimated row count was not confirmed") {
		t.Fatalf("a declined prompt did not stop the query: %s", text)
	}

	ec.setAnswer("accept")
	res, text = ec.query(t, "SELECT n FROM "+table)
	if res.IsError {
		t.Fatalf("an accepted prompt did not run the query: %s", text)
	}
	if len(ec.prompts()) != 2 {
		t.Errorf("prompts = %q; want a second one for the accepted run", ec.prompts())
	}
	if after := issue2052ExplainCount(t); after < before+2 {
		t.Errorf(`trino_queries_total{query_kind="explain",status="ok"} went from %g to %g; want at least two more`, before, after)
	}
}

// TestIssue2052_ATableTaggedPIIAsksForConsent: the catalog tags the dataset
// PII, the client is asked for consent, and declining stops the query before
// anything is sent to Trino.
func TestIssue2052_ATableTaggedPIIAsksForConsent(t *testing.T) {
	ec := connect2052(t, "")
	res, text := ec.query(t, issue2052PII)
	asked := ec.prompts()
	if len(asked) != 1 || !strings.Contains(asked[0], "tagged PII") {
		t.Fatalf("the client was asked %q; want one PII consent prompt", asked)
	}
	if !res.IsError || !strings.Contains(text, "PII access not authorized") {
		t.Fatalf("a declined consent did not stop the query: %s", text)
	}
}

// TestIssue2052_AnOlderClientOnAStatelessDeploymentIsNotBlocked: the dev
// stack keeps sessions in the database, which runs the SDK stateless, and a
// stateless server rejects every server-to-client request. A client on
// 2025-11-25 takes no input requests either, so it cannot be prompted on this
// deployment shape; the prompt is skipped rather than refusing the call. (A
// deployment that keeps sessions in process asks such a client in band, which
// pkg/toolkits/trino's TestNewMulti_AnOlderClientIsAskedInBand holds.)
func TestIssue2052_AnOlderClientOnAStatelessDeploymentIsNotBlocked(t *testing.T) {
	ec := connect2052(t, "2025-11-25")
	if got := ec.session.InitializeResult().ProtocolVersion; got != "2025-11-25" {
		t.Fatalf("negotiated %s; the criterion needs a 2025-11-25 session", got)
	}
	_, text := ec.query(t, issue2052PII)
	if len(ec.prompts()) != 0 {
		t.Fatalf("the older client was asked %q on a stateless deployment", ec.prompts())
	}
	if strings.Contains(text, "PII access not authorized") {
		t.Fatalf("a prompt that could not be delivered refused the call: %s", text)
	}
}
