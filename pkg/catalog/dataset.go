package catalog

import "time"

// DatasetType is what kind of thing a dataset is at its source.
type DatasetType string

// The dataset types a connection scan produces.
const (
	DatasetTable            DatasetType = "table"
	DatasetView             DatasetType = "view"
	DatasetRegisteredTable  DatasetType = "registered_table"
	DatasetWebhookTable     DatasetType = "webhook_table"
	DatasetAPIOperation     DatasetType = "api_operation"
	DatasetGraphQLOperation DatasetType = "graphql_operation"
)

// Valid reports whether t is one of the dataset types.
func (t DatasetType) Valid() bool {
	switch t {
	case DatasetTable, DatasetView, DatasetRegisteredTable, DatasetWebhookTable,
		DatasetAPIOperation, DatasetGraphQLOperation:
		return true
	default:
		return false
	}
}

// Column is one column of a dataset: a stored row, not something parsed on
// demand out of the dataset's schema document.
type Column struct {
	Name     string  `json:"name"`
	DataType string  `json:"data_type,omitempty"`
	Ordinal  int     `json:"ordinal"`
	Nullable bool    `json:"nullable"`
	Aspects  Aspects `json:"aspects"`
}

// Dataset is one dataset and everything the catalog holds about it.
type Dataset struct {
	ID        string      `json:"id"`
	Reference string      `json:"reference"`
	Ref       Ref         `json:"-"`
	Name      string      `json:"name"`
	Type      DatasetType `json:"type"`
	// SchemaResourceID is the managed resource a registered table was created
	// from, when it was created from one.
	SchemaResourceID string `json:"schema_resource_id,omitempty"`
	// SchemaDocument is the reference of the connection's schema document the
	// scan read the dataset from, when it was read from one (#1918).
	SchemaDocument string   `json:"schema_document,omitempty"`
	Columns        []Column `json:"columns"`
	Aspects        Aspects  `json:"aspects"`
	// RemovedAt is when a scan stopped seeing the dataset. Its curation stays
	// readable, and a scan that sees it again clears this.
	RemovedAt *time.Time `json:"removed_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// ColumnInput is one column as a writer declares it.
type ColumnInput struct {
	Name     string
	DataType string
	Nullable bool
}

// DatasetInput is a dataset as a writer declares it: its identity, its type,
// its schema document and its columns in order. Curation is written separately,
// one aspect at a time, so declaring a dataset never overwrites curation.
type DatasetInput struct {
	Ref              Ref
	Name             string
	Type             DatasetType
	SchemaResourceID string
	// SchemaDocument is the filename of the connection's schema document the
	// dataset was read from. Empty keeps the one already recorded.
	SchemaDocument string
	Columns        []ColumnInput
}
