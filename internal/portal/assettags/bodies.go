package assettags

// The bodies the administrator's tag routes take and answer with.

// RenameRequest renames one tag.
type RenameRequest struct {
	From string `json:"from" example:"fin"`
	To   string `json:"to" example:"finance"`
}

// MergeRequest merges tags into one.
type MergeRequest struct {
	From []string `json:"from" example:"fin,Finance"`
	Into string   `json:"into" example:"finance"`
}

// RetagResult is what a rename or merge did.
type RetagResult struct {
	Tag   string `json:"tag" example:"finance"`
	Moved int64  `json:"moved" example:"14"`
}
