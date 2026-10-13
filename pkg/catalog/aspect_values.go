package catalog

// Owner is one owner of an entity: a person's email or a group name, and the
// kind of ownership (technical_owner, business_owner, data_steward, ...).
type Owner struct {
	Owner string `json:"owner"`
	Type  string `json:"type,omitempty"`
}

// Link is a documentation link.
type Link struct {
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
}

// CuratedQuery is a saved statement known to answer something about a dataset.
type CuratedQuery struct {
	Name        string `json:"name"`
	SQL         string `json:"sql"`
	Description string `json:"description,omitempty"`
}

// Incident is a reported problem with a dataset. State is "active" or
// "resolved".
type Incident struct {
	ID          string `json:"id"`
	Type        string `json:"type,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	State       string `json:"state"`
	// Resolution is what was said when the incident was resolved.
	Resolution string `json:"resolution,omitempty"`
}

// ContextDocument is a document written about a dataset: how it is loaded,
// what a field means in practice, what to watch for.
type ContextDocument struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Content     string `json:"content"`
	ContentType string `json:"content_type,omitempty"`
	Category    string `json:"category,omitempty"`
}

// The states an incident can be in.
const (
	IncidentActive   = "active"
	IncidentResolved = "resolved"
)
