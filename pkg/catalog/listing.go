package catalog

import (
	"context"
	"errors"
	"fmt"
)

// ErrInUse is returned when a definition cannot be deleted because it is still
// used: a glossary node with terms or nodes under it, or a term, tag or domain
// a dataset or column carries.
var ErrInUse = errors.New("catalog definition in use")

// InUseError says what still uses a definition a delete was refused for.
type InUseError struct {
	Reference string
	// Children is how many glossary terms and nodes sit under a glossary node.
	Children int
	// Entities is how many datasets and columns carry the definition.
	Entities int
}

// Error implements the error interface.
func (e *InUseError) Error() string {
	if e.Children > 0 {
		return fmt.Sprintf("%s has %d terms or nodes under it; move or delete them first", e.Reference, e.Children)
	}
	return fmt.Sprintf("%s is used by %d datasets or columns; remove it from them first", e.Reference, e.Entities)
}

// Unwrap lets errors.Is match ErrInUse.
func (*InUseError) Unwrap() error { return ErrInUse }

// DefinitionQuery narrows a definition listing.
type DefinitionQuery struct {
	// Kinds are the definition kinds listed; empty lists every kind.
	Kinds []Kind
	// Parent lists the glossary terms and nodes directly under this glossary
	// node reference.
	Parent string
	// Roots lists only the definitions under no glossary node. It is ignored
	// when Parent is set.
	Roots bool
	// Text matches the name, the slug or the description, ignoring case.
	Text   string
	Limit  int
	Offset int
}

// DefinitionSummary is one definition found by a listing.
type DefinitionSummary struct {
	Reference   string `json:"reference"`
	Kind        Kind   `json:"kind"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Parent is the glossary node the definition sits under, as a reference.
	Parent string `json:"parent,omitempty"`
	// Children is how many glossary terms and nodes sit directly under a
	// glossary node.
	Children int `json:"children"`
}

// Use is one dataset or column carrying a definition.
type Use struct {
	// Reference is the dataset's or the column's.
	Reference string `json:"reference"`
	// Dataset is the dataset's reference, the same as Reference for a dataset.
	Dataset string `json:"dataset"`
	Name    string `json:"name"`
	// Column is set when a column, rather than the dataset, carries it.
	Column     string `json:"column,omitempty"`
	Connection string `json:"connection"`
}

// Lister lists and resolves definitions, the uses of a definition, and the
// levels of a connection's datasets. The catalog store implements it.
type Lister interface {
	// ListDefinitions returns a page of definitions, ordered by name.
	ListDefinitions(ctx context.Context, q DefinitionQuery) ([]DefinitionSummary, error)
	// DeleteDefinition deletes a definition. It refuses with an *InUseError
	// while anything uses it, and with ErrNotFound for an unknown one.
	DeleteDefinition(ctx context.Context, ref Ref) error
	// Uses returns the datasets and columns carrying a glossary term, a tag or
	// a domain, at most limit of them, ordered by reference.
	Uses(ctx context.Context, ref Ref, limit int) ([]Use, error)
	// Browse lists one level of a connection's datasets that are not removed.
	Browse(ctx context.Context, q BrowseQuery) ([]BrowseEntry, error)
}
