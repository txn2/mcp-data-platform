package memory

// VectorQuery defines parameters for vector similarity search.
//
// CreatedBy and Dimension are optional scope filters. CreatedBy restricts
// results to a single owner (the portal's per-user "my knowledge" search
// scopes by the caller's email so a user cannot search another user's
// records); Dimension restricts to one LOCOMO dimension (the Knowledge
// tab scopes to DimensionKnowledge, since insights are knowledge-dimension
// memory records). Persona and Status mirror the other scope filters.
type VectorQuery struct {
	Embedding []float32
	Limit     int
	MinScore  float64
	CreatedBy string
	Dimension string
	Persona   string
	Status    string
	// ExcludeStatuses drops rows of the listed statuses, the complement of
	// Status (which restricts to one). It composes with the default archived
	// exclusion (applied when Status is empty). The recall-first capture check
	// uses it to skip superseded rows (a dead predecessor must not absorb a
	// new capture's supersede, #762) while still matching stale rows (a
	// restatement is exactly how a stale record gets corrected).
	ExcludeStatuses []string
}

// HybridQuery defines parameters for hybrid (vector + lexical) recall.
// Embedding drives the cosine arm; QueryText drives the lexical arm
// (Postgres full-text). A row matching either arm is a candidate; the
// two signals are fused per row by fuseHybridScore. CreatedBy, Dimension,
// Persona, and Status are optional scope filters mirroring VectorQuery.
type HybridQuery struct {
	Embedding []float32
	QueryText string
	Limit     int
	CreatedBy string
	Dimension string
	// ExcludeDimension drops rows of one dimension from the results. It is
	// the complement of Dimension (which restricts to one): the unified
	// knowledge search excludes the knowledge dimension here because those
	// rows are served by the insights provider, and excluding them in SQL
	// (rather than after LIMIT) keeps the result count honest.
	ExcludeDimension string
	Persona          string
	Status           string
	// InsightStatus restricts to one exact insight review status; see
	// Filter.InsightStatus. It is applied in SQL, before the top-k cut, so a
	// status-restricted search cannot be crowded out by higher-ranking rows of
	// another status.
	InsightStatus string
}

// LexicalQuery defines parameters for lexical-only recall, used as the
// graceful-degradation path when no embedding provider is available.
// Unlike the vector arm, lexical search does not filter on a non-null
// embedding, so it also surfaces rows whose embedding was never
// computed (saved during an embedder outage). CreatedBy, Dimension,
// Persona, and Status are optional scope filters mirroring VectorQuery.
type LexicalQuery struct {
	QueryText string
	Limit     int
	CreatedBy string
	Dimension string
	// ExcludeDimension drops rows of one dimension; see HybridQuery.
	ExcludeDimension string
	Persona          string
	Status           string
	// InsightStatus restricts to one exact insight review status; see
	// HybridQuery.InsightStatus.
	InsightStatus string
}

// ScoredRecord pairs a memory record with a similarity score.
type ScoredRecord struct {
	Record Record
	Score  float64
}
