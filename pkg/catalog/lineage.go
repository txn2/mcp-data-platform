package catalog

import "time"

// WriterKind says what recorded a lineage edge.
type WriterKind string

// The writers of lineage edges.
const (
	WriterScript       WriterKind = "script"
	WriterSQL          WriterKind = "sql"
	WriterRegistration WriterKind = "registration"
	WriterWebhook      WriterKind = "webhook"
	WriterManual       WriterKind = "manual"
	// WriterImport is an edge the DataHub import carried over (#1923).
	WriterImport WriterKind = "import"
)

// Edge is one lineage edge: data moved from Source to Target. Both are
// references, so an edge can name a dataset the catalog has not scanned yet.
type Edge struct {
	Source     string     `json:"source"`
	Target     string     `json:"target"`
	WriterKind WriterKind `json:"writer_kind"`
	// WriterRef names the writer: a script, an audited call, a registration.
	WriterRef string    `json:"writer_ref,omitempty"`
	WrittenBy string    `json:"written_by,omitempty"`
	WrittenAt time.Time `json:"written_at"`
}

// Direction is which way a lineage walk goes from its starting dataset.
type Direction string

// The walk directions.
const (
	Upstream   Direction = "upstream"
	Downstream Direction = "downstream"
)

// Hop is an edge reached by a lineage walk, at its distance from the start.
type Hop struct {
	Edge
	Depth int `json:"depth"`
}
