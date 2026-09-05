package apigateway

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ToolDiscover is the MCP tool name for API-connection discovery. One
// tool answers "what does this connection expose" at the depth the
// caller sets: the component specs of a multi-spec catalog, the
// operations of one spec or of a query, or one operation's schema
// (#1592). Exported so audit code and tests reference the same literal
// as the registration site.
const ToolDiscover = "api_discover"

// The three depths api_discover answers at, reported in
// DiscoverOutput.Level so a caller can branch on the shape it received
// rather than on which keys happen to be present.
const (
	// DiscoverLevelSpecs is the catalog's component specs: a bare call
	// on a multi-spec catalog.
	DiscoverLevelSpecs = "specs"
	// DiscoverLevelOperations is a ranked list of operations: a call
	// with spec or query, or a bare call on a single-spec catalog.
	DiscoverLevelOperations = "operations"
	// DiscoverLevelOperation is one operation's full schema: a call
	// with operation_id.
	DiscoverLevelOperation = "operation"
)

// DiscoverInput is the parsed argument shape for api_discover. Field
// names match the JSON schema.
//
// Depth is decided by which arguments are set. OperationID selects the
// schema of one operation, with Spec disambiguating an id that more
// than one component spec defines. Otherwise Spec restricts the
// operations to one component spec and Query ranks them; a call with
// neither on a multi-spec catalog answers with the specs, and on a
// single-spec catalog with the operations, so a connection that has
// only one section needs no section-selection step.
type DiscoverInput struct {
	Connection  string `json:"connection"`
	Spec        string `json:"spec,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	Query       string `json:"query,omitempty"`
	Limit       int    `json:"limit,omitempty"`
	Ranking     string `json:"ranking,omitempty"`
}

// DiscoverOutput is the structured result at every level. Level names
// the shape; exactly one of Specs, Operations, and Operation is
// populated for it. Next says what argument goes one level deeper, so
// the path from a bare call to api_invoke_endpoint reads out of the
// responses themselves. Note carries what changed the answer: a
// catalog-less connection, a ranking that fell back to lexical, an
// empty match, or a schema too large to return whole.
type DiscoverOutput struct {
	Level      string                   `json:"level"`
	Specs      []SpecSummary            `json:"specs,omitempty"`
	Operations []RankedOperationSummary `json:"operations,omitempty"`
	Operation  *EndpointSchemaOutput    `json:"operation,omitempty"`
	// MatchedLexical and ShownSemantic report where relevance ended: how
	// many of the operations returned contain every token, and how many
	// followed them as neighbors by intent. Absent unless the call carried a
	// query; a query that matched nothing reports zero rather than nothing,
	// which is "none contain your words" rather than "this was not ranked"
	// (#1626).
	MatchedLexical *int   `json:"matched_lexical,omitempty"`
	ShownSemantic  *int   `json:"shown_semantic,omitempty"`
	Note           string `json:"note,omitempty"`
	Next           string `json:"next,omitempty"`
}

// RankedOperationSummary is one operation at the operations level: the
// summary, plus what put it in the result when a query ranked it. Score is the
// mode's score in [0,1] (blended under hybrid, the normalized cosine under
// semantic, positional under the lexical filter); LexicalMatch says whether
// the operation contains every token.
//
// Both are pointers because both are absent from an unranked list, and a zero
// score is a real score, so omitempty on a plain float would drop it. Before
// #1626 the score was computed and discarded (#1626).
type RankedOperationSummary struct {
	OperationSummary
	Score        *float64 `json:"score,omitempty"`
	LexicalMatch *bool    `json:"lexical_match,omitempty"`
}

// The Next sentence at each level. The operation level's is composed
// from the call, since it names the connection and id to invoke.
const (
	discoverNextAfterSpecs = "pass spec=<name> for one spec's operations, or query=<text> " +
		"to rank operations across every spec"
	discoverNextAfterOperations = "pass operation_id=<id> for that operation's parameters, " +
		"request body and responses; api_invoke_endpoint takes the same operation_id"
	// discoverNoCatalogNote is the answer for a connection with no
	// catalog at every depth: there is nothing to discover, and the
	// connection is called by method and path.
	discoverNoCatalogNote = "no catalog configured for this connection; call api_invoke_endpoint " +
		"with method+path directly"
)

// defaultDiscoverLimit caps the operations returned when the caller
// does not pass limit. Keeps the response from blowing context on
// large APIs while staying generous enough for casual queries; the
// caller asks for more by passing limit explicitly.
const defaultDiscoverLimit = 50

// maxResponseChars caps the marshaled response payload at the
// operation level. Spec-heavy APIs (Salesforce, Microsoft Graph)
// routinely have multi-megabyte schemas; surfacing one would devour
// the model's context. The truncation note tells the model that a
// partial result was returned so it can fall back to
// api_invoke_endpoint to probe shape.
const maxResponseChars = 50000

func (t *Toolkit) handleDiscover(ctx context.Context, _ *mcp.CallToolRequest, in DiscoverInput) (*mcp.CallToolResult, any, error) {
	if in.Connection == "" {
		return toolkit.ErrorResult("connection is required"), nil, nil
	}
	t.mu.RLock()
	c, ok := t.connections[in.Connection]
	policy := t.routePolicy
	t.mu.RUnlock()
	if !ok {
		return toolkit.ErrorResult(fmt.Sprintf("connection %q not found (use list_connections to discover api connections)", in.Connection)), nil, nil
	}
	if len(c.specs) == 0 {
		return discoverNoCatalog(in)
	}
	if in.Spec != "" {
		if _, known := c.specs[in.Spec]; !known {
			return toolkit.ErrorResult(fmt.Sprintf("spec %q is not in this connection's catalog; its specs are %s",
				in.Spec, strings.Join(specNames(c), ", "))), nil, nil
		}
	}
	if in.OperationID != "" {
		return t.discoverOperation(ctx, policy, c, in)
	}
	if in.Spec == "" && in.Query == "" && len(c.specs) > 1 {
		out := discoverSpecs(c)
		return toolkit.JSONResult(out), out, nil
	}
	return t.discoverOperations(ctx, policy, c, in)
}

// discoverNoCatalog answers for a connection with no catalog. There is
// nothing to list at any depth, so the answer is the note that says
// how such a connection is called; an operation_id is refused, since
// no id can resolve without a catalog to resolve it against.
func discoverNoCatalog(in DiscoverInput) (*mcp.CallToolResult, any, error) {
	if in.OperationID != "" {
		return toolkit.ErrorResult(fmt.Sprintf("operation_id %q cannot be resolved: %s", in.OperationID, discoverNoCatalogNote)), nil, nil
	}
	out := DiscoverOutput{Level: DiscoverLevelOperations, Note: discoverNoCatalogNote}
	return toolkit.JSONResult(out), out, nil
}

// discoverSpecs is the specs level: one summary per component spec,
// sorted by name, and the argument that drills into one.
func discoverSpecs(c *conn) DiscoverOutput {
	specs := buildSpecSummaries(c)
	return DiscoverOutput{
		Level: DiscoverLevelSpecs,
		Specs: specs,
		Note:  fmt.Sprintf("this connection's catalog has %d component specs", len(specs)),
		Next:  discoverNextAfterSpecs,
	}
}

// discoverOperations is the operations level. The route policy narrows
// the index to what the caller may invoke, the spec filter to one
// section, and the query ranks what is left: lexical by default, hybrid
// whenever the connection has an embedding index and the caller pinned
// no mode, with the documented fallback to lexical and its note.
func (t *Toolkit) discoverOperations(ctx context.Context, policy RoutePolicy, c *conn, in DiscoverInput) (*mcp.CallToolResult, any, error) {
	mode, modeErr := parseRankingMode(in.Ranking)
	if modeErr != nil {
		return toolkit.ErrorResult(modeErr.Error()), nil, nil
	}
	// Filter through the route policy so a persona only sees the
	// operations it could actually invoke, then by spec so the rank
	// limit applies within the requested section rather than to the
	// unfiltered catalog.
	visible := filterBySpec(filterByRoutePolicy(ctx, policy, in.Connection, c.operations), in.Spec)
	limit := in.Limit
	if limit <= 0 {
		limit = defaultDiscoverLimit
	}
	// Default-ON semantic ranking: an omitted ranking resolves to
	// hybrid whenever this connection has an embedding index, so an
	// intent query is ranked instead of failing the lexical AND filter
	// closed. An explicit ranking="lexical" is preserved, and an empty
	// query is served from the lexical "return all" path regardless of
	// mode, so the readiness gate would be wasted work there (#858).
	rankingDefaulted := in.Query != "" && in.Ranking == "" && t.embeddingsAvailable(c)
	if rankingDefaulted {
		mode = RankingHybrid
	}
	ranked := rankWithMode(ctx, rankRequest{
		tk: t, conn: c, ops: visible, query: in.Query, limit: limit, mode: mode,
	})
	out := operationsOutput(in, ranked, rankingFallbackNote(rankingDefaulted, mode, ranked.fallbackReason))
	return toolkit.JSONResult(out), out, nil
}

// operationsOutput renders one operations level with what the caller needs to
// read it: where relevance ended, and the note for a result that is only
// neighbors, or empty.
func operationsOutput(in DiscoverInput, ranked rankedResult, note string) DiscoverOutput {
	out := DiscoverOutput{
		Level:      DiscoverLevelOperations,
		Operations: ranked.operations,
		Note:       note,
		Next:       discoverNextAfterOperations,
	}
	if strings.TrimSpace(in.Query) != "" {
		matched, shown := ranked.matchedLexical, ranked.shownSemantic
		out.MatchedLexical, out.ShownSemantic = &matched, &shown
		out.Note = joinNotes(out.Note, neighborsOnlyNote(ranked))
	}
	if len(ranked.operations) == 0 {
		out.Note = joinNotes(out.Note, noOperationsNote(in))
	}
	return out
}

// neighborsOnlyNote keeps a result of near neighbors from reading as a set of
// matches. Empty when something matched, and when nothing came back at all --
// noOperationsNote answers that.
func neighborsOnlyNote(ranked rankedResult) string {
	if ranked.matchedLexical > 0 || ranked.shownSemantic == 0 {
		return ""
	}
	return fmt.Sprintf("no operation contains every token of the query; the %d shown are the closest by intent",
		ranked.shownSemantic)
}

// noOperationsNote says what an empty operations level was narrowed by,
// so the caller widens the right argument.
func noOperationsNote(in DiscoverInput) string {
	switch {
	case in.Query != "" && in.Spec != "":
		return fmt.Sprintf("no operations in spec %q match query %q", in.Spec, in.Query)
	case in.Query != "":
		return fmt.Sprintf("no operations match query %q", in.Query)
	case in.Spec != "":
		return fmt.Sprintf("spec %q has no operations this caller may invoke", in.Spec)
	}
	return "this connection has no operations this caller may invoke"
}

// joinNotes composes two optional notes into one sentence pair.
func joinNotes(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// discoverOperation is the operation level: the schema of one
// operation, resolved by id (spec disambiguating a collision), with the
// requests promoted on it. An operation the route policy denies is
// reported as not found, matching its absence from the operations
// level.
func (t *Toolkit) discoverOperation(ctx context.Context, policy RoutePolicy, c *conn, in DiscoverInput) (*mcp.CallToolResult, any, error) {
	match, candidates := resolveOperation(c.specs, in.OperationID, in.Spec)
	if match == nil {
		if len(candidates) > 1 {
			// The message names the candidate specs, as the invoke path's
			// does: the platform's error contract carries a message, not a
			// payload, so a candidate list in a JSON body never reaches the
			// caller through the real surface.
			return toolkit.ErrorResult(ambiguousOperationError(in.OperationID, candidates).Error()), nil, nil
		}
		return toolkit.ErrorResult(fmt.Sprintf("operation_id %q not found", in.OperationID)), nil, nil
	}
	if policy != nil {
		if allowed, _ := policy.Allow(ctx, in.Connection, match.method, match.path, match.path); !allowed {
			return toolkit.ErrorResult(fmt.Sprintf("operation_id %q not found", in.OperationID)), nil, nil
		}
	}
	schema := buildEndpointSchemaOutput(match)
	schema.SavedExamples = t.savedExamples(ctx, in.Connection, schema.OperationID)
	out := DiscoverOutput{
		Level:     DiscoverLevelOperation,
		Operation: &schema,
		Next:      invokeNext(in.Connection, schema.OperationID, in.Spec),
	}
	return cappedJSONResult(out), out, nil
}

// invokeNext names the api_invoke_endpoint call that follows a schema
// read. The spec is repeated only when the caller needed it here, since
// invoke needs it for the same collision.
func invokeNext(connection, operationID, spec string) string {
	specClause := ""
	if spec != "" {
		specClause = fmt.Sprintf(", spec=%q", spec)
	}
	return fmt.Sprintf("call api_invoke_endpoint with connection=%q, operation_id=%q%s, "+
		"and values for any {placeholders} in path_params", connection, operationID, specClause)
}

// specNames lists a connection's component spec names in stable order,
// for the refusal of a spec the catalog does not have.
func specNames(c *conn) []string {
	names := make([]string, 0, len(c.specs))
	for name := range c.specs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// cappedJSONResult marshals out and returns a tool result with the
// JSON body truncated to maxResponseChars when needed. The bulky
// parameter and response schemas are dropped and the note is set
// before re-marshaling, so the model sees that truncation happened
// and can fall back to api_invoke_endpoint to probe the shape.
func cappedJSONResult(out DiscoverOutput) *mcp.CallToolResult {
	encoded, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return toolkit.ErrorResult("internal: marshal endpoint schema: " + err.Error())
	}
	if len(encoded) <= maxResponseChars || out.Operation == nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
		}
	}
	elided := *out.Operation
	elided.Parameters = nil
	elided.RequestBody = nil
	elided.Responses = nil
	elided.Note = fmt.Sprintf("schema details elided (full size %d chars exceeds %d-char cap)",
		len(encoded), maxResponseChars)
	out.Operation = &elided
	out.Note = joinNotes(out.Note, elided.Note)
	encoded, _ = json.MarshalIndent(out, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}},
	}
}
