package memory

// manageInput is the deserialized input for the memory_manage tool. Its fields
// are exactly the properties memoryManageSchema publishes: the schema is closed
// to unknown arguments, so a field here with no property would be an argument
// the boundary refuses, and a property here with no field would be an argument
// silently dropped.
type manageInput struct {
	Command         string         `json:"command"`
	Content         string         `json:"content,omitempty"`
	ID              string         `json:"id,omitempty"`
	DuplicateID     string         `json:"duplicate_id,omitempty"`
	Dimension       string         `json:"dimension,omitempty"`
	Category        string         `json:"category,omitempty"`
	Confidence      string         `json:"confidence,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	FilterDimension string         `json:"filter_dimension,omitempty"`
	FilterCategory  string         `json:"filter_category,omitempty"`
	FilterStatus    string         `json:"filter_status,omitempty"`
	FilterEntityURN string         `json:"filter_entity_urn,omitempty"`
	Limit           int            `json:"limit,omitempty"`
	Offset          int            `json:"offset,omitempty"`
}
