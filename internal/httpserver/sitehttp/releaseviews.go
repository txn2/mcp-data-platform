package sitehttp

import (
	"github.com/txn2/mcp-data-platform/internal/site"
)

// ReleaseList is a site's releases and where each is served.
type ReleaseList struct {
	Releases []ReleaseView `json:"releases"`
}

// ReleaseView is a release, the environments serving it, and what keeps it
// from the next sweep (#2602).
type ReleaseView struct {
	site.Release
	ServedIn []string `json:"served_in"`
	// KeptBy is the environments whose release limit keeps it, an
	// "application <name>" release pinning it, or "approval" for a pending
	// request; empty for a release the next sweep removes, or one it removed.
	KeptBy []string `json:"kept_by"`
}

// ReleaseDetail is one release with its files and redirect map. A file's bytes
// are never read back through the portal origin.
type ReleaseDetail struct {
	ReleaseView
	Files []site.File `json:"files"`
}

// ApplicationAddresses is where an application answers.
type ApplicationAddresses struct {
	PageOrigin string   `json:"page_origin"`
	Domains    []string `json:"domains"`
}

// DeploymentList is every move of a site's environments, newest first.
type DeploymentList struct {
	Deployments []site.Deployment `json:"deployments"`
}
