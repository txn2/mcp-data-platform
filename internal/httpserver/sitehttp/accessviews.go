package sitehttp

import (
	"github.com/txn2/mcp-data-platform/internal/site"
)

// DomainBody adds a domain to a site.
type DomainBody struct {
	Hostname         string `json:"hostname" example:"dev.example.com"`
	Environment      string `json:"environment" example:"prod"`
	RequiresApproval bool   `json:"requires_approval,omitempty"`
	// Public applies when the request creates the environment, which is
	// otherwise private.
	Public bool `json:"public,omitempty"`
}

// GrantBody gives a person, by email, or a persona access.
type GrantBody struct {
	Email   string `json:"email,omitempty" example:"pat@example.com"`
	Persona string `json:"persona,omitempty" example:"finance-team"`
	Level   string `json:"level" example:"view"`
	// Environments are the private environments a view grant opens; none is
	// every private environment.
	Environments []string `json:"environments,omitempty"`
	// Notify emails them the site's address.
	Notify bool `json:"notify,omitempty"`
}

// FormList is the platform forms a copied page's forms can post to.
type FormList struct {
	Forms []string `json:"forms"`
}

// JobList is a site's running jobs.
type JobList struct {
	Jobs []site.Job `json:"jobs"`
}

// AccessList is who has access to a site, for someone who manages it.
type AccessList struct {
	Owner  string       `json:"owner_email"`
	Grants []site.Grant `json:"grants"`
	// Personas are every persona an environment can be shared with, for the
	// Share dialog.
	Personas []string `json:"personas"`
}
