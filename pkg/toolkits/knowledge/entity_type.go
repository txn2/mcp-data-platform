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

	// entityTypeGlossaryTerm is the DataHub entity type string for glossary terms.
	entityTypeGlossaryTerm = "glossaryTerm"

	// entityTypeContainer is the DataHub entity type string for containers.
	entityTypeContainer = "container"

	// entityTypeDashboard is the DataHub entity type string for dashboards.
	entityTypeDashboard = "dashboard"

	// entityTypeDataProduct is the DataHub entity type string for data products.
	entityTypeDataProduct = "dataProduct"

	// entityTypeDomain is the DataHub entity type string for domains.
	entityTypeDomain = "domain"

	// entityTypeDocument is the DataHub entity type string for documents.
	entityTypeDocument = "document"

	// opsSeparator is the delimiter used when joining supported operations in error messages.
	opsSeparator = ", "
)

// incidentSupportedTypes are the entity types DataHub raises incidents on.
// flag_quality_issue raises its detail incident only for these; on any other type
// (glossary terms, glossary nodes, domains, documents, etc.) it sets the QualityIssue
// tag alone, so it never fails an apply on an unsupported type and never orphans an
// incident a rollback cannot resolve. Being conservative here can only skip an
// incident on a type that would have supported one, which is not a regression
// (flag_quality_issue was tag-only before #722).
var incidentSupportedTypes = map[string]bool{
	entityTypeDataset:     true,
	entityTypeDashboard:   true,
	"chart":               true,
	"dataFlow":            true,
	"dataJob":             true,
	entityTypeContainer:   true,
	entityTypeDataProduct: true,
}

// descriptionReadableTypes are entity types whose description GetCurrentMetadata can
// read back, and therefore capture in a changeset before-image: dataset and
// dashboard via the entity query, glossaryTerm and dataProduct via dedicated getters.
// For any other type the before-image description is empty (not read), so rolling
// back an update_description would blank a real description; such a rollback is
// refused instead. Keep in sync with GetCurrentMetadata.
var descriptionReadableTypes = map[string]bool{
	entityTypeDataset:      true,
	entityTypeDashboard:    true,
	entityTypeGlossaryTerm: true,
	entityTypeDataProduct:  true,
}

// descriptionReadable reports whether GetCurrentMetadata can read an entity type's
// description (and thus whether update_description is safely revertible).
func descriptionReadable(entityType string) bool {
	return descriptionReadableTypes[entityType]
}

// associationsReadable reports whether GetCurrentMetadata can read an entity type's
// tags and glossary terms. Every tag/term-writable type can be read except document,
// whose globalTags/glossaryTerms aspects have no read path; rolling back a tag or
// glossary-term change on a document could strip a pre-existing value, so it is
// refused instead. Keep in sync with GetCurrentMetadata.
func associationsReadable(entityType string) bool {
	return entityType != entityTypeDocument
}

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
	entityTypeDataset:      true,
	entityTypeGlossaryTerm: true,
	"glossaryNode":         true,
	entityTypeContainer:    true,
}

// contextDocumentOps are change types that require context document support.
var contextDocumentOps = map[actionType]bool{
	actionAddContextDocument:    true,
	actionUpdateContextDocument: true,
	actionRemoveContextDocument: true,
}

// supportedOpsForType returns the list of supported operations for a given entity type.
// All entity types support tag, glossary term, documentation, and quality issue operations.
// Only datasets support column descriptions and curated queries.
// update_description is supported for the 10 entity types handled by mcp-datahub.
func supportedOpsForType(entityType string) []string {
	ops := []string{
		string(actionAddTag), string(actionRemoveTag), string(actionAddGlossaryTerm),
		string(actionAddDocumentation), string(actionFlagQualityIssue),
	}

	if descriptionSupportedTypes[entityType] {
		ops = slices.Insert(ops, 0, "update_description")
	}

	if entityType == entityTypeDataset {
		ops = append(ops, "add_curated_query")
	}

	if contextDocumentSupportedTypes[entityType] {
		ops = append(ops, "add_context_document", "update_context_document", "remove_context_document")
	}

	return ops
}

// descriptionSupportedTypes are entity types that support update_description.
// Must stay in sync with the upstream mcp-datahub descriptionAspectMap.
var descriptionSupportedTypes = map[string]bool{
	entityTypeDataset:      true,
	entityTypeDashboard:    true,
	"chart":                true,
	"dataFlow":             true,
	"dataJob":              true,
	entityTypeContainer:    true,
	entityTypeDataProduct:  true,
	entityTypeDomain:       true,
	entityTypeGlossaryTerm: true,
	"glossaryNode":         true,
}

// validateEntityTypeForChange checks whether a change type is supported for the
// given entity URN. Returns a user-friendly error message when incompatible.
func validateEntityTypeForChange(urn string, c ApplyChange) error {
	// add_prompt is a platform operation, not a DataHub entity change.
	if c.ChangeType == string(actionAddPrompt) {
		return nil
	}

	entityType, err := entityTypeFromURN(urn)
	if err != nil {
		return err
	}

	// Column-level descriptions are dataset-only (schema metadata is a dataset concept).
	if c.ChangeType == string(actionUpdateDescription) {
		return validateDescriptionChange(entityType, c)
	}

	// Dataset-only operations.
	if datasetOnlyOperations[actionType(c.ChangeType)] && entityType != entityTypeDataset {
		return fmt.Errorf(
			"%s is only supported for datasets, not %s entities. "+
				"Supported operations for %s: %s",
			c.ChangeType, entityType, entityType, strings.Join(supportedOpsForType(entityType), opsSeparator),
		)
	}

	// Context document operations require supported entity types.
	if contextDocumentOps[actionType(c.ChangeType)] && !contextDocumentSupportedTypes[entityType] {
		return fmt.Errorf(
			"%s is only supported for datasets, glossaryTerms, glossaryNodes, and containers, not %s entities. "+
				"Supported operations for %s: %s",
			c.ChangeType, entityType, entityType, strings.Join(supportedOpsForType(entityType), opsSeparator),
		)
	}

	return nil
}

// validateDescriptionChange validates update_description changes, handling both
// column-level (dataset-only) and entity-level (specific types) descriptions.
func validateDescriptionChange(entityType string, c ApplyChange) error {
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

	if !descriptionSupportedTypes[entityType] {
		return fmt.Errorf(
			"update_description is not supported for %s entities. "+
				"Supported operations for %s: %s",
			entityType, entityType, strings.Join(supportedOpsForType(entityType), opsSeparator),
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
