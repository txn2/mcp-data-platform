// Package portal provides the MCP toolkit for saving and managing
// AI-generated assets (JSX dashboards, HTML reports, SVG charts).
package portal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/middleware"
	"github.com/txn2/mcp-data-platform/pkg/portal"
	"github.com/txn2/mcp-data-platform/pkg/query"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	"github.com/txn2/mcp-data-platform/pkg/semantic"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// Tool names registered by the portal toolkit. Exported because pkg/platform
// needs the save tool's name to configure provenance harvesting.
const (
	// SaveToolName is the name of the asset-save tool.
	SaveToolName = "save_asset"
	// ManageToolName is the name of the asset-management tool.
	ManageToolName = "manage_asset"
)

const (
	feedbackToolName = "manage_feedback"

	// Prompt names registered by the portal toolkit.
	saveAssetPromptName  = "save-this-as-an-asset"
	showAssetsPromptName = "show-my-saved-assets"

	// idLength is the number of random bytes for asset IDs (32 hex chars).
	idLength = 16

	// defaultVersionListLimit is the default page size for version listing.
	defaultVersionListLimit = 50

	// validationFmt is the format string for wrapping validation errors.
	validationFmt = "validation: %w"

	// manage_asset action names. These are the values of the "action"
	// input field that select which sub-handler runs. Defined as
	// constants because the same string is referenced from the dispatch
	// table, error messages, and (potentially) tests.
	actionList             = "list"
	actionGet              = "get"
	actionUpdate           = "update"
	actionDelete           = "delete"
	actionListVersions     = "list_versions"
	actionRevert           = "revert"
	actionCreateCollection = "create_collection"
	actionListCollections  = "list_collections"
	actionGetCollection    = "get_collection"
	actionUpdateCollection = "update_collection"
	actionDeleteCollection = "delete_collection"
	actionSetSections      = "set_sections"
	actionSearch           = "search"

	// Content editing and navigation actions (#1033). These make the cost of
	// an edit proportional to the size of the edit rather than the size of
	// the document; the grammar is shared with manage_prompt via
	// pkg/textpatch.
	actionPatch      = "patch"
	actionLocate     = "locate"
	actionGetContent = "get_content"
	actionOutline    = "outline"
	actionStats      = "stats"
	actionDiff       = "diff"

	// manage_feedback action names (#618). Feedback is its own tool so agents can
	// discover it by name; these are the values of its "action" field.
	fbActionList              = "list"
	fbActionGet               = "get"
	fbActionReply             = "reply"
	fbActionResolve           = "resolve"
	fbActionRequestValidation = "request_validation"
	fbActionRespondValidation = "respond_validation"

	// JSON field names used in MCP tool result payloads.
	fieldAssetID = "asset_id"
	fieldMessage = "message"
	fieldTotal   = "total"

	// defaultChangeSummary labels a content version whose author supplied no
	// change_summary.
	defaultChangeSummary = "Content updated via MCP"

	// assetNotFoundHint is the corrective hint on an asset-not-found error.
	assetNotFoundHint = "Verify the asset_id; call manage_asset action=list to see your assets."

	// anonymousUserName is the fallback owner identifier when the request has
	// no authenticated user (no PlatformContext or empty user/email).
	anonymousUserName = "anonymous"
)

// saveAssetInput defines the input for save_asset.
type saveAssetInput struct {
	Name        string   `json:"name"`
	Content     string   `json:"content"`
	ContentType string   `json:"content_type"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// manageAssetInput defines the input for manage_asset.
type manageAssetInput struct {
	Action       string         `json:"action"`
	AssetID      string         `json:"asset_id,omitempty"`
	Content      string         `json:"content,omitempty"`
	Name         string         `json:"name,omitempty"`
	Description  string         `json:"description,omitempty"`
	Tags         []string       `json:"tags,omitempty"`
	ContentType  string         `json:"content_type,omitempty"`
	Limit        int            `json:"limit,omitempty"`
	Version      int            `json:"version,omitempty"`
	CollectionID string         `json:"collection_id,omitempty"`
	Sections     []sectionInput `json:"sections,omitempty"`
	Search       string         `json:"search,omitempty"`
	Offset       int            `json:"offset,omitempty"`

	// Query (search action) ranks the caller's assets by relevance to a
	// free-text query instead of the substring Search filter.
	Query string `json:"query,omitempty"`

	// Content editing and navigation arguments (#1033). Edits carries the
	// ordered patch; the rest select what to read or search.
	Edits         []textpatch.Edit `json:"edits,omitempty"`
	BaseVersion   int              `json:"base_version,omitempty"`
	DryRun        bool             `json:"dry_run,omitempty"`
	ChangeSummary string           `json:"change_summary,omitempty"`
	Find          string           `json:"find,omitempty"`
	Pattern       string           `json:"pattern,omitempty"`
	Section       string           `json:"section,omitempty"`
	Selector      string           `json:"selector,omitempty"`
	Occurrence    string           `json:"occurrence,omitempty"`
	LineStart     int              `json:"line_start,omitempty"`
	LineEnd       int              `json:"line_end,omitempty"`
	ContextBytes  int              `json:"context_bytes,omitempty"`
	FromVersion   int              `json:"from_version,omitempty"`
	ToVersion     int              `json:"to_version,omitempty"`
}

// manageFeedbackInput defines the input for manage_feedback (#618).
type manageFeedbackInput struct {
	Action       string `json:"action"`
	AssetID      string `json:"asset_id,omitempty"`
	CollectionID string `json:"collection_id,omitempty"`
	PromptID     string `json:"prompt_id,omitempty"`
	TargetType   string `json:"target_type,omitempty"`
	ThreadID     string `json:"thread_id,omitempty"`
	Body         string `json:"body,omitempty"`

	// Filters for a targeted list.
	Status             string `json:"status,omitempty"`
	ValidationState    string `json:"validation_state,omitempty"`
	RequiresResolution *bool  `json:"requires_resolution,omitempty"`

	// respond_validation outcome.
	ValidationResult string `json:"validation_result,omitempty"`
	ValidationReason string `json:"validation_reason,omitempty"`

	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// sectionInput defines a collection section in MCP tool input.
type sectionInput struct {
	Title       string      `json:"title"`
	Description string      `json:"description,omitempty"`
	Items       []itemInput `json:"items"`
}

// itemInput defines a collection item in MCP tool input.
type itemInput struct {
	AssetID string `json:"asset_id"`
}

// saveAssetOutput is the success response for save_asset.
type saveAssetOutput struct {
	AssetID            string `json:"asset_id"`
	PortalURL          string `json:"portal_url,omitempty"`
	Message            string `json:"message"`
	ProvenanceCaptured bool   `json:"provenance_captured"`
	ToolCallsRecorded  int    `json:"tool_calls_recorded"`
}

// Config holds configuration for creating a portal toolkit.
type Config struct {
	Name            string
	AssetStore      portal.AssetStore
	ShareStore      portal.ShareStore
	VersionStore    portal.VersionStore
	CollectionStore portal.CollectionStore
	ThreadStore     portal.ThreadStore
	S3Client        portal.S3Client
	S3Bucket        string
	S3Prefix        string
	BaseURL         string
	MaxContentSize  int // max asset content size in bytes (0 = no limit)

	// Embedder embeds search queries for the ranked `search` action. When nil
	// or the noop placeholder, search degrades to lexical-only ranking (the
	// store decides via embedding.EmbedForSearch).
	Embedder embedding.Provider
}

// Toolkit implements the portal asset toolkit.
type Toolkit struct {
	name            string
	assetStore      portal.AssetStore
	shareStore      portal.ShareStore
	versionStore    portal.VersionStore
	collectionStore portal.CollectionStore
	threadStore     portal.ThreadStore
	s3Client        portal.S3Client
	s3Bucket        string
	s3Prefix        string
	baseURL         string
	maxContentSize  int
	embedder        embedding.Provider
	actions         map[string]manageActionHandler
	feedbackActions map[string]feedbackActionHandler

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
	collectionStore := cfg.CollectionStore
	if collectionStore == nil {
		collectionStore = portal.NewNoopCollectionStore()
	}
	tk := &Toolkit{
		name:            cfg.Name,
		assetStore:      assetStore,
		shareStore:      shareStore,
		versionStore:    versionStore,
		collectionStore: collectionStore,
		threadStore:     cfg.ThreadStore,
		s3Client:        cfg.S3Client,
		s3Bucket:        cfg.S3Bucket,
		s3Prefix:        cfg.S3Prefix,
		baseURL:         cfg.BaseURL,
		maxContentSize:  cfg.MaxContentSize,
		embedder:        cfg.Embedder,
	}
	tk.actions = tk.buildActions()
	tk.feedbackActions = tk.buildFeedbackActions()
	return tk
}

// Kind returns the toolkit kind.
func (*Toolkit) Kind() string { return "portal" }

// Name returns the toolkit instance name.
func (t *Toolkit) Name() string { return t.name }

// Connection returns the connection name for audit logging.
func (*Toolkit) Connection() string { return "" }

// saveToolDescription is the advertised description of the canonical
// save_asset tool.
const saveToolDescription = "Saves AI-generated content (JSX dashboard, HTML report, SVG chart, etc.) " +
	"to the asset portal as a versioned, viewable, shareable asset. " +
	"IMPORTANT: When creating content that should be saved, call this tool directly with the content " +
	"rather than first outputting it to the conversation and then saving separately - " +
	"this avoids regenerating the whole asset. " +
	"Automatically captures provenance (which tool calls produced this asset)."

// manageToolDescription is the advertised description of the canonical
// manage_asset tool.
const manageToolDescription = "Manages saved assets and collections. " +
	"Asset actions: list, get, update, delete, list_versions, revert, search. " +
	"Content actions: patch, locate, get_content, outline, stats, diff. " +
	"Collection actions: create_collection, list_collections, get_collection, " +
	"update_collection, delete_collection, set_sections. " +
	"Note: 'list' returns full metadata including provenance for each asset. " +
	"Use 'get' with a specific asset_id for the metadata row and 'get_content' for the body. " +
	"Use 'search' with a 'query' to rank your assets by relevance (semantic + " +
	"keyword) instead of paging the whole list. " +
	textpatch.VerbsDescription + " " +
	"Human feedback on assets is handled by the separate manage_feedback tool."

// RegisterTools registers save_asset and manage_asset with the MCP server.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        SaveToolName,
		Title:       "Save Asset",
		Description: saveToolDescription,
		InputSchema: saveAssetSchema,
	}, t.handleSaveAsset)

	mcp.AddTool(s, &mcp.Tool{
		Name:        ManageToolName,
		Title:       "Manage Asset",
		Description: manageToolDescription,
		InputSchema: manageAssetSchema,
	}, t.handleManageAsset)

	mcp.AddTool(s, &mcp.Tool{
		Name:  feedbackToolName,
		Title: "Manage Feedback",
		Description: "Reviews and responds to human feedback on your work. " +
			"Call action=list with NO target to get all pending feedback across the assets and collections you own " +
			"or can edit AND the shared general channel (newest first, excluding your own threads, plus any " +
			"awaiting your validation) — use this to 'review and act on any pending feedback'. " +
			"Call action=list with an asset_id/collection_id/prompt_id (or target_type=standalone) to scope to one target. " +
			"Other actions: get (a thread + its timeline), reply (post a comment), resolve (mark resolved), " +
			"request_validation, respond_validation (the thread author records validated/disputed via validation_result). " +
			"memory_capture thread_ids=[...] folds a thread into the knowledge loop and resolves it.",
		InputSchema: manageFeedbackSchema,
	}, t.handleManageFeedback)

	t.registerPrompts(s)
}

// registerPrompts registers user-facing prompts for the portal toolkit.
func (*Toolkit) registerPrompts(s *mcp.Server) {
	s.AddPrompt(&mcp.Prompt{
		Name:        saveAssetPromptName,
		Description: "Save output from this conversation as a viewable, shareable asset",
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
		Description: "Browse your saved assets",
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
			Description: "Save output from this conversation as a viewable, shareable asset",
			Category:    "toolkit",
			Content:     saveAssetPromptContent,
		},
		{
			Name:        showAssetsPromptName,
			Description: "Browse your saved assets",
			Category:    "toolkit",
			Content:     showAssetsPromptContent,
		},
	}
}

const saveAssetPromptContent = `Save the most recent output or analysis from this conversation as a shareable asset.

1. Identify the key output from our conversation (dashboard, report, chart, or analysis)
2. Package it with an appropriate name, description, and tags
3. Save it as an asset so it can be viewed and shared
4. Return the link to the saved asset`

const showAssetsPromptContent = `List my saved assets.

1. Retrieve all assets I have saved
2. Present them with names, descriptions, tags, and creation dates
3. Highlight the most recent items`

// Tools returns the list of tool names provided by this toolkit.
func (*Toolkit) Tools() []string {
	return []string{SaveToolName, ManageToolName, feedbackToolName}
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

// handleSaveAsset persists an asset to S3 and records metadata.
func (t *Toolkit) handleSaveAsset(ctx context.Context, _ *mcp.CallToolRequest, input saveAssetInput) (*mcp.CallToolResult, any, error) {
	if err := t.validateAndCheckSize(input); err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}

	userID := resolveOwnerID(ctx)
	userEmail := resolveOwnerEmail(ctx)
	sessionID := resolveSessionID(ctx)

	assetID, err := generateID()
	if err != nil {
		return toolkit.ErrorResult("internal error generating asset ID"), nil, nil
	}

	// A generic declaration (text/plain, application/octet-stream) is replaced
	// by the type detected from the content itself, so an agent that saved a
	// JSON payload under a catch-all type still lands in the JSON viewer.
	contentType := portal.ResolveContentType(input.ContentType, []byte(input.Content))
	s3Key := t.buildS3Key(userID, assetID, contentType)

	if t.s3Client == nil {
		return toolkit.ErrorResult("content storage not configured"), nil, nil
	}
	if err := t.s3Client.PutObject(ctx, t.s3Bucket, s3Key, []byte(input.Content), contentType); err != nil {
		return toolkit.ErrorResult("failed to upload content: " + err.Error()), nil, nil
	}

	prov := buildProvenance(ctx, userID, sessionID)
	if contentType != input.ContentType {
		prov.DeclaredContentType = input.ContentType
	}
	slog.Info("save_asset.provenance",
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
		ContentType: contentType,
		S3Bucket:    t.s3Bucket,
		S3Key:       s3Key,
		SizeBytes:   int64(len(input.Content)),
		Tags:        tags,
		Provenance:  prov,
		SessionID:   sessionID,
	}

	if err := t.assetStore.Insert(ctx, asset); err != nil {
		return toolkit.ErrorResult("failed to save asset metadata: " + err.Error()), nil, nil
	}

	// Create initial v1 version record.
	versionID, err := generateID()
	if err != nil {
		return toolkit.ErrorResult("failed to generate version ID: " + err.Error()), nil, nil
	}
	v1 := portal.AssetVersion{
		ID:            versionID,
		AssetID:       assetID,
		S3Key:         s3Key,
		S3Bucket:      t.s3Bucket,
		ContentType:   contentType,
		SizeBytes:     int64(len(input.Content)),
		CreatedBy:     userEmail,
		ChangeSummary: "Initial version",
	}
	if _, err := t.versionStore.CreateVersion(ctx, v1); err != nil {
		return toolkit.ErrorResult("failed to create initial version record: " + err.Error()), nil, nil
	}

	return toolkit.JSONResultTyped(t.buildSaveOutput(assetID, prov))
}

// manageActionHandler is a function that handles a manage_asset action.
type manageActionHandler func(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error)

// feedbackActionHandler is a function that handles a manage_feedback action.
type feedbackActionHandler func(ctx context.Context, input manageFeedbackInput) (*mcp.CallToolResult, any, error)

// handleManageFeedback dispatches to the appropriate manage_feedback action.
func (t *Toolkit) handleManageFeedback(ctx context.Context, _ *mcp.CallToolRequest, input manageFeedbackInput) (*mcp.CallToolResult, any, error) {
	handler, ok := t.feedbackActions[input.Action]
	if !ok {
		return toolkit.ErrorResult(fmt.Sprintf(
			"invalid action %q: must be one of: list, get, reply, resolve, request_validation, respond_validation",
			input.Action)), nil, nil
	}
	return handler(ctx, input)
}

// buildActions constructs the action dispatch table, called once during New().
func (t *Toolkit) buildActions() map[string]manageActionHandler {
	return map[string]manageActionHandler{
		actionList:             t.handleList,
		actionGet:              t.handleGet,
		actionUpdate:           t.handleUpdate,
		actionDelete:           t.handleDelete,
		actionListVersions:     t.handleListVersions,
		actionRevert:           t.handleRevert,
		actionCreateCollection: t.handleCreateCollection,
		actionListCollections:  t.handleListCollections,
		actionGetCollection:    t.handleGetCollection,
		actionUpdateCollection: t.handleUpdateCollection,
		actionDeleteCollection: t.handleDeleteCollection,
		actionSetSections:      t.handleSetSections,
		actionSearch:           t.handleSearch,
		actionPatch:            t.handlePatch,
		actionLocate:           t.handleLocate,
		actionGetContent:       t.handleGetContent,
		actionOutline:          t.handleOutline,
		actionStats:            t.handleStats,
		actionDiff:             t.handleDiff,
	}
}

// buildFeedbackActions constructs the manage_feedback dispatch table (#618).
func (t *Toolkit) buildFeedbackActions() map[string]feedbackActionHandler {
	return map[string]feedbackActionHandler{
		fbActionList:              t.handleListThreads,
		fbActionGet:               t.handleGetThread,
		fbActionReply:             t.handleReplyThread,
		fbActionResolve:           t.handleResolveThread,
		fbActionRequestValidation: t.handleRequestValidation,
		fbActionRespondValidation: t.handleRespondValidation,
	}
}

// handleManageAsset dispatches to the appropriate action handler.
func (t *Toolkit) handleManageAsset(ctx context.Context, _ *mcp.CallToolRequest, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	handler, ok := t.actions[input.Action]
	if !ok {
		return toolkit.ErrorResult(fmt.Sprintf(
			"invalid action %q: must be one of: list, get, update, delete, list_versions, revert, search, "+
				"patch, locate, get_content, outline, stats, diff, "+
				"create_collection, list_collections, get_collection, update_collection, delete_collection, set_sections",
			input.Action)), nil, nil
	}
	return handler(ctx, input)
}

func (t *Toolkit) handleList(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	ownerID := resolveOwnerID(ctx)

	assets, total, err := t.assetStore.List(ctx, portal.AssetFilter{
		OwnerID: ownerID,
		Limit:   input.Limit,
	})
	if err != nil {
		return toolkit.ErrorResult("failed to list assets: " + err.Error()), nil, nil
	}

	if assets == nil {
		assets = []portal.Asset{}
	}

	result := map[string]any{
		"assets":   assets,
		fieldTotal: total,
	}
	return toolkit.JSONResultTyped(result)
}

func (t *Toolkit) handleGet(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return toolkit.ErrorResult("asset_id is required for get action"), nil, nil
	}

	asset, err := t.assetStore.Get(ctx, input.AssetID)
	if err != nil {
		return middleware.NotFoundResult("asset not found: "+err.Error(), assetNotFoundHint), nil, nil
	}

	if asset.DeletedAt != nil {
		return toolkit.ErrorResult("asset has been deleted"), nil, nil
	}

	return toolkit.JSONResultTyped(asset)
}

func (t *Toolkit) handleUpdate(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return toolkit.ErrorResult("asset_id is required for update action"), nil, nil
	}

	asset, err := t.assetStore.Get(ctx, input.AssetID)
	if err != nil {
		return middleware.NotFoundResult("asset not found: "+err.Error(), assetNotFoundHint), nil, nil
	}

	ownerID := resolveOwnerID(ctx)
	if !t.isAdmin(ctx) && asset.OwnerID != ownerID {
		return toolkit.ErrorResult("you can only update your own assets"), nil, nil
	}

	updates, hasMetadata := metadataUpdate(input)
	hasContent := input.Content != ""

	if !hasContent && !hasMetadata {
		return toolkit.ErrorResult("no fields to update: provide content, name, description, or tags"), nil, nil
	}

	if hasContent {
		if _, contentErr := t.uploadContentUpdate(ctx, asset, input.Content, input.ContentType, input.ChangeSummary); contentErr != nil {
			return toolkit.ErrorResult("failed to upload new content: " + contentErr.Error()), nil, nil
		}
	}

	if hasMetadata {
		if err := t.assetStore.Update(ctx, input.AssetID, updates); err != nil {
			return toolkit.ErrorResult("failed to update asset: " + err.Error()), nil, nil
		}
	}

	return toolkit.JSONResultTyped(map[string]any{
		fieldAssetID: input.AssetID,
		fieldMessage: "Asset updated successfully.",
	})
}

// metadataUpdate builds the store update for the indexable fields and reports
// whether any were supplied. Content is versioned separately by
// uploadContentUpdate, which moves the asset's current-version pointer, so a
// content-only edit must not reach the metadata Update: the store's
// applyUpdateFields rejects an empty update with "no fields to update", which
// would report failure even though the content write already committed.
func metadataUpdate(input manageAssetInput) (update portal.AssetUpdate, present bool) {
	update = portal.AssetUpdate{Tags: input.Tags}
	if input.Name != "" {
		update.Name = &input.Name
	}
	if input.Description != "" {
		update.Description = &input.Description
	}
	return update, update.Name != nil || update.Description != nil || update.Tags != nil
}

// uploadContentUpdate writes replacement content as a new version and returns
// the version number assigned. Creating the version is what moves the asset's
// own s3_key, content_type and size_bytes forward — the version store does that
// in the same transaction — so a replacement whose type differs from the
// asset's carries the asset with it.
//
// summary is recorded as the version's change summary, which is what makes the
// version history readable; an empty summary falls back to the generic label.
func (t *Toolkit) uploadContentUpdate(ctx context.Context, asset *portal.Asset, content, declaredType, summary string) (int, error) {
	if t.maxContentSize > 0 && len(content) > t.maxContentSize {
		return 0, fmt.Errorf("content size %d exceeds maximum %d bytes", len(content), t.maxContentSize)
	}
	// The caller's declaration wins when specific; otherwise the asset's
	// existing type is the declaration, so an edit to a JSON asset stays JSON.
	declared := declaredType
	if declared == "" {
		declared = asset.ContentType
	}
	// One conversion feeds both detection and the upload; the body can be
	// megabytes, so converting per call site would copy it twice.
	data := []byte(content)
	ct := portal.ResolveContentType(declared, data)

	versionID, err := generateID()
	if err != nil {
		return 0, fmt.Errorf("generating version ID: %w", err)
	}
	ext := portal.ExtensionForContentType(ct)
	s3Key := path.Join(t.s3Prefix, asset.OwnerID, asset.ID, versionID, "content"+ext)

	if t.s3Client == nil {
		return 0, errors.New("content storage not configured")
	}
	if err := t.s3Client.PutObject(ctx, t.s3Bucket, s3Key, data, ct); err != nil {
		return 0, fmt.Errorf("s3 put: %w", err)
	}

	if summary == "" {
		summary = defaultChangeSummary
	}
	av := portal.AssetVersion{
		ID:            versionID,
		AssetID:       asset.ID,
		S3Key:         s3Key,
		S3Bucket:      t.s3Bucket,
		ContentType:   ct,
		SizeBytes:     int64(len(data)),
		CreatedBy:     resolveOwnerEmail(ctx),
		ChangeSummary: summary,
	}
	version, err := t.versionStore.CreateVersion(ctx, av)
	if err != nil {
		t.cleanupOrphanedS3(ctx, t.s3Bucket, s3Key)
		return 0, fmt.Errorf("creating version: %w", err)
	}
	return version, nil
}

func (t *Toolkit) handleDelete(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return toolkit.ErrorResult("asset_id is required for delete action"), nil, nil
	}

	asset, err := t.assetStore.Get(ctx, input.AssetID)
	if err != nil {
		return middleware.NotFoundResult("asset not found: "+err.Error(), assetNotFoundHint), nil, nil
	}

	ownerID := resolveOwnerID(ctx)
	if !t.isAdmin(ctx) && asset.OwnerID != ownerID {
		return toolkit.ErrorResult("you can only delete your own assets"), nil, nil
	}

	if err := t.assetStore.SoftDelete(ctx, input.AssetID); err != nil {
		return toolkit.ErrorResult("failed to delete asset: " + err.Error()), nil, nil
	}

	return toolkit.JSONResultTyped(map[string]any{
		fieldAssetID: input.AssetID,
		fieldMessage: "Asset deleted successfully.",
	})
}

func (t *Toolkit) handleListVersions(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if input.AssetID == "" {
		return toolkit.ErrorResult("asset_id is required for list_versions action"), nil, nil
	}

	limit := input.Limit
	if limit <= 0 {
		limit = defaultVersionListLimit
	}
	versions, total, err := t.versionStore.ListByAsset(ctx, input.AssetID, limit, 0)
	if err != nil {
		return toolkit.ErrorResult("failed to list versions: " + err.Error()), nil, nil
	}
	if versions == nil {
		versions = []portal.AssetVersion{}
	}
	return toolkit.JSONResultTyped(map[string]any{
		"versions": versions,
		fieldTotal: total,
	})
}

func (t *Toolkit) handleRevert(ctx context.Context, input manageAssetInput) (*mcp.CallToolResult, any, error) {
	if !input.validForRevert() {
		return toolkit.ErrorResult("asset_id and version (> 0) are required for revert action"), nil, nil
	}

	asset, err := t.assetStore.Get(ctx, input.AssetID)
	if err != nil {
		return middleware.NotFoundResult("asset not found: "+err.Error(), assetNotFoundHint), nil, nil
	}

	ownerID := resolveOwnerID(ctx)
	if !t.isAdmin(ctx) && asset.OwnerID != ownerID {
		return toolkit.ErrorResult("you can only revert your own assets"), nil, nil
	}

	targetVer, err := t.versionStore.GetByVersion(ctx, input.AssetID, input.Version)
	if err != nil {
		return middleware.NotFoundResult("version not found: "+err.Error(), "Call manage_asset action=list_versions to see valid version numbers."), nil, nil
	}

	if t.s3Client == nil {
		return toolkit.ErrorResult("content storage not configured"), nil, nil
	}
	data, _, err := t.s3Client.GetObject(ctx, targetVer.S3Bucket, targetVer.S3Key)
	if err != nil {
		return toolkit.ErrorResult("failed to read version content: " + err.Error()), nil, nil
	}

	versionID, err := generateID()
	if err != nil {
		return toolkit.ErrorResult("failed to generate version ID: " + err.Error()), nil, nil
	}
	ext := portal.ExtensionForContentType(targetVer.ContentType)
	newKey := path.Join(t.s3Prefix, asset.OwnerID, asset.ID, versionID, "content"+ext)

	if err := t.s3Client.PutObject(ctx, t.s3Bucket, newKey, data, targetVer.ContentType); err != nil {
		return toolkit.ErrorResult("failed to upload reverted content: " + err.Error()), nil, nil
	}

	av := portal.AssetVersion{
		ID:            versionID,
		AssetID:       input.AssetID,
		S3Key:         newKey,
		S3Bucket:      t.s3Bucket,
		ContentType:   targetVer.ContentType,
		SizeBytes:     int64(len(data)),
		CreatedBy:     resolveOwnerEmail(ctx),
		ChangeSummary: fmt.Sprintf("Reverted from v%d", input.Version),
	}
	assignedVersion, err := t.versionStore.CreateVersion(ctx, av)
	if err != nil {
		t.cleanupOrphanedS3(ctx, t.s3Bucket, newKey)
		return toolkit.ErrorResult("failed to create revert version: " + err.Error()), nil, nil
	}

	return toolkit.JSONResultTyped(map[string]any{
		fieldAssetID: input.AssetID,
		"version":    assignedVersion,
		fieldMessage: fmt.Sprintf("Reverted to version %d. New version: %d.", input.Version, assignedVersion),
	})
}

func (m manageAssetInput) validForRevert() bool {
	return m.AssetID != "" && m.Version > 0
}

// --- Helpers ---

// cleanupOrphanedS3 attempts to delete an S3 object that was uploaded but whose
// corresponding version record failed to persist. Errors are logged but not propagated.
func (t *Toolkit) cleanupOrphanedS3(ctx context.Context, bucket, key string) {
	if t.s3Client == nil {
		return
	}
	if err := t.s3Client.DeleteObject(ctx, bucket, key); err != nil {
		slog.Warn("failed to clean up orphaned S3 object", // #nosec G706 -- structured log, not user-facing
			"bucket", bucket, "key", key, "error", err)
	}
}

// resolveOwnerID returns the authenticated user ID from the context, defaulting to "anonymous".
func resolveOwnerID(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil && pc.UserID != "" {
		return pc.UserID
	}
	return anonymousUserName
}

// resolveOwnerEmail returns the authenticated user's email from the context,
// defaulting to "anonymous" if no context or email is available.
func resolveOwnerEmail(ctx context.Context) string {
	pc := middleware.GetPlatformContext(ctx)
	if pc != nil && pc.UserEmail != "" {
		return pc.UserEmail
	}
	return anonymousUserName
}

func (t *Toolkit) validateAndCheckSize(input saveAssetInput) error {
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

func (t *Toolkit) buildSaveOutput(assetID string, prov portal.Provenance) saveAssetOutput {
	out := saveAssetOutput{
		AssetID:            assetID,
		Message:            "Asset saved successfully.",
		ProvenanceCaptured: len(prov.ToolCalls) > 0,
		ToolCallsRecorded:  len(prov.ToolCalls),
	}
	if t.baseURL != "" {
		out.PortalURL = t.baseURL + "/portal/assets/" + assetID
	}
	return out
}

func validateSaveInput(input saveAssetInput) error {
	if err := portal.ValidateAssetName(input.Name); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	if err := portal.ValidateContentType(input.ContentType); err != nil {
		return fmt.Errorf(validationFmt, err)
	}
	if input.Content == "" {
		return errors.New("content is required")
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
