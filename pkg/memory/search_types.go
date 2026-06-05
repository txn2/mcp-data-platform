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
	Persona   string
	Status    string
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
	Persona   string
	Status    string
}

// ScoredRecord pairs a memory record with a similarity score.
type ScoredRecord struct {
	Record Record
	Score  float64
}
