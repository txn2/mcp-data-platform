// Package datahub provides a DataHub implementation of the semantic provider.
package datahub

import (
	"context"
	"fmt"
	"strings"
	"time"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
	"github.com/txn2/mcp-datahub/pkg/types"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Config holds DataHub adapter configuration.
type Config struct {
	URL      string
	Token    string
	Platform string // Default platform for URN building (e.g., "trino")
	Timeout  time.Duration
}

// Client defines the interface for DataHub operations.
// This allows for mocking in tests.
type Client interface {
	Search(ctx context.Context, query string, opts ...dhclient.SearchOption) (*types.SearchResult, error)
	GetEntity(ctx context.Context, urn string) (*types.Entity, error)
	GetSchema(ctx context.Context, urn string) (*types.SchemaMetadata, error)
	GetLineage(ctx context.Context, urn string, opts ...dhclient.LineageOption) (*types.LineageResult, error)
	GetGlossaryTerm(ctx context.Context, urn string) (*types.GlossaryTerm, error)
	Ping(ctx context.Context) error
	Close() error
}

// Adapter implements semantic.Provider using DataHub.
type Adapter struct {
	cfg    Config
	client Client
}

// New creates a new DataHub adapter with a real client.
func New(cfg Config) (*Adapter, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("datahub URL is required")
	}
	if cfg.Platform == "" {
		cfg.Platform = "trino"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	clientCfg := dhclient.DefaultConfig()
	clientCfg.URL = cfg.URL
	clientCfg.Token = cfg.Token
	clientCfg.Timeout = cfg.Timeout

	client, err := dhclient.New(clientCfg)
	if err != nil {
		return nil, fmt.Errorf("creating datahub client: %w", err)
	}

	return &Adapter{
		cfg:    cfg,
		client: client,
	}, nil
}

// NewWithClient creates a new DataHub adapter with a provided client (for testing).
func NewWithClient(cfg Config, client Client) (*Adapter, error) {
	if client == nil {
		return nil, fmt.Errorf("datahub client is required")
	}
	if cfg.Platform == "" {
		cfg.Platform = "trino"
	}
	return &Adapter{
		cfg:    cfg,
		client: client,
	}, nil
}

// Name returns the provider name.
func (a *Adapter) Name() string {
	return "datahub"
}

// GetTableContext retrieves table context from DataHub.
func (a *Adapter) GetTableContext(ctx context.Context, table semantic.TableIdentifier) (*semantic.TableContext, error) {
	urn := a.buildDatasetURN(table)

	entity, err := a.client.GetEntity(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting entity from datahub: %w", err)
	}

	return a.entityToTableContext(entity), nil
}

// GetColumnContext retrieves column context from DataHub.
func (a *Adapter) GetColumnContext(ctx context.Context, column semantic.ColumnIdentifier) (*semantic.ColumnContext, error) {
	urn := a.buildDatasetURN(column.TableIdentifier)

	schema, err := a.client.GetSchema(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting schema from datahub: %w", err)
	}

	// Find the column in the schema by FieldPath
	for _, field := range schema.Fields {
		fieldName := extractFieldName(field.FieldPath)
		if fieldName == column.Column {
			return a.fieldToColumnContext(field), nil
		}
	}

	return nil, fmt.Errorf("column %s not found in schema", column.Column)
}

// GetColumnsContext retrieves all columns context from DataHub.
func (a *Adapter) GetColumnsContext(ctx context.Context, table semantic.TableIdentifier) (map[string]*semantic.ColumnContext, error) {
	urn := a.buildDatasetURN(table)

	schema, err := a.client.GetSchema(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting schema from datahub: %w", err)
	}

	columns := make(map[string]*semantic.ColumnContext, len(schema.Fields))
	for _, field := range schema.Fields {
		fieldName := extractFieldName(field.FieldPath)
		columns[fieldName] = a.fieldToColumnContext(field)
	}

	return columns, nil
}

// GetLineage retrieves lineage from DataHub.
func (a *Adapter) GetLineage(ctx context.Context, table semantic.TableIdentifier, direction semantic.LineageDirection, maxDepth int) (*semantic.LineageInfo, error) {
	urn := a.buildDatasetURN(table)

	dhDirection := dhclient.LineageDirectionUpstream
	if direction == semantic.LineageDownstream {
		dhDirection = dhclient.LineageDirectionDownstream
	}

	result, err := a.client.GetLineage(ctx, urn,
		dhclient.WithDirection(dhDirection),
		dhclient.WithDepth(maxDepth),
	)
	if err != nil {
		return nil, fmt.Errorf("getting lineage from datahub: %w", err)
	}

	return a.lineageResultToInfo(result, direction, maxDepth), nil
}

// GetGlossaryTerm retrieves a glossary term from DataHub.
func (a *Adapter) GetGlossaryTerm(ctx context.Context, urn string) (*semantic.GlossaryTerm, error) {
	term, err := a.client.GetGlossaryTerm(ctx, urn)
	if err != nil {
		return nil, fmt.Errorf("getting glossary term from datahub: %w", err)
	}

	if term == nil {
		return nil, fmt.Errorf("glossary term not found: %s", urn)
	}

	return &semantic.GlossaryTerm{
		URN:         term.URN,
		Name:        term.Name,
		Description: term.Description,
	}, nil
}

// SearchTables searches for tables in DataHub.
func (a *Adapter) SearchTables(ctx context.Context, filter semantic.SearchFilter) ([]semantic.TableSearchResult, error) {
	var opts []dhclient.SearchOption
	if filter.Limit > 0 {
		opts = append(opts, dhclient.WithLimit(filter.Limit))
	}
	if filter.Offset > 0 {
		opts = append(opts, dhclient.WithOffset(filter.Offset))
	}
	// DataHub doesn't have a direct platform filter, use entity type DATASET
	opts = append(opts, dhclient.WithEntityType("DATASET"))

	result, err := a.client.Search(ctx, filter.Query, opts...)
	if err != nil {
		return nil, fmt.Errorf("searching datahub: %w", err)
	}

	results := make([]semantic.TableSearchResult, 0, len(result.Entities))
	for _, entity := range result.Entities {
		matchedField := ""
		if len(entity.MatchedFields) > 0 {
			matchedField = entity.MatchedFields[0].Name
		}

		domainName := ""
		if entity.Domain != nil {
			domainName = entity.Domain.Name
		}

		tags := make([]string, len(entity.Tags))
		for i, tag := range entity.Tags {
			tags[i] = tag.Name
		}

		results = append(results, semantic.TableSearchResult{
			URN:          entity.URN,
			Name:         entity.Name,
			Platform:     entity.Platform,
			Description:  entity.Description,
			Tags:         tags,
			Domain:       domainName,
			MatchedField: matchedField,
		})
	}

	return results, nil
}

// Close releases resources.
func (a *Adapter) Close() error {
	if a.client != nil {
		return a.client.Close()
	}
	return nil
}

// buildDatasetURN creates a DataHub URN for a table.
func (a *Adapter) buildDatasetURN(table semantic.TableIdentifier) string {
	// DataHub URN format: urn:li:dataset:(urn:li:dataPlatform:platform,database.schema.table,PROD)
	name := table.String()
	return fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:%s,%s,PROD)", a.cfg.Platform, name)
}

// ResolveURN converts a DataHub URN to a table identifier.
func (a *Adapter) ResolveURN(_ context.Context, urn string) (*semantic.TableIdentifier, error) {
	// Parse URN format: urn:li:dataset:(urn:li:dataPlatform:platform,name,env)
	if !strings.HasPrefix(urn, "urn:li:dataset:") {
		return nil, fmt.Errorf("invalid dataset URN: %s", urn)
	}

	// Extract the name part
	start := strings.Index(urn, ",")
	end := strings.LastIndex(urn, ",")
	if start == -1 || end == -1 || start == end {
		return nil, fmt.Errorf("invalid URN format: %s", urn)
	}

	name := urn[start+1 : end]
	parts := strings.Split(name, ".")

	switch len(parts) {
	case 2:
		return &semantic.TableIdentifier{
			Schema: parts[0],
			Table:  parts[1],
		}, nil
	case 3:
		return &semantic.TableIdentifier{
			Catalog: parts[0],
			Schema:  parts[1],
			Table:   parts[2],
		}, nil
	default:
		return nil, fmt.Errorf("invalid table name in URN: %s", name)
	}
}

// BuildURN creates a URN from a table identifier.
func (a *Adapter) BuildURN(_ context.Context, table semantic.TableIdentifier) (string, error) {
	return a.buildDatasetURN(table), nil
}

// entityToTableContext converts a DataHub entity to semantic table context.
func (a *Adapter) entityToTableContext(entity *types.Entity) *semantic.TableContext {
	tc := &semantic.TableContext{
		URN:              entity.URN,
		Description:      entity.Description,
		Owners:           convertOwners(entity.Owners),
		Tags:             convertTags(entity.Tags),
		GlossaryTerms:    convertGlossaryTerms(entity.GlossaryTerms),
		Domain:           convertDomain(entity.Domain),
		Deprecation:      convertDeprecation(entity.Deprecation),
		CustomProperties: convertProperties(entity.Properties),
		LastModified:     convertTimestamp(entity.LastModified),
	}

	return tc
}

// convertOwners converts DataHub owners to semantic owners.
func convertOwners(owners []types.Owner) []semantic.Owner {
	result := make([]semantic.Owner, len(owners))
	for i, owner := range owners {
		result[i] = semantic.Owner{
			URN:   owner.URN,
			Type:  ownerTypeToSemantic(owner.Type),
			Name:  owner.Name,
			Email: owner.Email,
		}
	}
	return result
}

// convertTags converts DataHub tags to string slice.
func convertTags(tags []types.Tag) []string {
	result := make([]string, len(tags))
	for i, tag := range tags {
		result[i] = tag.Name
	}
	return result
}

// convertGlossaryTerms converts DataHub glossary terms to semantic terms.
func convertGlossaryTerms(terms []types.GlossaryTerm) []semantic.GlossaryTerm {
	result := make([]semantic.GlossaryTerm, len(terms))
	for i, term := range terms {
		result[i] = semantic.GlossaryTerm{
			URN:         term.URN,
			Name:        term.Name,
			Description: term.Description,
		}
	}
	return result
}

// convertDomain converts DataHub domain to semantic domain.
func convertDomain(domain *types.Domain) *semantic.Domain {
	if domain == nil {
		return nil
	}
	return &semantic.Domain{
		URN:         domain.URN,
		Name:        domain.Name,
		Description: domain.Description,
	}
}

// convertDeprecation converts DataHub deprecation to semantic deprecation.
func convertDeprecation(dep *types.Deprecation) *semantic.Deprecation {
	if dep == nil {
		return nil
	}
	result := &semantic.Deprecation{
		Deprecated: dep.Deprecated, //nolint:staticcheck // Using deprecated field as it's the only option
		Note:       dep.Note,
		Actor:      dep.Actor,
	}
	if dep.DecommissionTime > 0 {
		t := time.Unix(dep.DecommissionTime/1000, 0)
		result.DecommDate = &t
	}
	return result
}

// convertProperties converts DataHub properties to string map.
func convertProperties(props map[string]any) map[string]string {
	if len(props) == 0 {
		return nil
	}
	result := make(map[string]string, len(props))
	for k, v := range props {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

// convertTimestamp converts a millisecond timestamp to a time pointer.
func convertTimestamp(ms int64) *time.Time {
	if ms == 0 {
		return nil
	}
	t := time.Unix(ms/1000, 0)
	return &t
}

// fieldToColumnContext converts a DataHub schema field to semantic column context.
func (a *Adapter) fieldToColumnContext(field types.SchemaField) *semantic.ColumnContext {
	fieldName := extractFieldName(field.FieldPath)
	cc := &semantic.ColumnContext{
		Name:        fieldName,
		Description: field.Description,
	}

	// Check for PII and sensitivity tags
	for _, tag := range field.Tags {
		tagLower := strings.ToLower(tag.Name)
		if strings.Contains(tagLower, "pii") {
			cc.IsPII = true
		}
		if strings.Contains(tagLower, "sensitive") || strings.Contains(tagLower, "confidential") {
			cc.IsSensitive = true
		}
		cc.Tags = append(cc.Tags, tag.Name)
	}

	// Convert glossary terms
	cc.GlossaryTerms = make([]semantic.GlossaryTerm, len(field.GlossaryTerms))
	for i, term := range field.GlossaryTerms {
		cc.GlossaryTerms[i] = semantic.GlossaryTerm{
			URN:         term.URN,
			Name:        term.Name,
			Description: term.Description,
		}
	}

	return cc
}

// lineageResultToInfo converts a DataHub lineage result to semantic lineage info.
func (a *Adapter) lineageResultToInfo(result *types.LineageResult, direction semantic.LineageDirection, maxDepth int) *semantic.LineageInfo {
	info := &semantic.LineageInfo{
		Direction: direction,
		MaxDepth:  maxDepth,
		Entities:  make([]semantic.LineageEntity, len(result.Nodes)),
	}

	// Build edge map for quick lookup
	edgeMap := make(map[string][]string)
	for _, edge := range result.Edges {
		edgeMap[edge.Target] = append(edgeMap[edge.Target], edge.Source)
	}

	for i, node := range result.Nodes {
		entity := semantic.LineageEntity{
			URN:      node.URN,
			Type:     node.Type,
			Name:     node.Name,
			Platform: node.Platform,
			Depth:    node.Level,
		}

		// Add parent edges
		if parents, ok := edgeMap[node.URN]; ok {
			entity.Parents = make([]semantic.LineageEdge, len(parents))
			for j, parent := range parents {
				entity.Parents[j] = semantic.LineageEdge{URN: parent}
			}
		}

		info.Entities[i] = entity
	}

	return info
}

// ownerTypeToSemantic converts DataHub ownership type to semantic type.
func ownerTypeToSemantic(t types.OwnershipType) semantic.OwnerType {
	switch t {
	case types.OwnershipTypeTechnicalOwner:
		return semantic.OwnerTypeUser
	case types.OwnershipTypeBusinessOwner:
		return semantic.OwnerTypeUser
	default:
		return semantic.OwnerTypeUser
	}
}

// extractFieldName extracts the simple field name from a FieldPath.
// FieldPath can be "user.address.city" - we want "city".
func extractFieldName(fieldPath string) string {
	parts := strings.Split(fieldPath, ".")
	if len(parts) == 0 {
		return fieldPath
	}
	return parts[len(parts)-1]
}

// Verify interface compliance.
var (
	_ semantic.Provider    = (*Adapter)(nil)
	_ semantic.URNResolver = (*Adapter)(nil)
)
