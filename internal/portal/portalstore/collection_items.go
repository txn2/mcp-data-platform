package portalstore

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/portal/collitems"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

var _ portaldomain.CollectionItemAppender = (*postgresCollectionStore)(nil)

// AppendItem adds one asset at the end of one section (#2260).
func (s *postgresCollectionStore) AppendItem(ctx context.Context, collectionID, sectionID, assetID string) (portaldomain.AppendedItem, error) { //nolint:revive // interface impl
	return collitems.AppendItem(ctx, s.db, collectionID, sectionID, assetID) //nolint:wrapcheck // the package's errors are the contract
}

// SectionSummaries reads the sections of the given collections (#2260).
func (s *postgresCollectionStore) SectionSummaries(ctx context.Context, ids []string, assetID string) (map[string][]portaldomain.SectionSummary, error) { //nolint:revive // interface impl
	return collitems.SectionSummaries(ctx, s.db, ids, assetID) //nolint:wrapcheck // the package's errors are the contract
}
