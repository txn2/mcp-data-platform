package memory

// manageInput is the deserialized input for the memory_manage tool.
type manageInput struct {
	Command         string         `json:"command"`
	Content         string         `json:"content,omitempty"`
	ID              string         `json:"id,omitempty"`
	Dimension       string         `json:"dimension,omitempty"`
	Category        string         `json:"category,omitempty"`
	Confidence      string         `json:"confidence,omitempty"`
	Source          string         `json:"source,omitempty"`
	EntityURNs      []string       `json:"entity_urns,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	FilterDimension string         `json:"filter_dimension,omitempty"`
	FilterCategory  string         `json:"filter_category,omitempty"`
	FilterStatus    string         `json:"filter_status,omitempty"`
	FilterEntityURN string         `json:"filter_entity_urn,omitempty"`
	Limit           int            `json:"limit,omitempty"`
	Offset          int            `json:"offset,omitempty"`
}

// recallInput is the deserialized input for the memory_recall tool.
type recallInput struct {
	Query        string   `json:"query"`
	Strategy     string   `json:"strategy,omitempty"`
	EntityURNs   []string `json:"entity_urns,omitempty"`
	Dimension    string   `json:"dimension,omitempty"`
	IncludeStale bool     `json:"include_stale,omitempty"`
	Limit        int      `json:"limit,omitempty"`
}

// Recall strategy constants.
const (
	strategyEntity   = "entity"
	strategySemantic = "semantic"
	strategyGraph    = "graph"
	strategyAuto     = "auto"
	// strategyLexical forces lexical-only (Postgres full-text) recall with
	// no embedding call. Useful for exact-token lookups and as the explicit
	// counterpart to the automatic lexical fallback the semantic/auto
	// strategies use when the embedder is unavailable.
	strategyLexical = "lexical"
)

// Ranking labels reported on the recall response so the caller knows
// which path produced the results. "hybrid" = vector fused with lexical;
// "lexical" = full-text only (the degraded or forced path); "entity"/
// "graph" = exact URN/lineage lookups.
const (
	rankingHybrid  = "hybrid"
	rankingLexical = "lexical"
)

// degradedNote is surfaced on the recall response when the semantic path
// fell back to lexical because no embedding provider was available, so
// the degradation is visible to the caller rather than silent.
const degradedNote = "embedding provider unavailable; results are lexical-only " +
	"(exact-term matches), not semantic"

// Default recall limits.
const (
	defaultRecallLimit = 10
	maxRecallLimit     = 50
)
