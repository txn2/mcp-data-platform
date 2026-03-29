package knowledge

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
)

const (
	// entityTypeDataset is the DataHub entity type string for datasets.
	entityTypeDataset = "dataset"

	// opsSeparator is the delimiter used when joining supported operations in error messages.
	opsSeparator = ", "
)

// entityTypeFromURN extracts the entity type from a DataHub URN.
// For example, "urn:li:dataset:(...)" returns "dataset".
func entityTypeFromURN(urn string) (string, error) {
	parsed, err := dhclient.ParseURN(urn)
	if err != nil {
		return "", fmt.Errorf("invalid URN %q: %w", urn, err)
	}
	return parsed.EntityType, nil
}

// datasetOnlyOperations are change types that only work on dataset entities.
var datasetOnlyOperations = map[actionType]bool{
	actionAddCuratedQuery: true,
}

// contextDocumentSupportedTypes are entity types that support context document
// operations. Must stay in sync with the inline fragments in mcp-datahub's
// GetContextDocumentsQuery (Dataset, GlossaryTerm, GlossaryNode, Container).
var contextDocumentSupportedTypes = map[string]bool{
	"dataset":      true,
	"glossaryTerm": true,
	"glossaryNode": true,
	"container":    true,
}

// contextDocumentWriteOps are change types that require context document support.
var contextDocumentWriteOps = map[actionType]bool{
	actionAddContextDocument:    true,
	actionUpdateContextDocument: true,
}

// supportedOpsForType returns the list of supported operations for a given entity type.
// All entity types support tag, glossary term, documentation, and quality issue operations.
// Only datasets support column descriptions and curated queries.
// update_description is supported for the 10 entity types handled by mcp-datahub.
func supportedOpsForType(entityType string) []string {
	ops := []string{
		"add_tag", "remove_tag", "add_glossary_term",
		"add_documentation", "flag_quality_issue",
	}

	if descriptionSupportedTypes[entityType] {
		ops = slices.Insert(ops, 0, "update_description")
	}

	if entityType == entityTypeDataset {
		ops = append(ops, "add_curated_query")
	}

	if contextDocumentSupportedTypes[entityType] {
		ops = append(ops, "add_context_document", "update_context_document")
	}

	// remove_context_document is always listed since it deletes by document ID
	// and is entity-type-independent.
	ops = append(ops, "remove_context_document")

	return ops
}

// descriptionSupportedTypes are entity types that support update_description.
// Must stay in sync with the upstream mcp-datahub descriptionAspectMap.
var descriptionSupportedTypes = map[string]bool{
	"dataset":      true,
	"dashboard":    true,
	"chart":        true,
	"dataFlow":     true,
	"dataJob":      true,
	"container":    true,
	"dataProduct":  true,
	"domain":       true,
	"glossaryTerm": true,
	"glossaryNode": true,
}

// validateEntityTypeForChange checks whether a change type is supported for the
// given entity URN. Returns a user-friendly error message when incompatible.
func validateEntityTypeForChange(urn string, c ApplyChange) error {
	entityType, err := entityTypeFromURN(urn)
	if err != nil {
		return err
	}

	// Column-level descriptions are dataset-only (schema metadata is a dataset concept).
	if c.ChangeType == string(actionUpdateDescription) {
		if _, isColumn := parseColumnTarget(c.Target); isColumn {
			if entityType != entityTypeDataset {
				return fmt.Errorf(
					"column-level update_description is only supported for datasets, not %s entities. "+
						"Supported operations for %s: %s",
					entityType, entityType, strings.Join(supportedOpsForType(entityType), opsSeparator),
				)
			}
			return nil
		}

		// Entity-level update_description is only supported for specific entity types.
		if !descriptionSupportedTypes[entityType] {
			return fmt.Errorf(
				"update_description is not supported for %s entities. "+
					"Supported operations for %s: %s",
				entityType, entityType, strings.Join(supportedOpsForType(entityType), opsSeparator),
			)
		}
	}

	// Dataset-only operations.
	if datasetOnlyOperations[actionType(c.ChangeType)] && entityType != entityTypeDataset {
		return fmt.Errorf(
			"%s is only supported for datasets, not %s entities. "+
				"Supported operations for %s: %s",
			c.ChangeType, entityType, entityType, strings.Join(supportedOpsForType(entityType), opsSeparator),
		)
	}

	// Context document write operations (add/update) require supported entity types.
	// remove_context_document is entity-type-independent (deletes by document ID).
	if contextDocumentWriteOps[actionType(c.ChangeType)] && !contextDocumentSupportedTypes[entityType] {
		return fmt.Errorf(
			"%s is only supported for datasets, glossaryTerms, glossaryNodes, and containers, not %s entities. "+
				"Supported operations for %s: %s",
			c.ChangeType, entityType, entityType, strings.Join(supportedOpsForType(entityType), opsSeparator),
		)
	}

	return nil
}

// wrapUnsupportedEntityTypeError checks if an error is an ErrUnsupportedEntityType
// from the upstream mcp-datahub library and wraps it with a user-friendly message.
func wrapUnsupportedEntityTypeError(err error, urn string) error {
	if err == nil {
		return nil
	}

	if !errors.Is(err, dhclient.ErrUnsupportedEntityType) {
		return err
	}

	entityType, parseErr := entityTypeFromURN(urn)
	if parseErr != nil {
		return err // Fall back to original error if URN parsing fails
	}

	return fmt.Errorf(
		"update_description is not supported for %s entities. "+
			"Supported operations for %s: %s: %w",
		entityType, entityType, strings.Join(supportedOpsForType(entityType), opsSeparator), err,
	)
}

// wrapDescriptionError converts ErrUnsupportedEntityType into a user-friendly message,
// and falls back to a generic "description update" wrapper for all other errors.
func wrapDescriptionError(err error, urn string) error {
	if errors.Is(err, dhclient.ErrUnsupportedEntityType) {
		return wrapUnsupportedEntityTypeError(err, urn)
	}
	return fmt.Errorf("description update: %w", err)
}
