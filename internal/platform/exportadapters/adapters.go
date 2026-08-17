// Package exportadapters adapts the platform's portal stores to the export
// interfaces consumed by the trino and api-gateway toolkits.
//
// The trino_export and api_export tools each declare a minimal interface
// subset of the portal store they need (to avoid a portal→registry→toolkit
// import cycle). An Exporter bridges concrete portal.AssetStore /
// portal.VersionStore / portal.ShareStore to those toolkit-side interfaces,
// translating between the toolkit's export DTOs and the portal row types.
//
// One exporter per toolkit implements all three interfaces (asset store,
// version store, share creator), so wiring is a single construction whose
// result is assigned to each ExportDeps field. Each exporter takes its
// dependencies as constructor parameters (issue #756).
//
// TrinoExporter and APIExporter are near-identical: the two toolkits define
// field-identical-but-distinct ExportAsset/ExportVersion DTOs in separate
// packages (to avoid the import cycle), and Go cannot map fields across
// unrelated struct types generically. The mapping is therefore written once
// per toolkit; token generation, the default notice, and the public-share
// flow are shared via portal primitives and createExportShare.
package exportadapters

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	apigatewaykit "github.com/txn2/mcp-data-platform/pkg/toolkits/apigateway"
	trinokit "github.com/txn2/mcp-data-platform/pkg/toolkits/trino"
)

// Compile-time guarantees that each exporter satisfies every toolkit-side
// interface it is wired into. These catch drift if a toolkit changes its
// export contract.
var (
	_ trinokit.ExportAssetStore        = (*TrinoExporter)(nil)
	_ trinokit.ExportVersionStore      = (*TrinoExporter)(nil)
	_ trinokit.ExportShareCreator      = (*TrinoExporter)(nil)
	_ apigatewaykit.ExportAssetStore   = (*APIExporter)(nil)
	_ apigatewaykit.ExportVersionStore = (*APIExporter)(nil)
	_ apigatewaykit.ExportShareCreator = (*APIExporter)(nil)
)

// TrinoExporter adapts the portal stores to the trino toolkit's export
// interfaces (ExportAssetStore, ExportVersionStore, ExportShareCreator).
type TrinoExporter struct {
	assetStore   portal.AssetStore
	versionStore portal.VersionStore
	shareStore   portal.ShareStore
	baseURL      string
	capture      portal.ProvenanceCapturer
}

// NewTrinoExporter builds a TrinoExporter over the given portal stores. An
// empty baseURL disables share-URL computation (the share row is still
// inserted). The capturer resolves the calls the exported asset was built
// from; nil records only the export's own statement.
func NewTrinoExporter(assets portal.AssetStore, versions portal.VersionStore, shares portal.ShareStore, baseURL string, capture portal.ProvenanceCapturer) *TrinoExporter {
	return &TrinoExporter{assetStore: assets, versionStore: versions, shareStore: shares, baseURL: baseURL, capture: capture}
}

func (e *TrinoExporter) InsertExportAsset(ctx context.Context, asset trinokit.ExportAsset) error { //nolint:revive // implements trino.ExportAssetStore
	if err := e.assetStore.Insert(ctx, portal.Asset{
		ID:          asset.ID,
		OwnerID:     asset.OwnerID,
		OwnerEmail:  asset.OwnerEmail,
		Name:        asset.Name,
		Description: asset.Description,
		ContentType: asset.ContentType,
		S3Bucket:    asset.S3Bucket,
		S3Key:       asset.S3Key,
		SizeBytes:   asset.SizeBytes,
		Tags:        asset.Tags,
		Provenance: capturedProvenance(ctx, e.capture, provenanceInput{
			userID:    asset.Provenance.UserID,
			sessionID: asset.Provenance.SessionID,
			tool:      trinoExportTool,
			own:       trinoOwnCall(asset.Provenance.ToolCalls),
		}),
		SessionID:      asset.SessionID,
		IdempotencyKey: asset.IdempotencyKey,
	}); err != nil {
		return fmt.Errorf("inserting export asset: %w", err)
	}
	return nil
}

func (e *TrinoExporter) GetByIdempotencyKey(ctx context.Context, ownerID, key string) (*trinokit.ExportAssetRef, error) { //nolint:revive // implements trino.ExportAssetStore
	asset, err := e.assetStore.GetByIdempotencyKey(ctx, ownerID, key)
	if err != nil {
		return nil, fmt.Errorf("looking up export idempotency key: %w", err)
	}
	return &trinokit.ExportAssetRef{ID: asset.ID, SizeBytes: asset.SizeBytes}, nil
}

func (e *TrinoExporter) CreateExportVersion(ctx context.Context, ver trinokit.ExportVersion) (int, error) { //nolint:revive // implements trino.ExportVersionStore
	n, err := e.versionStore.CreateVersion(ctx, portal.AssetVersion{
		ID:            ver.ID,
		AssetID:       ver.AssetID,
		S3Key:         ver.S3Key,
		S3Bucket:      ver.S3Bucket,
		ContentType:   ver.ContentType,
		SizeBytes:     ver.SizeBytes,
		CreatedBy:     ver.CreatedBy,
		ChangeSummary: ver.ChangeSummary,
	})
	if err != nil {
		return 0, fmt.Errorf("creating export version: %w", err)
	}
	return n, nil
}

func (e *TrinoExporter) CreatePublicShare(ctx context.Context, assetID, createdBy string) (string, error) { //nolint:revive // implements trino.ExportShareCreator
	return createExportShare(ctx, e.shareStore, e.baseURL, assetID, createdBy)
}

// trinoOwnCall renders the export's record of itself as a captured call: the
// statement it ran and the connection it ran against. The export's own audit
// row is written after the tool returns, so this is the only account of it
// available at insert time.
func trinoOwnCall(calls []trinokit.ExportProvenanceCall) *portal.ProvenanceCall {
	if len(calls) == 0 {
		return nil
	}
	c := calls[len(calls)-1]
	return &portal.ProvenanceCall{
		Kind:       portal.ProvenanceKindSQL,
		Tool:       c.ToolName,
		Connection: stringParam(c.Parameters, "connection"),
		Statement:  stringParam(c.Parameters, "export_query"),
		Outcome:    portal.ProvenanceOutcomeSuccess,
		Timestamp:  parseTimestamp(c.Timestamp),
	}
}

// APIExporter adapts the portal stores to the api-gateway toolkit's export
// interfaces. It mirrors TrinoExporter; the divergence is the field-identical-
// but-distinct apigatewaykit.Export* DTO types, which Go cannot map across
// generically (see the package doc).
type APIExporter struct {
	assetStore   portal.AssetStore
	versionStore portal.VersionStore
	shareStore   portal.ShareStore
	baseURL      string
	capture      portal.ProvenanceCapturer
}

// NewAPIExporter builds an APIExporter over the given portal stores. An empty
// baseURL disables share-URL computation (the share row is still inserted).
// The capturer resolves the calls the exported asset was built from; nil
// records only the export's own request.
func NewAPIExporter(assets portal.AssetStore, versions portal.VersionStore, shares portal.ShareStore, baseURL string, capture portal.ProvenanceCapturer) *APIExporter {
	return &APIExporter{assetStore: assets, versionStore: versions, shareStore: shares, baseURL: baseURL, capture: capture}
}

func (e *APIExporter) InsertExportAsset(ctx context.Context, asset apigatewaykit.ExportAsset) error { //nolint:dupl,revive // implements apigateway.ExportAssetStore; mirrors TrinoExporter over a distinct field-identical DTO (see package doc)
	if err := e.assetStore.Insert(ctx, portal.Asset{
		ID:          asset.ID,
		OwnerID:     asset.OwnerID,
		OwnerEmail:  asset.OwnerEmail,
		Name:        asset.Name,
		Description: asset.Description,
		ContentType: asset.ContentType,
		S3Bucket:    asset.S3Bucket,
		S3Key:       asset.S3Key,
		SizeBytes:   asset.SizeBytes,
		Tags:        asset.Tags,
		Provenance: capturedProvenance(ctx, e.capture, provenanceInput{
			userID:              asset.Provenance.UserID,
			sessionID:           asset.Provenance.SessionID,
			tool:                apiExportTool,
			own:                 apiOwnCall(asset.Provenance.ToolCalls),
			declaredContentType: asset.Provenance.DeclaredContentType,
		}),
		SessionID:      asset.SessionID,
		IdempotencyKey: asset.IdempotencyKey,
	}); err != nil {
		return fmt.Errorf("inserting export asset: %w", err)
	}
	return nil
}

func (e *APIExporter) GetByIdempotencyKey(ctx context.Context, ownerID, key string) (*apigatewaykit.ExportAssetRef, error) { //nolint:revive // implements apigateway.ExportAssetStore
	asset, err := e.assetStore.GetByIdempotencyKey(ctx, ownerID, key)
	if err != nil {
		return nil, fmt.Errorf("looking up export idempotency key: %w", err)
	}
	return &apigatewaykit.ExportAssetRef{ID: asset.ID, SizeBytes: asset.SizeBytes}, nil
}

func (e *APIExporter) CreateExportVersion(ctx context.Context, ver apigatewaykit.ExportVersion) (int, error) { //nolint:revive // implements apigateway.ExportVersionStore
	n, err := e.versionStore.CreateVersion(ctx, portal.AssetVersion{
		ID:            ver.ID,
		AssetID:       ver.AssetID,
		S3Key:         ver.S3Key,
		S3Bucket:      ver.S3Bucket,
		ContentType:   ver.ContentType,
		SizeBytes:     ver.SizeBytes,
		CreatedBy:     ver.CreatedBy,
		ChangeSummary: ver.ChangeSummary,
	})
	if err != nil {
		return 0, fmt.Errorf("creating export version: %w", err)
	}
	return n, nil
}

func (e *APIExporter) CreatePublicShare(ctx context.Context, assetID, createdBy string) (string, error) { //nolint:revive // implements apigateway.ExportShareCreator
	return createExportShare(ctx, e.shareStore, e.baseURL, assetID, createdBy)
}

// apiOwnCall renders the export's record of itself as a captured call: the
// request it issued and how the upstream answered. An upstream error status is
// recorded as a failed call — the export still produced an asset, and what it
// holds is that response.
func apiOwnCall(calls []apigatewaykit.ExportProvenanceCall) *portal.ProvenanceCall {
	if len(calls) == 0 {
		return nil
	}
	c := calls[len(calls)-1]
	call := &portal.ProvenanceCall{
		Kind:       portal.ProvenanceKindAPI,
		Tool:       c.ToolName,
		Connection: stringParam(c.Parameters, "connection"),
		Method:     stringParam(c.Parameters, "method"),
		Path:       stringParam(c.Parameters, "path"),
		Outcome:    portal.ProvenanceOutcomeSuccess,
		Timestamp:  parseTimestamp(c.Timestamp),
	}
	if status, ok := c.Parameters["upstream_status"].(int); ok && status >= httpErrorStatus {
		call.Outcome = portal.ProvenanceOutcomeError
		call.Error = fmt.Sprintf("upstream returned %d", status)
	}
	return call
}

// createExportShare inserts a share row for an exported asset and returns its
// view URL (or an empty string when baseURL is unset). Returning a bare token
// in place of the URL would put a non-URL value in the model-visible share_url
// field, so an unset baseURL yields an empty URL while still persisting the
// share token.
//
// The share is authenticated-mode: an export names no recipient, and the URL
// is handed back into a conversation, so it opens for signed-in platform users
// rather than for anyone who receives a copy of it (#999).
//
// Token generation and the default notice reuse portal's primitives so export-
// created shares stay identical to portal-created ones.
func createExportShare(ctx context.Context, shareStore portal.ShareStore, baseURL, assetID, createdBy string) (string, error) {
	token, err := portal.GenerateShareToken()
	if err != nil {
		return "", fmt.Errorf("generating share token: %w", err)
	}

	share := portal.Share{
		ID:         uuid.New().String(),
		AssetID:    assetID,
		Token:      token,
		CreatedBy:  createdBy,
		NoticeText: portal.DefaultNoticeText,
		AccessMode: portal.AccessModeAuthenticated,
	}

	if err := shareStore.Insert(ctx, share); err != nil {
		return "", fmt.Errorf("inserting export share: %w", err)
	}

	if baseURL != "" {
		return fmt.Sprintf("%s/portal/view/%s", baseURL, token), nil
	}
	return "", nil
}

// Tool names of the two export paths, as they appear in provenance and in the
// audit log.
const (
	trinoExportTool = "trino_export"
	apiExportTool   = "api_export"

	// httpErrorStatus is the first HTTP status an upstream returns that makes
	// the export's own call a failed one.
	httpErrorStatus = 400
)

// provenanceInput is what an exporter knows about the write it is performing:
// who ran it, in which session, under which tool, and the export call's own
// account of itself.
type provenanceInput struct {
	userID              string
	sessionID           string
	tool                string
	own                 *portal.ProvenanceCall
	declaredContentType string
}

// capturedProvenance builds the provenance stored on an exported asset: the
// calls the session made since its previous capture, resolved from the audit
// log, with the export's own call last (#1320). Without a capturer — a
// deployment with no audit store — the asset still records the export itself.
func capturedProvenance(ctx context.Context, capture portal.ProvenanceCapturer, in provenanceInput) portal.Provenance {
	prov := portal.Provenance{
		UserID:              in.userID,
		SessionID:           in.sessionID,
		DeclaredContentType: in.declaredContentType,
	}
	req := portal.ProvenanceRequest{
		Tool:      in.tool,
		SessionID: in.sessionID,
		UserID:    in.userID,
		Version:   1,
		Own:       in.own,
	}
	if capture == nil {
		if in.own != nil {
			prov.Captures = []portal.ProvenanceCapture{{
				Tool:       in.tool,
				CapturedAt: time.Now().UTC(),
				Version:    1,
				SessionID:  in.sessionID,
				Calls:      []portal.ProvenanceCall{*in.own},
			}}
		}
		return prov
	}
	if result := capture(ctx, req); len(result.Calls) > 0 {
		prov.Captures = []portal.ProvenanceCapture{result}
	}
	return prov
}

// stringParam reads a string out of a toolkit-side provenance parameter map.
func stringParam(params map[string]any, key string) string {
	v, _ := params[key].(string)
	return v
}

// parseTimestamp reads the RFC 3339 timestamp a toolkit stamped on its own
// call record, falling back to now when it is absent or unparseable — a call
// with no time reads as having no provenance at all.
func parseTimestamp(value string) time.Time {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC()
	}
	return time.Now().UTC()
}
