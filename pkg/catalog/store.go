package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"
)

// ErrNotFound is returned when a reference names no stored entity or change.
var ErrNotFound = errors.New("catalog entity not found")

// Change is one write of one aspect: what it replaced, what it wrote, who wrote
// it and when. A rollback is itself a change, pointing at the change it undid.
type Change struct {
	ID        string          `json:"id"`
	Reference string          `json:"reference"`
	Aspect    AspectName      `json:"aspect"`
	Version   int             `json:"version"`
	Previous  json.RawMessage `json:"previous,omitempty"`
	Value     json.RawMessage `json:"value,omitempty"`
	WrittenBy string          `json:"written_by"`
	WrittenAt time.Time       `json:"written_at"`
	// Reverts names the change this one rolled back.
	Reverts string `json:"reverts,omitempty"`
}

// Store is the catalog's storage.
type Store interface {
	Lister
	// PutDataset creates or updates a dataset and replaces its column set with
	// the one given. Curation on a column that is still declared is kept; a
	// column no longer declared is removed with its curation.
	PutDataset(ctx context.Context, in DatasetInput) (*Dataset, error)
	// GetDataset reads a dataset with its columns and all curation.
	GetDataset(ctx context.Context, ref Ref) (*Dataset, error)
	// PutDefinition creates or updates a glossary term, glossary node, domain,
	// data product or tag, keyed by kind and slug.
	PutDefinition(ctx context.Context, in DefinitionInput) (*Definition, error)
	// GetDefinition reads a definition by its reference, whose key may be the
	// id or the slug.
	GetDefinition(ctx context.Context, ref Ref) (*Definition, error)
	// WriteAspect writes one aspect of a dataset, column or definition and
	// records the change. A nil or JSON-null value clears the aspect.
	WriteAspect(ctx context.Context, ref Ref, name AspectName, value json.RawMessage, by string) (*Change, error)
	// History lists the changes to one aspect, newest first.
	History(ctx context.Context, ref Ref, name AspectName) ([]Change, error)
	// Rollback restores the value a change replaced and records that as a new
	// change.
	Rollback(ctx context.Context, changeID, by string) (*Change, error)
	// WriteAspects writes several aspects in one transaction: every edit is
	// recorded as a change, or none is. Each value is checked as WriteAspect
	// checks it.
	WriteAspects(ctx context.Context, edits []AspectEdit, by string) ([]Change, error)
	// RevertChanges rolls back several changes in one transaction. It refuses
	// with a *ConflictError, reverting none, when an aspect one of them wrote
	// has been written again since, and with ErrNotFound for an unknown change.
	RevertChanges(ctx context.Context, changeIDs []string, by string) ([]Change, error)
	// TaggedEntities returns the references of the datasets and columns whose
	// tags aspect holds the tag, ordered by reference, at most limit of them.
	TaggedEntities(ctx context.Context, tag string, limit int) ([]string, error)
	// MarkRemoved marks the datasets a scan of one connection did not see as
	// removed, and returns how many it marked. A dataset already marked is
	// left as it is.
	MarkRemoved(ctx context.Context, r Removal) (int, error)
	// MarkDatasetRemoved marks one dataset removed, keeping its curation. A
	// dataset the store does not hold is not an error.
	MarkDatasetRemoved(ctx context.Context, ref Ref) error
	// AddEdges records lineage edges; recording an edge again refreshes when
	// it was written.
	AddEdges(ctx context.Context, edges []Edge) error
	// Lineage walks the edges from a dataset reference in one direction, at
	// most maxDepth hops, each edge once at its shortest distance.
	Lineage(ctx context.Context, ref string, direction Direction, maxDepth int) ([]Hop, error)
	// DatasetsByPath returns the references of the datasets of one connection
	// kind with the given path, one per connection that sees it, ordered by
	// connection.
	DatasetsByPath(ctx context.Context, connectionKind, path string) ([]string, error)
	// SearchDatasets returns a page of datasets that are not removed and match
	// the query, with their curation.
	SearchDatasets(ctx context.Context, q DatasetQuery) ([]DatasetSummary, error)
}

// AspectEdit is one aspect value for WriteAspects. A nil or JSON-null value
// clears the aspect.
type AspectEdit struct {
	Ref    Ref
	Aspect AspectName
	Value  json.RawMessage
	// Expect, when Checked, is the value the writer read the aspect as, nil
	// for absent. WriteAspects refuses the whole write with ErrStale when the
	// aspect holds anything else, so a value computed from what was read never
	// replaces a write made since.
	Expect  json.RawMessage
	Checked bool
}

// ErrInvalidDefinition refuses a definition that cannot be stored as given: no
// name, a name no slug can be derived from, or a parent that is not a glossary
// node or that its kind cannot have.
var ErrInvalidDefinition = errors.New("invalid definition")

// ErrStale refuses a write whose writer read an aspect that has been written
// since.
var ErrStale = errors.New("the aspect was written since it was read")

// ConflictError refuses a revert of a change whose aspect was written again
// after it: undoing it would also discard the newer write.
type ConflictError struct {
	// Change is the change asked to be reverted.
	Change string
	// Reference and Aspect name what both changes wrote.
	Reference string
	Aspect    AspectName
	// Newer is the latest change to that aspect.
	Newer Change
}

// Error implements the error interface.
func (e *ConflictError) Error() string {
	return "the " + string(e.Aspect) + " of " + e.Reference + " was written again by " +
		e.Newer.WrittenBy + " at " + e.Newer.WrittenAt.UTC().Format(time.RFC3339) +
		" (change " + e.Newer.ID + ") after change " + e.Change
}

// DatasetQuery narrows a dataset search. Text "*" or empty matches every
// dataset; other text matches the name, the path or the description, ignoring
// case.
type DatasetQuery struct {
	Text           string
	ConnectionKind string
	// Connection narrows the search to one connection of ConnectionKind.
	Connection string
	// Scoped limits the search to the connections in Within, each written
	// "<connection kind>/<connection>"; a scoped search with none finds
	// nothing. It is how a caller holds a search to the connections its
	// persona reaches.
	Scoped bool
	Within []string
	Tags   []string
	Limit  int
	Offset int
}

// DatasetSummary is one dataset found by a search.
type DatasetSummary struct {
	Reference      string      `json:"reference"`
	ConnectionKind string      `json:"connection_kind"`
	Connection     string      `json:"connection"`
	Path           string      `json:"path"`
	Name           string      `json:"name"`
	Type           DatasetType `json:"type"`
	Description    string      `json:"description,omitempty"`
	Tags           []string    `json:"tags,omitempty"`
	Domain         string      `json:"domain,omitempty"`
}

// Slug derives a definition's readable key from its name: lower case, with
// every run of characters other than letters and digits turned into one hyphen.
func Slug(name string) string {
	out := make([]rune, 0, len(name))
	hyphen := false
	for _, r := range strings.ToLower(name) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out = append(out, r)
			hyphen = false
			continue
		}
		if !hyphen && len(out) > 0 {
			out = append(out, '-')
			hyphen = true
		}
	}
	return strings.TrimSuffix(string(out), "-")
}

// Removal is what a scan of one connection read, for marking the rest of it
// removed.
type Removal struct {
	ConnectionKind string
	Connection     string
	// Types are the dataset types the scan produces; no other type is marked.
	Types []DatasetType
	// Keep are the references of the datasets the scan saw.
	Keep []string
	// Unread are dataset path prefixes, in reference form, the scan could not
	// read; nothing under one is marked.
	Unread []string
}
