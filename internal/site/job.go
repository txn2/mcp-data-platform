package site

import "time"

// The kinds of work that run apart from the request that started them.
const (
	JobPublish = "publish"
	JobCrawl   = "crawl"
)

// The states a job is in.
const (
	JobRunning = "running"
	JobDone    = "done"
	JobFailed  = "failed"
	JobStopped = "stopped"
)

// Skipped is one address a copy of a site could not bring across, and why.
type Skipped struct {
	Address string `json:"address" example:"/old/report.pdf"`
	Reason  string `json:"reason" example:"answered 404"`
}

// Job is a publish or a copy running in the background.
type Job struct {
	ID          string `json:"id" example:"sjob_01J9"`
	SiteID      string `json:"site_id"`
	Kind        string `json:"kind" example:"publish"`
	Status      string `json:"status" example:"running"`
	Phase       string `json:"phase" example:"Writing files"`
	Environment string `json:"environment" example:"default"`
	Source      string `json:"source"`
	// Done and Total count the files written, or the pages copied.
	Done  int `json:"done"`
	Total int `json:"total"`
	Pages int `json:"pages"`
	// Bytes is what a copy has downloaded; SkippedCount the addresses it could
	// not bring across so far, listed in Skipped when it ends.
	Bytes        int64     `json:"bytes"`
	SkippedCount int       `json:"skipped_count"`
	Redirects    int       `json:"redirects"`
	Skipped      []Skipped `json:"skipped"`
	Release      int       `json:"release"`
	Error        string    `json:"error,omitempty"`
	CreatedBy    string    `json:"created_by"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// JobProgress is one report of a running job's state.
type JobProgress struct {
	Phase     string
	Done      int
	Total     int
	Pages     int
	Bytes     int64
	Redirects int
	Skipped   int
}

// JobResult is how a job ended.
type JobResult struct {
	Status    string
	Release   int
	Pages     int
	Redirects int
	Skipped   []Skipped
	Error     string
}
