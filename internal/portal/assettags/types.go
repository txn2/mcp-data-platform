package assettags

// What a tag listing answers with: each tag, its spellings and the page.

// Tag is one tag, compared without case: its most used spelling, how many
// assets carry it in any spelling, and the spellings it covers.
type Tag struct {
	Tag       string     `json:"tag" example:"finance"`
	Count     int        `json:"count" example:"14"`
	Spellings []Spelling `json:"spellings"`
}

// Spelling is one way a tag is written and how many assets carry it so.
type Spelling struct {
	Tag   string `json:"tag" example:"Finance"`
	Count int    `json:"count" example:"9"`
}

// Page is one page of tags and how many tags there are in all.
type Page struct {
	Tags  []Tag `json:"tags"`
	Total int   `json:"total"`
}
