package datahub

import (
	"context"
	"strings"
	"testing"

	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

func TestApplicableTransforms(t *testing.T) {
	unscoped := ColumnTransformConfig{StripSuffix: "_v2"}
	orders := ColumnTransformConfig{TargetPattern: "elasticsearch.prod.orders", StripSuffix: "_raw"}
	anyProd := ColumnTransformConfig{TargetPattern: "elasticsearch.prod.*", StripPrefix: "payload_"}
	malformed := ColumnTransformConfig{TargetPattern: "[unclosed", StripSuffix: "_x"}

	all := []ColumnTransformConfig{unscoped, orders, anyProd, malformed}

	tests := []struct {
		name  string
		table string
		want  []ColumnTransformConfig
	}{
		{"unscoped transform applies everywhere", "postgres.public.users", []ColumnTransformConfig{unscoped}},
		{"exact pattern and glob both match", "elasticsearch.prod.orders", []ColumnTransformConfig{unscoped, orders, anyProd}},
		{"glob matches, exact does not", "elasticsearch.prod.clicks", []ColumnTransformConfig{unscoped, anyProd}},
		{"malformed pattern matches nothing", "[unclosed", []ColumnTransformConfig{unscoped}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := applicableTransforms(all, tt.table)
			if len(got) != len(tt.want) {
				t.Fatalf("applicableTransforms(%q) = %v, want %v", tt.table, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("transform %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestScopedTo_UnscopedConfigIsNotCopied(t *testing.T) {
	cfg := LineageConfig{ColumnTransforms: []ColumnTransformConfig{{StripSuffix: "_v2"}}}
	resolver := newLineageResolver(nil, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	if resolver.scopedTo("any.table.name") != resolver {
		t.Error("a config with no target_pattern must not allocate a scoped copy")
	}

	scopedCfg := LineageConfig{ColumnTransforms: []ColumnTransformConfig{
		{TargetPattern: "kafka.*", StripSuffix: "_v2"},
	}}
	scopedResolver := newLineageResolver(nil, scopedCfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))
	if scopedResolver.scopedTo("kafka.events") == scopedResolver {
		t.Error("a scoped config must resolve against the target table")
	}
	if got := len(scopedResolver.cfg.ColumnTransforms); got != 1 {
		t.Errorf("scoping mutated the shared config: %d transforms left, want 1", got)
	}
}

// TestResolveColumnsWithLineage_TargetPatternScopesTransforms proves target_pattern
// selects which datasets a column transform acts on. Without it the transform runs
// on every target, so a rule written for one index silently rewrites column names
// everywhere; the second case is what fails when that regresses.
func TestResolveColumnsWithLineage_TargetPatternScopesTransforms(t *testing.T) {
	const (
		sourceTable = "source.orders"
		description = "Transaction amount"
	)

	client := &mockDataHubClient{
		getSchemaFunc: func(_ context.Context, urn string) (*types.SchemaMetadata, error) {
			if strings.Contains(urn, sourceTable) {
				return &types.SchemaMetadata{Fields: []types.SchemaField{
					{FieldPath: "amount", Description: description},
				}}, nil
			}
			return &types.SchemaMetadata{Fields: []types.SchemaField{
				{FieldPath: "amount_v2"},
			}}, nil
		},
	}

	cfg := LineageConfig{
		Enabled:  true,
		MaxHops:  2,
		Inherit:  []string{lineageTestDescriptions},
		Aliases:  []AliasConfig{{Source: sourceTable, Targets: []string{"elasticsearch.prod.*"}}},
		Timeout:  0,
		CacheTTL: 0,
		ColumnTransforms: []ColumnTransformConfig{
			{TargetPattern: "elasticsearch.prod.orders", StripSuffix: "_v2"},
		},
	}
	resolver := newLineageResolver(client, cfg, semantic.NewSanitizer(semantic.DefaultSanitizeConfig()))

	matching, err := resolver.resolveColumnsWithLineage(context.Background(), "urn:li:dataset:target", "elasticsearch.prod.orders")
	if err != nil {
		t.Fatalf(lineageTestUnexpectedErr, err)
	}
	if got := matching["amount_v2"]; got == nil || got.Description != description {
		t.Fatalf("matching target did not inherit through the transform: %+v", got)
	}

	other, err := resolver.resolveColumnsWithLineage(context.Background(), "urn:li:dataset:target", "elasticsearch.prod.clicks")
	if err != nil {
		t.Fatalf(lineageTestUnexpectedErr, err)
	}
	if got := other["amount_v2"]; got == nil || got.Description != "" {
		t.Fatalf("transform scoped to another dataset was applied anyway: %+v", got)
	}
}
