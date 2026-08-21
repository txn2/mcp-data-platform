// Package user provides a directory of known people keyed by email (#614).
//
// It is NOT an authorization layer. A row simply records that a person exists
// so share pickers can resolve a name from an email address. Rows are upserted
// when a person authenticates (token claims fill the name) and can be pre-added
// by an admin before the person has ever logged in. Admin-entered names take
// precedence: a login only fills blank name fields.
package user

import "time"

// Source records how a directory row first came to exist.
const (
	// SourceAuth marks a row upserted from a real authenticated session.
	SourceAuth = "auth"
	// SourceAdmin marks a row pre-added by an admin via the API.
	SourceAdmin = "admin"
)

// User is a directory entry for a known person.
type User struct {
	Email      string     `json:"email" example:"marcus.johnson@example.com"`
	FirstName  string     `json:"first_name" example:"Marcus"`
	LastName   string     `json:"last_name" example:"Johnson"`
	Source     string     `json:"source" example:"auth"`
	Confirmed  bool       `json:"confirmed" example:"true"`
	AddedBy    string     `json:"added_by,omitempty" example:"admin@example.com"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Filter specifies criteria for listing directory users.
type Filter struct {
	// Query optionally matches (case-insensitive substring) against email,
	// first name, or last name.
	Query string
	// ConfirmedOnly narrows the listing to people who have actually
	// authenticated, leaving out the rows an admin pre-added for somebody who
	// has never signed in. A picker that hands something over — a script's
	// ownership (#1407) — offers only people who can come and collect it,
	// and it is applied here rather than over the returned page so the row
	// cap cannot hide a confirmed person behind unconfirmed ones.
	ConfirmedOnly bool
	Limit         int
	Offset        int
}

// Update holds mutable fields for an admin edit. A nil pointer leaves the
// field unchanged.
type Update struct {
	FirstName *string `json:"first_name,omitempty"`
	LastName  *string `json:"last_name,omitempty"`
}
