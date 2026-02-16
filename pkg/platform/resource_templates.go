package platform

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yosida95/uritemplate/v3"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// Resource template URI patterns.
const (
	schemaTemplateURI       = "schema://{catalog}.{schema_name}/{table}"
	glossaryTemplateURI     = "glossary://{term}"
	availabilityTemplateURI = "availability://{catalog}.{schema_name}/{table}"
)

// registerResourceTemplates registers all MCP resource templates.
// Only called when resources.enabled is true.
func (p *Platform) registerResourceTemplates() {
	if !p.config.Resources.Enabled {
		return
	}

	p.mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: schemaTemplateURI,
		Name:        "Table Schema",
		Description: "Table schema with column types and semantic context (descriptions, owners, tags, glossary terms)",
		MIMEType:    "application/json",
	}, p.handleSchemaResource)

	p.mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: glossaryTemplateURI,
		Name:        "Glossary Term",
		Description: "Business glossary term definition and related assets",
		MIMEType:    "application/json",
	}, p.handleGlossaryResource)

	p.mcpServer.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: availabilityTemplateURI,
		Name:        "Data Availability",
		Description: "Table availability status including row count and connection info",
		MIMEType:    "application/json",
	}, p.handleAvailabilityResource)
}

// parseTemplateVars extracts named variables from a URI using a URI template.
// Returns a map of variable names to their values, or an error if the URI
// doesn't match the template.
func parseTemplateVars(templateStr, uri string) (map[string]string, error) {
	tmpl, err := uritemplate.New(templateStr)
	if err != nil {
		return nil, fmt.Errorf("invalid template %q: %w", templateStr, err)
	}

	match := tmpl.Match(uri)
	if match == nil {
		return nil, fmt.Errorf("uri %q does not match template %q", uri, templateStr)
	}

	result := make(map[string]string)
	for _, name := range tmpl.Varnames() {
		val := match.Get(name)
		result[name] = val.String()
	}
	return result, nil
}

// schemaResourceResult combines query schema and semantic context.
type schemaResourceResult struct {
	Catalog     string                        `json:"catalog"`
	Schema      string                        `json:"schema"`
	Table       string                        `json:"table"`
	Columns     []query.Column                `json:"columns,omitempty"`
	Semantic    *semantic.TableContext        `json:"semantic,omitempty"`
	ColumnsMeta map[string]*columnSemanticCtx `json:"columns_semantic,omitempty"`
}

// columnSemanticCtx is the serializable subset of semantic.ColumnContext.
type columnSemanticCtx struct {
	Description   string                  `json:"description,omitempty"`
	Tags          []string                `json:"tags,omitempty"`
	GlossaryTerms []semantic.GlossaryTerm `json:"glossary_terms,omitempty"`
	IsPII         bool                    `json:"is_pii,omitempty"`
	IsSensitive   bool                    `json:"is_sensitive,omitempty"`
	BusinessName  string                  `json:"business_name,omitempty"`
}

// handleSchemaResource handles schema://{catalog}.{schema_name}/{table} requests.
func (p *Platform) handleSchemaResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	vars, err := parseTemplateVars(schemaTemplateURI, uri)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error returned as-is for SDK type matching
	}

	catalog := vars["catalog"]
	schemaName := vars["schema_name"]
	table := vars["table"]

	if catalog == "" || schemaName == "" || table == "" {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error returned as-is for SDK type matching
	}

	result := schemaResourceResult{
		Catalog: catalog,
		Schema:  schemaName,
		Table:   table,
	}

	tableID := query.TableIdentifier{Catalog: catalog, Schema: schemaName, Table: table}
	semanticTableID := semantic.TableIdentifier{Catalog: catalog, Schema: schemaName, Table: table}

	queryEnriched := p.enrichSchemaFromQuery(ctx, tableID, &result)
	semanticEnriched := p.enrichSchemaFromSemantic(ctx, semanticTableID, &result)
	hasData := queryEnriched || semanticEnriched

	if !hasData {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error returned as-is for SDK type matching
	}

	return marshalResourceResult(uri, result)
}

// enrichSchemaFromQuery populates the schema result with query provider data.
// Returns true if any data was added.
func (p *Platform) enrichSchemaFromQuery(ctx context.Context, tableID query.TableIdentifier, result *schemaResourceResult) bool {
	if p.queryProvider == nil {
		return false
	}
	schema, err := p.queryProvider.GetTableSchema(ctx, tableID)
	if err != nil || schema == nil {
		return false
	}
	result.Columns = schema.Columns
	return true
}

// enrichSchemaFromSemantic populates the schema result with semantic provider data.
// Returns true if any data was added.
func (p *Platform) enrichSchemaFromSemantic(ctx context.Context, tableID semantic.TableIdentifier, result *schemaResourceResult) bool {
	if p.semanticProvider == nil {
		return false
	}

	hasData := false

	tableCtx, err := p.semanticProvider.GetTableContext(ctx, tableID)
	if err == nil && tableCtx != nil {
		result.Semantic = tableCtx
		hasData = true
	}

	colsCtx, err := p.semanticProvider.GetColumnsContext(ctx, tableID)
	if err == nil && len(colsCtx) > 0 {
		result.ColumnsMeta = toColumnSemanticMap(colsCtx)
		hasData = true
	}

	return hasData
}

// toColumnSemanticMap converts semantic column contexts to the serializable format.
func toColumnSemanticMap(colsCtx map[string]*semantic.ColumnContext) map[string]*columnSemanticCtx {
	out := make(map[string]*columnSemanticCtx, len(colsCtx))
	for name, cc := range colsCtx {
		out[name] = &columnSemanticCtx{
			Description:   cc.Description,
			Tags:          cc.Tags,
			GlossaryTerms: cc.GlossaryTerms,
			IsPII:         cc.IsPII,
			IsSensitive:   cc.IsSensitive,
			BusinessName:  cc.BusinessName,
		}
	}
	return out
}

// handleGlossaryResource handles glossary://{term} requests.
func (p *Platform) handleGlossaryResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	vars, err := parseTemplateVars(glossaryTemplateURI, uri)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	term := vars["term"]
	if term == "" {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	if p.semanticProvider == nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	urn := "urn:li:glossaryTerm:" + term
	glossary, err := p.semanticProvider.GetGlossaryTerm(ctx, urn)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	return marshalResourceResult(uri, glossary)
}

// availabilityResourceResult wraps availability info for serialization.
type availabilityResourceResult struct {
	Catalog    string `json:"catalog"`
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Available  bool   `json:"available"`
	QueryTable string `json:"query_table,omitempty"`
	Connection string `json:"connection,omitempty"`
	EstRows    *int64 `json:"estimated_rows,omitempty"`
	Error      string `json:"error,omitempty"`
}

// handleAvailabilityResource handles availability://{catalog}.{schema_name}/{table} requests.
func (p *Platform) handleAvailabilityResource(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	vars, err := parseTemplateVars(availabilityTemplateURI, uri)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	catalog := vars["catalog"]
	schemaName := vars["schema_name"]
	table := vars["table"]

	if catalog == "" || schemaName == "" || table == "" {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	if p.queryProvider == nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	// Build the DataHub URN for lookup.
	urn := buildDataHubURN(p.config.Semantic.URNMapping, catalog, schemaName, table)

	avail, err := p.queryProvider.GetTableAvailability(ctx, urn)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	result := availabilityResourceResult{
		Catalog:    catalog,
		Schema:     schemaName,
		Table:      table,
		Available:  avail.Available,
		QueryTable: avail.QueryTable,
		Connection: avail.Connection,
		EstRows:    avail.EstimatedRows,
		Error:      avail.Error,
	}

	return marshalResourceResult(uri, result)
}

// buildDataHubURN constructs a DataHub dataset URN from table components.
// Uses URN mapping config to determine the platform name and catalog mapping.
func buildDataHubURN(mapping URNMappingConfig, catalog, schema, table string) string {
	platform := mapping.Platform
	if platform == "" {
		platform = toolkitKindTrino
	}

	// Apply catalog mapping if configured.
	mappedCatalog := catalog
	if mapped, ok := mapping.CatalogMapping[catalog]; ok {
		mappedCatalog = mapped
	}

	return fmt.Sprintf("urn:li:dataset:(urn:li:dataPlatform:%s,%s.%s.%s,PROD)",
		platform,
		mappedCatalog,
		schema,
		table,
	)
}

// marshalResourceResult marshals a value to JSON and wraps it in a ReadResourceResult.
func marshalResourceResult(uri string, v any) (*mcp.ReadResourceResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling resource %s: %w", uri, err)
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{
				URI:      uri,
				MIMEType: "application/json",
				Text:     string(data),
			},
		},
	}, nil
}
