package catalog

// BrowseQuery names one level of a connection's datasets: the datasets and
// path groups directly under Prefix.
type BrowseQuery struct {
	ConnectionKind string
	Connection     string
	// Prefix is the path above the level listed; empty lists the top level.
	Prefix []string
}

// BrowseEntry is one entry of a browsed level: a dataset, or a path group
// holding datasets further down.
type BrowseEntry struct {
	Name string `json:"name"`
	// Reference is the dataset's reference, or for a group a dataset reference
	// whose path is the group's, which fetching browses in turn.
	Reference string `json:"reference"`
	// Datasets is how many datasets a group holds; zero for a dataset.
	Datasets int         `json:"datasets"`
	Type     DatasetType `json:"type,omitempty"`
}
