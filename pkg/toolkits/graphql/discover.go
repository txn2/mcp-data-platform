package graphql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/gqlschema"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// The two depths graphql_discover answers at, reported in
// DiscoverOutput.Level so a caller can branch on the shape it received
// rather than on which keys happen to be present.
const (
	// DiscoverLevelOperations is a ranked list of operations.
	DiscoverLevelOperations = "operations"
	// DiscoverLevelOperation is one operation's arguments, return shape
	// and runnable skeleton.
	DiscoverLevelOperation = "operation"
)

// defaultDiscoverLimit caps the operations returned when the caller
// names no limit. Generous enough for a casual look at a schema,
// bounded enough that a large one does not fill the model's context;
// a caller asks for more by passing limit.
const defaultDiscoverLimit = 50

// defaultSelectionDepth is how deep graphql_discover renders a return
// shape and its skeleton when the caller names no depth.
const defaultSelectionDepth = gqlschema.DefaultSelectionDepth

// OperationSummary is the slim per-operation view a ranked list
// returns. It carries what a caller needs to choose an operation and to
// ask for it by id, and nothing more: the full argument and return
// detail is one call away behind operation_id.
type OperationSummary struct {
	// OperationID is the kind-prefixed dotted id
	// ("query:masterData.product.query"). It is what operation_id
	// takes.
	OperationID string `json:"operation_id"`
	// Kind is QUERY or MUTATION. It is also the method a persona rule
	// names for this operation.
	Kind string `json:"kind"`
	// Path is the path a persona rule names this operation under: the
	// dotted id with dots as slashes.
	Path string `json:"path"`
	// Summary is the descriptions the schema carries along the
	// operation's path.
	Summary string `json:"summary,omitempty"`
	// ReturnType is the rendered type the operation returns.
	ReturnType string `json:"return_type,omitempty"`
	// Arguments are the operation's argument names, rendered
	// "name: Type" so a caller can see what it takes without a second
	// call.
	Arguments []string `json:"arguments,omitempty"`
	// Deprecated reports the schema marking this operation
	// @deprecated.
	Deprecated bool `json:"deprecated,omitempty"`
}

// RankedOperation is one operation in a list, plus what put it there
// when a query ranked it. Score and LexicalMatch are pointers because
// both are absent from an unranked list and a zero score is a real
// score.
type RankedOperation struct {
	OperationSummary
	Score        *float64 `json:"score,omitempty"`
	LexicalMatch *bool    `json:"lexical_match,omitempty"`
}

// OperationDetail is one operation at the operation level: everything
// needed to write the call, plus a document that already makes it.
type OperationDetail struct {
	OperationSummary
	// ArgumentDetails are the operation's arguments with their types,
	// descriptions, defaults and whether they are required.
	ArgumentDetails []gqlschema.Argument `json:"argument_details,omitempty"`
	// InputTypes expands the input-object types the arguments
	// reference, so a caller filling in a filter or a create payload
	// does not need a second lookup.
	InputTypes []gqlschema.InputType `json:"input_types,omitempty"`
	// ReturnShape is the return type's field tree, depth-limited.
	ReturnShape []gqlschema.FieldNode `json:"return_shape,omitempty"`
	// Skeleton is a document that validates against this connection's
	// schema and calls this operation, with every argument bound to a
	// variable. It is the load-bearing part of this level: it is the
	// difference between a correct selection on the first try and
	// several calls spent on validation errors.
	Skeleton string `json:"skeleton"`
	// Variables is a JSON object carrying one entry per required
	// argument, ready to edit and pass as graphql_query's variables.
	// An optional argument is declared in the skeleton but absent here,
	// so leaving it out sends the schema's own default; add its key to
	// set it.
	Variables string `json:"variables"`
}

// DiscoverInput is the parsed argument shape for graphql_discover.
type DiscoverInput struct {
	Connection  string `json:"connection"`
	Query       string `json:"query,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Ranking     string `json:"ranking,omitempty"`
	Depth       int    `json:"depth,omitempty"`
}

// DiscoverOutput is the structured result at both levels. Level names
// the shape; exactly one of Operations and Operation is populated for
// it. Next says what argument goes one level deeper.
type DiscoverOutput struct {
	Level      string            `json:"level"`
	Connection string            `json:"connection"`
	Operations []RankedOperation `json:"operations,omitempty"`
	Operation  *OperationDetail  `json:"operation,omitempty"`
	// SchemaHash and SchemaFetchedAt identify the schema version this
	// answer was read from, so a caller comparing two answers can see
	// whether the schema moved underneath them.
	SchemaHash      string `json:"schema_hash,omitempty"`
	SchemaFetchedAt string `json:"schema_fetched_at,omitempty"`
	// MatchedLexical and ShownSemantic report where relevance ended:
	// how many operations contain every token, and how many followed
	// them as neighbors by intent. Absent unless the call carried a
	// query.
	MatchedLexical *int   `json:"matched_lexical,omitempty"`
	ShownSemantic  *int   `json:"shown_semantic,omitempty"`
	Note           string `json:"note,omitempty"`
	Next           string `json:"next,omitempty"`
}

// discoverTool describes graphql_discover to the model.
func discoverTool() *mcp.Tool {
	return &mcp.Tool{
		Name:  ToolDiscover,
		Title: "Discover GraphQL Operations",
		Description: "Find what a registered GraphQL connection can do, and get a document that runs it, " +
			"before calling graphql_query. With a query, returns the matching operations ranked " +
			"(operation_id, kind, path, summary, return_type, arguments). With operation_id, returns that " +
			"operation's arguments with their types and defaults, the input-object types they reference, " +
			"the return type's field tree, and a ready-to-edit skeleton document with a variables stub — " +
			"the skeleton already carries the selection set the operation needs, including the " +
			"edges { node { ... } } shape of a paged connection. The schema is the one the platform read " +
			"from this connection's endpoint; schema_hash and schema_fetched_at say which version answered. " +
			"Operations the persona's route rules deny are absent, and those rules still apply at query time. " +
			"Use list_connections to discover available kind=graphql connections.",
		InputSchema: discoverSchema,
		Annotations: toolkit.ReadOnlyAnnotations(),
	}
}

// handleDiscover serves both levels of graphql_discover.
func (t *Toolkit) handleDiscover(ctx context.Context, _ *mcp.CallToolRequest, in DiscoverInput) (*mcp.CallToolResult, any, error) {
	if in.Connection == "" {
		return toolkit.ErrorResult("connection is required"), nil, nil
	}
	c, policy, ok := t.lookup(in.Connection)
	if !ok {
		return toolkit.ErrorResult(fmt.Sprintf(
			"connection %q not found (use list_connections to discover graphql connections)", in.Connection)), nil, nil
	}
	mode, err := ParseRankingMode(in.Ranking)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	c.schemaMu.RLock()
	schema, ops, schemaErr := c.schema, c.operations, c.schemaErr
	fetchedAt := c.fetchedAt
	c.schemaMu.RUnlock()
	if schema == nil {
		return toolkit.ErrorResult(noSchemaMessage(in.Connection, schemaErr)), nil, nil
	}
	out := DiscoverOutput{
		Connection:      in.Connection,
		SchemaHash:      schema.Hash(),
		SchemaFetchedAt: fetchedAt.UTC().Format(time.RFC3339),
	}
	visible := filterByRoutePolicy(ctx, policy, c.cfg.ConnectionName, ops)
	if in.OperationID != "" {
		return t.discoverOperation(in, schema, visible, out)
	}
	return t.discoverOperations(ctx, operationsRequest{in: in, conn: c, visible: visible, mode: mode, out: out})
}

// noSchemaMessage explains a connection the platform holds no schema
// for, naming the cause when there is one rather than reporting an
// empty schema.
func noSchemaMessage(connection, schemaErr string) string {
	msg := fmt.Sprintf("connection %q has no schema: the platform could not read one from its endpoint", connection)
	if schemaErr != "" {
		msg += " (" + schemaErr + ")"
	}
	return msg + ". An administrator can retry the read, or upload the schema, from Admin > Connections."
}

// discoverOperation answers the operation level.
func (*Toolkit) discoverOperation(in DiscoverInput, schema *gqlschema.Schema, visible []gqlschema.Operation, out DiscoverOutput) (*mcp.CallToolResult, any, error) {
	op, found := gqlschema.Lookup(visible, in.OperationID)
	if !found {
		return toolkit.ErrorResult(fmt.Sprintf(
			"operation %q not found on connection %q (call graphql_discover with a query to list its operations; an operation your persona denies is absent from that list)",
			in.OperationID, in.Connection)), nil, nil
	}
	depth := in.Depth
	if depth <= 0 {
		depth = defaultSelectionDepth
	}
	detail, err := gqlschema.Describe(schema, op, depth)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	out.Level = DiscoverLevelOperation
	out.Operation = &OperationDetail{
		OperationSummary: summarize(op),
		ArgumentDetails:  op.Arguments,
		InputTypes:       detail.InputTypes,
		ReturnShape:      detail.ReturnShape,
		Skeleton:         detail.Skeleton,
		Variables:        detail.Variables,
	}
	out.Next = "call graphql_query with query set to the skeleton (edit its selection set to the fields you need) and variables set to the stub"
	return toolkit.JSONResult(out), out, nil
}

// operationsRequest bundles what the operations level needs, keeping the
// call site under the argument ceiling.
type operationsRequest struct {
	in      DiscoverInput
	conn    *conn
	visible []gqlschema.Operation
	mode    RankingMode
	out     DiscoverOutput
}

// discoverOperations answers the operations level.
func (t *Toolkit) discoverOperations(ctx context.Context, r operationsRequest) (*mcp.CallToolResult, any, error) {
	in, c, visible, mode, out := r.in, r.conn, r.visible, r.mode, r.out
	limit := in.Limit
	if limit <= 0 {
		limit = defaultDiscoverLimit
	}
	// Default-on semantic ranking: an omitted ranking resolves to
	// hybrid whenever this connection has an embedding index, so an
	// intent query is ranked rather than failing the lexical AND filter
	// closed. An explicit ranking is preserved, and an empty query is
	// served unranked whatever the mode.
	defaulted := in.Query != "" && in.Ranking == "" && t.embeddingsAvailable(c)
	if mode == "" {
		mode = RankingLexical
		if defaulted {
			mode = RankingHybrid
		}
	}
	ranked := t.rank(ctx, rankRequest{conn: c, ops: visible, query: in.Query, limit: limit, mode: mode})
	out.Level = DiscoverLevelOperations
	out.Operations = ranked.operations
	out.Note = rankingNote(defaulted, mode, ranked)
	out.Next = "call graphql_discover again with operation_id set to one of these, to get its arguments, its return shape, and a document that runs it"
	if strings.TrimSpace(in.Query) != "" {
		matched, shown := ranked.matchedLexical, ranked.shownSemantic
		out.MatchedLexical, out.ShownSemantic = &matched, &shown
	}
	return toolkit.JSONResult(out), out, nil
}

// rankingNote explains a result the caller would otherwise have to
// guess at: a ranking that fell back, and a query that matched nothing
// or only neighbors.
func rankingNote(defaulted bool, mode RankingMode, ranked rankedOperations) string {
	if ranked.fallbackReason != "" {
		if defaulted {
			return "ranked lexically: " + ranked.fallbackReason
		}
		return fmt.Sprintf("%s ranking fell back to lexical: %s", mode, ranked.fallbackReason)
	}
	if len(ranked.operations) == 0 {
		return "no operation matched. Try fewer words, or call again with no query to see what this connection exposes."
	}
	if ranked.matchedLexical == 0 && ranked.shownSemantic > 0 {
		return "no operation contains every word you used; these are the closest by intent."
	}
	return ""
}

// summarize renders one operation's slim view.
func summarize(op gqlschema.Operation) OperationSummary {
	s := OperationSummary{
		OperationID: op.ID,
		Kind:        string(op.Kind),
		Path:        op.PolicyPath(),
		Summary:     op.Description,
		ReturnType:  op.ReturnType,
		Deprecated:  op.Deprecated,
	}
	for _, a := range op.Arguments {
		s.Arguments = append(s.Arguments, a.Name+": "+a.Type)
	}
	return s
}
