package knowledge

import (
	"context"
	"log/slog"

	"github.com/txn2/mcp-data-platform/pkg/semantic"
)

// dataProductPrefix is the URN form of a catalog data product reference, the
// second form the catalog provider owns for fetch (#1590).
const dataProductPrefix = "urn:li:dataProduct:"

// DataProductEntity is the content of a fetched data product reference: the
// product itself and the datasets that make it up, each put through the
// caller's connection boundary exactly as a governance entity's carriers are.
type DataProductEntity struct {
	URN         string `json:"urn"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Domain is the domain the product is filed under.
	Domain *semantic.Domain `json:"domain,omitempty"`
	// Owners are the product's owners.
	Owners []semantic.Owner `json:"owners,omitempty"`
	// CustomProperties are the product's free-form key/value properties.
	CustomProperties map[string]string `json:"custom_properties,omitempty"`
	// Datasets are the member datasets the caller may see; each can be fetched
	// in turn by its URN.
	Datasets []GovernanceDataset `json:"datasets,omitempty"`
	// DatasetsWithheld counts the members the caller's connection boundary
	// removed, and Notice explains it.
	DatasetsWithheld int    `json:"datasets_withheld,omitempty"`
	Notice           string `json:"notice,omitempty"`
}

// kindDataProduct is the machine label on a fetched data product.
const kindDataProduct = "data_product"

// fetchDataProduct dereferences a urn:li:dataProduct:<id> reference (#1590). A
// deployment whose catalog cannot read products, and a URN the catalog has no
// product for, are both a clean not-found. A missing product is not reported as
// an error: DataHub answers with the URN it was handed and no properties, so the
// miss is recognized by the record carrying nothing of its own
// (dataProductExists, #1605). The product itself carries no connection
// attribution, so the boundary leaves it visible; its member datasets are
// catalog entities and are filtered like any other, with the removed count and
// the reason reported rather than the list silently shortening.
func (p *CatalogProvider) fetchDataProduct(ctx context.Context, ref string, caller Caller) (*Document, bool, error) {
	if p.products == nil {
		return nil, true, ErrNotFound
	}
	product, err := p.products.GetDataProduct(ctx, ref)
	if err != nil {
		slog.Debug("catalog data product fetch miss", "urn", ref, "error", err)
		return nil, true, ErrNotFound
	}
	if product == nil || product.URN == "" {
		return nil, true, ErrNotFound
	}
	if !dataProductExists(product) {
		// A product the catalog has no entry for comes back as its own URN and
		// nothing else, not as an error (#1605).
		slog.Debug("catalog data product fetch resolved to a urn-only record", "urn", ref)
		return nil, true, ErrNotFound
	}
	entity := DataProductEntity{
		URN:              product.URN,
		Kind:             kindDataProduct,
		Name:             governanceName(semantic.EntityRef{URN: product.URN, Name: product.Name}),
		Description:      product.Description,
		Domain:           product.Domain,
		Owners:           product.Owners,
		CustomProperties: product.CustomProperties,
	}
	urns := make([]string, 0, len(product.Assets))
	for _, asset := range product.Assets {
		if asset.URN == "" {
			continue
		}
		if !caller.allowsURN(asset.URN) {
			entity.DatasetsWithheld++
			continue
		}
		entity.Datasets = append(entity.Datasets, GovernanceDataset{URN: asset.URN, Name: asset.Name})
		urns = append(urns, asset.URN)
	}
	entity.Notice = withheldContentNotice(entity.DatasetsWithheld, caller.Persona)
	return &Document{
		Reference:  ref,
		Source:     SourceCatalog,
		Title:      entity.Name,
		Body:       entity.Description,
		Content:    entity,
		EntityURNs: urns,
	}, true, nil
}
