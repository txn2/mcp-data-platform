package site

import "time"

// Domain maps one hostname to a site environment or to an application.
type Domain struct {
	Hostname    string `json:"hostname" example:"www.example.com"`
	SiteID      string `json:"site_id,omitempty"`
	Site        string `json:"site,omitempty" example:"marketing"`
	Environment string `json:"environment,omitempty" example:"prod"`
	// ApplicationID and Application name the application whose endpoints the
	// hostname serves.
	ApplicationID string `json:"application_id,omitempty"`
	Application   string `json:"application,omitempty" example:"shop"`
	// Token is what the verification step looks for. It is public by design:
	// it says nothing but "this deployment asked".
	Token      string     `json:"token"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	VerifiedBy string     `json:"verified_by,omitempty"`
	CreatedBy  string     `json:"created_by"`
	CreatedAt  time.Time  `json:"created_at"`
}

// Verified reports whether the hostname routes.
func (d Domain) Verified() bool { return d.VerifiedAt != nil }

// Route is what a verified hostname or a preview hostname serves.
type Route struct {
	SiteID      string
	Site        string
	Environment string
	// Release is the release served, nil for an environment with none.
	Release *Release
	// Application is the application whose endpoints and pages the host
	// serves, empty for a site.
	Application string
	// Hostname is the verified hostname as stored, empty for a preview.
	Hostname string
}
