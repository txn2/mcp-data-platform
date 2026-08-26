package assetrefs

import "github.com/txn2/mcp-data-platform/pkg/portal/knowledgepage"

// AssetURI is the canonical form an asset is referenced by from another
// asset's content: mcp:asset:<id> (#1488).
//
// It is built from the platform's one reference vocabulary rather than
// formatted here, so the string a picker records, the string a search hit
// hands an agent, and the string fetch dereferences are the same string. An
// author who found an asset through search can paste that reference straight
// into their markup.
func AssetURI(assetID string) string {
	return knowledgepage.EntityRef{
		TargetType: knowledgepage.RefTargetAsset,
		AssetID:    assetID,
	}.URN()
}
