package portaldomain

import (
	"context"
	"errors"
	"time"
)

// Errors an item append is refused with. The handler maps each to its status;
// the text is what the person adding the asset reads.
var (
	// ErrSectionNotFound means the section named is not one of the
	// collection's sections.
	ErrSectionNotFound = errors.New("section not found in this collection")
	// ErrItemAlreadyInSection means the asset is already an item of the
	// section; a section holds an asset once.
	ErrItemAlreadyInSection = errors.New("the asset is already in this section")
	// ErrSectionFull means the section already holds MaxItemsPerSection items.
	ErrSectionFull = errors.New("the section is full")
)

// AppendedItem is what an append wrote: the item, and the section it landed
// in, which the caller did not name when the collection had none or the
// caller let the last section be chosen.
type AppendedItem struct {
	Item         CollectionItem `json:"item"`
	SectionID    string         `json:"section_id" example:"sec_01HK7R9C"`
	SectionTitle string         `json:"section_title" example:"Key Findings"`
}

// SectionSummary is one section of a collection as the add-to-collection
// picker reads it: enough to name the section and say whether the asset is
// already in it, without the items.
type SectionSummary struct {
	ID        string `json:"id" example:"sec_01HK7R9C"`
	Title     string `json:"title" example:"Key Findings"`
	ItemCount int    `json:"item_count" example:"4"`
	// HasAsset reports whether the asset the picker was opened on is an item
	// of this section.
	HasAsset bool `json:"has_asset"`
}

// CollectionTarget is one collection an asset can be added to: the
// collection, its sections, and the caller's authority over it.
type CollectionTarget struct {
	ID         string           `json:"id" example:"col_01HK7R8Z"`
	Name       string           `json:"name" example:"Q4 Analysis"`
	OwnerEmail string           `json:"owner_email,omitempty" example:"alice@example.com"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Sections   []SectionSummary `json:"sections"`
	// Shared reports that the caller edits this collection through a share
	// rather than owning it.
	Shared bool `json:"shared"`
}

// CollectionItemAppender is the store side of adding one asset to a
// collection without rewriting its sections. PostgreSQL implements it; a
// store that does not leaves the portal without the add-to-collection routes.
type CollectionItemAppender interface {
	// AppendItem adds assetID at the end of sectionID. An empty sectionID
	// means the collection's last section, or a new untitled section when the
	// collection has none. The section's other items, the other sections and
	// the collection's name, description and config are untouched.
	AppendItem(ctx context.Context, collectionID, sectionID, assetID string) (AppendedItem, error)
	// SectionSummaries returns, for each collection id given, its sections in
	// order, each marked with whether assetID is one of its items.
	SectionSummaries(ctx context.Context, collectionIDs []string, assetID string) (map[string][]SectionSummary, error)
}
