package platformfeedback

// Caller is who posts.
type Caller struct {
	UserID    string
	Persona   string
	SessionID string
}

// Result is what a post did.
type Result struct {
	ID          string `json:"id"`
	ReportCount int    `json:"report_count"`
	// Folded is true when the post repeated an open one and was counted on it
	// rather than stored and sent again.
	Folded bool `json:"folded"`
	// Channels is how many channels the new post was queued for.
	Channels int    `json:"channels"`
	Message  string `json:"message"`
}

// Detail is one post with its reports and deliveries.
type Detail struct {
	Item
	Reports    []Report   `json:"reports"`
	Deliveries []Delivery `json:"deliveries"`
}
