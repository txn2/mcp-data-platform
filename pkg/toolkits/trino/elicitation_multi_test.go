package trino

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/txn2/mcp-data-platform/pkg/observability"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// explainRows is the estimate the stub coordinator's EXPLAIN IO reports.
const explainRows = "5000000"

// explainingStub is a coordinator that answers an EXPLAIN with an IO plan
// estimating explainRows rows, and any other statement with one row. It
// speaks the protocol the driver drives: the POST is queued with a nextUri,
// and the GET of that uri carries the columns, the rows and the finished
// state.
func explainingStub(t *testing.T) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	explain := map[string]bool{}
	n := 0
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("POST /v1/statement", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		n++
		id := fmt.Sprintf("q%d", n)
		explain[id] = strings.HasPrefix(strings.ToUpper(strings.TrimSpace(string(body))), "EXPLAIN")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"%s","infoUri":"%s/ui/%s","nextUri":"%s/v1/statement/%s/1","stats":{"state":"QUEUED"}}`,
			id, srv.URL, id, srv.URL, id)
	})
	mux.HandleFunc("GET /v1/statement/{id}/1", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		mu.Lock()
		isExplain := explain[id]
		mu.Unlock()
		columns := `[{"name":"_col0","type":"integer","typeSignature":{"rawType":"integer","arguments":[]}}]`
		data := `[[1]]`
		if isExplain {
			columns = `[{"name":"Query Plan","type":"varchar","typeSignature":{"rawType":"varchar","arguments":[]}}]`
			// What Trino answers EXPLAIN (TYPE IO) with: JSON, one entry per
			// table read, its estimate in outputRowCount.
			data = `[["{\"inputTableColumnInfos\":[{\"table\":{\"catalog\":\"hive\"},\"estimate\":{\"outputRowCount\":` +
				explainRows + `.0}}],\"estimate\":{\"outputRowCount\":` + explainRows + `.0}}"]]`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"%s","infoUri":"%s/ui/%s","columns":%s,"data":%s,"stats":{"state":"FINISHED"}}`,
			id, srv.URL, id, columns, data)
	})
	return srv
}

// elicitRig is the platform's assembly in miniature: NewMulti, the toolkit's
// tools on a real MCP server with its consent layer installed inside a
// tool_call span, and a client that declares elicitation and records every
// prompt it is shown. The client speaks the latest revision, so it is asked
// through input requests and answers by retrying the call (SEP-2322).
type elicitRig struct {
	tk      *Toolkit
	metrics *observability.Metrics
	spans   *tracetest.SpanRecorder
	session *mcp.ClientSession

	mu      sync.Mutex
	prompts []string
	answer  string
}

func newElicitRig(t *testing.T, instances map[string]Config) *elicitRig {
	t.Helper()
	return newElicitRigAt(t, instances, "")
}

// newElicitRigAt is newElicitRig with the client speaking protocolVersion,
// "" for the latest.
func newElicitRigAt(t *testing.T, instances map[string]Config, protocolVersion string) *elicitRig {
	t.Helper()
	tk, err := NewMulti(MultiConfig{DefaultConnection: "warehouse", Instances: instances})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tk.Close() })

	m, err := observability.New(observability.Config{Enabled: true})
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	tk.SetMetrics(m)

	sr := tracetest.NewSpanRecorder()
	tr := observability.NewTracerFromProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr)), observability.TracingConfig{Enabled: true})

	server := mcp.NewServer(&mcp.Implementation{Name: "trino-test", Version: "v0"}, nil)
	// The consent layer, inner to the span the platform's tracing opens.
	server.AddReceivingMiddleware(tk.ConsentMiddleware())
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}
			ctx, span := tr.Start(ctx, "tool_call")
			defer span.End()
			return next(ctx, method, req)
		}
	})
	tk.RegisterTools(server)

	r := &elicitRig{tk: tk, metrics: m, spans: sr, answer: "decline"}
	st, ct := mcp.NewInMemoryTransports()
	_, err = server.Connect(context.Background(), st, nil)
	require.NoError(t, err)
	client := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "v0"}, &mcp.ClientOptions{
		ElicitationHandler: func(_ context.Context, req *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.prompts = append(r.prompts, req.Params.Message)
			return &mcp.ElicitResult{Action: r.answer}, nil
		},
	})
	sess, err := client.Connect(context.Background(), ct, &mcp.ClientSessionOptions{ProtocolVersion: protocolVersion})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	r.session = sess
	return r
}

func (r *elicitRig) query(t *testing.T, connection string) *mcp.CallToolResult {
	t.Helper()
	args := map[string]any{"sql": "SELECT * FROM hive.sales.orders"}
	if connection != "" {
		args["connection"] = connection
	}
	res, err := r.session.CallTool(context.Background(), &mcp.CallToolParams{Name: string(trinotools.ToolQuery), Arguments: args})
	require.NoError(t, err)
	return res
}

func (r *elicitRig) shown() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.prompts...)
}

func costPrompting(threshold int64) ElicitationConfig {
	return ElicitationConfig{Enabled: true, CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: threshold}}
}

// TestNewMulti_PromptsForAQueryOverTheThreshold is #2052's first criterion:
// the toolkit the platform builds installs the elicitation middleware, the
// estimate's EXPLAIN runs on the connection the call names, and a declined
// prompt stops the query.
func TestNewMulti_PromptsForAQueryOverTheThreshold(t *testing.T) {
	srv := explainingStub(t)
	host, port := hostPortOf(t, srv)
	off := false
	r := newElicitRig(t, map[string]Config{
		"warehouse": {Host: host, Port: port, User: "u", SSL: &off, Elicitation: costPrompting(1000)},
		"lake":      {Host: host, Port: port, User: "u", SSL: &off},
	})

	res := r.query(t, "warehouse")
	require.True(t, res.IsError, "a declined prompt must stop the query: %v", res.Content)
	prompts := r.shown()
	require.Len(t, prompts, 1)
	assert.Contains(t, prompts[0], "5,000,000 rows")

	// A connection whose settings leave the prompt off runs without one.
	res = r.query(t, "lake")
	assert.False(t, res.IsError, "%v", res.Content)
	assert.Len(t, r.shown(), 1, "the lake connection prompted")

	// Accepting lets the query run.
	r.answer = "accept"
	res = r.query(t, "")
	assert.False(t, res.IsError, "an accepted prompt must let the query run: %v", res.Content)
	assert.Len(t, r.shown(), 2, "a call naming no connection runs on, and prompts for, the default")
}

// TestNewMulti_TheEstimateIsMeasured is #2052's third criterion: the EXPLAIN
// the estimate sends is a statement the platform issues, so it is counted
// under query_kind="explain" and has a trino.explain span under the call's.
func TestNewMulti_TheEstimateIsMeasured(t *testing.T) {
	srv := explainingStub(t)
	host, port := hostPortOf(t, srv)
	off := false
	r := newElicitRig(t, map[string]Config{
		"warehouse": {Host: host, Port: port, User: "u", SSL: &off, Elicitation: costPrompting(1000)},
	})
	r.answer = "accept"
	require.False(t, r.query(t, "warehouse").IsError)

	body := scrapeMetrics(t, r.metrics)
	assert.Contains(t, body, `trino_queries_total{query_kind="explain",status="ok"} 1`)
	var explain sdktrace.ReadOnlySpan
	for _, s := range r.spans.Ended() {
		if s.Name() == spanPrefix+kindExplain {
			explain = s
		}
	}
	require.NotNil(t, explain, "no trino.explain span")
	assert.Equal(t, "tool_call", parentName(r.spans.Ended(), explain))
	assert.Equal(t, "warehouse", spanAttr(explain, attrConnection))
	assert.Equal(t, "SELECT hive.sales.orders", spanAttr(explain, attrDBSummary))
}

// A connection added at run time takes its own elicitation block when it has
// one, and the default connection's (the platform's settings) when it has
// none; a removed connection's settings go with it.
func TestNewMulti_AddedConnectionsCarryElicitation(t *testing.T) {
	srv := explainingStub(t)
	host, port := hostPortOf(t, srv)
	off := false
	r := newElicitRig(t, map[string]Config{
		"warehouse": {Host: host, Port: port, User: "u", SSL: &off, Elicitation: costPrompting(1000)},
	})
	base := map[string]any{"host": host, "port": port, "user": "u", "ssl": false}
	inherit := map[string]any{}
	own := map[string]any{"elicitation": map[string]any{"enabled": false}}
	for k, v := range base {
		inherit[k], own[k] = v, v
	}
	require.NoError(t, r.tk.AddConnection("inherits", inherit))
	require.NoError(t, r.tk.AddConnection("opted-out", own))

	assert.True(t, r.query(t, "inherits").IsError)
	assert.Len(t, r.shown(), 1, "a connection with no block of its own takes the default's")
	assert.False(t, r.query(t, "opted-out").IsError)
	assert.Len(t, r.shown(), 1, "a connection's own block wins")

	require.NoError(t, r.tk.RemoveConnection("inherits"))
	assert.False(t, r.tk.elicitation.configFor("inherits").Enabled)
}

// scrapeMetrics reads the recorder's exposition.
func scrapeMetrics(t *testing.T, m *observability.Metrics) string {
	t.Helper()
	return (&telemetryRig{metrics: m}).scrape(t)
}

// TestNewMulti_AsksConsentForAPIIColumn is #2052's second criterion: a query
// reading a column the semantic layer tags PII asks for consent, in the same
// round as a cost prompt when both are owed, and a declined consent stops it.
func TestNewMulti_AsksConsentForAPIIColumn(t *testing.T) {
	srv := explainingStub(t)
	host, port := hostPortOf(t, srv)
	off := false
	both := ElicitationConfig{
		Enabled:        true,
		CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: 1000},
		PIIConsent:     PIIConsentConfig{Enabled: true},
	}
	piiOnly := ElicitationConfig{Enabled: true, PIIConsent: PIIConsentConfig{Enabled: true}}
	r := newElicitRig(t, map[string]Config{
		"warehouse": {Host: host, Port: port, User: "u", SSL: &off, Elicitation: piiOnly},
		"both":      {Host: host, Port: port, User: "u", SSL: &off, Elicitation: both},
	})
	r.tk.SetSemanticProvider(&mockSemanticProvider{columns: map[string]map[string]*semantic.ColumnContext{
		"hive.sales.orders": {"email": {IsPII: true}, "total": {}},
	}})

	res := r.query(t, "warehouse")
	require.True(t, res.IsError, "a declined consent must stop the query: %v", res.Content)
	prompts := r.shown()
	require.Len(t, prompts, 1)
	assert.Contains(t, prompts[0], "1 PII column(s)")
	assert.Contains(t, firstTextBlock(res), "PII access not authorized")

	require.True(t, r.query(t, "both").IsError)
	assert.Len(t, r.shown(), 3, "a statement owed both prompts is asked both in one round")
}

// A client on a revision before 2026-07-28 is asked in band, while the call
// waits, as the platform always asked; the answer decides the call the same
// way.
func TestNewMulti_AnOlderClientIsAskedInBand(t *testing.T) {
	srv := explainingStub(t)
	host, port := hostPortOf(t, srv)
	off := false
	r := newElicitRigAt(t, map[string]Config{
		"warehouse": {Host: host, Port: port, User: "u", SSL: &off, Elicitation: costPrompting(1000)},
	}, "2025-11-25")
	require.Equal(t, "2025-11-25", r.session.InitializeResult().ProtocolVersion)

	res := r.query(t, "warehouse")
	require.True(t, res.IsError, "%v", res.Content)
	require.Len(t, r.shown(), 1)
	assert.Contains(t, firstTextBlock(res), "estimated row count was not confirmed")

	r.answer = "accept"
	assert.False(t, r.query(t, "warehouse").IsError)
}

// taggedTables is a semantic provider that tags whole tables PII and no
// column, the way a catalog commonly tags a dataset holding personal data.
type taggedTables struct{ mockSemanticProvider }

func (*taggedTables) GetTableContext(_ context.Context, t semantic.TableIdentifier) (*semantic.TableContext, error) {
	if t.String() == "warehouse.public.customers" {
		return &semantic.TableContext{Tags: []string{"DimensionTable", "PII"}}, nil
	}
	return &semantic.TableContext{}, nil
}

// A table the catalog tags PII is asked about though none of its columns is
// tagged (#2052): column tags alone never asked about such a table.
func TestNewMulti_AsksConsentForATableTaggedPII(t *testing.T) {
	srv := explainingStub(t)
	host, port := hostPortOf(t, srv)
	off := false
	r := newElicitRig(t, map[string]Config{
		"warehouse": {
			Host: host, Port: port, User: "u", SSL: &off,
			Elicitation: ElicitationConfig{Enabled: true, PIIConsent: PIIConsentConfig{Enabled: true}},
		},
	})
	r.tk.SetSemanticProvider(&taggedTables{})

	res, err := r.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      string(trinotools.ToolQuery),
		Arguments: map[string]any{"sql": "SELECT email FROM warehouse.public.customers"},
	})
	require.NoError(t, err)
	require.True(t, res.IsError)
	require.Len(t, r.shown(), 1)
	assert.Contains(t, r.shown()[0], "1 table(s) tagged PII")

	// A table the catalog does not tag runs without a prompt.
	res, err = r.session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      string(trinotools.ToolQuery),
		Arguments: map[string]any{"sql": "SELECT region FROM warehouse.public.regions"},
	})
	require.NoError(t, err)
	assert.False(t, res.IsError, "%v", res.Content)
	assert.Len(t, r.shown(), 1)
}
