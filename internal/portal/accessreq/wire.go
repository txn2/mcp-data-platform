package accessreq

// The bodies the routes read and write.

// Page is what the page a person who cannot open an item shows.
type Page struct {
	// HasAccess is true when the person already opens the item.
	HasAccess bool `json:"has_access"`
	// Name is the item's name, only when the deployment reveals names.
	Name string `json:"name,omitempty"`
	// OwnerEmail is who a request goes to.
	OwnerEmail string `json:"owner_email"`
	// Pending is the person's live request for the item, if any.
	Pending *Request `json:"pending,omitempty"`
}

// OpenRequest is the body of a request for access.
type OpenRequest struct {
	Message string `json:"message,omitempty" example:"I need this for the quarterly review"`
}

// DecideRequest is the body of an approval.
type DecideRequest struct {
	// Permission is viewer or editor.
	Permission string `json:"permission" example:"viewer"`
}

// RequestList is an owner's waiting requests.
type RequestList struct {
	Requests []Request `json:"requests"`
}
