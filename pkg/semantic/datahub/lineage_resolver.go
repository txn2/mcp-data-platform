package datahub

import (
	"context"
	"path/filepath"
	"strings"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// lineageResolver handles lineage-aware column resolution.
type lineageResolver struct {
	client    Client
	cfg       LineageConfig
	sanitizer *semantic.Sanitizer
}

// newLineageResolver creates a new lineage resolver.
func newLineageResolver(client Client, cfg LineageConfig, sanitizer *semantic.Sanitizer) *lineageResolver {
	return &lineageResolver{
		client:    client,
		cfg:       cfg,
		sanitizer: sanitizer,
	}
}

// resolveColumnsWithLineage resolves column metadata, inheriting from upstream when needed.
func (r *lineageResolver) resolveColumnsWithLineage(
	ctx context.Context,
	urn string,
	tableName string,
) (map[string]*semantic.ColumnContext, error) {
	schema, err := r.client.GetSchema(ctx, urn)
	if err != nil {
		return nil, err
	}

	columns, undocumented := r.buildColumnsAndFindUndocumented(schema)

	if len(undocumented) == 0 || !r.cfg.Enabled {
		return columns, nil
	}

	return r.resolveUndocumentedColumns(ctx, columns, undocumented, urn, tableName)
}

// buildColumnsAndFindUndocumented converts schema fields to column contexts and identifies undocumented ones.
func (r *lineageResolver) buildColumnsAndFindUndocumented(schema *types.SchemaMetadata) (map[string]*semantic.ColumnContext, map[string]bool) {
	columns := make(map[string]*semantic.ColumnContext, len(schema.Fields))
	undocumented := make(map[string]bool)

	for _, field := range schema.Fields {
		fieldName := extractFieldName(field.FieldPath)
		cc := r.fieldToColumnContext(field, fieldName)
		columns[fieldName] = cc

		if r.needsDocumentation(cc) {
			undocumented[fieldName] = true
		}
	}

	return columns, undocumented
}

// resolveUndocumentedColumns attempts to resolve documentation for undocumented columns.
func (r *lineageResolver) resolveUndocumentedColumns(
	ctx context.Context,
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	urn string,
	tableName string,
) (map[string]*semantic.ColumnContext, error) {
	// Check for alias match first
	if source, columnMapping := r.resolveAlias(tableName); source != "" {
		return r.inheritFromAlias(ctx, columns, undocumented, source, columnMapping)
	}

	// Try column-level lineage if preferred
	if r.cfg.PreferColumnLineage {
		if result := r.tryColumnLineage(ctx, columns, undocumented, urn); result != nil {
			return result, nil
		}
	}

	// Fall back to table-level lineage
	return r.tryTableLineage(ctx, columns, undocumented, urn)
}

// tryColumnLineage attempts to use column-level lineage for inheritance.
func (r *lineageResolver) tryColumnLineage(
	ctx context.Context,
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	urn string,
) map[string]*semantic.ColumnContext {
	columnLineage, err := r.client.GetColumnLineage(ctx, urn)
	if err != nil || len(columnLineage.Mappings) == 0 {
		return nil
	}
	result, _ := r.inheritFromColumnLineage(ctx, columns, undocumented, columnLineage)
	return result
}

// tryTableLineage attempts to use table-level lineage for inheritance.
func (r *lineageResolver) tryTableLineage(
	ctx context.Context,
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	urn string,
) (map[string]*semantic.ColumnContext, error) {
	lineage, err := r.client.GetLineage(ctx, urn,
		dhclient.WithDirection(dhclient.LineageDirectionUpstream),
		dhclient.WithDepth(r.cfg.MaxHops),
	)
	if err != nil {
		return columns, nil
	}
	return r.inheritFromTableLineage(ctx, columns, undocumented, lineage)
}

// needsDocumentation checks if a column needs documentation.
func (r *lineageResolver) needsDocumentation(cc *semantic.ColumnContext) bool {
	return r.needsDescription(cc) || r.needsGlossaryTerms(cc) || r.needsTags(cc)
}

func (r *lineageResolver) needsDescription(cc *semantic.ColumnContext) bool {
	return r.cfg.shouldInherit("descriptions") && cc.Description == ""
}

func (r *lineageResolver) needsGlossaryTerms(cc *semantic.ColumnContext) bool {
	return r.cfg.shouldInherit("glossary_terms") && len(cc.GlossaryTerms) == 0
}

func (r *lineageResolver) needsTags(cc *semantic.ColumnContext) bool {
	return r.cfg.shouldInherit("tags") && len(cc.Tags) == 0
}

// resolveAlias checks if the table matches any configured alias.
func (r *lineageResolver) resolveAlias(tableName string) (source string, columnMapping map[string]string) {
	for _, alias := range r.cfg.Aliases {
		for _, pattern := range alias.Targets {
			if matched, _ := filepath.Match(pattern, tableName); matched {
				return alias.Source, alias.ColumnMapping
			}
		}
	}
	return "", nil
}

// inheritFromAlias inherits metadata from an aliased source.
func (r *lineageResolver) inheritFromAlias(
	ctx context.Context,
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	source string,
	columnMapping map[string]string,
) (map[string]*semantic.ColumnContext, error) {
	parts := strings.Split(source, ".")
	if len(parts) < 2 {
		return columns, nil
	}

	sourceURN := "urn:li:dataset:(urn:li:dataPlatform:trino," + source + ",PROD)"
	sourceSchema, err := r.client.GetSchema(ctx, sourceURN)
	if err != nil {
		return columns, nil
	}

	sourceColumns := r.buildFieldMap(sourceSchema.Fields)
	r.applyAliasInheritance(columns, undocumented, sourceColumns, columnMapping, sourceURN)

	return columns, nil
}

// applyAliasInheritance applies inheritance from alias source to target columns.
func (r *lineageResolver) applyAliasInheritance(
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	sourceColumns map[string]types.SchemaField,
	columnMapping map[string]string,
	sourceURN string,
) {
	for targetCol := range undocumented {
		sourceCol, ok := columnMapping[targetCol]
		if !ok {
			sourceCol = r.transformColumnName(targetCol)
		}
		if sourceField, ok := sourceColumns[sourceCol]; ok {
			r.inheritMetadata(columns[targetCol], sourceField, sourceURN, sourceCol, 1, "alias")
		}
	}
}

// inheritFromColumnLineage uses fine-grained column lineage to inherit metadata.
func (r *lineageResolver) inheritFromColumnLineage(
	ctx context.Context,
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	columnLineage *types.ColumnLineage,
) (map[string]*semantic.ColumnContext, error) {
	upstreamDatasets := r.groupMappingsByDataset(columnLineage.Mappings, undocumented)
	if len(upstreamDatasets) == 0 {
		return columns, nil
	}

	upstreamSchemas := r.fetchUpstreamSchemas(ctx, upstreamDatasets)
	r.applyColumnLineageInheritance(columns, upstreamDatasets, upstreamSchemas)

	return columns, nil
}

// groupMappingsByDataset groups column lineage mappings by upstream dataset.
func (r *lineageResolver) groupMappingsByDataset(
	mappings []types.ColumnLineageMapping,
	undocumented map[string]bool,
) map[string][]types.ColumnLineageMapping {
	upstreamDatasets := make(map[string][]types.ColumnLineageMapping)
	for _, mapping := range mappings {
		downstreamCol := extractFieldName(mapping.DownstreamColumn)
		if undocumented[downstreamCol] {
			upstreamDatasets[mapping.UpstreamDataset] = append(
				upstreamDatasets[mapping.UpstreamDataset],
				mapping,
			)
		}
	}
	return upstreamDatasets
}

// fetchUpstreamSchemas fetches schemas for all upstream datasets.
func (r *lineageResolver) fetchUpstreamSchemas(
	ctx context.Context,
	upstreamDatasets map[string][]types.ColumnLineageMapping,
) map[string]*types.SchemaMetadata {
	upstreamURNs := make([]string, 0, len(upstreamDatasets))
	for urn := range upstreamDatasets {
		upstreamURNs = append(upstreamURNs, urn)
	}
	schemas, _ := r.client.GetSchemas(ctx, upstreamURNs)
	return schemas
}

// applyColumnLineageInheritance applies inheritance based on column lineage mappings.
func (r *lineageResolver) applyColumnLineageInheritance(
	columns map[string]*semantic.ColumnContext,
	upstreamDatasets map[string][]types.ColumnLineageMapping,
	upstreamSchemas map[string]*types.SchemaMetadata,
) {
	for upstreamURN, mappings := range upstreamDatasets {
		upstreamSchema, ok := upstreamSchemas[upstreamURN]
		if !ok || upstreamSchema == nil {
			continue
		}

		upstreamColumns := r.buildFieldMapByPath(upstreamSchema.Fields)
		for _, mapping := range mappings {
			downstreamCol := extractFieldName(mapping.DownstreamColumn)
			if sourceField, ok := upstreamColumns[mapping.UpstreamColumn]; ok {
				r.inheritMetadata(columns[downstreamCol], sourceField, upstreamURN, mapping.UpstreamColumn, 1, "column_lineage")
			}
		}
	}
}

// inheritFromTableLineage uses table-level lineage with name matching.
func (r *lineageResolver) inheritFromTableLineage(
	ctx context.Context,
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	lineage *types.LineageResult,
) (map[string]*semantic.ColumnContext, error) {
	if len(lineage.Nodes) == 0 {
		return columns, nil
	}

	nodesByLevel := r.groupNodesByLevel(lineage.Nodes)
	upstreamURNs := r.collectUpstreamURNs(nodesByLevel)
	if len(upstreamURNs) == 0 {
		return columns, nil
	}

	upstreamSchemas, _ := r.client.GetSchemas(ctx, upstreamURNs)
	r.applyTableLineageInheritance(columns, undocumented, nodesByLevel, upstreamSchemas)

	return columns, nil
}

// groupNodesByLevel groups lineage nodes by their level (hop distance).
func (r *lineageResolver) groupNodesByLevel(nodes []types.LineageNode) map[int][]types.LineageNode {
	nodesByLevel := make(map[int][]types.LineageNode)
	for _, node := range nodes {
		if node.Level <= r.cfg.MaxHops {
			nodesByLevel[node.Level] = append(nodesByLevel[node.Level], node)
		}
	}
	return nodesByLevel
}

// collectUpstreamURNs collects all upstream URNs from nodes grouped by level.
func (r *lineageResolver) collectUpstreamURNs(nodesByLevel map[int][]types.LineageNode) []string {
	var urns []string
	for level := 1; level <= r.cfg.MaxHops; level++ {
		for _, node := range nodesByLevel[level] {
			urns = append(urns, node.URN)
		}
	}
	return urns
}

// applyTableLineageInheritance applies inheritance based on table-level lineage.
func (r *lineageResolver) applyTableLineageInheritance(
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	nodesByLevel map[int][]types.LineageNode,
	upstreamSchemas map[string]*types.SchemaMetadata,
) {
	for level := 1; level <= r.cfg.MaxHops; level++ {
		for _, node := range nodesByLevel[level] {
			upstreamSchema, ok := upstreamSchemas[node.URN]
			if !ok || upstreamSchema == nil {
				continue
			}
			upstreamColumns := r.buildFieldMap(upstreamSchema.Fields)
			r.matchAndInheritColumns(columns, undocumented, upstreamColumns, node.URN, level)
		}
	}
}

// matchAndInheritColumns matches undocumented columns to upstream and inherits metadata.
func (r *lineageResolver) matchAndInheritColumns(
	columns map[string]*semantic.ColumnContext,
	undocumented map[string]bool,
	upstreamColumns map[string]types.SchemaField,
	upstreamURN string,
	level int,
) {
	for targetCol := range undocumented {
		if columns[targetCol].InheritedFrom != nil && r.cfg.ConflictResolution == "nearest" {
			continue
		}
		sourceCol := r.transformColumnName(targetCol)
		if sourceField, ok := upstreamColumns[sourceCol]; ok {
			matchMethod := r.determineMatchMethod(targetCol, sourceCol)
			r.inheritMetadata(columns[targetCol], sourceField, upstreamURN, sourceCol, level, matchMethod)
		}
	}
}

// determineMatchMethod determines the match method based on column name comparison.
func (r *lineageResolver) determineMatchMethod(targetCol, sourceCol string) string {
	if sourceCol != targetCol {
		return "name_transformed"
	}
	return "name_exact"
}

// buildFieldMap builds a map of field name to schema field.
func (r *lineageResolver) buildFieldMap(fields []types.SchemaField) map[string]types.SchemaField {
	m := make(map[string]types.SchemaField, len(fields))
	for _, field := range fields {
		m[extractFieldName(field.FieldPath)] = field
	}
	return m
}

// buildFieldMapByPath builds a map of field path to schema field.
func (r *lineageResolver) buildFieldMapByPath(fields []types.SchemaField) map[string]types.SchemaField {
	m := make(map[string]types.SchemaField, len(fields))
	for _, field := range fields {
		m[field.FieldPath] = field
	}
	return m
}

// transformColumnName applies configured transforms to a column name.
func (r *lineageResolver) transformColumnName(columnName string) string {
	result := columnName
	for _, transform := range r.cfg.ColumnTransforms {
		result = r.applyTransform(result, transform)
	}
	return result
}

// applyTransform applies a single transform to a column name.
func (r *lineageResolver) applyTransform(columnName string, transform ColumnTransformConfig) string {
	result := columnName
	if transform.StripPrefix != "" {
		result = strings.TrimPrefix(result, transform.StripPrefix)
	}
	if transform.StripSuffix != "" {
		result = strings.TrimSuffix(result, transform.StripSuffix)
	}
	return result
}

// inheritMetadata copies metadata from a source field to a target column context.
func (r *lineageResolver) inheritMetadata(
	target *semantic.ColumnContext,
	source types.SchemaField,
	sourceURN string,
	sourceColumn string,
	hops int,
	matchMethod string,
) {
	inherited := r.inheritDescription(target, source)
	inherited = r.inheritGlossaryTerms(target, source) || inherited
	inherited = r.inheritTags(target, source) || inherited

	if inherited {
		target.InheritedFrom = &semantic.InheritedMetadata{
			SourceURN:    sourceURN,
			SourceColumn: sourceColumn,
			Hops:         hops,
			MatchMethod:  matchMethod,
		}
	}
}

// inheritDescription inherits description if needed.
func (r *lineageResolver) inheritDescription(target *semantic.ColumnContext, source types.SchemaField) bool {
	if !r.cfg.shouldInherit("descriptions") || target.Description != "" || source.Description == "" {
		return false
	}
	target.Description = r.sanitizer.SanitizeDescription(source.Description)
	return true
}

// inheritGlossaryTerms inherits glossary terms if needed.
func (r *lineageResolver) inheritGlossaryTerms(target *semantic.ColumnContext, source types.SchemaField) bool {
	if !r.cfg.shouldInherit("glossary_terms") || len(target.GlossaryTerms) > 0 || len(source.GlossaryTerms) == 0 {
		return false
	}
	target.GlossaryTerms = make([]semantic.GlossaryTerm, len(source.GlossaryTerms))
	for i, term := range source.GlossaryTerms {
		target.GlossaryTerms[i] = semantic.GlossaryTerm{
			URN:         term.URN,
			Name:        r.sanitizer.SanitizeString(term.Name),
			Description: r.sanitizer.SanitizeDescription(term.Description),
		}
	}
	return true
}

// inheritTags inherits tags if needed.
func (r *lineageResolver) inheritTags(target *semantic.ColumnContext, source types.SchemaField) bool {
	if !r.cfg.shouldInherit("tags") || len(target.Tags) > 0 || len(source.Tags) == 0 {
		return false
	}
	rawTags := make([]string, len(source.Tags))
	for i, tag := range source.Tags {
		rawTags[i] = tag.Name
	}
	target.Tags = r.sanitizer.SanitizeTags(rawTags)
	return true
}

// fieldToColumnContext converts a schema field to a column context.
func (r *lineageResolver) fieldToColumnContext(field types.SchemaField, fieldName string) *semantic.ColumnContext {
	cc := &semantic.ColumnContext{
		Name:        fieldName,
		Description: r.sanitizer.SanitizeDescription(field.Description),
	}

	r.processFieldTags(cc, field.Tags)
	cc.GlossaryTerms = r.convertGlossaryTerms(field.GlossaryTerms)

	return cc
}

// processFieldTags processes field tags and sets PII/sensitivity flags.
func (r *lineageResolver) processFieldTags(cc *semantic.ColumnContext, tags []types.Tag) {
	for _, tag := range tags {
		tagLower := strings.ToLower(tag.Name)
		if strings.Contains(tagLower, "pii") {
			cc.IsPII = true
		}
		if strings.Contains(tagLower, "sensitive") || strings.Contains(tagLower, "confidential") {
			cc.IsSensitive = true
		}
		cc.Tags = append(cc.Tags, tag.Name)
	}
	cc.Tags = r.sanitizer.SanitizeTags(cc.Tags)
}

// convertGlossaryTerms converts DataHub glossary terms to semantic glossary terms.
func (r *lineageResolver) convertGlossaryTerms(terms []types.GlossaryTerm) []semantic.GlossaryTerm {
	result := make([]semantic.GlossaryTerm, len(terms))
	for i, term := range terms {
		result[i] = semantic.GlossaryTerm{
			URN:         term.URN,
			Name:        r.sanitizer.SanitizeString(term.Name),
			Description: r.sanitizer.SanitizeDescription(term.Description),
		}
	}
	return result
}
