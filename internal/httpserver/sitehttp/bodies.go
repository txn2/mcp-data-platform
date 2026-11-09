package sitehttp

// PublishBody names what to publish: a managed-resource archive or folder, read
// as the caller, so someone cannot publish a file they could not open.
type PublishBody struct {
	Environment string `json:"environment" example:"default"`
	Note        string `json:"note"`
	Archive     string `json:"archive,omitempty"`
	Folder      string `json:"folder,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ScopeID     string `json:"scope_id,omitempty"`
	// Source is what the release says it came from, the file's name when
	// given; only ever displayed.
	Source string `json:"source,omitempty" example:"site-1108.zip"`
}

// CopyBody names a site to copy into a release.
type CopyBody struct {
	URL         string `json:"url" example:"https://www.example.com"`
	Environment string `json:"environment" example:"default"`
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

// EnvironmentBody says whether an environment requires approval.
type EnvironmentBody struct {
	RequiresApproval bool `json:"requires_approval"`
}
