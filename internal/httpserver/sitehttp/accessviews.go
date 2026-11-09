package sitehttp

import (
	"github.com/txn2/mcp-data-platform/internal/site"
)

// DomainBody adds a domain to a site.
type DomainBody struct {
	Hostname         string `json:"hostname" example:"dev.example.com"`
	Environment      string `json:"environment" example:"dev"`
	RequiresApproval bool   `json:"requires_approval,omitempty"`
}

// GrantBody gives a person access.
type GrantBody struct {
	Email string `json:"email" example:"pat@example.com"`
	Level string `json:"level" example:"view"`
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
	Owner        string       `json:"owner_email"`
	InternalView bool         `json:"internal_view"`
	Grants       []site.Grant `json:"grants"`
}
