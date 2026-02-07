package datahub

import (
	"context"
	"testing"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestDefaultLineageConfig(t *testing.T) {
	cfg := DefaultLineageConfig()

	if cfg.Enabled {
		t.Error("expected Enabled to be false by default")
	}
	if cfg.MaxHops != 2 {
		t.Errorf("expected MaxHops=2, got %d", cfg.MaxHops)
	}
	if cfg.ConflictResolution != "nearest" {
		t.Errorf("expected ConflictResolution='nearest', got %s", cfg.ConflictResolution)
	}
	if !cfg.PreferColumnLineage {
		t.Error("expected PreferColumnLineage=true")
	}
	if len(cfg.Inherit) != 2 {
		t.Errorf("expected 2 default inherit types, got %d", len(cfg.Inherit))
	}
}

func TestLineageConfig_ShouldInherit(t *testing.T) {
	cfg := LineageConfig{
		Inherit: []string{"descriptions", "glossary_terms"},
	}

	tests := []struct {
		metadataType string
		expected     bool
	}{
		{"descriptions", true},
		{"glossary_terms", true},
		{"tags", false},
		{"owners", false},
	}

	for _, tt := range tests {
		t.Run(tt.metadataType, func(t *testing.T) {
			if got := cfg.shouldInherit(tt.metadataType); got != tt.expected {
				t.Errorf("shouldInherit(%q) = %v, want %v", tt.metadataType, got, tt.expected)
			}
		})
	}
}

func TestNewLineageResolver(t *testing.T) {
	client := &mockDataHubClient{}
	cfg := DefaultLineageConfig()
	sanitizer := semantic.NewSanitizer(semantic.DefaultSanitizeConfig())

	resolver := newLineageResolver(client, cfg, sanitizer)

	if resolver == nil {
		t.Fatal("expected non-nil resolver")
	}
	if resolver.client != client {
		t.Error("client not set correctly")
	}
	if resolver.sanitizer != sanitizer {
		t.Error("sanitizer not set correctly")
	}
}

func TestResolveColumnsWithLineage_NoLineageEnabled(t *testing.T) {
	client := &mockDataHubClient{
		getSchemaFunc: func(ctx context.Context, urn string) (*types.SchemaMetadata, error) {
			return &types.SchemaMetadata{
				Fields: []types.SchemaField{
					{FieldPath: "id", Description: "Primary key"},
					{FieldPath: "name", Description: "User name"},
				},
			}, nil
		},
	}

	cfg := LineageConfig{
		Enabled: false,
		Inherit: []string{"descriptions"},
	}
	resolver := newLineageResolver(client, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	columns, err := resolver.resolveColumnsWithLineage(context.Background(), "urn:li:dataset:test", "test.table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(columns))
	}
	if columns["id"].Description != "Primary key" {
		t.Errorf("expected description 'Primary key', got %q", columns["id"].Description)
	}
}

func TestResolveColumnsWithLineage_WithAliasMatch(t *testing.T) {
	client := &mockDataHubClient{
		getSchemaFunc: func(ctx context.Context, urn string) (*types.SchemaMetadata, error) {
			if urn == "urn:li:dataset:(urn:li:dataPlatform:trino,source.table,PROD)" {
				return &types.SchemaMetadata{
					Fields: []types.SchemaField{
						{FieldPath: "amount", Description: "Transaction amount", GlossaryTerms: []types.GlossaryTerm{{URN: "urn:term", Name: "Amount"}}},
					},
				}, nil
			}
			// Target table has no documentation - field path extracts to "amount"
			return &types.SchemaMetadata{
				Fields: []types.SchemaField{
					{FieldPath: "amount", Description: ""},
				},
			}, nil
		},
	}

	cfg := LineageConfig{
		Enabled: true,
		Inherit: []string{"descriptions", "glossary_terms"},
		Aliases: []AliasConfig{
			{
				Source:  "source.table",
				Targets: []string{"target.*"},
				ColumnMapping: map[string]string{
					"amount": "amount", // Maps extracted field name to source field name
				},
			},
		},
	}
	resolver := newLineageResolver(client, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	columns, err := resolver.resolveColumnsWithLineage(context.Background(), "urn:li:dataset:test", "target.events")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	col := columns["amount"]
	if col == nil {
		t.Fatal("expected column 'amount'")
	}
	if col.Description != "Transaction amount" {
		t.Errorf("expected inherited description, got %q", col.Description)
	}
	if col.InheritedFrom == nil {
		t.Fatal("expected InheritedFrom to be set")
	}
	if col.InheritedFrom.MatchMethod != "alias" {
		t.Errorf("expected match_method='alias', got %q", col.InheritedFrom.MatchMethod)
	}
}

func TestResolveColumnsWithLineage_WithColumnLineage(t *testing.T) {
	client := &mockDataHubClient{
		getSchemaFunc: func(ctx context.Context, urn string) (*types.SchemaMetadata, error) {
			if urn == "urn:li:dataset:upstream" {
				return &types.SchemaMetadata{
					Fields: []types.SchemaField{
						{FieldPath: "[version=2.0].[type=struct].upstream_col", Description: "Upstream description"},
					},
				}, nil
			}
			return &types.SchemaMetadata{
				Fields: []types.SchemaField{
					{FieldPath: "downstream_col", Description: ""},
				},
			}, nil
		},
		getColumnLineageFunc: func(ctx context.Context, urn string) (*types.ColumnLineage, error) {
			return &types.ColumnLineage{
				Mappings: []types.ColumnLineageMapping{
					{
						DownstreamColumn: "downstream_col",
						UpstreamDataset:  "urn:li:dataset:upstream",
						UpstreamColumn:   "[version=2.0].[type=struct].upstream_col",
					},
				},
			}, nil
		},
		getSchemasFunc: func(ctx context.Context, urns []string) (map[string]*types.SchemaMetadata, error) {
			return map[string]*types.SchemaMetadata{
				"urn:li:dataset:upstream": {
					Fields: []types.SchemaField{
						{FieldPath: "[version=2.0].[type=struct].upstream_col", Description: "Upstream description"},
					},
				},
			}, nil
		},
	}

	cfg := LineageConfig{
		Enabled:             true,
		Inherit:             []string{"descriptions"},
		PreferColumnLineage: true,
		MaxHops:             2,
	}
	resolver := newLineageResolver(client, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	columns, err := resolver.resolveColumnsWithLineage(context.Background(), "urn:li:dataset:downstream", "test.table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	col := columns["downstream_col"]
	if col == nil {
		t.Fatal("expected column 'downstream_col'")
	}
	if col.Description != "Upstream description" {
		t.Errorf("expected inherited description, got %q", col.Description)
	}
	if col.InheritedFrom == nil {
		t.Fatal("expected InheritedFrom to be set")
	}
	if col.InheritedFrom.MatchMethod != "column_lineage" {
		t.Errorf("expected match_method='column_lineage', got %q", col.InheritedFrom.MatchMethod)
	}
}

func TestResolveColumnsWithLineage_WithTableLineage(t *testing.T) {
	client := &mockDataHubClient{
		getSchemaFunc: func(ctx context.Context, urn string) (*types.SchemaMetadata, error) {
			return &types.SchemaMetadata{
				Fields: []types.SchemaField{
					{FieldPath: "user_id", Description: ""},
				},
			}, nil
		},
		getColumnLineageFunc: func(ctx context.Context, urn string) (*types.ColumnLineage, error) {
			return &types.ColumnLineage{Mappings: nil}, nil // No column lineage
		},
		getLineageFunc: func(ctx context.Context, urn string, opts ...dhclient.LineageOption) (*types.LineageResult, error) {
			return &types.LineageResult{
				Nodes: []types.LineageNode{
					{URN: "urn:li:dataset:upstream1", Level: 1},
				},
			}, nil
		},
		getSchemasFunc: func(ctx context.Context, urns []string) (map[string]*types.SchemaMetadata, error) {
			return map[string]*types.SchemaMetadata{
				"urn:li:dataset:upstream1": {
					Fields: []types.SchemaField{
						{FieldPath: "user_id", Description: "User identifier", Tags: []types.Tag{{Name: "pii"}}},
					},
				},
			}, nil
		},
	}

	cfg := LineageConfig{
		Enabled:             true,
		Inherit:             []string{"descriptions", "tags"},
		PreferColumnLineage: true,
		MaxHops:             2,
		ConflictResolution:  "nearest",
	}
	resolver := newLineageResolver(client, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	columns, err := resolver.resolveColumnsWithLineage(context.Background(), "urn:li:dataset:downstream", "test.table")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	col := columns["user_id"]
	if col == nil {
		t.Fatal("expected column 'user_id'")
	}
	if col.Description != "User identifier" {
		t.Errorf("expected inherited description, got %q", col.Description)
	}
	if col.InheritedFrom == nil {
		t.Fatal("expected InheritedFrom to be set")
	}
	if col.InheritedFrom.MatchMethod != "name_exact" {
		t.Errorf("expected match_method='name_exact', got %q", col.InheritedFrom.MatchMethod)
	}
	if col.InheritedFrom.Hops != 1 {
		t.Errorf("expected hops=1, got %d", col.InheritedFrom.Hops)
	}
}

func TestResolveAlias(t *testing.T) {
	cfg := LineageConfig{
		Aliases: []AliasConfig{
			{
				Source:        "source.users",
				Targets:       []string{"elasticsearch.*.user-*", "kafka.events.*"},
				ColumnMapping: map[string]string{"target_col": "source_col"},
			},
		},
	}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	tests := []struct {
		tableName      string
		expectedSource string
		hasMapping     bool
	}{
		{"elasticsearch.default.user-events", "source.users", true},
		{"elasticsearch.prod.user-profiles", "source.users", true},
		{"kafka.events.clicks", "source.users", true},
		{"postgres.public.users", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.tableName, func(t *testing.T) {
			source, mapping := resolver.resolveAlias(tt.tableName)
			if source != tt.expectedSource {
				t.Errorf("expected source %q, got %q", tt.expectedSource, source)
			}
			if tt.hasMapping && mapping == nil {
				t.Error("expected column mapping")
			}
		})
	}
}

func TestTransformColumnName(t *testing.T) {
	cfg := LineageConfig{
		ColumnTransforms: []ColumnTransformConfig{
			{StripPrefix: "rxtxmsg.payload."},
			{StripSuffix: "_v2"},
		},
	}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	tests := []struct {
		input    string
		expected string
	}{
		{"rxtxmsg.payload.user_id", "user_id"},
		{"rxtxmsg.payload.amount_v2", "amount"},
		{"plain_column", "plain_column"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := resolver.transformColumnName(tt.input)
			if result != tt.expected {
				t.Errorf("transformColumnName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNeedsDocumentation(t *testing.T) {
	cfg := LineageConfig{
		Inherit: []string{"descriptions", "glossary_terms"},
	}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	tests := []struct {
		name     string
		column   *semantic.ColumnContext
		expected bool
	}{
		{
			name:     "empty column",
			column:   &semantic.ColumnContext{},
			expected: true,
		},
		{
			name: "has description",
			column: &semantic.ColumnContext{
				Description: "Has description",
			},
			expected: true, // Still needs glossary terms
		},
		{
			name: "has both",
			column: &semantic.ColumnContext{
				Description:   "Has description",
				GlossaryTerms: []semantic.GlossaryTerm{{Name: "Term"}},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolver.needsDocumentation(tt.column)
			if result != tt.expected {
				t.Errorf("needsDocumentation() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestFieldToColumnContext(t *testing.T) {
	resolver := newLineageResolver(nil, LineageConfig{}, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	field := types.SchemaField{
		FieldPath:   "test_field",
		Description: "Test description",
		Tags:        []types.Tag{{Name: "pii"}, {Name: "sensitive"}},
		GlossaryTerms: []types.GlossaryTerm{
			{URN: "urn:term:1", Name: "Term1", Description: "Term desc"},
		},
	}

	cc := resolver.fieldToColumnContext(field, "test_field")

	if cc.Name != "test_field" {
		t.Errorf("expected name 'test_field', got %q", cc.Name)
	}
	if cc.Description != "Test description" {
		t.Errorf("expected description 'Test description', got %q", cc.Description)
	}
	if !cc.IsPII {
		t.Error("expected IsPII=true")
	}
	if !cc.IsSensitive {
		t.Error("expected IsSensitive=true")
	}
	if len(cc.GlossaryTerms) != 1 {
		t.Errorf("expected 1 glossary term, got %d", len(cc.GlossaryTerms))
	}
}

func TestInheritMetadata(t *testing.T) {
	cfg := LineageConfig{
		Inherit: []string{"descriptions", "glossary_terms", "tags"},
	}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	target := &semantic.ColumnContext{}
	source := types.SchemaField{
		Description: "Source description",
		GlossaryTerms: []types.GlossaryTerm{
			{URN: "urn:term", Name: "Term"},
		},
		Tags: []types.Tag{{Name: "important"}},
	}

	resolver.inheritMetadata(target, inheritSource{
		Field:       source,
		URN:         "urn:upstream",
		Column:      "source_col",
		Hops:        1,
		MatchMethod: "name_exact",
	})

	if target.Description != "Source description" {
		t.Errorf("expected inherited description, got %q", target.Description)
	}
	if len(target.GlossaryTerms) != 1 {
		t.Errorf("expected 1 glossary term, got %d", len(target.GlossaryTerms))
	}
	if len(target.Tags) != 1 {
		t.Errorf("expected 1 tag, got %d", len(target.Tags))
	}
	if target.InheritedFrom == nil {
		t.Fatal("expected InheritedFrom to be set")
	}
	if target.InheritedFrom.SourceURN != "urn:upstream" {
		t.Errorf("expected SourceURN='urn:upstream', got %q", target.InheritedFrom.SourceURN)
	}
	if target.InheritedFrom.SourceColumn != "source_col" {
		t.Errorf("expected SourceColumn='source_col', got %q", target.InheritedFrom.SourceColumn)
	}
	if target.InheritedFrom.Hops != 1 {
		t.Errorf("expected Hops=1, got %d", target.InheritedFrom.Hops)
	}
	if target.InheritedFrom.MatchMethod != "name_exact" {
		t.Errorf("expected MatchMethod='name_exact', got %q", target.InheritedFrom.MatchMethod)
	}
}

func TestInheritMetadata_NoInheritanceWhenTargetPopulated(t *testing.T) {
	cfg := LineageConfig{
		Inherit: []string{"descriptions"},
	}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	target := &semantic.ColumnContext{
		Description: "Already has description",
	}
	source := types.SchemaField{
		Description: "Source description",
	}

	resolver.inheritMetadata(target, inheritSource{
		Field:       source,
		URN:         "urn:upstream",
		Column:      "source_col",
		Hops:        1,
		MatchMethod: "name_exact",
	})

	if target.Description != "Already has description" {
		t.Errorf("description should not be overwritten, got %q", target.Description)
	}
	if target.InheritedFrom != nil {
		t.Error("InheritedFrom should be nil when nothing was inherited")
	}
}

func TestDetermineMatchMethod(t *testing.T) {
	resolver := newLineageResolver(nil, LineageConfig{}, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	tests := []struct {
		targetCol string
		sourceCol string
		expected  string
	}{
		{"user_id", "user_id", "name_exact"},
		{"rxtxmsg.payload.user_id", "user_id", "name_transformed"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := resolver.determineMatchMethod(tt.targetCol, tt.sourceCol)
			if result != tt.expected {
				t.Errorf("determineMatchMethod(%q, %q) = %q, want %q", tt.targetCol, tt.sourceCol, result, tt.expected)
			}
		})
	}
}

func TestBuildColumnsAndFindUndocumented(t *testing.T) {
	cfg := LineageConfig{
		Inherit: []string{"descriptions"},
	}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	schema := &types.SchemaMetadata{
		Fields: []types.SchemaField{
			{FieldPath: "documented", Description: "Has description"},
			{FieldPath: "undocumented", Description: ""},
		},
	}

	columns, undocumented := resolver.buildColumnsAndFindUndocumented(schema)

	if len(columns) != 2 {
		t.Errorf("expected 2 columns, got %d", len(columns))
	}
	if len(undocumented) != 1 {
		t.Errorf("expected 1 undocumented, got %d", len(undocumented))
	}
	if !undocumented["undocumented"] {
		t.Error("expected 'undocumented' to be in undocumented map")
	}
}

func TestGroupNodesByLevel(t *testing.T) {
	cfg := LineageConfig{MaxHops: 2}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	nodes := []types.LineageNode{
		{URN: "urn:1", Level: 1},
		{URN: "urn:2", Level: 1},
		{URN: "urn:3", Level: 2},
		{URN: "urn:4", Level: 3}, // Should be excluded (> MaxHops)
	}

	result := resolver.groupNodesByLevel(nodes)

	if len(result[1]) != 2 {
		t.Errorf("expected 2 nodes at level 1, got %d", len(result[1]))
	}
	if len(result[2]) != 1 {
		t.Errorf("expected 1 node at level 2, got %d", len(result[2]))
	}
	if len(result[3]) != 0 {
		t.Errorf("expected 0 nodes at level 3, got %d", len(result[3]))
	}
}

func TestCollectUpstreamURNs(t *testing.T) {
	cfg := LineageConfig{MaxHops: 2}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	nodesByLevel := map[int][]types.LineageNode{
		1: {{URN: "urn:1"}, {URN: "urn:2"}},
		2: {{URN: "urn:3"}},
	}

	urns := resolver.collectUpstreamURNs(nodesByLevel)

	if len(urns) != 3 {
		t.Errorf("expected 3 URNs, got %d", len(urns))
	}
}

func TestBuildFieldMap(t *testing.T) {
	resolver := newLineageResolver(nil, LineageConfig{}, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	fields := []types.SchemaField{
		{FieldPath: "[version=2.0].[type=struct].user_id", Description: "User ID"},
		{FieldPath: "simple_field", Description: "Simple"},
	}

	result := resolver.buildFieldMap(fields)

	if len(result) != 2 {
		t.Errorf("expected 2 fields, got %d", len(result))
	}
	if _, ok := result["user_id"]; !ok {
		t.Error("expected 'user_id' in map (extracted from path)")
	}
	if _, ok := result["simple_field"]; !ok {
		t.Error("expected 'simple_field' in map")
	}
}

func TestBuildFieldMapByPath(t *testing.T) {
	resolver := newLineageResolver(nil, LineageConfig{}, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	fields := []types.SchemaField{
		{FieldPath: "[version=2.0].[type=struct].user_id", Description: "User ID"},
	}

	result := resolver.buildFieldMapByPath(fields)

	if len(result) != 1 {
		t.Errorf("expected 1 field, got %d", len(result))
	}
	if _, ok := result["[version=2.0].[type=struct].user_id"]; !ok {
		t.Error("expected full path in map")
	}
}

func TestGroupMappingsByDataset(t *testing.T) {
	resolver := newLineageResolver(nil, LineageConfig{}, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	mappings := []types.ColumnLineageMapping{
		{DownstreamColumn: "col1", UpstreamDataset: "urn:ds1", UpstreamColumn: "src1"},
		{DownstreamColumn: "col2", UpstreamDataset: "urn:ds1", UpstreamColumn: "src2"},
		{DownstreamColumn: "col3", UpstreamDataset: "urn:ds2", UpstreamColumn: "src3"},
		{DownstreamColumn: "documented", UpstreamDataset: "urn:ds1", UpstreamColumn: "src4"}, // Not undocumented
	}

	undocumented := map[string]bool{
		"col1": true,
		"col2": true,
		"col3": true,
	}

	result := resolver.groupMappingsByDataset(mappings, undocumented)

	if len(result["urn:ds1"]) != 2 {
		t.Errorf("expected 2 mappings for ds1, got %d", len(result["urn:ds1"]))
	}
	if len(result["urn:ds2"]) != 1 {
		t.Errorf("expected 1 mapping for ds2, got %d", len(result["urn:ds2"]))
	}
}
