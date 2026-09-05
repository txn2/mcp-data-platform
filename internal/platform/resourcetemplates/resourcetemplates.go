// Package resourcetemplates serves the platform's three read-only MCP resource
// templates: a table's schema with its semantic context, a glossary term, and
// a table's query availability.
//
// They are templates rather than resources because the address is the query: a
// client fills in the catalog, schema and table it wants and reads the result,
// so nothing has to be listed for one to be reachable. Each answers from the
// providers alone and writes nothing, which is why the whole surface fits
// behind one set of dependencies and needed no part of the platform facade
// beyond them.
//
// Extracted from pkg/platform, which the package size budget names as the next
// decomposition target (#1628).
package resourcetemplates

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yosida95/uritemplate/v3"

	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/urnbuild"
)

// The URI patterns the three templates are addressed by. Exported because the
// completion layer answers argument completions for these same templates and
// matches on the pattern: one definition, so a changed pattern cannot leave
// completions answering for an address nothing is registered at.
const (
	SchemaURI       = "schema://{catalog}.{schema_name}/{table}"
	GlossaryURI     = "glossary://{term}"
	AvailabilityURI = "availability://{catalog}.{schema_name}/{table}"
)

// mimeApplicationJSON is the MIME type for JSON-encoded resource bodies.
const mimeApplicationJSON = "application/json"

// Deps is what the templates answer from. Both providers are optional: a
// deployment with neither still registers the templates and answers every read
// as not found, which is what a client asking about a table this deployment
// cannot see should be told.
type Deps struct {
	Semantic semantic.Provider
	Query    query.Provider
	// URNPlatform and URNCatalogMapping build the catalog URN an availability
	// lookup is keyed by, from semantic.urn_mapping.
	URNPlatform       string
	URNCatalogMapping map[string]string
}

// Handler serves the three templates.
type Handler struct {
	deps Deps
}

// New builds the handler. The providers are read once here because they are
// fixed for the life of the process: the platform resolves them during
// initialization, before anything is registered.
func New(d Deps) *Handler {
	return &Handler{deps: d}
}

// Register adds the three templates to the server.
func (h *Handler) Register(server *mcp.Server) {
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: SchemaURI,
		Name:        "Table Schema",
		Description: "Table schema with column types and semantic context (descriptions, owners, tags, glossary terms)",
		MIMEType:    mimeApplicationJSON,
	}, h.Schema)

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: GlossaryURI,
		Name:        "Glossary Term",
		Description: "Business glossary term definition and related assets",
		MIMEType:    mimeApplicationJSON,
	}, h.Glossary)

	server.AddResourceTemplate(&mcp.ResourceTemplate{
		URITemplate: AvailabilityURI,
		Name:        "Data Availability",
		Description: "Table availability status including row count and connection info",
		MIMEType:    mimeApplicationJSON,
	}, h.Availability)
}

// parseVars extracts named variables from a URI using a URI template, or
// reports that the URI does not match it.
func parseVars(templateStr, uri string) (map[string]string, error) {
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

// schemaResult combines query schema and semantic context.
type schemaResult struct {
	Catalog     string                   `json:"catalog"`
	Schema      string                   `json:"schema"`
	Table       string                   `json:"table"`
	Columns     []query.Column           `json:"columns,omitempty"`
	Semantic    *semantic.TableContext   `json:"semantic,omitempty"`
	ColumnsMeta map[string]*columnResult `json:"columns_semantic,omitempty"`
}

// columnResult is the serializable subset of semantic.ColumnContext.
type columnResult struct {
	Description   string                  `json:"description,omitempty"`
	Tags          []string                `json:"tags,omitempty"`
	GlossaryTerms []semantic.GlossaryTerm `json:"glossary_terms,omitempty"`
	IsPII         bool                    `json:"is_pii,omitempty"`
	IsSensitive   bool                    `json:"is_sensitive,omitempty"`
	BusinessName  string                  `json:"business_name,omitempty"`
}

// Schema answers schema://{catalog}.{schema_name}/{table}.
func (h *Handler) Schema(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	vars, err := parseVars(SchemaURI, uri)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error returned as-is for SDK type matching
	}

	catalog, schemaName, table := vars["catalog"], vars["schema_name"], vars["table"]
	if catalog == "" || schemaName == "" || table == "" {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error returned as-is for SDK type matching
	}

	result := schemaResult{Catalog: catalog, Schema: schemaName, Table: table}
	tableID := query.TableIdentifier{Catalog: catalog, Schema: schemaName, Table: table}
	semanticTableID := semantic.TableIdentifier{Catalog: catalog, Schema: schemaName, Table: table}

	// Both are consulted before the verdict: a table the query engine knows
	// and the catalog does not is still a hit, and so is the reverse.
	fromQuery := h.enrichFromQuery(ctx, tableID, &result)
	fromSemantic := h.enrichFromSemantic(ctx, semanticTableID, &result)
	if !fromQuery && !fromSemantic {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error returned as-is for SDK type matching
	}

	return marshalResult(uri, result)
}

// enrichFromQuery populates the schema result with query provider data,
// reporting whether any was added.
func (h *Handler) enrichFromQuery(ctx context.Context, tableID query.TableIdentifier, result *schemaResult) bool {
	if h.deps.Query == nil {
		return false
	}
	schema, err := h.deps.Query.GetTableSchema(ctx, tableID)
	if err != nil || schema == nil {
		return false
	}
	result.Columns = schema.Columns
	return true
}

// enrichFromSemantic populates the schema result with semantic provider data,
// reporting whether any was added.
func (h *Handler) enrichFromSemantic(ctx context.Context, tableID semantic.TableIdentifier, result *schemaResult) bool {
	if h.deps.Semantic == nil {
		return false
	}

	hasData := false

	tableCtx, err := h.deps.Semantic.GetTableContext(ctx, tableID)
	if err == nil && tableCtx != nil {
		result.Semantic = tableCtx
		hasData = true
	}

	colsCtx, err := h.deps.Semantic.GetColumnsContext(ctx, tableID)
	if err == nil && len(colsCtx) > 0 {
		result.ColumnsMeta = toColumnMap(colsCtx)
		hasData = true
	}

	return hasData
}

// toColumnMap converts semantic column contexts to the serializable format.
func toColumnMap(colsCtx map[string]*semantic.ColumnContext) map[string]*columnResult {
	out := make(map[string]*columnResult, len(colsCtx))
	for name, cc := range colsCtx {
		out[name] = &columnResult{
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

// Glossary answers glossary://{term}.
func (h *Handler) Glossary(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	vars, err := parseVars(GlossaryURI, uri)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	term := vars["term"]
	if term == "" || h.deps.Semantic == nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	glossary, err := h.deps.Semantic.GetGlossaryTerm(ctx, "urn:li:glossaryTerm:"+term)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	return marshalResult(uri, glossary)
}

// availabilityResult wraps availability info for serialization.
type availabilityResult struct {
	Catalog    string `json:"catalog"`
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Available  bool   `json:"available"`
	QueryTable string `json:"query_table,omitempty"`
	Connection string `json:"connection,omitempty"`
	EstRows    *int64 `json:"estimated_rows,omitempty"`
	Error      string `json:"error,omitempty"`
}

// Availability answers availability://{catalog}.{schema_name}/{table}.
func (h *Handler) Availability(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
	uri := req.Params.URI
	vars, err := parseVars(AvailabilityURI, uri)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	catalog, schemaName, table := vars["catalog"], vars["schema_name"], vars["table"]
	if catalog == "" || schemaName == "" || table == "" || h.deps.Query == nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	urn := urnbuild.DatasetURN(h.deps.URNPlatform, h.deps.URNCatalogMapping, catalog, schemaName, table)
	avail, err := h.deps.Query.GetTableAvailability(ctx, urn)
	if err != nil {
		return nil, mcp.ResourceNotFoundError(uri) //nolint:wrapcheck // MCP protocol error
	}

	return marshalResult(uri, availabilityResult{
		Catalog:    catalog,
		Schema:     schemaName,
		Table:      table,
		Available:  avail.Available,
		QueryTable: avail.QueryTable,
		Connection: avail.Connection,
		EstRows:    avail.EstimatedRows,
		Error:      avail.Error,
	})
}

// marshalResult marshals a value to JSON and wraps it in a ReadResourceResult.
func marshalResult(uri string, v any) (*mcp.ReadResourceResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling resource %s: %w", uri, err)
	}
	return &mcp.ReadResourceResult{
		Contents: []*mcp.ResourceContents{
			{URI: uri, MIMEType: mimeApplicationJSON, Text: string(data)},
		},
	}, nil
}
