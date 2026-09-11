package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// maxTimeoutSeconds bounds what a caller may ask for on one call. The
// connection's own call_timeout still applies and is the lower of the
// two.
const maxTimeoutSeconds = 600

// QueryInput is the parsed argument shape for graphql_query.
//
// Variables is raw because the schema admits two forms: the object a
// GraphQL client sends, and a string holding that object's JSON, which
// is what a client that stringifies structured arguments sends. Both
// reach the same upstream request (#1548).
type QueryInput struct {
	Connection     string          `json:"connection"`
	Query          string          `json:"query"`
	Variables      json.RawMessage `json:"variables,omitempty"`
	OperationName  string          `json:"operation_name,omitempty"`
	TimeoutSeconds int             `json:"timeout_seconds,omitempty"`
	Paginate       *PaginateInput  `json:"paginate,omitempty"`
}

// QueryOutput is what a caller gets back from one document.
type QueryOutput struct {
	Connection string `json:"connection"`
	// Kind is QUERY or MUTATION: the operation kind that executed, and
	// the method the persona rules authorized it under.
	Kind string `json:"kind"`
	// OperationName is the operation that ran, when the document named
	// one.
	OperationName string `json:"operation_name,omitempty"`
	// Operations are the dotted operation ids this document invoked,
	// the same ids graphql_discover reports and persona rules name.
	Operations []string `json:"operations,omitempty"`
	// Status is the HTTP status the endpoint answered with. It is
	// almost always 200, including for a failure: what decides the
	// outcome is upstream_error.
	Status int `json:"status"`
	// Data is the GraphQL data object, as sent. Present even alongside
	// errors: a partial result is preserved rather than discarded.
	Data json.RawMessage `json:"data,omitempty"`
	// Errors is the response's errors array, passed through unchanged.
	Errors []Error `json:"errors,omitempty"`
	// Extensions is the response's extensions object, passed through.
	Extensions map[string]any `json:"extensions,omitempty"`
	// UpstreamError reports a call that failed however it was
	// transported: a non-2xx, or a 200 carrying errors. It is what the
	// platform's audit, call catalog and metrics classify the call on.
	UpstreamError bool `json:"upstream_error"`
	// ValidationWarnings are the schema violations found in a
	// connection whose schema_validation is warn. Empty under strict,
	// where a violation refuses the call instead.
	ValidationWarnings []string `json:"validation_warnings,omitempty"`
	// DataBytes is the size of the data read.
	DataBytes int `json:"data_bytes"`
	// DataTruncated reports data withheld because the rendered result
	// would exceed this connection's max_inline_bytes. A cut JSON
	// document cannot be parsed, so the data is omitted rather than
	// halved; export_arguments carries the call that streams it whole.
	DataTruncated bool `json:"data_truncated,omitempty"`
	// ExportArguments is the graphql_export call that writes this same
	// result to an asset, present when the data did not fit inline.
	ExportArguments map[string]any `json:"export_arguments,omitempty"`
	// Pagination reports a walk that ran.
	Pagination *PaginationReport `json:"pagination,omitempty"`
	// Note explains anything that changed the answer.
	Note string `json:"note,omitempty"`
}

// queryTool describes graphql_query to the model.
func queryTool() *mcp.Tool {
	return &mcp.Tool{
		Name:  ToolQuery,
		Title: "Run a GraphQL Document",
		Description: "Execute a GraphQL query or mutation against a registered GraphQL connection. The " +
			"connection's credential is applied automatically; the model never handles it. Before anything " +
			"is sent the document is parsed, the operation to run is resolved (operation_name is required " +
			"when the document defines more than one), and it is validated against the schema the platform " +
			"read from this endpoint — a connection in strict mode refuses a document naming a field its " +
			"schema does not have, naming the connection and when its schema was read. Subscriptions and " +
			"__schema / __type selections are refused; call graphql_discover for the schema. " +
			"A GraphQL failure arrives as HTTP 200 with an errors array: the result reports upstream_error " +
			"and passes the errors through unchanged, with any partial data preserved. Pass paginate to " +
			"walk a paged connection in this one call and receive the merged array. Write the document with " +
			"graphql_discover, whose skeleton already validates. " + toolkit.CaptureRoute,
		InputSchema: querySchema,
		Annotations: toolkit.WriteAnnotations(true),
	}
}

// handleQuery serves graphql_query.
func (t *Toolkit) handleQuery(ctx context.Context, _ *mcp.CallToolRequest, in QueryInput) (*mcp.CallToolResult, any, error) {
	prepared, errMsg := t.prepare(ctx, in)
	if errMsg != "" {
		return toolkit.ErrorResult(errMsg), nil, nil
	}
	ctx, cancel := withCallTimeout(ctx, prepared.conn.cfg, in.TimeoutSeconds)
	defer cancel()

	out, err := t.runPrepared(ctx, prepared, in.Paginate)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	t.fitInline(prepared, in, out)
	result := toolkit.JSONResult(out)
	stampAuditOutcome(result, classifyUpstream(out.Status, out.Errors, out.UpstreamError))
	return result, out, nil
}

// prepared is one authorized, validated call, ready to send.
type prepared struct {
	conn      *conn
	doc       *gqlschema.Document
	variables map[string]any
	warnings  []string
	invoked   []string
}

// prepare parses, validates and authorizes a document. The returned
// string is the caller-facing refusal when it is not empty.
func (t *Toolkit) prepare(ctx context.Context, in QueryInput) (ready prepared, refusal string) {
	c, policy, refusal := t.resolve(in)
	if refusal != "" {
		return prepared{}, refusal
	}
	variables, err := decodeVariables(in.Variables)
	if err != nil {
		return prepared{}, err.Error()
	}
	doc, err := gqlschema.Parse(in.Query, in.OperationName)
	if err != nil {
		return prepared{}, err.Error()
	}
	if doc.HasIntrospectionSelection() {
		return prepared{}, gqlschema.ErrIntrospectionSelection.Error()
	}
	if depth := doc.Depth(); depth > c.cfg.MaxQueryDepth {
		return prepared{}, fmt.Sprintf(
			"this document selects %d levels deep; connection %q allows %d (max_query_depth). Select fewer nested levels, or ask an administrator to raise the limit.",
			depth, in.Connection, c.cfg.MaxQueryDepth)
	}
	warnings, refusal := t.checkSchema(c, doc)
	if refusal != "" {
		return prepared{}, refusal
	}
	if err := t.authorizeDocument(ctx, policy, c, doc); err != nil {
		return prepared{}, err.Error()
	}
	c.schemaMu.RLock()
	ops := c.operations
	c.schemaMu.RUnlock()
	return prepared{
		conn:      c,
		doc:       doc,
		variables: variables,
		warnings:  warnings,
		invoked:   doc.InvokedPaths(gqlschema.NewPathIndex(ops, doc.Kind())),
	}, ""
}

// resolve finds the connection a document is for and refuses one that
// cannot take a document. A connection with no schema is one of those:
// the operation index is what a document is reduced to and authorized
// under, and with none the reduction would fall back to the document's
// root fields, which no persona rule for this kind is written against
// (#1676). The refusal names the cause the connection recorded, the
// same one graphql_discover reports.
func (t *Toolkit) resolve(in QueryInput) (c *conn, policy RoutePolicy, refusal string) {
	if in.Connection == "" {
		return nil, nil, "connection is required"
	}
	if strings.TrimSpace(in.Query) == "" {
		return nil, nil, "query is required: pass the GraphQL document to execute (graphql_discover renders one)"
	}
	c, policy, ok := t.lookup(in.Connection)
	if !ok {
		return nil, nil, fmt.Sprintf(
			"connection %q not found (use list_connections to discover graphql connections)", in.Connection)
	}
	c.schemaMu.RLock()
	schema, schemaErr := c.schema, c.schemaErr
	c.schemaMu.RUnlock()
	if schema == nil {
		return nil, nil, noSchemaMessage(in.Connection, schemaErr)
	}
	return c, policy, ""
}

// checkSchema validates a document against the connection's schema.
// Under strict the violations are a refusal naming the connection and
// the schema's fetch time, so a caller working from a newer schema than
// the platform holds can see that is what happened. Under warn they are
// carried on the result instead.
func (*Toolkit) checkSchema(c *conn, doc *gqlschema.Document) (warnings []string, refusal string) {
	c.schemaMu.RLock()
	schema, fetchedAt := c.schema, c.fetchedAt
	c.schemaMu.RUnlock()
	violations := doc.Validate(schema)
	if len(violations) == 0 {
		return nil, ""
	}
	if c.cfg.SchemaValidation == SchemaValidationWarn {
		return violations, ""
	}
	return nil, fmt.Sprintf(
		"this document does not validate against the schema the platform holds for connection %q, read %s: %s. Call graphql_discover to see what this connection's schema exposes; an administrator can re-read the schema from Admin > Connections if the endpoint has changed.",
		c.cfg.ConnectionName, fetchedAt.UTC().Format(time.RFC3339), strings.Join(violations, "; "))
}

// runPrepared sends the document, walking pages when asked to.
func (t *Toolkit) runPrepared(ctx context.Context, p prepared, paginate *PaginateInput) (*QueryOutput, error) {
	out := &QueryOutput{
		Connection:         p.conn.cfg.ConnectionName,
		Kind:               string(p.doc.Kind()),
		OperationName:      p.doc.Operation.Name,
		Operations:         p.invoked,
		ValidationWarnings: p.warnings,
	}
	if len(p.warnings) > 0 {
		out.Note = "this connection's schema_validation is warn: the document was sent despite not validating against the stored schema."
	}
	if paginate != nil {
		return out, t.walkPages(ctx, p, *paginate, out)
	}
	res, err := t.execute(ctx, p.conn, request(p.doc, p.variables))
	if err != nil {
		return nil, err
	}
	applyExecution(out, res)
	return out, nil
}

// request builds the wire body for one send. The document goes out as
// the caller wrote it: the platform never re-prints it, so a formatting
// quirk of theirs is never mistaken for a semantic change.
func request(doc *gqlschema.Document, vars map[string]any) graphQLRequest {
	return graphQLRequest{Query: doc.Raw, Variables: vars, OperationName: doc.Operation.Name}
}

// applyExecution copies one round trip's answer onto the result.
func applyExecution(out *QueryOutput, res *execution) {
	out.Status = res.status
	out.DataBytes = len(res.body)
	out.UpstreamError = res.upstreamFailed()
	if res.parsed == nil {
		out.Note = strings.TrimSpace(out.Note + " The endpoint's answer was not a GraphQL response: " + snippet(string(res.body)))
		return
	}
	out.Data = res.parsed.Data
	out.Errors = res.parsed.Errors
	out.Extensions = res.parsed.Extensions
}

// fitInline enforces the connection's inline budget. A JSON document
// cut in half cannot be parsed, so data past the budget is withheld
// whole and the export call that streams it is handed back instead.
func (*Toolkit) fitInline(p prepared, in QueryInput, out *QueryOutput) {
	rendered, err := json.Marshal(out)
	if err != nil || int64(len(rendered)) <= p.conn.cfg.MaxInlineBytes {
		return
	}
	out.Data = nil
	out.DataTruncated = true
	out.ExportArguments = map[string]any{
		"connection": in.Connection,
		"query":      in.Query,
	}
	if len(in.Variables) > 0 {
		out.ExportArguments["variables"] = in.Variables
	}
	if in.OperationName != "" {
		out.ExportArguments["operation_name"] = in.OperationName
	}
	out.Note = strings.TrimSpace(out.Note + fmt.Sprintf(
		" The result held %d bytes of data, past this connection's max_inline_bytes; call graphql_export with export_arguments to write it to an asset and read it from there.",
		out.DataBytes))
}

// decodeVariables reads the variables argument in either form the
// schema admits: the object itself, or a string holding that object's
// JSON. A client that stringifies structured arguments sends the
// second, and refusing it would make the tool work for some clients and
// not others (#1548).
func decodeVariables(raw json.RawMessage) (vars map[string]any, err error) {
	if len(raw) == 0 {
		return nil, nil //nolint:nilnil // no variables is the absence, not a failure
	}
	var asObject map[string]any
	if err := json.Unmarshal(raw, &asObject); err == nil {
		return asObject, nil
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err != nil {
		return nil, errors.New("graphql: variables must be a JSON object, or a string holding one")
	}
	if strings.TrimSpace(asString) == "" {
		return nil, nil //nolint:nilnil // as above: an empty string carries no variables
	}
	if err := json.Unmarshal([]byte(asString), &asObject); err != nil {
		return nil, fmt.Errorf("graphql: variables was a string but does not hold a JSON object: %w", err)
	}
	return asObject, nil
}

// withCallTimeout bounds one call. The caller may ask for less than the
// connection's call_timeout but never more: the connection's limit is
// the operator's, and a tool argument does not raise it.
func withCallTimeout(ctx context.Context, cfg Config, seconds int) (context.Context, context.CancelFunc) {
	limit := cfg.CallTimeout
	if limit <= 0 {
		limit = DefaultCallTimeout
	}
	if seconds > 0 && seconds <= maxTimeoutSeconds {
		if asked := time.Duration(seconds) * time.Second; asked < limit {
			limit = asked
		}
	}
	return context.WithTimeout(ctx, limit)
}
