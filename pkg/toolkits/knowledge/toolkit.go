package knowledge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	// toolName is the MCP tool name for capturing insights.
	toolName = "capture_insight"

	// idLength is the number of random bytes used to generate insight IDs.
	idLength = 16

	// promptName is the MCP prompt name for knowledge capture guidance.
	promptName = "knowledge_capture_guidance"
)

// captureInsightInput defines the input schema for the capture_insight tool.
type captureInsightInput struct {
	Category         string            `json:"category"`
	InsightText      string            `json:"insight_text"`
	Confidence       string            `json:"confidence,omitempty"`
	EntityURNs       []string          `json:"entity_urns,omitempty"`
	RelatedColumns   []RelatedColumn   `json:"related_columns,omitempty"`
	SuggestedActions []SuggestedAction `json:"suggested_actions,omitempty"`
}

// captureInsightOutput is the success response.
type captureInsightOutput struct {
	InsightID string `json:"insight_id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
}

// Toolkit implements the knowledge capture toolkit.
type Toolkit struct {
	name  string
	store InsightStore

	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// New creates a new knowledge toolkit.
// If store is nil, a no-op store is used.
func New(name string, store InsightStore) (*Toolkit, error) {
	if store == nil {
		store = NewNoopStore()
	}

	return &Toolkit{
		name:  name,
		store: store,
	}, nil
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string {
	return "knowledge"
}

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string {
	return t.name
}

// Connection returns the connection name for audit logging.
func (*Toolkit) Connection() string {
	return ""
}

// RegisterTools registers the capture_insight tool with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: toolName,
		Description: "Records domain knowledge shared during a session for later admin review and catalog integration. " +
			"Use this when you discover corrections to metadata, business context about data meaning, " +
			"data quality observations, usage tips, or relationships between datasets.",
	}, t.handleCaptureInsight)

	t.registerPrompt(s)
}

// Tools returns the list of tool names provided by this toolkit.
func (*Toolkit) Tools() []string {
	return []string{toolName}
}

// SetSemanticProvider sets the semantic metadata provider.
func (t *Toolkit) SetSemanticProvider(provider semantic.Provider) {
	t.semanticProvider = provider
}

// SetQueryProvider sets the query execution provider.
func (t *Toolkit) SetQueryProvider(provider query.Provider) {
	t.queryProvider = provider
}

// Close releases resources.
func (*Toolkit) Close() error {
	return nil
}

// handleCaptureInsight handles the capture_insight tool call.
func (t *Toolkit) handleCaptureInsight(ctx context.Context, _ *mcp.CallToolRequest, input captureInsightInput) (*mcp.CallToolResult, any, error) {
	// Validate all inputs
	if err := validateInput(input); err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	// Read context from PlatformContext (injected by auth middleware)
	pc := middleware.GetPlatformContext(ctx)

	// Generate unique ID
	id, err := generateID()
	if err != nil {
		return errorResult("internal error generating insight ID"), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	// Build the insight
	insight := buildInsight(id, pc, input)

	// Persist
	if err := t.store.Insert(ctx, insight); err != nil {
		return errorResult("failed to save insight: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	// Return success
	return successResult(id)
}

// validateInput validates all input fields.
func validateInput(input captureInsightInput) error {
	if err := ValidateCategory(input.Category); err != nil {
		return err
	}
	if err := ValidateInsightText(input.InsightText); err != nil {
		return err
	}
	if err := ValidateConfidence(input.Confidence); err != nil {
		return err
	}
	if err := ValidateEntityURNs(input.EntityURNs); err != nil {
		return err
	}
	if err := ValidateRelatedColumns(input.RelatedColumns); err != nil {
		return err
	}
	return ValidateSuggestedActions(input.SuggestedActions)
}

// buildInsight constructs an Insight from the validated input and platform context.
func buildInsight(id string, pc *middleware.PlatformContext, input captureInsightInput) Insight {
	insight := Insight{
		ID:               id,
		Category:         input.Category,
		InsightText:      input.InsightText,
		Confidence:       NormalizeConfidence(input.Confidence),
		EntityURNs:       input.EntityURNs,
		RelatedColumns:   input.RelatedColumns,
		SuggestedActions: input.SuggestedActions,
		Status:           "pending",
	}

	// Ensure slices are non-nil for JSON serialization
	if insight.EntityURNs == nil {
		insight.EntityURNs = []string{}
	}
	if insight.RelatedColumns == nil {
		insight.RelatedColumns = []RelatedColumn{}
	}
	if insight.SuggestedActions == nil {
		insight.SuggestedActions = []SuggestedAction{}
	}

	// Inject context from PlatformContext
	if pc != nil {
		insight.SessionID = pc.SessionID
		insight.CapturedBy = pc.UserID
		insight.Persona = pc.PersonaName
	}

	return insight
}

// generateID generates a cryptographically random hex ID.
func generateID() (string, error) {
	b := make([]byte, idLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// errorResult creates an error CallToolResult.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf(`{"error": %q}`, msg)},
		},
		IsError: true,
	}
}

// successResult creates a success CallToolResult.
func successResult(insightID string) (*mcp.CallToolResult, any, error) {
	output := captureInsightOutput{
		InsightID: insightID,
		Status:    "pending",
		Message:   "Insight captured. It will be reviewed by a data catalog administrator.",
	}

	data, err := json.Marshal(output)
	if err != nil {
		return errorResult("internal error marshaling response"), nil, nil //nolint:nilerr // MCP protocol: tool errors are returned in CallToolResult.IsError
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
}

// registerPrompt registers the knowledge capture guidance prompt.
func (*Toolkit) registerPrompt(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        promptName,
		Description: "Guidance on when and how to capture domain knowledge insights",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{
					Role:    "user",
					Content: &mcp.TextContent{Text: knowledgeCapturePrompt},
				},
			},
		}, nil
	})
}

// knowledgeCapturePrompt guides the AI agent on when to suggest capturing insights.
const knowledgeCapturePrompt = `## Knowledge Capture Guidance

### When to Capture an Insight

Use the capture_insight tool when the user shares domain knowledge that would improve the data catalog. Look for these signals:

**Corrections**: The user corrects a column description, table purpose, or data interpretation.
Example: "That column isn't actually revenue, it's gross margin before returns."

**Business Context**: The user explains what data means in business terms not captured in metadata.
Example: "We only count active subscriptions, not trial accounts, for MRR calculations."

**Data Quality**: The user reports data quality issues or known limitations.
Example: "The timestamps before March 2024 are in UTC but after that they switched to America/Chicago."

**Usage Guidance**: The user shares tips on how to query or interpret data correctly.
Example: "Always filter by status='active' on that table, otherwise you get duplicates from soft deletes."

**Relationships**: The user explains connections between datasets not captured in lineage.
Example: "The customer_id in orders joins to the legacy CRM export, not the new identity table."

**Enhancements**: The user suggests improvements to existing documentation or metadata.
Example: "It would help if the sales_daily table had a tag indicating it refreshes at 6 AM CT."

### When NOT to Capture

Do NOT capture insights for:
- Transient questions or debugging ("why is my query slow?")
- Personal preferences ("I prefer using CTEs")
- Information already present in the catalog metadata
- Vague or unverifiable claims without specific context
- Trivial observations that don't add catalog value

### Best Practices

- Include specific entity URNs when the insight relates to known datasets
- Suggest concrete actions (add_tag, update_description) when applicable
- Set confidence to "high" only when the user is clearly authoritative
- Capture the insight promptly while context is fresh`

// Verify interface compliance.
var _ interface {
	Kind() string
	Name() string
	Connection() string
	RegisterTools(s *mcp.Server)
	Tools() []string
	SetSemanticProvider(provider semantic.Provider)
	SetQueryProvider(provider query.Provider)
	Close() error
} = (*Toolkit)(nil)
