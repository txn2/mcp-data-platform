package site

import "time"

// Domain maps one hostname to a site environment or to an application. It
// routes as soon as it is added: the platform does not check DNS, certificates
// or who owns the name, and whoever runs the deployment adds its ingress.
type Domain struct {
	Hostname    string `json:"hostname" example:"www.example.com"`
	SiteID      string `json:"site_id,omitempty"`
	Site        string `json:"site,omitempty" example:"marketing"`
	Environment string `json:"environment,omitempty" example:"prod"`
	// ApplicationID and Application name the application whose endpoints the
	// hostname serves.
	ApplicationID string    `json:"application_id,omitempty"`
	Application   string    `json:"application,omitempty" example:"shop"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
}

// Route is what a hostname serves.
type Route struct {
	SiteID      string
	Site        string
	Environment string
	// Release is the release served, nil for an environment with none.
	Release *Release
	// Application is the application whose endpoints and pages the host
	// serves, empty for a site.
	Application string
	// Hostname is the added hostname as stored, empty for an address on the
	// pages domain.
	Hostname string
	// Private is true for a private environment: it answers only to people who
	// have signed in and may open it.
	Private bool
	// PartOf is the application this environment is a part of, "" for none;
	// its endpoints answer on the environment's own hosts.
	PartOf string
}
