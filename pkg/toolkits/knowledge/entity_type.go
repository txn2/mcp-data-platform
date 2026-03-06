package knowledge

import (
	"errors"
	"fmt"
	"strings"

	dhclient "github.com/txn2/mcp-datahub/pkg/client"
)

// entityTypeDataset is the DataHub entity type string for datasets.
const entityTypeDataset = "dataset"

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
		ops = append([]string{"update_description"}, ops...)
	}

	if entityType == entityTypeDataset {
		ops = append(ops, "add_curated_query")
	}

	return ops
}

// descriptionSupportedTypes are entity types that support update_description.
// This matches the upstream mcp-datahub descriptionAspectMap.
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
			if entityType != "dataset" {
				return fmt.Errorf(
					"column-level update_description is only supported for datasets, not %s entities. "+
						"Supported operations for %s: %s",
					entityType, entityType, strings.Join(supportedOpsForType(entityType), ", "),
				)
			}
			return nil
		}
	}

	// Dataset-only operations.
	if datasetOnlyOperations[actionType(c.ChangeType)] && entityType != "dataset" {
		return fmt.Errorf(
			"%s is only supported for datasets, not %s entities. "+
				"Supported operations for %s: %s",
			c.ChangeType, entityType, entityType, strings.Join(supportedOpsForType(entityType), ", "),
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
			"Supported operations for %s: %s",
		entityType, entityType, strings.Join(supportedOpsForType(entityType), ", "),
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
