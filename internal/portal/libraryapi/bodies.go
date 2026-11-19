package libraryapi

import "github.com/txn2/mcp-data-platform/internal/portal/library"

// The bodies the recents and favorites routes take and answer with.

// ItemsResponse is a list of recents or favorites.
type ItemsResponse struct {
	Items []library.Item `json:"items"`
}

// OpenRequest names the item a person opened.
type OpenRequest struct {
	Kind string `json:"kind" example:"asset"`
	ID   string `json:"id" example:"asset_01HK7R8Z8M0Y6A5G1R6FQ2VQNK"`
}
