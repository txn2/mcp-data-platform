package catalog

import "time"

// Definition is a glossary term, glossary node, domain, data product or tag.
type Definition struct {
	ID        string `json:"id"`
	Reference string `json:"reference"`
	Kind      Kind   `json:"kind"`
	// Slug is the stable readable key a reference may use in place of the id.
	// For a tag it is the tag's name.
	Slug string `json:"slug"`
	Name string `json:"name"`
	// Parent is the glossary node a term or node sits under, as a reference.
	Parent    string    `json:"parent,omitempty"`
	Aspects   Aspects   `json:"aspects"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DefinitionInput is a definition as a writer declares it. An empty Slug is
// derived from the name.
type DefinitionInput struct {
	Kind   Kind
	Slug   string
	Name   string
	Parent string
}
