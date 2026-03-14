// Package portal provides the MCP toolkit for saving and managing
// AI-generated artifacts (JSX dashboards, HTML reports, SVG charts).
package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

const (
	saveToolName   = "save_artifact"
	manageToolName = "manage_artifact"

	// Prompt names registered by the portal toolkit.
	saveAssetPromptName  = "save-this-as-an-asset"
	showAssetsPromptName = "show-my-saved-assets"

	// idLength is the number of random bytes for asset IDs (32 hex chars).
	idLength = 16

	// defaultVersionListLimit is the default page size for version listing.
	defaultVersionListLimit = 50

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
	Version     int      `json:"version,omitempty"`
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
	VersionStore   portal.VersionStore
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
	versionStore   portal.VersionStore
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
	versionStore := cfg.VersionStore
	if versionStore == nil {
		versionStore = portal.NewNoopVersionStore()
	}
	return &Toolkit{
		name:           cfg.Name,
		assetStore:     assetStore,
		shareStore:     shareStore,
		versionStore:   versionStore,
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
			"IMPORTANT: When creating content that should be saved, call this tool directly with the content " +
			"rather than first outputting it to the conversation and then saving separately — " +
			"this avoids regenerating the entire artifact. " +
			"Automatically captures provenance (which tool calls produced this artifact).",
		InputSchema: saveArtifactSchema,
	}, t.handleSaveArtifact)

	mcp.AddTool(s, &mcp.Tool{
		Name:  manageToolName,
		Title: "Manage Artifact",
		Description: "Lists, retrieves, updates, or deletes saved artifacts. " +
			"Actions: list (show user's artifacts), get (metadata + content), " +
			"update (change name/description/tags/content), delete (soft-delete), " +
			"list_versions (show version history), revert (revert to a previous version).",
		InputSchema: manageArtifactSchema,
	}, t.handleManageArtifact)

	t.registerPrompts(s)
}

// registerPrompts registers user-facing prompts for the portal toolkit.
func (*Toolkit) registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        saveAssetPromptName,
		Description: "Save an artifact from this conversation as a viewable, shareable asset",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{
					Role: "user",
					Content: &mcp.TextContent{
						Text: saveAssetPromptContent,
					},
				},
			},
		}, nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        showAssetsPromptName,
		Description: "Browse your saved artifacts and assets",
	}, func(_ context.Context, _ *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{
			Messages: []*mcp.PromptMessage{
				{
					Role: "user",
					Content: &mcp.TextContent{
						Text: showAssetsPromptContent,
					},
				},
			},
		}, nil
	})
}

// PromptInfos returns metadata for prompts registered by the portal toolkit.
func (*Toolkit) PromptInfos() []registry.PromptInfo {
	return []registry.PromptInfo{
		{
			Name:        saveAssetPromptName,
			Description: "Save an artifact from this conversation as a viewable, shareable asset",
			Category:    "toolkit",
			Content:     saveAssetPromptContent,
		},
		{
			Name:        showAssetsPromptName,
			Description: "Browse your saved artifacts and assets",
			Category:    "toolkit",
			Content:     showAssetsPromptContent,
		},
	}
}

const saveAssetPromptContent = `Save the most recent artifact or analysis from this conversation as a shareable asset.

1. Identify the key output from our conversation (dashboard, report, chart, or analysis)
2. Package it with an appropriate name, description, and tags
3. Save it as an artifact so it can be viewed and shared
4. Return the link to the saved asset`

const showAssetsPromptContent = `List my saved assets and artifacts.

1. Retrieve all assets I have saved
2. Present them with names, descriptions, tags, and creation dates
3. Highlight the most recent items`

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
	userEmail := resolveOwnerEmail(ctx)
	sessionID := resolveSessionID(ctx)

	assetID, err := generateID()
	if err != nil {
		return errorResult("internal error generating asset ID"), nil, nil //nolint:nilerr // MCP protocol
	}

	s3Key := t.buildS3Key(userID, assetID, input.ContentType)

	if t.s3Client == nil {
		return errorResult("content storage not configured"), nil, nil
	}
	if err := t.s3Client.PutObject(ctx, t.s3Bucket, s3Key, []byte(input.Content), input.ContentType); err != nil {
		return errorResult("failed to upload content: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	prov := buildProvenance(ctx, userID, sessionID)
	slog.Info("save_artifact.provenance",
		"session_id", sessionID,
		"user_id", userID,
		"tool_calls", len(prov.ToolCalls),
	)

	tags := input.Tags
	if tags == nil {
		tags = []string{}
	}

	asset := portal.Asset{
		ID:          assetID,
		OwnerID:     userID,
		OwnerEmail:  userEmail,
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

	// Create initial v1 version record.
	v1 := portal.AssetVersion{
		ID:            assetID + "-v1",
		AssetID:       assetID,
		S3Key:         s3Key,
		S3Bucket:      t.s3Bucket,
		ContentType:   input.ContentType,
		SizeBytes:     int64(len(input.Content)),
		CreatedBy:     userID,
		ChangeSummary: "Initial version",
	}
	if _, err := t.versionStore.CreateVersion(ctx, v1); err != nil {
		slog.Warn("failed to create initial version record", "asset_id", assetID, "error", err)
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
	case "list_versions":
		return t.handleListVersions(ctx, input)
	case "revert":
		return t.handleRevert(ctx, input)
	default:
		return errorResult(fmt.Sprintf("invalid action %q: must be one of: list, get, update, delete, list_versions, revert", input.Action)), nil, nil
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
		if contentErr := t.uploadContentUpdate(ctx, asset, input); contentErr != nil {
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

func (t *Toolkit) uploadContentUpdate(ctx context.Context, asset *portal.Asset, input manageArtifactInput) error {
	if t.maxContentSize > 0 && len(input.Content) > t.maxContentSize {
		return fmt.Errorf("content size %d exceeds maximum %d bytes", len(input.Content), t.maxContentSize)
	}
	ct := input.ContentType
	if ct == "" {
		ct = asset.ContentType
	}

	versionID, err := generateID()
	if err != nil {
		return fmt.Errorf("generating version ID: %w", err)
	}
	ext := portal.ExtensionForContentType(ct)
	s3Key := path.Join(t.s3Prefix, asset.OwnerID, asset.ID, versionID, "content"+ext)

	if t.s3Client == nil {
		return fmt.Errorf("content storage not configured")
	}
	if err := t.s3Client.PutObject(ctx, t.s3Bucket, s3Key, []byte(input.Content), ct); err != nil {
		return fmt.Errorf("s3 put: %w", err)
	}

	userID := resolveOwnerID(ctx)
	av := portal.AssetVersion{
		ID:            versionID,
		AssetID:       asset.ID,
		S3Key:         s3Key,
		S3Bucket:      t.s3Bucket,
		ContentType:   ct,
		SizeBytes:     int64(len(input.Content)),
		CreatedBy:     userID,
		ChangeSummary: "Content updated via MCP",
	}
	if _, err = t.versionStore.CreateVersion(ctx, av); err != nil {
		return fmt.Errorf("creating version: %w", err)
	}
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

func (t *Toolkit) handleListVersions(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return errorResult("asset_id is required for list_versions action"), nil, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultVersionListLimit
	}
	versions, total, err := t.versionStore.ListByAsset(ctx, input.AssetID, limit, 0)
	if err != nil {
		return errorResult("failed to list versions: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}
	if versions == nil {
		versions = []portal.AssetVersion{}
	}
	return jsonResult(map[string]any{
		"versions": versions,
		"total":    total,
	})
}

func (t *Toolkit) handleRevert(ctx context.Context, input manageArtifactInput) (*mcp.CallToolResult, any, error) {
	if !input.validForRevert() {
		return errorResult("asset_id and version (> 0) are required for revert action"), nil, nil
	}

	asset, err := t.assetStore.Get(ctx, input.AssetID)
	if err != nil {
		return errorResult("asset not found: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	ownerID := resolveOwnerID(ctx)
	if asset.OwnerID != ownerID {
		return errorResult("you can only revert your own artifacts"), nil, nil
	}

	targetVer, err := t.versionStore.GetByVersion(ctx, input.AssetID, input.Version)
	if err != nil {
		return errorResult("version not found: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	if t.s3Client == nil {
		return errorResult("content storage not configured"), nil, nil
	}
	data, _, err := t.s3Client.GetObject(ctx, targetVer.S3Bucket, targetVer.S3Key)
	if err != nil {
		return errorResult("failed to read version content: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	versionID, err := generateID()
	if err != nil {
		return errorResult("failed to generate version ID: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}
	ext := portal.ExtensionForContentType(targetVer.ContentType)
	newKey := path.Join(t.s3Prefix, asset.OwnerID, asset.ID, versionID, "content"+ext)

	if err := t.s3Client.PutObject(ctx, t.s3Bucket, newKey, data, targetVer.ContentType); err != nil {
		return errorResult("failed to upload reverted content: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	av := portal.AssetVersion{
		ID:            versionID,
		AssetID:       input.AssetID,
		S3Key:         newKey,
		S3Bucket:      t.s3Bucket,
		ContentType:   targetVer.ContentType,
		SizeBytes:     int64(len(data)),
		CreatedBy:     ownerID,
		ChangeSummary: fmt.Sprintf("Reverted from v%d", input.Version),
	}
	assignedVersion, err := t.versionStore.CreateVersion(ctx, av)
	if err != nil {
		return errorResult("failed to create revert version: " + err.Error()), nil, nil //nolint:nilerr // MCP protocol
	}

	return jsonResult(map[string]any{
		"asset_id": input.AssetID,
		"version":  assignedVersion,
		"message":  fmt.Sprintf("Reverted to version %d. New version: %d.", input.Version, assignedVersion),
	})
}

func (m manageArtifactInput) validForRevert() bool {
	return m.AssetID != "" && m.Version > 0
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

// resolveOwnerEmail returns the authenticated user's email from the context.
func resolveOwnerEmail(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil {
		return pc.UserEmail
	}
	return ""
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
	ext := portal.ExtensionForContentType(contentType)
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
		out.PortalURL = t.baseURL + "/portal/assets/" + assetID
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
				ToolName:   c.ToolName,
				Timestamp:  c.Timestamp,
				Parameters: c.Parameters,
			}
		}
	}

	return prov
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

// Verify PromptDescriber compliance.
var _ registry.PromptDescriber = (*Toolkit)(nil)
