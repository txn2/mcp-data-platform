package trino

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// mockElicitor implements elicitor for testing without a real MCP session.
type mockElicitor struct {
	elicitResult *mcp.ElicitResult
	elicitErr    error
	initParams   *mcp.InitializeParams
}

func (m *mockElicitor) Elicit(_ context.Context, _ *mcp.ElicitParams) (*mcp.ElicitResult, error) {
	return m.elicitResult, m.elicitErr
}

func (m *mockElicitor) InitializeParams() *mcp.InitializeParams {
	return m.initParams
}

// mockExplainer implements queryExplainer for testing without a real Trino client.
type mockExplainer struct {
	result *trinoclient.ExplainResult
	err    error
}

func (m *mockExplainer) Explain(_ context.Context, _ string, _ trinoclient.ExplainType) (*trinoclient.ExplainResult, error) {
	return m.result, m.err
}

// elicitCapableParams returns InitializeParams with elicitation capability set.
func elicitCapableParams() *mcp.InitializeParams {
	return &mcp.InitializeParams{
		Capabilities: &mcp.ClientCapabilities{
			Elicitation: &mcp.ElicitationCapabilities{},
		},
	}
}

// noElicitParams returns InitializeParams without elicitation capability.
func noElicitParams() *mcp.InitializeParams {
	return &mcp.InitializeParams{
		Capabilities: &mcp.ClientCapabilities{},
	}
}

// TestParseReturnedRows pins #2586: the estimate is the plan's own output,
// the top-level estimate, never a table's scan; an estimate Trino could not
// make is unknown.
func TestParseReturnedRows(t *testing.T) {
	tests := []struct {
		name  string
		plan  string
		want  int64
		known bool
	}{
		{
			// The ticket's statement: 1.4M rows read from one table, 60 returned.
			name: "an aggregate reads more than it returns",
			plan: `{"inputTableColumnInfos":[{"table":{"catalog":"pg"},"estimate":{"outputRowCount":1430361.5}}],"estimate":{"outputRowCount":60.0}}`,
			want: 60, known: true,
		},
		{
			name: "a large result",
			plan: `{"inputTableColumnInfos":[],"estimate":{"outputRowCount":2500000.0,"cpuCost":NaN}}`,
			want: 2_500_000, known: true,
		},
		{
			name: "an unknown output estimate",
			plan: `{"inputTableColumnInfos":[{"estimate":{"outputRowCount":1100000.0}}],"estimate":{"outputRowCount":NaN,"cpuCost":NaN}}`,
		},
		{
			name: "no top-level estimate",
			plan: `{"inputTableColumnInfos":[{"estimate":{"outputRowCount":1100000.0}}]}`,
		},
		{
			name: "a text plan is not read",
			plan: "Fragment 0 [SOURCE]\n    Estimates: {rows: 5000000 (47.68MB)}",
		},
		{name: "empty plan", plan: ""},
		{name: "zero rows", plan: `{"estimate":{"outputRowCount":0.0}}`, want: 0, known: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, known := parseReturnedRows(tt.plan)
			if got != tt.want || known != tt.known {
				t.Errorf("parseReturnedRows() = %d, %v; want %d, %v", got, known, tt.want, tt.known)
			}
		})
	}
}

func TestFormatRowCount(t *testing.T) {
	tests := []struct {
		n    int64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{1_000_000, "1,000,000"},
		{1_234_567, "1,234,567"},
		{10, "10"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := formatRowCount(tt.n)
			if got != tt.want {
				t.Errorf("formatRowCount(%d) = %q, want %q", tt.n, got, tt.want)
			}
		})
	}
}

func TestExtractTablesFromSQL(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []semantic.TableIdentifier
	}{
		{
			name: "three-part table",
			sql:  "SELECT * FROM catalog.schema.table1 ",
			want: []semantic.TableIdentifier{
				{Catalog: "catalog", Schema: "schema", Table: "table1"},
			},
		},
		{
			name: "two-part table",
			sql:  "SELECT * FROM schema.table1 ",
			want: []semantic.TableIdentifier{
				{Schema: "schema", Table: "table1"},
			},
		},
		{
			name: "join with multiple tables",
			sql:  "SELECT * FROM cat.s.t1 JOIN cat.s.t2 ON t1.id = t2.id ",
			want: []semantic.TableIdentifier{
				{Catalog: "cat", Schema: "s", Table: "t1"},
				{Catalog: "cat", Schema: "s", Table: "t2"},
			},
		},
		{
			name: "deduplicates same table",
			sql:  "SELECT * FROM s.t JOIN s.t ON 1=1 ",
			want: []semantic.TableIdentifier{
				{Schema: "s", Table: "t"},
			},
		},
		{
			name: "no tables",
			sql:  "SELECT 1",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTablesFromSQL(tt.sql)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d tables, want %d", len(got), len(tt.want))
			}
			for i, g := range got {
				if g != tt.want[i] {
					t.Errorf("table[%d] = %+v, want %+v", i, g, tt.want[i])
				}
			}
		})
	}
}

func TestElicitationDeclinedError(t *testing.T) {
	decErr := &ElicitationDeclinedError{Reason: "query declined"}

	if decErr.Error() != "query declined" {
		t.Errorf("Error() = %q, want %q", decErr.Error(), "query declined")
	}
	if decErr.ErrorCategory() != "user_declined" {
		t.Errorf("ErrorCategory() = %q, want %q", decErr.ErrorCategory(), "user_declined")
	}

	// Verify errors.As works
	var de *ElicitationDeclinedError
	if !errors.As(decErr, &de) {
		t.Fatal("errors.As should match ElicitationDeclinedError")
	}
}

// The gate lets through, untouched, every call it has nothing to ask about:
// another method, another tool, no statement, a connection with prompts off,
// a client that cannot be asked.
func TestElicitationMiddleware_PassesThroughWhatItDoesNotGate(t *testing.T) {
	em := &ElicitationMiddleware{configs: map[string]ElicitationConfig{"w": costPrompting(100)}, defaultConn: "w"}
	reached := 0
	h := em.Middleware()(func(context.Context, string, mcp.Request) (mcp.Result, error) {
		reached++
		return &mcp.CallToolResult{}, nil
	})
	call := func(name, args string) *mcp.CallToolRequest {
		return &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: name, Arguments: json.RawMessage(args)}}
	}
	for _, tc := range []struct {
		name   string
		method string
		req    mcp.Request
	}{
		{"another method", "tools/list", &mcp.ListToolsRequest{}},
		{"another tool", methodToolsCall, call(string(trinotools.ToolExplain), `{"sql":"SELECT 1"}`)},
		{"no statement", methodToolsCall, call(string(trinotools.ToolQuery), `{"sql":""}`)},
		{"arguments that are not an object", methodToolsCall, call(string(trinotools.ToolQuery), `"SELECT 1"`)},
		{"a connection with prompts off", methodToolsCall, call(string(trinotools.ToolQuery), `{"sql":"SELECT 1","connection":"other"}`)},
		{"a client that declares no elicitation", methodToolsCall, call(string(trinotools.ToolQuery), `{"sql":"SELECT 1"}`)},
	} {
		before := reached
		res, err := h(context.Background(), tc.method, tc.req)
		if err != nil || res == nil || reached != before+1 {
			t.Errorf("%s: not passed through (res=%v err=%v)", tc.name, res, err)
		}
	}
}

// A retry's answers decide the call: a declined prompt is the refusal named
// for it, with the user_declined category the audit row reads; an accepted
// one runs the query.
func TestDeclinedAnswer(t *testing.T) {
	declined := declinedAnswer(mcp.InputResponseMap{
		inputCostEstimate: &mcp.ElicitResult{Action: "accept"},
		inputPIIConsent:   &mcp.ElicitResult{Action: "decline"},
	})
	if declined == nil || !declined.IsError {
		t.Fatalf("a declined PII prompt did not refuse: %+v", declined)
	}
	var de *ElicitationDeclinedError
	if !errors.As(declined.GetError(), &de) || de.Reason != declineReasons[inputPIIConsent] {
		t.Errorf("refusal error = %v", declined.GetError())
	}
	if got := declinedAnswer(mcp.InputResponseMap{inputCostEstimate: &mcp.ElicitResult{Action: "accept"}}); got != nil {
		t.Errorf("an accepted prompt refused: %+v", got)
	}
	if got := declinedAnswer(mcp.InputResponseMap{inputCostEstimate: &mcp.ElicitResult{Action: "cancel"}}); got == nil {
		t.Error("a canceled prompt ran the query")
	}
}

func TestElicitationMiddleware_SetSemanticProvider(t *testing.T) {
	em := &ElicitationMiddleware{}

	if em.getSemanticProvider() != nil {
		t.Fatal("initial provider should be nil")
	}

	mock := &mockSemanticProvider{}
	em.SetSemanticProvider(mock)

	if em.getSemanticProvider() != mock {
		t.Fatal("provider should be set after SetSemanticProvider")
	}
}

func TestGetElicitationConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  map[string]any
		want ElicitationConfig
	}{
		{
			name: "no elicitation key",
			cfg:  map[string]any{},
			want: ElicitationConfig{},
		},
		{
			name: "full config",
			cfg: map[string]any{
				"elicitation": map[string]any{
					"enabled": true,
					"cost_estimation": map[string]any{
						"enabled":       true,
						"row_threshold": int64(500000),
					},
					"pii_consent": map[string]any{
						"enabled": true,
					},
				},
			},
			want: ElicitationConfig{
				Enabled: true,
				CostEstimation: CostEstimationConfig{
					Enabled:      true,
					RowThreshold: 500000,
				},
				PIIConsent: PIIConsentConfig{Enabled: true},
			},
		},
		{
			name: "cost only",
			cfg: map[string]any{
				"elicitation": map[string]any{
					"enabled": true,
					"cost_estimation": map[string]any{
						"enabled":       true,
						"row_threshold": 1000000,
					},
				},
			},
			want: ElicitationConfig{
				Enabled: true,
				CostEstimation: CostEstimationConfig{
					Enabled:      true,
					RowThreshold: 1000000,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getElicitationConfig(tt.cfg)
			if got != tt.want {
				t.Errorf("getElicitationConfig() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// mockSemanticProvider implements semantic.Provider for testing.
type mockSemanticProvider struct {
	columns map[string]map[string]*semantic.ColumnContext
}

func (*mockSemanticProvider) Name() string { return "mock" }

func (*mockSemanticProvider) GetTableContext(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
	return nil, nil //nolint:nilnil // mock returns zero values
}

func (*mockSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // mock returns zero values
}

func (m *mockSemanticProvider) GetColumnsContext(_ context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	if m.columns != nil {
		if cols, ok := m.columns[table.String()]; ok {
			return cols, nil
		}
	}
	return nil, nil //nolint:nilnil // mock returns zero values
}

func (*mockSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil //nolint:nilnil // mock returns zero values
}

func (*mockSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return nil, nil //nolint:nilnil // mock returns zero values
}

func (*mockSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return nil, nil //nolint:nilnil // mock returns zero values
}

func (*mockSemanticProvider) GetCuratedQueryCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (*mockSemanticProvider) Close() error { return nil }

func TestClientSupportsElicitation(t *testing.T) {
	tests := []struct {
		name   string
		params *mcp.InitializeParams
		want   bool
	}{
		{
			name:   "nil params",
			params: nil,
			want:   false,
		},
		{
			name:   "nil capabilities",
			params: &mcp.InitializeParams{},
			want:   false,
		},
		{
			name:   "no elicitation capability",
			params: noElicitParams(),
			want:   false,
		},
		{
			name:   "elicitation capable",
			params: elicitCapableParams(),
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &mockElicitor{initParams: tt.params}
			got := clientSupportsElicitation(e)
			if got != tt.want {
				t.Errorf("clientSupportsElicitation() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCheckPIIConsent_NoSemanticProvider(t *testing.T) {
	em := &connElicitation{
		config: ElicitationConfig{
			Enabled:    true,
			PIIConsent: PIIConsentConfig{Enabled: true},
		},
	}
	// No semantic provider set — should return nil (skip PII check)
	err := em.checkPIIConsent(context.Background(), nil, "SELECT * FROM schema.table1 ")
	if err != nil {
		t.Fatalf("expected nil error without semantic provider, got: %v", err)
	}
}

func TestCheckPIIConsent_NoTablesInSQL(t *testing.T) {
	mock := &mockSemanticProvider{}
	em := &connElicitation{
		config: ElicitationConfig{
			Enabled:    true,
			PIIConsent: PIIConsentConfig{Enabled: true},
		},
		semanticProvider: mock,
	}
	// SQL with no FROM/JOIN — should return nil
	err := em.checkPIIConsent(context.Background(), nil, "SELECT 1")
	if err != nil {
		t.Fatalf("expected nil error for SQL without tables, got: %v", err)
	}
}

func TestCheckPIIConsent_NoPIIColumns(t *testing.T) {
	mock := &mockSemanticProvider{
		columns: map[string]map[string]*semantic.ColumnContext{
			"schema.table1": {
				"id":   {IsPII: false},
				"name": {IsPII: false},
			},
		},
	}
	em := &connElicitation{
		config: ElicitationConfig{
			Enabled:    true,
			PIIConsent: PIIConsentConfig{Enabled: true},
		},
		semanticProvider: mock,
	}
	// Tables found but no PII columns — should return nil
	err := em.checkPIIConsent(context.Background(), nil, "SELECT * FROM schema.table1 ")
	if err != nil {
		t.Fatalf("expected nil for no PII columns, got: %v", err)
	}
}

func TestCheckPIIConsent_PIIFound_Accepted(t *testing.T) {
	sp := &mockSemanticProvider{
		columns: map[string]map[string]*semantic.ColumnContext{
			"schema.table1": {
				"id":    {IsPII: false},
				"email": {IsPII: true},
			},
		},
	}
	e := &mockElicitor{
		initParams:   elicitCapableParams(),
		elicitResult: &mcp.ElicitResult{Action: "accept"},
	}
	em := &connElicitation{
		config: ElicitationConfig{
			Enabled:    true,
			PIIConsent: PIIConsentConfig{Enabled: true},
		},
		semanticProvider: sp,
	}

	err := em.checkPIIConsent(context.Background(), e, "SELECT * FROM schema.table1 ")
	if err != nil {
		t.Fatalf("expected nil when user accepts PII consent, got: %v", err)
	}
}

func TestCheckPIIConsent_PIIFound_Declined(t *testing.T) {
	sp := &mockSemanticProvider{
		columns: map[string]map[string]*semantic.ColumnContext{
			"schema.table1": {
				"email": {IsPII: true},
			},
		},
	}
	e := &mockElicitor{
		initParams:   elicitCapableParams(),
		elicitResult: &mcp.ElicitResult{Action: "decline"},
	}
	em := &connElicitation{
		config: ElicitationConfig{
			Enabled:    true,
			PIIConsent: PIIConsentConfig{Enabled: true},
		},
		semanticProvider: sp,
	}

	err := em.checkPIIConsent(context.Background(), e, "SELECT * FROM schema.table1 ")
	var decErr *ElicitationDeclinedError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected ElicitationDeclinedError, got: %v", err)
	}
	if decErr.ErrorCategory() != "user_declined" {
		t.Errorf("expected user_declined category, got %q", decErr.ErrorCategory())
	}
}

func TestCheckPIIConsent_PIIFound_ElicitFails(t *testing.T) {
	sp := &mockSemanticProvider{
		columns: map[string]map[string]*semantic.ColumnContext{
			"schema.table1": {
				"email": {IsPII: true},
			},
		},
	}
	e := &mockElicitor{
		initParams: elicitCapableParams(),
		elicitErr:  errors.New("elicit failed"),
	}
	em := &connElicitation{
		config: ElicitationConfig{
			Enabled:    true,
			PIIConsent: PIIConsentConfig{Enabled: true},
		},
		semanticProvider: sp,
	}

	// Elicit error → graceful nil (degradation)
	err := em.checkPIIConsent(context.Background(), e, "SELECT * FROM schema.table1 ")
	if err != nil {
		t.Fatalf("expected nil on elicit failure (graceful degradation), got: %v", err)
	}
}

func TestCheckPIIConsent_ColumnsError(t *testing.T) {
	// Mock that returns error for column lookup — should skip gracefully
	errMock := &errSemanticProvider{}
	em := &connElicitation{
		config: ElicitationConfig{
			Enabled:    true,
			PIIConsent: PIIConsentConfig{Enabled: true},
		},
		semanticProvider: errMock,
	}
	err := em.checkPIIConsent(context.Background(), nil, "SELECT * FROM schema.table1 ")
	if err != nil {
		t.Fatalf("expected nil error when column lookup fails, got: %v", err)
	}
}

// errSemanticProvider always returns errors for column lookups.
type errSemanticProvider struct{}

func (*errSemanticProvider) Name() string { return "err-mock" }
func (*errSemanticProvider) GetTableContext(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*errSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*errSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return nil, errors.New("lookup failed")
}

func (*errSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*errSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*errSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return nil, nil //nolint:nilnil // mock
}

func (*errSemanticProvider) GetCuratedQueryCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (*errSemanticProvider) Close() error { return nil }

func TestEstimateRows_Success(t *testing.T) {
	explainer := &mockExplainer{
		result: &trinoclient.ExplainResult{
			Type: trinoclient.ExplainIO,
			Plan: `{"inputTableColumnInfos":[{"estimate":{"outputRowCount":9000000.0}}],"estimate":{"outputRowCount":5000000.0}}`,
		},
	}
	em := &connElicitation{client: explainer}

	rows, known, err := em.estimateRows(context.Background(), "SELECT * FROM big_table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows != 5_000_000 || !known {
		t.Errorf("estimateRows() = %d, %v; want the returned 5000000, not the scanned 9000000", rows, known)
	}
}

func TestEstimateRows_ExplainError(t *testing.T) {
	explainer := &mockExplainer{
		err: errors.New("connection refused"),
	}
	em := &connElicitation{client: explainer}

	_, _, err := em.estimateRows(context.Background(), "SELECT 1")
	if err == nil {
		t.Fatal("expected error when explain fails")
	}
}

func TestCheckCostEstimation_BelowThreshold(t *testing.T) {
	explainer := &mockExplainer{
		result: &trinoclient.ExplainResult{
			Plan: `{"estimate":{"outputRowCount":100.0}}`,
		},
	}
	e := &mockElicitor{initParams: elicitCapableParams()}
	em := &connElicitation{
		client: explainer,
		config: ElicitationConfig{
			CostEstimation: CostEstimationConfig{
				Enabled:      true,
				RowThreshold: 1000,
			},
		},
	}

	err := em.checkCostEstimation(context.Background(), e, "SELECT 1")
	if err != nil {
		t.Fatalf("expected nil for below-threshold query, got: %v", err)
	}
}

func TestCheckCostEstimation_AboveThreshold_Accepted(t *testing.T) {
	explainer := &mockExplainer{
		result: &trinoclient.ExplainResult{
			Plan: `{"estimate":{"outputRowCount":5000000.0}}`,
		},
	}
	e := &mockElicitor{
		initParams:   elicitCapableParams(),
		elicitResult: &mcp.ElicitResult{Action: "accept"},
	}
	em := &connElicitation{
		client: explainer,
		config: ElicitationConfig{
			CostEstimation: CostEstimationConfig{
				Enabled:      true,
				RowThreshold: 1000,
			},
		},
	}

	err := em.checkCostEstimation(context.Background(), e, "SELECT * FROM big")
	if err != nil {
		t.Fatalf("expected nil when user accepts, got: %v", err)
	}
}

func TestCheckCostEstimation_AboveThreshold_Declined(t *testing.T) {
	explainer := &mockExplainer{
		result: &trinoclient.ExplainResult{
			Plan: `{"estimate":{"outputRowCount":5000000.0}}`,
		},
	}
	e := &mockElicitor{
		initParams:   elicitCapableParams(),
		elicitResult: &mcp.ElicitResult{Action: "decline"},
	}
	em := &connElicitation{
		client: explainer,
		config: ElicitationConfig{
			CostEstimation: CostEstimationConfig{
				Enabled:      true,
				RowThreshold: 1000,
			},
		},
	}

	err := em.checkCostEstimation(context.Background(), e, "SELECT * FROM big")
	var decErr *ElicitationDeclinedError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected ElicitationDeclinedError, got: %v", err)
	}
}

func TestCheckCostEstimation_ExplainFails(t *testing.T) {
	explainer := &mockExplainer{
		err: errors.New("explain failed"),
	}
	e := &mockElicitor{initParams: elicitCapableParams()}
	em := &connElicitation{
		client: explainer,
		config: ElicitationConfig{
			CostEstimation: CostEstimationConfig{
				Enabled:      true,
				RowThreshold: 1000,
			},
		},
	}

	// Explain error → graceful nil
	err := em.checkCostEstimation(context.Background(), e, "SELECT 1")
	if err != nil {
		t.Fatalf("expected nil on explain failure (graceful degradation), got: %v", err)
	}
}

func TestCheckCostEstimation_ElicitFails(t *testing.T) {
	explainer := &mockExplainer{
		result: &trinoclient.ExplainResult{
			Plan: `{"estimate":{"outputRowCount":5000000.0}}`,
		},
	}
	e := &mockElicitor{
		initParams: elicitCapableParams(),
		elicitErr:  errors.New("elicit failed"),
	}
	em := &connElicitation{
		client: explainer,
		config: ElicitationConfig{
			CostEstimation: CostEstimationConfig{
				Enabled:      true,
				RowThreshold: 1000,
			},
		},
	}

	// Elicit error → graceful nil
	err := em.checkCostEstimation(context.Background(), e, "SELECT * FROM big")
	if err != nil {
		t.Fatalf("expected nil on elicit failure (graceful degradation), got: %v", err)
	}
}

func TestBeforeWithSession_ClientNoElicitation(t *testing.T) {
	e := &mockElicitor{initParams: noElicitParams()}
	em := &connElicitation{
		config: ElicitationConfig{
			Enabled:        true,
			CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: 100},
			PIIConsent:     PIIConsentConfig{Enabled: true},
		},
	}

	err := em.beforeWithSession(context.Background(), e, "SELECT * FROM schema.table1 ")
	if err != nil {
		t.Fatalf("expected nil when client lacks elicitation, got: %v", err)
	}
}

func TestBeforeWithSession_CostAndPII(t *testing.T) {
	explainer := &mockExplainer{
		result: &trinoclient.ExplainResult{
			Plan: `{"estimate":{"outputRowCount":5000000.0}}`,
		},
	}
	e := &mockElicitor{
		initParams:   elicitCapableParams(),
		elicitResult: &mcp.ElicitResult{Action: "accept"},
	}
	sp := &mockSemanticProvider{
		columns: map[string]map[string]*semantic.ColumnContext{
			"schema.table1": {
				"email": {IsPII: true},
			},
		},
	}
	em := &connElicitation{
		client: explainer,
		config: ElicitationConfig{
			Enabled:        true,
			CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: 1000},
			PIIConsent:     PIIConsentConfig{Enabled: true},
		},
		semanticProvider: sp,
	}

	// Both cost and PII enabled, user accepts both
	err := em.beforeWithSession(context.Background(), e, "SELECT * FROM schema.table1 ")
	if err != nil {
		t.Fatalf("expected nil when user accepts both checks, got: %v", err)
	}
}

func TestBeforeWithSession_CostDeclined(t *testing.T) {
	explainer := &mockExplainer{
		result: &trinoclient.ExplainResult{
			Plan: `{"estimate":{"outputRowCount":5000000.0}}`,
		},
	}
	e := &mockElicitor{
		initParams:   elicitCapableParams(),
		elicitResult: &mcp.ElicitResult{Action: "decline"},
	}
	em := &connElicitation{
		client: explainer,
		config: ElicitationConfig{
			Enabled:        true,
			CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: 1000},
			PIIConsent:     PIIConsentConfig{Enabled: true},
		},
	}

	// Cost declined — should short-circuit without reaching PII check
	err := em.beforeWithSession(context.Background(), e, "SELECT * FROM schema.table1 ")
	var decErr *ElicitationDeclinedError
	if !errors.As(err, &decErr) {
		t.Fatalf("expected ElicitationDeclinedError, got: %v", err)
	}
}

// costPromptFor is a connection with the cost prompt on, its EXPLAIN answering
// plan, and the call's row limit.
func costPromptFor(plan string, threshold, rowLimit int64) *connElicitation {
	return &connElicitation{
		client:   &mockExplainer{result: &trinoclient.ExplainResult{Plan: plan}},
		config:   ElicitationConfig{CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: threshold}},
		rowLimit: rowLimit,
	}
}

// TestCostPrompt_ReturnedRowsCappedByTheLimit pins #2586's first item: the
// prompt is about what comes back, the output estimate capped by the call's
// row limit, never a table's scan.
func TestCostPrompt_ReturnedRowsCappedByTheLimit(t *testing.T) {
	const aggregate = `{"inputTableColumnInfos":[{"estimate":{"outputRowCount":1430361.5}}],"estimate":{"outputRowCount":60.0}}`
	if p := costPromptFor(aggregate, 1000, 10000).costPrompt(context.Background(), "SELECT region, count(*) FROM t GROUP BY region"); p != nil {
		t.Errorf("an aggregate returning 60 rows prompted: %q", p.Message)
	}

	const large = `{"estimate":{"outputRowCount":5000000.0}}`
	p := costPromptFor(large, 500, 1000).costPrompt(context.Background(), "SELECT * FROM t")
	if p == nil || !strings.Contains(p.Message, "return about 1,000 rows (threshold: 500)") {
		t.Errorf("a result capped at its 1,000-row limit, over a 500 threshold: %+v", p)
	}
	if p := costPromptFor(large, 1000, 1000).costPrompt(context.Background(), "SELECT * FROM t"); p != nil {
		t.Errorf("a result its row limit holds at the threshold prompted: %q", p.Message)
	}
	if p := costPromptFor(large, 1000, 0).costPrompt(context.Background(), "SELECT * FROM t"); p == nil {
		t.Error("with no row limit known, the estimate itself is compared")
	}
}

// TestCostPrompt_UnknownEstimateDoesNotPrompt: an estimate Trino could not
// make asks nothing; the row limit and the result budget still hold.
func TestCostPrompt_UnknownEstimateDoesNotPrompt(t *testing.T) {
	const unknown = `{"inputTableColumnInfos":[{"estimate":{"outputRowCount":NaN}}],"estimate":{"outputRowCount":NaN}}`
	if p := costPromptFor(unknown, 1, 0).costPrompt(context.Background(), "SELECT * FROM t"); p != nil {
		t.Errorf("an unknown estimate prompted: %q", p.Message)
	}
}

// TestRefusal_AnUnansweredPromptIsNotADecline pins #2586's fourth item, on
// both paths: decline is the person saying no (user_declined); cancel, or any
// other action that is not accept, is a person not reached
// (prompt_unanswered), with text telling the agent a resend only asks again.
func TestRefusal_AnUnansweredPromptIsNotADecline(t *testing.T) {
	for _, tc := range []struct {
		action, category, says string
	}{
		{"decline", "user_declined", "did not approve"},
		{"cancel", "prompt_unanswered", "the person was not reached"},
		{"timeout", "prompt_unanswered", "sending the same call again will only ask again"},
	} {
		res := declinedAnswer(mcp.InputResponseMap{inputCostEstimate: &mcp.ElicitResult{Action: tc.action}})
		var de *ElicitationDeclinedError
		if res == nil || !errors.As(res.GetError(), &de) {
			t.Fatalf("%s: no refusal: %+v", tc.action, res)
		}
		if de.ErrorCategory() != tc.category || !strings.Contains(de.Error(), tc.says) {
			t.Errorf("retry answered %s: %s %q", tc.action, de.ErrorCategory(), de.Error())
		}

		err := ask(context.Background(), &mockElicitor{elicitResult: &mcp.ElicitResult{Action: tc.action}},
			confirmation("?"), inputPIIConsent)
		if !errors.As(err, &de) || de.ErrorCategory() != tc.category {
			t.Errorf("in band answered %s: %v", tc.action, err)
		}
	}
}

// TestPIIPrompt_CountsOnlyTheColumnsTheStatementReads pins #2586's third
// item: a PII column the statement never names is not counted; a star reads
// every column.
func TestPIIPrompt_CountsOnlyTheColumnsTheStatementReads(t *testing.T) {
	em := &connElicitation{
		config: ElicitationConfig{PIIConsent: PIIConsentConfig{Enabled: true}},
		semanticProvider: &mockSemanticProvider{columns: map[string]map[string]*semantic.ColumnContext{
			"schema.table1": {"email": {IsPII: true}, "region": {}, "total": {}},
		}},
	}
	ctx := context.Background()
	if p := em.piiPrompt(ctx, "SELECT region, count(*), sum(total) FROM schema.table1 GROUP BY region"); p != nil {
		t.Errorf("an aggregate over non-PII columns prompted: %q", p.Message)
	}
	for _, sql := range []string{
		"SELECT email FROM schema.table1",
		"SELECT region FROM schema.table1 WHERE Email LIKE '%@example.com'",
		"SELECT * FROM schema.table1",
		"SELECT t.* FROM schema.table1 t",
	} {
		if p := em.piiPrompt(ctx, sql); p == nil || !strings.Contains(p.Message, "1 PII column(s)") {
			t.Errorf("%s: %+v", sql, p)
		}
	}
}

// TestRowLimits: a call's row limit is clamped as trino_query clamps it, and
// a connection that sets none takes the toolkit's defaults.
func TestRowLimits(t *testing.T) {
	l := rowLimits{defaultRows: 1000, maxRows: 10000}
	for requested, want := range map[int]int64{0: 1000, -5: 1000, 50: 50, 10000: 10000, 50000: 10000} {
		if got := l.effective(requested); got != want {
			t.Errorf("effective(%d) = %d, want %d", requested, got, want)
		}
	}
	if got := limitsOf(Config{}); got != (rowLimits{defaultRows: defaultQueryLimit, maxRows: defaultMaxLimit}) {
		t.Errorf("limitsOf(empty) = %+v", got)
	}
	if got := limitsOf(Config{DefaultLimit: 10, MaxLimit: 20}); got != (rowLimits{defaultRows: 10, maxRows: 20}) {
		t.Errorf("limitsOf(set) = %+v", got)
	}
}
