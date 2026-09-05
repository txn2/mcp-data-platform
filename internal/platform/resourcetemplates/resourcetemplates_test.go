package resourcetemplates

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestParseVars(t *testing.T) {
	tests := []struct {
		name     string
		template string
		uri      string
		want     map[string]string
		wantErr  bool
	}{
		{
			name:     "schema URI",
			template: SchemaURI,
			uri:      "schema://rdbms.public/transactions",
			want:     map[string]string{"catalog": "rdbms", "schema_name": "public", "table": "transactions"},
		},
		{
			name:     "glossary URI",
			template: GlossaryURI,
			uri:      "glossary://revenue",
			want:     map[string]string{"term": "revenue"},
		},
		{
			name:     "availability URI",
			template: AvailabilityURI,
			uri:      "availability://warehouse.analytics/orders",
			want:     map[string]string{"catalog": "warehouse", "schema_name": "analytics", "table": "orders"},
		},
		{
			name:     "mismatch URI",
			template: SchemaURI,
			uri:      "glossary://revenue",
			wantErr:  true,
		},
		{
			name:     "empty URI",
			template: SchemaURI,
			uri:      "",
			wantErr:  true,
		},
		{
			name:     "invalid template",
			template: "{{{bad",
			uri:      "anything",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVars(tt.template, tt.uri)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseVars() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			for k, want := range tt.want {
				if got[k] != want {
					t.Errorf("parseVars()[%q] = %q, want %q", k, got[k], want)
				}
			}
		})
	}
}

// mockSemanticProvider implements semantic.Provider for testing.
type mockSemanticProvider struct {
	tableCtx    *semantic.TableContext
	tableErr    error
	colsCtx     map[string]*semantic.ColumnContext
	colsErr     error
	glossary    *semantic.GlossaryTerm
	glossaryErr error
}

func (*mockSemanticProvider) Name() string { return "mock" }

func (m *mockSemanticProvider) GetTableContext(_ context.Context, _ semantic.TableIdentifier) (*semantic.TableContext, error) {
	return m.tableCtx, m.tableErr
}

func (*mockSemanticProvider) GetColumnContext(_ context.Context, _ semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (m *mockSemanticProvider) GetColumnsContext(_ context.Context, _ semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	return m.colsCtx, m.colsErr
}

func (*mockSemanticProvider) GetLineage(_ context.Context, _ semantic.TableIdentifier, _ semantic.LineageDirection, _ int) (*semantic.LineageInfo, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (m *mockSemanticProvider) GetGlossaryTerm(_ context.Context, _ string) (*semantic.GlossaryTerm, error) {
	return m.glossary, m.glossaryErr
}

func (*mockSemanticProvider) SearchTables(_ context.Context, _ semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (*mockSemanticProvider) GetCuratedQueryCount(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (*mockSemanticProvider) Close() error { return nil }

// mockQueryProvider implements query.Provider for testing.
type mockQueryProvider struct {
	schema    *query.TableSchema
	schemaErr error
	avail     *query.TableAvailability
	availErr  error
}

func (*mockQueryProvider) Name() string { return "mock" }

func (*mockQueryProvider) ResolveTable(_ context.Context, _ string) (*query.TableIdentifier, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (m *mockQueryProvider) GetTableAvailability(_ context.Context, _ string) (*query.TableAvailability, error) {
	return m.avail, m.availErr
}

func (*mockQueryProvider) GetQueryExamples(_ context.Context, _ string) ([]query.Example, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (*mockQueryProvider) GetExecutionContext(_ context.Context, _ []string) (*query.ExecutionContext, error) {
	return nil, nil //nolint:nilnil // mock stub: unused method required by interface
}

func (m *mockQueryProvider) GetTableSchema(_ context.Context, _ query.TableIdentifier) (*query.TableSchema, error) {
	return m.schema, m.schemaErr
}

func (*mockQueryProvider) Close() error { return nil }

func TestSchema(t *testing.T) {
	t.Run("both providers", func(t *testing.T) {
		h := New(Deps{
			Query: &mockQueryProvider{
				schema: &query.TableSchema{
					Columns: []query.Column{
						{Name: "id", Type: "INTEGER"},
						{Name: "name", Type: "VARCHAR"},
					},
				},
			},
			Semantic: &mockSemanticProvider{
				tableCtx: &semantic.TableContext{
					Description: "Test table",
				},
				colsCtx: map[string]*semantic.ColumnContext{
					"id": {Name: "id", Description: "Primary key"},
				},
			},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://rdbms.public/transactions"},
		}
		result, err := h.Schema(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
		if result.Contents[0].Text == "" {
			t.Error("expected non-empty content")
		}
	})

	t.Run("query only", func(t *testing.T) {
		h := New(Deps{
			Query: &mockQueryProvider{
				schema: &query.TableSchema{
					Columns: []query.Column{{Name: "id", Type: "INTEGER"}},
				},
			},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://rdbms.public/transactions"},
		}
		result, err := h.Schema(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("semantic only", func(t *testing.T) {
		h := New(Deps{
			Semantic: &mockSemanticProvider{
				tableCtx: &semantic.TableContext{Description: "Test table"},
			},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://rdbms.public/transactions"},
		}
		result, err := h.Schema(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := New(Deps{
			Query:    &mockQueryProvider{schemaErr: errors.New("not found")},
			Semantic: &mockSemanticProvider{tableErr: errors.New("not found")},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://rdbms.public/missing"},
		}
		_, err := h.Schema(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for not found table")
		}
	})

	t.Run("invalid URI", func(t *testing.T) {
		h := New(Deps{})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "glossary://something"},
		}
		_, err := h.Schema(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for invalid URI")
		}
	})
}

func TestGlossary(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		h := New(Deps{
			Semantic: &mockSemanticProvider{
				glossary: &semantic.GlossaryTerm{
					URN:         "urn:li:glossaryTerm:revenue",
					Name:        "revenue",
					Description: "Total income",
				},
			},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "glossary://revenue"},
		}
		result, err := h.Glossary(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("not found", func(t *testing.T) {
		h := New(Deps{
			Semantic: &mockSemanticProvider{
				glossaryErr: errors.New("not found"),
			},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "glossary://missing"},
		}
		_, err := h.Glossary(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for missing term")
		}
	})

	t.Run("no semantic provider", func(t *testing.T) {
		h := New(Deps{})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "glossary://revenue"},
		}
		_, err := h.Glossary(context.Background(), req)
		if err == nil {
			t.Fatal("expected error when no semantic provider")
		}
	})

	t.Run("invalid URI", func(t *testing.T) {
		h := New(Deps{})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "schema://bad"},
		}
		_, err := h.Glossary(context.Background(), req)
		if err == nil {
			t.Fatal("expected error for invalid URI")
		}
	})
}

func TestAvailability(t *testing.T) {
	estRows := int64(1000)

	t.Run("available", func(t *testing.T) {
		h := New(Deps{
			Query: &mockQueryProvider{
				avail: &query.TableAvailability{
					Available:     true,
					QueryTable:    "rdbms.public.transactions",
					Connection:    "default",
					EstimatedRows: &estRows,
				},
			},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "availability://rdbms.public/transactions"},
		}
		result, err := h.Availability(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("not available", func(t *testing.T) {
		h := New(Deps{
			Query: &mockQueryProvider{
				avail: &query.TableAvailability{
					Available: false,
					Error:     "table does not exist",
				},
			},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "availability://rdbms.public/missing"},
		}
		result, err := h.Availability(context.Background(), req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
	})

	t.Run("provider error", func(t *testing.T) {
		h := New(Deps{
			Query: &mockQueryProvider{
				availErr: errors.New("connection failed"),
			},
		})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "availability://rdbms.public/transactions"},
		}
		_, err := h.Availability(context.Background(), req)
		if err == nil {
			t.Fatal("expected error on provider failure")
		}
	})

	t.Run("no query provider", func(t *testing.T) {
		h := New(Deps{})

		req := &mcp.ReadResourceRequest{
			Params: &mcp.ReadResourceParams{URI: "availability://rdbms.public/transactions"},
		}
		_, err := h.Availability(context.Background(), req)
		if err == nil {
			t.Fatal("expected error when no query provider")
		}
	})
}

// TestRegister proves the three templates actually reach a client, through a
// real session rather than by asserting that registration did not panic. What
// the old test in pkg/platform checked was that Register returned; a client
// asking what this server offers is the thing a template exists for.
func TestRegister(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.1"}, nil)
	New(Deps{}).Register(server)

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serverSession, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("connecting the server: %v", err)
	}
	defer serverSession.Close() //nolint:errcheck // best-effort close

	session, err := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.1"}, nil).
		Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("connecting the client: %v", err)
	}
	defer session.Close() //nolint:errcheck // best-effort close

	res, err := session.ListResourceTemplates(ctx, nil)
	if err != nil {
		t.Fatalf("listing resource templates: %v", err)
	}

	got := make(map[string]bool, len(res.ResourceTemplates))
	for _, rt := range res.ResourceTemplates {
		got[rt.URITemplate] = true
	}
	for _, want := range []string{SchemaURI, GlossaryURI, AvailabilityURI} {
		if !got[want] {
			t.Errorf("template %q was not offered to a client; registered: %v", want, got)
		}
	}
}

func TestMarshalResult(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		result, err := marshalResult("test://uri", map[string]string{"key": "value"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Contents) != 1 {
			t.Fatalf("expected 1 content, got %d", len(result.Contents))
		}
		if result.Contents[0].URI != "test://uri" {
			t.Errorf("URI = %q, want %q", result.Contents[0].URI, "test://uri")
		}
		if result.Contents[0].MIMEType != "application/json" {
			t.Errorf("MIMEType = %q, want %q", result.Contents[0].MIMEType, "application/json")
		}
	})
}
