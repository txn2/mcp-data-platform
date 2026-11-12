package sitehttp

import (
	"github.com/txn2/mcp-data-platform/internal/site"
)

// DomainBody adds a domain to a site.
type DomainBody struct {
	Hostname    string `json:"hostname" example:"www.example.com"`
	Environment string `json:"environment" example:"prod"`
	// Public opens the domain to anyone (#2619): the owner's or an
	// administrator's, through Approvals where the environment requires
	// approval. A domain is added private otherwise.
	Public bool   `json:"public,omitempty"`
	Reason string `json:"reason,omitempty"`
	// RequiresApproval sets whether the environment requires approval, on
	// one the request creates or one that exists; After is where a new
	// environment goes (#2617).
	RequiresApproval *bool  `json:"requires_approval,omitempty"`
	After            string `json:"after,omitempty"`
}

// DomainAccessBody makes a domain public or private (#2619).
type DomainAccessBody struct {
	Public bool `json:"public"`
	// Reason goes with a request to make it public.
	Reason string `json:"reason,omitempty"`
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
