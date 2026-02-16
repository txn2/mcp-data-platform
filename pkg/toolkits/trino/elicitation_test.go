package trino

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	trinotools "github.com/txn2/mcp-trino/pkg/tools"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestParseRowEstimates(t *testing.T) {
	tests := []struct {
		name string
		plan string
		want int64
	}{
		{
			name: "single table estimate",
			plan: `Fragment 0 [SOURCE]
    Layout: [col1, col2]
    Estimates: {rows: 5000000 (47.68MB), cpu: ?, memory: ?, network: ?}`,
			want: 5_000_000,
		},
		{
			name: "multiple tables takes max",
			plan: `Fragment 0
    Estimates: {rows: 100 (1KB), cpu: ?, memory: ?, network: ?}
Fragment 1
    Estimates: {rows: 2000000 (190MB), cpu: ?, memory: ?, network: ?}`,
			want: 2_000_000,
		},
		{
			name: "no row estimates",
			plan: `Fragment 0 [SOURCE]
    Estimates: {rows: ? (?B), cpu: ?, memory: ?, network: ?}`,
			want: 0,
		},
		{
			name: "empty plan",
			plan: "",
			want: 0,
		},
		{
			name: "zero rows",
			plan: "Estimates: {rows: 0 (0B)}",
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseRowEstimates(tt.plan)
			if got != tt.want {
				t.Errorf("parseRowEstimates() = %d, want %d", got, tt.want)
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

func TestExtractSQLFromInput(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  string
	}{
		{
			name:  "valid QueryInput",
			input: trinotools.QueryInput{SQL: "SELECT 1"},
			want:  "SELECT 1",
		},
		{
			name:  "wrong type",
			input: "not a QueryInput",
			want:  "",
		},
		{
			name:  "nil input",
			input: nil,
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractSQLFromInput(tt.input)
			if got != tt.want {
				t.Errorf("extractSQLFromInput() = %q, want %q", got, tt.want)
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

func TestElicitationMiddleware_Before_NonQueryTool(t *testing.T) {
	em := &ElicitationMiddleware{
		config: ElicitationConfig{
			Enabled:        true,
			CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: 100},
		},
	}

	tc := trinotools.NewToolContext(trinotools.ToolExplain, trinotools.ExplainInput{SQL: "SELECT 1"})
	ctx, err := em.Before(context.Background(), tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
}

func TestElicitationMiddleware_Before_EmptySQL(t *testing.T) {
	em := &ElicitationMiddleware{
		config: ElicitationConfig{
			Enabled:        true,
			CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: 100},
		},
	}

	tc := trinotools.NewToolContext(trinotools.ToolQuery, trinotools.QueryInput{SQL: ""})
	ctx, err := em.Before(context.Background(), tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
}

func TestElicitationMiddleware_Before_NoSession(t *testing.T) {
	em := &ElicitationMiddleware{
		config: ElicitationConfig{
			Enabled:        true,
			CostEstimation: CostEstimationConfig{Enabled: true, RowThreshold: 100},
		},
	}

	tc := trinotools.NewToolContext(trinotools.ToolQuery, trinotools.QueryInput{SQL: "SELECT * FROM big_table"})
	// No ServerSession in context
	ctx, err := em.Before(context.Background(), tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctx == nil {
		t.Fatal("context should not be nil")
	}
}

func TestElicitationMiddleware_After_Passthrough(t *testing.T) {
	em := &ElicitationMiddleware{}
	result := &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: "ok"}},
	}

	got, err := em.After(context.Background(), nil, result, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != result {
		t.Error("After should pass through result unchanged")
	}
}

func TestElicitationMiddleware_After_PassthroughError(t *testing.T) {
	em := &ElicitationMiddleware{}
	origErr := errors.New("handler error")

	got, err := em.After(context.Background(), nil, nil, origErr)
	if !errors.Is(err, origErr) {
		t.Errorf("After should pass through error, got %v", err)
	}
	if got != nil {
		t.Error("After should pass through nil result")
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

func (*mockSemanticProvider) Close() error { return nil }

func TestClientSupportsElicitation(_ *testing.T) {
	// clientSupportsElicitation takes *mcp.ServerSession which requires
	// a real MCP handshake to construct. Tested indirectly via Before
	// with no session in context (returns early at GetServerSession).
}

func TestCheckPIIConsent_NoSemanticProvider(t *testing.T) {
	em := &ElicitationMiddleware{
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
	em := &ElicitationMiddleware{
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
	em := &ElicitationMiddleware{
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

func TestCheckPIIConsent_PIIColumnsFound_NilSession(_ *testing.T) {
	mock := &mockSemanticProvider{
		columns: map[string]map[string]*semantic.ColumnContext{
			"schema.table1": {
				"id":    {IsPII: false},
				"email": {IsPII: true},
			},
		},
	}
	em := &ElicitationMiddleware{
		config: ElicitationConfig{
			Enabled:    true,
			PIIConsent: PIIConsentConfig{Enabled: true},
		},
		semanticProvider: mock,
	}
	// PII columns found but nil session — Elicit call will panic/fail gracefully
	// This tests lines 224-232 (PII columns found, message formatted)
	// The ss.Elicit call will panic on nil, so we test up to the point of elicitation
	defer func() {
		// The nil session will cause a panic in ss.Elicit — that's expected
		// since we can't easily construct a *mcp.ServerSession for testing.
		// The important thing is we exercised the PII detection path.
		recover() //nolint:errcheck // intentional panic recovery for nil session test
	}()
	_ = em.checkPIIConsent(context.Background(), nil, "SELECT * FROM schema.table1 ")
}

func TestCheckPIIConsent_ColumnsError(t *testing.T) {
	// Mock that returns error for column lookup — should skip gracefully
	errMock := &errSemanticProvider{}
	em := &ElicitationMiddleware{
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
func (*errSemanticProvider) Close() error { return nil }
