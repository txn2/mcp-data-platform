package sitehttp

import (
	"encoding/json"
	"time"

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

// EnvironmentBody creates, places or changes an environment; a field left
// out is left alone.
type EnvironmentBody struct {
	RequiresApproval *bool `json:"requires_approval,omitempty"`
	// Public is refused (#2619): public is a setting of a domain.
	Public       *bool `json:"public,omitempty"`
	InternalView *bool `json:"internal_view,omitempty"`
	// After places a new environment, or moves one, immediately after the
	// environment it names: dev or one between dev and prod (#2617).
	After string `json:"after,omitempty"`
	// KeepReleases is how many releases the environment keeps: a whole number
	// (0 keeps every release), or "default" for the deployment's default
	// (#2602). The owner and administrators only.
	KeepReleases json.RawMessage `json:"keep_releases,omitempty" swaggertype:"string" example:"50"`
	// Status is what every address of the environment answers (#2631,
	// #2632): online, offline (with message and back_at) or redirect (with
	// redirect_to and keep_path).
	Status     string     `json:"status,omitempty" enums:"online,offline,redirect"`
	Message    string     `json:"message,omitempty"`
	BackAt     *time.Time `json:"back_at,omitempty"`
	RedirectTo string     `json:"redirect_to,omitempty" example:"https://status.example.com"`
	KeepPath   bool       `json:"keep_path,omitempty"`
	// Reason goes with a redirect that waits on Approvals.
	Reason string `json:"reason,omitempty"`
	// RenameTo renames an environment between dev and prod, alone (#2630).
	RenameTo string `json:"rename_to,omitempty" example:"qa"`
	// AgentsMayPromote lets an agent's promote, rollback or public domain here
	// go ahead without a person's decision (#2643). Administrators only.
	AgentsMayPromote *bool `json:"agents_may_promote,omitempty"`
}

// settings reports whether the body changes a setting, beyond status.
func (b EnvironmentBody) settings() bool {
	return b.RequiresApproval != nil || b.Public != nil || b.InternalView != nil || b.AgentsMayPromote != nil ||
		b.After != "" || len(b.KeepReleases) > 0
}
