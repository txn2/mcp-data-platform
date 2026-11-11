package sitehttp

import (
	"encoding/json"

	"github.com/txn2/mcp-data-platform/internal/bucketsource"
)

// PublishBody names what to publish: a managed-resource archive or folder, or
// a bucket prefix on an S3 connection, read as the caller, so someone cannot
// publish a file they could not open.
type PublishBody struct {
	// Environment is always dev, and may be left out.
	Environment string `json:"environment,omitempty" example:"dev"`
	Note        string `json:"note"`
	Archive     string `json:"archive,omitempty"`
	Folder      string `json:"folder,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ScopeID     string `json:"scope_id,omitempty"`
	// FromBucket is the objects under a prefix of a bucket on an S3
	// connection the caller's persona may use, inside its bucket_prefix
	// (#2593).
	FromBucket *bucketsource.Source `json:"from_bucket,omitempty"`
	// Source is what the release says it came from, the file's name when
	// given; only ever displayed.
	Source string `json:"source,omitempty" example:"site-1108.zip"`
}

// CopyBody names a site to copy into a release.
type CopyBody struct {
	URL         string `json:"url" example:"https://www.example.com"`
	Environment string `json:"environment,omitempty" example:"dev"`
	Note        string `json:"note"`
	MaxPages    int    `json:"max_pages,omitempty"`
	MaxBytes    int64  `json:"max_bytes,omitempty"`
	// FormMap names, for a page path, the platform form that replaces its forms.
	FormMap map[string]string `json:"form_map,omitempty"`
}

// PromoteBody moves a release to an environment.
type PromoteBody struct {
	From    string `json:"from,omitempty" example:"stage"`
	To      string `json:"to" example:"prod"`
	Release int    `json:"release,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// RollbackBody returns an environment to an earlier release.
type RollbackBody struct {
	Environment string `json:"environment" example:"prod"`
	Release     int    `json:"release,omitempty"`
}

// EnvironmentBody creates or changes an environment; a field left out is left
// alone, and a new environment is private.
type EnvironmentBody struct {
	RequiresApproval *bool  `json:"requires_approval,omitempty"`
	Public           *bool  `json:"public,omitempty"`
	InternalView     *bool  `json:"internal_view,omitempty"`
	Reason           string `json:"reason,omitempty"`
	// KeepReleases is how many releases the environment keeps: a whole number
	// (0 keeps every release), or "default" for the deployment's default
	// (#2602). The owner and administrators only.
	KeepReleases json.RawMessage `json:"keep_releases,omitempty" swaggertype:"string" example:"50"`
}
