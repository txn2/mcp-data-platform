package exportadapters

import (
	"context"
	"fmt"

	"github.com/txn2/mcp-data-platform/pkg/portal"
	graphqlkit "github.com/txn2/mcp-data-platform/pkg/toolkits/graphql"
)

// Compile-time guarantees that the exporter satisfies every toolkit-side
// interface it is wired into.
var (
	_ graphqlkit.ExportAssetStore   = (*GraphQLExporter)(nil)
	_ graphqlkit.ExportVersionStore = (*GraphQLExporter)(nil)
	_ graphqlkit.ExportShareCreator = (*GraphQLExporter)(nil)
)

// graphqlExportTool is the tool name recorded on an exported asset's
// provenance.
const graphqlExportTool = "graphql_export"

// GraphQLExporter adapts the portal stores to the graphql toolkit's export
// interfaces. It mirrors APIExporter for the reason the package doc gives: the
// two toolkits declare field-identical but distinct export DTOs so that neither
// imports the other, and Go cannot map fields across unrelated struct types
// generically.
type GraphQLExporter struct {
	assetStore   portal.AssetStore
	versionStore portal.VersionStore
	shareStore   portal.ShareStore
	baseURL      string
	capture      portal.ProvenanceCapturer
}

// NewGraphQLExporter builds a GraphQLExporter over the given portal stores. An
// empty baseURL disables share-URL computation (the share row is still
// inserted). The capturer resolves the calls the exported asset was built from;
// nil records only the export's own call.
func NewGraphQLExporter(assets portal.AssetStore, versions portal.VersionStore, shares portal.ShareStore, baseURL string, capture portal.ProvenanceCapturer) *GraphQLExporter {
	return &GraphQLExporter{assetStore: assets, versionStore: versions, shareStore: shares, baseURL: baseURL, capture: capture}
}

func (e *GraphQLExporter) InsertExportAsset(ctx context.Context, asset graphqlkit.ExportAsset) error { //nolint:dupl,revive // implements graphql.ExportAssetStore; mirrors APIExporter over a distinct field-identical DTO (see package doc)
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
			tool:      graphqlExportTool,
			own:       graphqlOwnCall(asset.Provenance.ToolCalls),
		}),
		SessionID:      asset.SessionID,
		IdempotencyKey: asset.IdempotencyKey,
	}); err != nil {
		return fmt.Errorf("inserting export asset: %w", err)
	}
	return nil
}

func (e *GraphQLExporter) GetByIdempotencyKey(ctx context.Context, ownerID, key string) (*graphqlkit.ExportAssetRef, error) { //nolint:revive // implements graphql.ExportAssetStore
	asset, err := e.assetStore.GetByIdempotencyKey(ctx, ownerID, key)
	if err != nil {
		return nil, fmt.Errorf("looking up export idempotency key: %w", err)
	}
	return &graphqlkit.ExportAssetRef{ID: asset.ID, SizeBytes: asset.SizeBytes}, nil
}

func (e *GraphQLExporter) CreateExportVersion(ctx context.Context, ver graphqlkit.ExportVersion) (int, error) { //nolint:revive // implements graphql.ExportVersionStore
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

func (e *GraphQLExporter) CreatePublicShare(ctx context.Context, assetID, createdBy string) (string, error) { //nolint:revive // implements graphql.ExportShareCreator
	return createExportShare(ctx, e.shareStore, e.baseURL, assetID, createdBy)
}

// graphqlOwnCall renders the export's record of itself as a captured call: the
// document it ran and the connection it ran against. The document is the
// statement, because a GraphQL call's address is one URL and the document is
// the whole of what it asked for.
func graphqlOwnCall(calls []graphqlkit.ExportProvenanceCall) *portal.ProvenanceCall {
	if len(calls) == 0 {
		return nil
	}
	c := calls[len(calls)-1]
	return &portal.ProvenanceCall{
		Kind:       portal.ProvenanceKindGraphQL,
		Tool:       c.ToolName,
		Connection: stringParam(c.Parameters, "connection"),
		Statement:  stringParam(c.Parameters, "query"),
		Outcome:    portal.ProvenanceOutcomeSuccess,
		Timestamp:  parseTimestamp(c.Timestamp),
	}
}
