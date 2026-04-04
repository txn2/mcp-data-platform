// Package semantic provides abstractions for semantic metadata providers.
//
//nolint:revive // package contains related semantic data types
package semantic

import "time"

const identifierSeparator = "."

// TableIdentifier uniquely identifies a table.
type TableIdentifier struct {
	Catalog string `json:"catalog,omitempty"`
	Schema  string `json:"schema"`
	Table   string `json:"table"`
}

// String returns a dot-separated representation.
func (t TableIdentifier) String() string {
	if t.Catalog != "" {
		return t.Catalog + identifierSeparator + t.Schema + identifierSeparator + t.Table
	}
	return t.Schema + identifierSeparator + t.Table
}

// ColumnIdentifier uniquely identifies a column.
type ColumnIdentifier struct {
	TableIdentifier
	Column string `json:"column"`
}

// String returns a dot-separated representation including the column.
func (c ColumnIdentifier) String() string {
	return c.TableIdentifier.String() + identifierSeparator + c.Column
}

// TableContext provides semantic context for a table.
type TableContext struct {
	// Basic info
	URN         string `json:"urn,omitempty"`
	Description string `json:"description,omitempty"`

	// Ownership
	Owners []Owner `json:"owners,omitempty"`

	// Classification
	Tags          []string       `json:"tags,omitempty"`
	GlossaryTerms []GlossaryTerm `json:"glossary_terms,omitempty"`
	Domain        *Domain        `json:"domain,omitempty"`

	// Status
	Deprecation *Deprecation `json:"deprecation,omitempty"`

	// Quality
	QualityScore *float64 `json:"quality_score,omitempty"`

	// Metadata
	CustomProperties map[string]string `json:"custom_properties,omitempty"`
	LastModified     *time.Time        `json:"last_modified,omitempty"`

	// Structured properties (DataHub 1.4.x)
	StructuredProperties []StructuredProperty `json:"structured_properties,omitempty"`

	// Incidents (DataHub 1.4.x)
	ActiveIncidents int        `json:"active_incidents,omitempty"`
	Incidents       []Incident `json:"incidents,omitempty"`

	// Data contracts (DataHub 1.4.x)
	DataContract *DataContractStatus `json:"data_contract,omitempty"`
}

// ColumnContext provides semantic context for a column.
type ColumnContext struct {
	// Basic info
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Classification
	Tags          []string       `json:"tags,omitempty"`
	GlossaryTerms []GlossaryTerm `json:"glossary_terms,omitempty"`

	// Sensitivity
	IsPII       bool `json:"is_pii,omitempty"`
	IsSensitive bool `json:"is_sensitive,omitempty"`

	// Business metadata
	BusinessName string `json:"business_name,omitempty"`

	// InheritedFrom is set when metadata was inherited from upstream lineage.
	InheritedFrom *InheritedMetadata `json:"inherited_from,omitempty"`
}

// HasContent reports whether the column has any meaningful metadata worth
// including in enrichment responses. Columns with no description, tags,
// glossary terms, sensitivity flags, business name, or inherited metadata
// are considered empty and can be omitted to save tokens.
func (c *ColumnContext) HasContent() bool {
	return c.Description != "" ||
		len(c.Tags) > 0 ||
		len(c.GlossaryTerms) > 0 ||
		c.IsPII ||
		c.IsSensitive ||
		c.BusinessName != "" ||
		c.InheritedFrom != nil
}

// InheritedMetadata tracks the provenance of inherited column metadata.
type InheritedMetadata struct {
	// SourceURN is the DataHub URN of the upstream dataset.
	SourceURN string `json:"source_urn"`

	// SourceColumn is the column name in the upstream dataset.
	SourceColumn string `json:"source_column"`

	// Hops is the distance from the target dataset (1 = direct upstream).
	Hops int `json:"hops"`

	// MatchMethod indicates how the column was matched.
	// Values: "column_lineage", "name_exact", "name_transformed", "alias"
	MatchMethod string `json:"match_method"`
}

// Owner represents a data owner.
type Owner struct {
	URN   string    `json:"urn"`
	Type  OwnerType `json:"type"`
	Name  string    `json:"name,omitempty"`
	Email string    `json:"email,omitempty"`
}

// OwnerType indicates the type of owner.
type OwnerType string

// Owner type constants.
const (
	OwnerTypeUser  OwnerType = "user"
	OwnerTypeGroup OwnerType = "group"
)

// GlossaryTerm represents a business glossary term.
type GlossaryTerm struct {
	URN         string `json:"urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Domain represents a data domain.
type Domain struct {
	URN         string `json:"urn"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Deprecation indicates if an entity is deprecated.
type Deprecation struct {
	Deprecated bool       `json:"deprecated"`
	Note       string     `json:"note,omitempty"`
	Actor      string     `json:"actor,omitempty"`
	DecommDate *time.Time `json:"decommission_date,omitempty"`
}

// LineageDirection indicates the direction of lineage traversal.
type LineageDirection string

// Lineage direction constants.
const (
	LineageUpstream   LineageDirection = "upstream"
	LineageDownstream LineageDirection = "downstream"
)

// LineageInfo contains lineage information for an entity.
type LineageInfo struct {
	Direction LineageDirection `json:"direction"`
	Entities  []LineageEntity  `json:"entities"`
	MaxDepth  int              `json:"max_depth"`
}

// LineageEntity represents an entity in a lineage graph.
type LineageEntity struct {
	URN      string        `json:"urn"`
	Type     string        `json:"type"`
	Name     string        `json:"name"`
	Platform string        `json:"platform,omitempty"`
	Depth    int           `json:"depth"`
	Parents  []LineageEdge `json:"parents,omitempty"`
	Children []LineageEdge `json:"children,omitempty"`
	Context  *TableContext `json:"context,omitempty"`
}

// LineageEdge represents an edge in the lineage graph.
type LineageEdge struct {
	URN            string `json:"urn"`
	Type           string `json:"type,omitempty"`
	TransformLogic string `json:"transform_logic,omitempty"`
}

// SearchFilter defines criteria for searching tables.
type SearchFilter struct {
	Query    string   `json:"query"`
	Platform string   `json:"platform,omitempty"`
	Tags     []string `json:"tags,omitempty"`
	Domain   string   `json:"domain,omitempty"`
	Owner    string   `json:"owner,omitempty"`
	Limit    int      `json:"limit,omitempty"`
	Offset   int      `json:"offset,omitempty"`

	// EntityTypes restricts search to specific entity types (e.g., "DATASET", "DASHBOARD").
	// Defaults to ["DATASET"] if empty.
	EntityTypes []string `json:"entity_types,omitempty"`

	// Mode selects the search strategy: "keyword" (default) or "semantic".
	Mode string `json:"mode,omitempty"`

	// Filters provides advanced field-level filtering (e.g., by column name, column tag).
	// All filters are AND'd together. These map directly to DataHub's searchAcrossEntities
	// orFilters and support fields like fieldPaths, fieldTags, fieldGlossaryTerms, etc.
	Filters []FieldFilter `json:"filters,omitempty"`
}

// FieldFilter is a single search filter criterion.
type FieldFilter struct {
	// Field is the filter field (e.g., "fieldPaths", "fieldTags", "platform", "owners").
	Field string `json:"field"`

	// Values to match against.
	Values []string `json:"values"`

	// Condition is the match operator: CONTAIN, EQUAL (default), IN, EXISTS.
	Condition string `json:"condition,omitempty"`

	// Negated inverts the filter (exclude matching entities).
	Negated bool `json:"negated,omitempty"`
}

// TableSearchResult represents a search result.
type TableSearchResult struct {
	URN          string   `json:"urn"`
	Name         string   `json:"name"`
	Platform     string   `json:"platform,omitempty"`
	Description  string   `json:"description,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Domain       string   `json:"domain,omitempty"`
	MatchedField string   `json:"matched_field,omitempty"`
}

// StructuredProperty represents a typed custom property from DataHub 1.4.x.
type StructuredProperty struct {
	QualifiedName string `json:"qualified_name"`
	DisplayName   string `json:"display_name,omitempty"`
	Values        []any  `json:"values"`
}

// Incident represents an active data incident from DataHub 1.4.x.
type Incident struct {
	URN         string `json:"urn"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"`
	Created     int64  `json:"created,omitempty"`
}

// DataContractStatus represents the pass/fail status of a data contract from DataHub 1.4.x.
type DataContractStatus struct {
	Status           string            `json:"status"` // PASSING or FAILING
	AssertionResults []AssertionResult `json:"assertion_results,omitempty"`
}

// AssertionResult represents a single assertion reference within a data contract.
type AssertionResult struct {
	AssertionURN string `json:"assertion_urn,omitempty"` // URN identifying the assertion
	Type         string `json:"type"`                    // FRESHNESS, SCHEMA, DATA_QUALITY
}
