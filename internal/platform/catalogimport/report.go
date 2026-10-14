package catalogimport

// Report is what one run of the import did.
type Report struct {
	// Definitions counts the definitions imported, by kind.
	Definitions map[string]int `json:"definitions"`
	Datasets    DatasetCounts  `json:"datasets"`
	// AspectsWritten counts the aspects the import wrote; AspectsUnchanged
	// those that already held DataHub's value; AspectsKept those it left
	// alone because something other than the import wrote them in the
	// platform catalog.
	AspectsWritten   int `json:"aspects_written"`
	AspectsUnchanged int `json:"aspects_unchanged"`
	AspectsKept      int `json:"aspects_kept"`
	LineageEdges     int `json:"lineage_edges"`
	// Skipped lists what was not imported and why, at most maxListed of
	// them; SkippedByReason counts every one.
	Skipped         []Skip         `json:"skipped"`
	SkippedByReason map[string]int `json:"skipped_by_reason"`
	// Rewrite is what the rewrite of stored references did.
	Rewrite *RewriteReport `json:"rewrite,omitempty"`
}

// DatasetCounts counts the datasets the import read and what became of them.
type DatasetCounts struct {
	// Read is every dataset record DataHub listed, removed ones included.
	Read int `json:"read"`
	// Imported is the catalog datasets the import wrote curation onto.
	Imported int `json:"imported"`
	// FromRemoved is the catalog datasets that took curation from a record
	// DataHub has marked removed.
	FromRemoved int `json:"from_removed"`
	Skipped     int `json:"skipped"`
}

// Skip is one thing the import or the rewrite did not carry over.
type Skip struct {
	URN    string `json:"urn"`
	Reason string `json:"reason"`
	// Where names the platform table and column a rewrite skip was found in.
	Where string `json:"where,omitempty"`
}

// maxListed bounds how many skips a report lists; the counts cover all.
const maxListed = 500

// The reasons a dataset or reference is not carried over.
const (
	ReasonNotCataloged = "no connection's catalog holds a dataset with this path; " +
		"scan the connection that sees it, then import again"
	ReasonPlatform    = "its platform is not a connection kind the catalog scans"
	ReasonTableau     = "Tableau entities are not imported"
	ReasonUnmapped    = "the import has no catalog entity for this URN"
	ReasonUnsupported = "this kind of DataHub entity has no catalog counterpart"
	ReasonKept        = "written in the platform catalog since, so the catalog's value is kept"
	ReasonNoColumn    = "the catalog dataset has no column with this name"
	ReasonUnreadable  = "DataHub could not read it"
)

// newReport is an empty report with its maps and lists made, so it encodes as
// {} and [] rather than null.
func newReport() *Report {
	return &Report{
		Definitions:     map[string]int{},
		Skipped:         []Skip{},
		SkippedByReason: map[string]int{},
	}
}

// skip records something not carried over.
func (r *Report) skip(urn, reason, where string) {
	r.SkippedByReason[reason]++
	if len(r.Skipped) < maxListed {
		r.Skipped = append(r.Skipped, Skip{URN: urn, Reason: reason, Where: where})
	}
}
