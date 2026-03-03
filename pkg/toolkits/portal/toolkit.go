// Package portal provides the MCP toolkit for saving and managing
// AI-generated artifacts (JSX dashboards, HTML reports, SVG charts).
package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	saveToolName   = "save_artifact"
	manageToolName = "manage_artifact"

	// idLength is the number of random bytes for asset IDs (32 hex chars).
	idLength = 16

	// validationFmt is the format string for wrapping validation errors.
	validationFmt = "validation: %w"
)

// saveArtifactInput defines the input for save_artifact.
type saveArtifactInput struct {
	Name        string   `json:"name"`
	Content     string   `json:"content"`
	ContentType string   `json:"content_type"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// manageArtifactInput defines the input for manage_artifact.
type manageArtifactInput struct {
	Action      string   `json:"action"`
	AssetID     string   `json:"asset_id,omitempty"`
	Content     string   `json:"content,omitempty"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ContentType string   `json:"content_type,omitempty"`
	Limit       int      `json:"limit,omitempty"`
}

// saveArtifactOutput is the success response for save_artifact.
type saveArtifactOutput struct {
	AssetID            string `json:"asset_id"`
	PortalURL          string `json:"portal_url,omitempty"`
	Message            string `json:"message"`
	ProvenanceCaptured bool   `json:"provenance_captured"`
	ToolCallsRecorded  int    `json:"tool_calls_recorded"`
}

// Config holds configuration for creating a portal toolkit.
type Config struct {
	Name           string
	AssetStore     portal.AssetStore
	ShareStore     portal.ShareStore
	S3Client       portal.S3Client
	S3Bucket       string
	S3Prefix       string
	BaseURL        string
	MaxContentSize int // max artifact content size in bytes (0 = no limit)
}

// Toolkit implements the portal artifact toolkit.
type Toolkit struct {
	name           string
	assetStore     portal.AssetStore
	shareStore     portal.ShareStore
	s3Client       portal.S3Client
	s3Bucket       string
	s3Prefix       string
	baseURL        string
	maxContentSize int

	semanticProvider semantic.Provider
	queryProvider    query.Provider
}

// New creates a new portal toolkit.
func New(cfg Config) *Toolkit {
	assetStore := cfg.AssetStore
	if assetStore == nil {
		assetStore = portal.NewNoopAssetStore()
	}
	shareStore := cfg.ShareStore
	if shareStore == nil {
		shareStore = portal.NewNoopShareStore()
	}
	return &Toolkit{
		name:           cfg.Name,
		assetStore:     assetStore,
		shareStore:     shareStore,
		s3Client:       cfg.S3Client,
		s3Bucket:       cfg.S3Bucket,
		s3Prefix:       cfg.S3Prefix,
		baseURL:        cfg.BaseURL,
		maxContentSize: cfg.MaxContentSize,
	}
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string { return "portal" }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the connection name for audit logging.
func (*Toolkit) Connection() string { return "" }

// RegisterTools registers save_artifact and manage_artifact with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:  saveToolName,
		Title: "Save Artifact",
		Description: "Saves an AI-generated artifact (JSX dashboard, HTML report, SVG chart, etc.) " +
			"to the asset portal for persistence, viewing, and sharing. " +
			"Automatically captures provenance (which tool calls produced this artifact).",
		InputSchema: saveArtifactSchema,
	}, t.handleSaveArtifact)

	mcp.AddTool(s, &mcp.Tool{
		Name:  manageToolName,
		Title: "Manage Artifact",
		Description: "Lists, retrieves, updates, or deletes saved artifacts. " +
			"Actions: list (show user's artifacts), get (metadata + content), " +
			"update (change name/description/tags/content), delete (soft-delete).",
		InputSchema: manageArtifactSchema,
	}, t.handleManageArtifact)
}

// Tools returns the list of tool names provided by this toolkit.
func (*Toolkit) Tools() []string {
	return []string{saveToolName, manageToolName}
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
func (*Toolkit) Close() error { return nil }

// handleSaveArtifact persists an artifact to S3 and records metadata.
func (t *Toolkit) handleSaveArtifact(ctx context.Context, _ *mcp.CallToolRequest, input saveArtifactInput) (*mcp.CallToolResult, any, error) {
	if err := t.validateAndCheckSize(input); err != nil {
		return errorResult(err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	userID := resolveOwnerID(ctx)
	sessionID := resolveSessionID(ctx)

	assetID, err := generateID()
	if err != nil {
		return errorResult("internal error generating asset ID"), nil, nil //nolint:nilerr // MCP protocol
	}

	s3Key := t.buildS3Key(userID, assetID, input.ContentType)

	if t.s3Client != nil {
		if err := t.s3Client.PutObject(ctx, t.s3Bucket, s3Key, []byte(input.Content), input.ContentType); err != nil {
			return errorResult("failed to upload content: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
		}
	}

	prov := buildProvenance(ctx, userID, sessionID)

	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}

	asset := portal.Asset{
		ID:          assetID,
		OwnerID:     userID,
		Name:        input.Name,
		Description: input.Description,
		ContentType: input.ContentType,
		S3Bucket:    t.s3Bucket,
		S3Key:       s3Key,
		SizeBytes:   int64(len(input.Content)),
		Tags:        tags,
		Provenance:  prov,
		SessionID:   sessionID,
	}

	if err := t.assetStore.Insert(ctx, asset); err != nil {
		return errorResult("failed to save asset metadata: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(t.buildSaveOutput(assetID, prov))
}

// handleManageArtifact dispatches to list/get/update/delete.
func (t *Toolkit) handleManageArtifact(ctx context.Context, _ *mcp.CallToolRequest, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	switch input.Action {
	case "list":
		return t.handleList(ctx, input)
	case "get":
		return t.handleGet(ctx, input)
	case "update":
		return t.handleUpdate(ctx, input)
	case "delete":
		return t.handleDelete(ctx, input)
	default:
		return errorResult(fmt.Sprintf("invalid action %q: must be one of: list, get, update, delete", input.Action)), nil, nil
	}
}

func (t *Toolkit) handleList(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	ownerID := resolveOwnerID(ctx)

	assets, total, err := t.assetStore.List(ctx, portal.AssetFilter{
		OwnerID: ownerID,
		Limit:   input.Limit,
	})
	if err != nil {
		return errorResult("failed to list assets: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	if assets == nil {
		assets = []portal.Asset{}
	}

	result := map[string]any{
		"assets": assets,
		"total":  total,
	}
	return jsonResult(result)
}

func (t *Toolkit) handleGet(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return errorResult("asset_id is required for get action"), nil, nil
	}

	asset, err := t.assetStore.Get(ctx, input.AssetID)
	if err != nil {
		return errorResult("asset not found: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	if asset.DeletedAt != nil {
		return errorResult("asset has been deleted"), nil, nil
	}

	return jsonResult(asset)
}

func (t *Toolkit) handleUpdate(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return errorResult("asset_id is required for update action"), nil, nil
	}

	asset, err := t.assetStore.Get(ctx, input.AssetID)
	if err != nil {
		return errorResult("asset not found: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	ownerID := resolveOwnerID(ctx)
	if asset.OwnerID != ownerID {
		return errorResult("you can only update your own artifacts"), nil, nil
	}

	updates := portal.AssetUpdate{
		Tags: input.Tags,
	}
	if input.Name != "" {
		updates.Name = &input.Name
	}
	if input.Description != "" {
		updates.Description = &input.Description
	}

	if input.Content != "" {
		if contentErr := t.uploadContentUpdate(ctx, asset, input, &updates); contentErr != nil {
			return errorResult("failed to upload new content: " + contentErr.Error()), nil, nil //nolint:nilerr // MCP protocol
		}
	}

	if err := t.assetStore.Update(ctx, input.AssetID, updates); err != nil {
		return errorResult("failed to update asset: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"asset_id": input.AssetID,
		"message":  "Asset updated successfully.",
	})
}

func (t *Toolkit) uploadContentUpdate(ctx context.Context, asset *portal.Asset, input manageArtifactInput, updates *portal.AssetUpdate) error {
	if t.maxContentSize > 0 && len(input.Content) > t.maxContentSize {
		return fmt.Errorf("content size %d exceeds maximum %d bytes", len(input.Content), t.maxContentSize)
	}
	ct := input.ContentType
	if ct == "" {
		ct = asset.ContentType
	}
	ext := extensionForContentType(ct)
	s3Key := path.Join(t.s3Prefix, asset.OwnerID, asset.ID, "content"+ext)

	if t.s3Client != nil {
		if err := t.s3Client.PutObject(ctx, t.s3Bucket, s3Key, []byte(input.Content), ct); err != nil {
			return fmt.Errorf("s3 put: %w", err)
		}
	}
	updates.S3Key = s3Key
	updates.SizeBytes = int64(len(input.Content))
	updates.HasContent = true
	updates.ContentType = ct
	return nil
}

func (t *Toolkit) handleDelete(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return errorResult("asset_id is required for delete action"), nil, nil
	}

	asset, err := t.assetStore.Get(ctx, input.AssetID)
	if err != nil {
		return errorResult("asset not found: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	ownerID := resolveOwnerID(ctx)
	if asset.OwnerID != ownerID {
		return errorResult("you can only delete your own artifacts"), nil, nil
	}

	if err := t.assetStore.SoftDelete(ctx, input.AssetID); err != nil {
		return errorResult("failed to delete asset: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"asset_id": input.AssetID,
		"message":  "Asset deleted successfully.",
	})
}

// --- Helpers ---

// resolveOwnerID returns the authenticated user ID from the context, defaulting to "anonymous".
func resolveOwnerID(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil && pc.UserID != "" {
		return pc.UserID
	}
	return "anonymous"
}

func (t *Toolkit) validateAndCheckSize(input saveArtifactInput) error {
	if err := validateSaveInput(input); err != nil {
		return err
	}
	if t.maxContentSize > 0 && len(input.Content) > t.maxContentSize {
		return fmt.Errorf("content size %d exceeds maximum %d bytes", len(input.Content), t.maxContentSize)
	}
	return nil
}

func resolveSessionID(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil {
		return pc.SessionID
	}
	return ""
}

func (t *Toolkit) buildS3Key(ownerID, assetID, contentType string) string {
	ext := extensionForContentType(contentType)
	return path.Join(t.s3Prefix, ownerID, assetID, "content"+ext)
}

func (t *Toolkit) buildSaveOutput(assetID string, prov portal.Provenance) saveArtifactOutput {
	out := saveArtifactOutput{
		AssetID:            assetID,
		Message:            "Artifact saved successfully.",
		ProvenanceCaptured: len(prov.ToolCalls) > 0,
		ToolCallsRecorded:  len(prov.ToolCalls),
	}
	if t.baseURL != "" {
		out.PortalURL = t.baseURL + "/artifacts/" + assetID
	}
	return out
}

func validateSaveInput(input saveArtifactInput) error {
	if err := portal.ValidateAssetName(input.Name); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	if err := portal.ValidateContentType(input.ContentType); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	if input.Content == "" {
		return fmt.Errorf("content is required")
	}
	if err := portal.ValidateDescription(input.Description); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	if err := portal.ValidateTags(input.Tags); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	return nil
}

func buildProvenance(ctx context.Context, userID, sessionID string) portal.Provenance {
	prov := portal.Provenance{
		UserID:    userID,
		SessionID: sessionID,
	}

	calls := middleware.GetProvenanceToolCalls(ctx)
	if len(calls) > 0 {
		prov.ToolCalls = make([]portal.ProvenanceToolCall, len(calls))
		for i, c := range calls {
			prov.ToolCalls[i] = portal.ProvenanceToolCall{
				ToolName:  c.ToolName,
				Timestamp: c.Timestamp,
				Summary:   c.Summary,
			}
		}
	}

	return prov
}

func extensionForContentType(ct string) string {
	switch {
	case strings.Contains(ct, "html") || strings.Contains(ct, "jsx"):
		return ".html"
	case strings.Contains(ct, "svg"):
		return ".svg"
	case strings.Contains(ct, "markdown"):
		return ".md"
	case strings.Contains(ct, "json"):
		return ".json"
	case strings.Contains(ct, "csv"):
		return ".csv"
	default:
		return ".bin"
	}
}

func generateID() (string, error) {
	b := make([]byte, idLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func errorResult(msg string) *mcp.CallToolResult {
	errObj := struct {
		Error string `json:"error"`
	}{Error: msg}
	data, _ := json.Marshal(errObj) //nolint:errcheck // simple struct, cannot fail
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
		IsError: true,
	}
}

func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errorResult("internal error marshaling response"), nil, nil //nolint:nilerr // MCP protocol
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(data)},
		},
	}, nil, nil
}

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
